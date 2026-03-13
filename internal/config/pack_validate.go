package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
)

// Remediation string constants.
const (
	remediationFixYAMLSyntax   = "Fix the YAML syntax in the frontmatter block"
	remediationFixSchemaValue  = "Fix the value to match the schema; see docs/pack-format.md"
	remediationCheckRefID      = "Check that the referenced ID exists in pack.json or mcp/*.json"
	remediationUnknownAgentKey = "Remove or rename the unknown field; see docs/pack-format.md Section 4.4 for valid agent fields"
)

type packValidationPolicy struct {
	skipDirs              map[string]struct{}
	skipFiles             map[string]struct{}
	secretScanExemptDirs  map[string]struct{}
	forbiddenPathSnippets []string
	secretPatterns        []*regexp.Regexp
}

var defaultPackValidationPolicy = packValidationPolicy{
	skipDirs: map[string]struct{}{
		".generated": {},
		".git":       {},
	},
	skipFiles: map[string]struct{}{
		".gitkeep": {},
	},
	secretScanExemptDirs: map[string]struct{}{
		"docs": {},
	},
	forbiddenPathSnippets: []string{"/Users/", "/home/", `\\Users\\`},
	secretPatterns: []*regexp.Regexp{
		regexp.MustCompile(`BEGIN (RSA|OPENSSH) PRIVATE KEY`),
		regexp.MustCompile(`ssh-rsa\s`),
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		regexp.MustCompile(`\bocid1\.[a-z0-9.]+`),
	},
}

type packValidator struct {
	root     string
	policy   packValidationPolicy
	findings []Finding
}

func ValidatePackRoot(packRoot string) []Finding {
	root := strings.TrimSpace(packRoot)
	if root == "" {
		return []Finding{{Message: "pack root must be set", Category: FindingCategoryInventory, Severity: FindingSeverityError}}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return []Finding{{Message: err.Error(), Category: FindingCategoryInventory, Severity: FindingSeverityError}}
	}
	v := packValidator{root: absRoot, policy: defaultPackValidationPolicy}
	v.validateManifestAndInventory()
	v.walkPackFiles()
	sort.Slice(v.findings, func(i, j int) bool {
		return v.findings[i].String() < v.findings[j].String()
	})
	return v.findings
}

func (v *packValidator) validateManifestAndInventory() {
	manifestPath := filepath.Join(v.root, "pack.json")
	st, err := os.Stat(manifestPath)
	if err != nil || !st.Mode().IsRegular() {
		v.addFinding(Finding{Path: "pack.json", Category: FindingCategoryInventory, Severity: FindingSeverityError,
			Message: fmt.Sprintf("not found in %s", v.root)})
		return
	}

	// Schema validation on raw bytes.
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		v.addFinding(Finding{Path: "pack.json", Category: FindingCategoryInventory, Severity: FindingSeverityError,
			Message: err.Error()})
		return
	}
	v.findings = append(v.findings, ValidatePackJSONSchema(raw)...)

	manifest, err := ParsePackManifest(raw)
	if err != nil {
		v.addFinding(Finding{Path: "pack.json", Category: FindingCategoryInventory, Severity: FindingSeverityError,
			Message: err.Error()})
		return
	}
	resolvedRoot := ResolvePackRoot(manifestPath, manifest.Root)
	if resolvedRoot == "" {
		v.addFinding(Finding{Path: "pack.json", Category: FindingCategoryInventory, Severity: FindingSeverityError,
			Message: fmt.Sprintf("pack %q root could not be resolved", manifest.Name)})
		return
	}
	if err := validatePackInventory(manifest.Name, resolvedRoot, manifest); err != nil {
		v.addFinding(Finding{Path: "pack.json", Category: FindingCategoryInventory, Severity: FindingSeverityError,
			Message: err.Error()})
	}

	// Validate each MCP server definition against its schema.
	for name := range manifest.MCP.Servers {
		mcpRelPath := filepath.ToSlash(filepath.Join("mcp", name+".json"))
		mcpPath := filepath.Join(resolvedRoot, "mcp", name+".json")
		mcpRaw, readErr := os.ReadFile(mcpPath)
		if readErr != nil {
			continue // existence already validated by validatePackInventory
		}
		v.findings = append(v.findings, ValidateMCPServerSchema(mcpRelPath, mcpRaw)...)
	}

	v.validateFrontmatter(manifest, resolvedRoot)
}

