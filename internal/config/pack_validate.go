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
		v.addFinding("pack.json", FindingCategoryInventory, FindingSeverityError, fmt.Sprintf("not found in %s", v.root))
		return
	}

	// Schema validation on raw bytes.
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		v.addFinding("pack.json", FindingCategoryInventory, FindingSeverityError, err.Error())
		return
	}
	v.findings = append(v.findings, ValidatePackJSONSchema(raw)...)

	manifest, err := LoadPackManifest(manifestPath)
	if err != nil {
		v.addFinding("pack.json", FindingCategoryInventory, FindingSeverityError, err.Error())
		return
	}
	resolvedRoot := ResolvePackRoot(manifestPath, manifest.Root)
	if resolvedRoot == "" {
		v.addFinding("pack.json", FindingCategoryInventory, FindingSeverityError, fmt.Sprintf("pack %q root could not be resolved", manifest.Name))
		return
	}
	if err := validatePackInventory(manifest.Name, resolvedRoot, manifest); err != nil {
		v.addFinding("pack.json", FindingCategoryInventory, FindingSeverityError, err.Error())
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
		v.validateRuleFrontmatter(packRoot, id)
	}
	for _, id := range manifest.Agents {
		v.validateAgentFrontmatter(packRoot, id, knownServers, knownSkills)
	}
	for _, id := range manifest.Workflows {
		v.validateWorkflowFrontmatter(packRoot, id)
	}
	for _, id := range manifest.Skills {
		v.validateSkillFrontmatter(packRoot, id)
	}
}

func (v *packValidator) validateRuleFrontmatter(packRoot, id string) {
	raw, err := os.ReadFile(filepath.Join(packRoot, "rules", id+".md"))
	if err != nil {
		return
	}
	fmBytes, _, _ := domain.SplitFrontmatter(raw)
	if len(fmBytes) == 0 {
		return
	}
	var fm domain.RuleFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		v.addFinding("rules/"+id+".md", FindingCategoryFrontmatter, FindingSeverityWarning,
			fmt.Sprintf("[frontmatter] invalid YAML: %s", err.Error()))
		return
	}
	for _, w := range fm.Validate(id) {
		v.addFinding("rules/"+id+".md", FindingCategoryFrontmatter, FindingSeverityWarning, fmt.Sprintf("[%s] %s", w.Field, w.Message))
	}
}

func (v *packValidator) validateAgentFrontmatter(packRoot, id string, knownServers, knownSkills map[string]struct{}) {
	raw, err := os.ReadFile(filepath.Join(packRoot, "agents", id+".md"))
	if err != nil {
		return
	}
	fmBytes, _, _ := domain.SplitFrontmatter(raw)
	if len(fmBytes) == 0 {
		return
	}
	var fm domain.AgentFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		v.addFinding("agents/"+id+".md", FindingCategoryFrontmatter, FindingSeverityWarning,
			fmt.Sprintf("[frontmatter] invalid YAML: %s", err.Error()))
		return
	}

	// Strict mode: detect unknown fields. AgentFrontmatter has no catch-all
	// metadata field, so any unknown key is likely a typo.
	var strict domain.AgentFrontmatter
	dec := yaml.NewDecoder(bytes.NewReader(fmBytes))
	dec.KnownFields(true)
	if err := dec.Decode(&strict); err != nil {
		v.addFinding("agents/"+id+".md", FindingCategoryFrontmatter, FindingSeverityWarning,
			fmt.Sprintf("[frontmatter] %s", err.Error()))
	}

	for _, w := range fm.Validate(id) {
		v.addFinding("agents/"+id+".md", FindingCategoryFrontmatter, FindingSeverityWarning, fmt.Sprintf("[%s] %s", w.Field, w.Message))
	}
	for _, w := range fm.ValidateRefs(id, knownServers, knownSkills) {
		v.addFinding("agents/"+id+".md", FindingCategoryConsistency, FindingSeverityWarning, fmt.Sprintf("[%s] %s", w.Field, w.Message))
	}
}

func (v *packValidator) validateWorkflowFrontmatter(packRoot, id string) {
	raw, err := os.ReadFile(filepath.Join(packRoot, "workflows", id+".md"))
	if err != nil {
		return
	}
	fmBytes, _, _ := domain.SplitFrontmatter(raw)
	if len(fmBytes) == 0 {
		return
	}
	var fm domain.WorkflowFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		v.addFinding("workflows/"+id+".md", FindingCategoryFrontmatter, FindingSeverityWarning,
			fmt.Sprintf("[frontmatter] invalid YAML: %s", err.Error()))
		return
	}
	for _, w := range fm.Validate(id) {
		v.addFinding("workflows/"+id+".md", FindingCategoryFrontmatter, FindingSeverityWarning, fmt.Sprintf("[%s] %s", w.Field, w.Message))
	}
}

func (v *packValidator) validateSkillFrontmatter(packRoot, id string) {
	raw, err := os.ReadFile(filepath.Join(packRoot, "skills", id, "SKILL.md"))
	if err != nil {
		return
	}
	fmBytes, _, _ := domain.SplitFrontmatter(raw)
	if len(fmBytes) == 0 {
		return
	}
	var fm domain.SkillFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		v.addFinding("skills/"+id+"/SKILL.md", FindingCategoryFrontmatter, FindingSeverityWarning,
			fmt.Sprintf("[frontmatter] invalid YAML: %s", err.Error()))
		return
	}
	for _, w := range fm.Validate(id) {
		v.addFinding("skills/"+id+"/SKILL.md", FindingCategoryFrontmatter, FindingSeverityWarning, fmt.Sprintf("[%s] %s", w.Field, w.Message))
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
		v.addFinding(rel, FindingCategoryPolicy, FindingSeverityError, "forbidden .env file")
		return
	}
	for _, snippet := range v.policy.forbiddenPathSnippets {
		if strings.Contains(rel, snippet) {
			v.addFinding(rel, FindingCategoryPolicy, FindingSeverityError, fmt.Sprintf("filename contains forbidden snippet '%s'", snippet))
			break
		}
	}
	b, err := os.ReadFile(fullPath)
	if err != nil {
		return
	}
	if !domain.HasFrontmatterPrefix(b) {
		if _, _, ok := domain.MatchPrimaryContentFile(rel); ok {
			v.addFinding(rel, FindingCategoryFrontmatter, FindingSeverityError, "missing YAML frontmatter")
		}
	}
	if v.policy.shouldSkipSecretScan(rel) {
		return
	}
	text := string(b)
	if pattern, ok := v.policy.firstMatchingSecretPattern(text); ok {
		v.addFinding(rel, FindingCategoryPolicy, FindingSeverityError, fmt.Sprintf("matches secret pattern '%s'", pattern))
	}
}

func (v *packValidator) addFinding(path, category, severity, message string) {
	v.findings = append(v.findings, Finding{
		Path:     path,
		Category: category,
		Severity: severity,
		Message:  message,
	})
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

func (p packValidationPolicy) firstMatchingSecretPattern(text string) (string, bool) {
	for _, pat := range p.secretPatterns {
		if pat.FindStringIndex(text) != nil {
			return pat.String(), true
		}
	}
	return "", false
}

func isForbiddenEnvFile(name string) bool {
	return name == ".env" || (strings.HasPrefix(name, ".env.") && name != ".env.example")
}