func (v *packValidator) validateFrontmatter(manifest PackManifest, packRoot string) {
	// Build known-ID sets for cross-reference checks.
	knownServers := map[string]struct{}{}
	for name := range manifest.MCP.Servers {
		knownServers[name] = struct{}{}
	}
	knownSkills := map[string]struct{}{}
	for _, id := range manifest.Skills {
		knownSkills[id] = struct{}{}
	}

	for _, id := range manifest.Rules {
		v.validateContentFrontmatter(filepath.Join(packRoot, "rules", id+".md"), "rules/"+id+".md", id, &domain.RuleFrontmatter{})
	}
	for _, id := range manifest.Agents {
		v.validateAgentFrontmatter(packRoot, id, knownServers, knownSkills)
	}
	for _, id := range manifest.Workflows {
		v.validateContentFrontmatter(filepath.Join(packRoot, "workflows", id+".md"), "workflows/"+id+".md", id, &domain.WorkflowFrontmatter{})
	}
	for _, id := range manifest.Skills {
		v.validateContentFrontmatter(filepath.Join(packRoot, "skills", id, "SKILL.md"), "skills/"+id+"/SKILL.md", id, &domain.SkillFrontmatter{})
	}
}

// validateContentFrontmatter is the generic frontmatter validation path for
// rules, workflows, and skills. The dest parameter must be a pointer to a
// zero-value frontmatter struct that implements domain.FrontmatterValidator.
func (v *packValidator) validateContentFrontmatter(fullPath, relPath, id string, dest domain.FrontmatterValidator) {
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		if !os.IsNotExist(err) {
			v.addFinding(Finding{Path: relPath, Category: FindingCategoryFrontmatter, Severity: FindingSeverityWarning,
				Message: fmt.Sprintf("cannot read file: %s", err.Error())})
		}
		return
	}
	fmBytes, _, _ := domain.SplitFrontmatter(raw)
	if len(fmBytes) == 0 {
		return
	}
	if err := yaml.Unmarshal(fmBytes, dest); err != nil {
		v.addFinding(Finding{Path: relPath, Category: FindingCategoryFrontmatter, Severity: FindingSeverityWarning,
			Message: fmt.Sprintf("[frontmatter] invalid YAML: %s", err.Error()), Remediation: remediationFixYAMLSyntax})
		return
	}
	for _, w := range dest.Validate(id) {
		v.addFinding(Finding{Path: relPath, Category: FindingCategoryFrontmatter, Severity: FindingSeverityWarning,
			Message: fmt.Sprintf("[%s] %s", w.Field, w.Message), Remediation: remediationForField(w.Field)})
	}
}

func (v *packValidator) validateAgentFrontmatter(packRoot, id string, knownServers, knownSkills map[string]struct{}) {
	relPath := "agents/" + id + ".md"
	raw, err := os.ReadFile(filepath.Join(packRoot, "agents", id+".md"))
	if err != nil {
		if !os.IsNotExist(err) {
			v.addFinding(Finding{Path: relPath, Category: FindingCategoryFrontmatter, Severity: FindingSeverityWarning,
				Message: fmt.Sprintf("cannot read file: %s", err.Error())})
		}
		return
	}
	fmBytes, _, _ := domain.SplitFrontmatter(raw)
	if len(fmBytes) == 0 {
		return
	}

	// Strict decode: detect unknown fields and populate fm in one pass.
	var fm domain.AgentFrontmatter
	dec := yaml.NewDecoder(bytes.NewReader(fmBytes))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil {
		// Distinguish unknown-field errors from parse errors by attempting
		// a lenient decode. If lenient also fails, it's a syntax error.
		var lenient domain.AgentFrontmatter
		if yamlErr := yaml.Unmarshal(fmBytes, &lenient); yamlErr != nil {
			v.addFinding(Finding{Path: relPath, Category: FindingCategoryFrontmatter, Severity: FindingSeverityWarning,
				Message: fmt.Sprintf("[frontmatter] invalid YAML: %s", yamlErr.Error()), Remediation: remediationFixYAMLSyntax})
			return
		}
		// Strict failed but lenient succeeded → unknown fields.
		v.addFinding(Finding{Path: relPath, Category: FindingCategoryFrontmatter, Severity: FindingSeverityWarning,
			Message: fmt.Sprintf("[frontmatter] %s", err.Error()), Remediation: remediationUnknownAgentKey})
		fm = lenient
	}

	for _, w := range fm.Validate(id) {
		v.addFinding(Finding{Path: relPath, Category: FindingCategoryFrontmatter, Severity: FindingSeverityWarning,
			Message: fmt.Sprintf("[%s] %s", w.Field, w.Message), Remediation: remediationForField(w.Field)})
	}
	for _, w := range fm.ValidateRefs(id, knownServers, knownSkills) {
		v.addFinding(Finding{Path: relPath, Category: FindingCategoryConsistency, Severity: FindingSeverityWarning,
			Message: fmt.Sprintf("[%s] %s", w.Field, w.Message), Remediation: remediationCheckRefID})
	}
}

func (v *packValidator) walkPackFiles() {
	_ = filepath.WalkDir(v.root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if v.policy.shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if v.policy.shouldSkipFile(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(v.root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		v.validateScannedFile(rel, path, d.Name())
		return nil
	})
}

func (v *packValidator) validateScannedFile(rel string, fullPath string, baseName string) {
	if isForbiddenEnvFile(baseName) {
		v.addFinding(Finding{Path: rel, Category: FindingCategoryPolicy, Severity: FindingSeverityError,
			Message: "forbidden .env file", Remediation: "Remove the .env file or add it to .gitignore"})
		return
	}
	for _, snippet := range v.policy.forbiddenPathSnippets {
		if strings.Contains(rel, snippet) {
			v.addFinding(Finding{Path: rel, Category: FindingCategoryPolicy, Severity: FindingSeverityError,
				Message: fmt.Sprintf("filename contains forbidden snippet '%s'", snippet), Remediation: "Rename the file to remove absolute path fragments"})
			break
		}
	}
	b, err := os.ReadFile(fullPath)
	if err != nil {
		return
	}
	if !domain.HasFrontmatterPrefix(b) {
		if _, _, ok := domain.MatchPrimaryContentFile(rel); ok {
			v.addFinding(Finding{Path: rel, Category: FindingCategoryFrontmatter, Severity: FindingSeverityError,
				Message: "missing YAML frontmatter", Remediation: "Add a YAML frontmatter block with at least name and description fields"})
		}
	}
	if v.policy.shouldSkipSecretScan(rel) {
		return
	}
	if pattern, ok := v.policy.firstMatchingSecretPattern(b); ok {
		v.addFinding(Finding{Path: rel, Category: FindingCategoryPolicy, Severity: FindingSeverityError,
			Message: fmt.Sprintf("matches secret pattern '%s'", pattern), Remediation: "Remove the secret or move it to an environment variable"})
	}
}

func (v *packValidator) addFinding(f Finding) {
	v.findings = append(v.findings, f)
}

func remediationForField(field string) string {
	switch field {
	case "name":
		return "Set the name field to match the file/directory ID"
	case "description":
		return "Add a description field explaining when this content applies"
	default:
		return ""
	}
}

func (p packValidationPolicy) shouldSkipDir(name string) bool {
	_, ok := p.skipDirs[name]
	return ok
}

func (p packValidationPolicy) shouldSkipFile(name string) bool {
	_, ok := p.skipFiles[name]
	return ok
}

func (p packValidationPolicy) shouldSkipSecretScan(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) > 0 {
		if _, ok := p.secretScanExemptDirs[parts[0]]; ok {
			return true
		}
	}
	return false
}

func (p packValidationPolicy) firstMatchingSecretPattern(b []byte) (string, bool) {
	for _, pat := range p.secretPatterns {
		if pat.Match(b) {
			return pat.String(), true
		}
	}
	return "", false
}

func isForbiddenEnvFile(name string) bool {
	return name == ".env" || (strings.HasPrefix(name, ".env.") && name != ".env.example")
}
