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

// secretPattern pairs a compiled regex with a severity level.
type secretPattern struct {
	pat      *regexp.Regexp
	severity FindingSeverity
}

type packValidationPolicy struct {
	skipDirs              map[string]struct{}
	skipFiles             map[string]struct{}
	secretScanExemptDirs  map[string]struct{}
	forbiddenPathSnippets []string
	secretPatterns        []secretPattern
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
	secretPatterns: []secretPattern{
		{pat: regexp.MustCompile(`BEGIN (RSA|OPENSSH) PRIVATE KEY`), severity: FindingSeverityError},
		{pat: regexp.MustCompile(`ssh-rsa\s`), severity: FindingSeverityError},
		{pat: regexp.MustCompile(`AKIA[0-9A-Z]{16}`), severity: FindingSeverityError},
		{pat: regexp.MustCompile(`\bocid1\.[a-z0-9.]+`), severity: FindingSeverityWarning},
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
	// Prompts are intentionally skipped: PromptFrontmatter has no Validate()
	// method because prompts are opaque text blobs with no structural constraints
	// beyond having valid YAML frontmatter (checked by walkPackFiles).
}

// frontmatterValidator is implemented by frontmatter types that support Validate.
type frontmatterValidator interface {
	Validate(fileID string) []domain.Warning
}

// validateContentFrontmatter reads a content file, splits and unmarshals its
// frontmatter into target, and emits findings for parse errors and Validate()
// warnings. Returns the raw frontmatter bytes and true on success so callers
// can perform additional type-specific checks (e.g. strict-mode, cross-refs).
func (v *packValidator) validateContentFrontmatter(filePath, relPath, id string, target frontmatterValidator) ([]byte, bool) {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		v.addFinding(relPath, FindingCategoryInventory, FindingSeverityError,
			fmt.Sprintf("cannot read file: %s", err.Error()))
		return nil, false
	}
	fmBytes, _, splitErr := domain.SplitFrontmatter(raw)
	if splitErr != nil {
		v.addFinding(relPath, FindingCategoryFrontmatter, FindingSeverityWarning,
			fmt.Sprintf("[frontmatter] %s", splitErr.Error()))
		return nil, false
	}
	if len(fmBytes) == 0 {
		v.addFinding(relPath, FindingCategoryFrontmatter, FindingSeverityWarning,
			"[frontmatter] empty or malformed frontmatter block")
		return nil, false
	}
	if err := yaml.Unmarshal(fmBytes, target); err != nil {
		v.addFinding(relPath, FindingCategoryFrontmatter, FindingSeverityWarning,
			fmt.Sprintf("[frontmatter] invalid YAML: %s", err.Error()))
		return nil, false
	}
	for _, w := range target.Validate(id) {
		v.addFindingWithField(relPath, FindingCategoryFrontmatter, FindingSeverityWarning, w.Field, w.Message)
	}
	return fmBytes, true
}

func (v *packValidator) validateRuleFrontmatter(packRoot, id string) {
	var fm domain.RuleFrontmatter
	v.validateContentFrontmatter(
		filepath.Join(packRoot, "rules", id+".md"), "rules/"+id+".md", id, &fm)
}

func (v *packValidator) validateAgentFrontmatter(packRoot, id string, knownServers, knownSkills map[string]struct{}) {
	var fm domain.AgentFrontmatter
	relPath := "agents/" + id + ".md"
	fmBytes, ok := v.validateContentFrontmatter(
		filepath.Join(packRoot, "agents", id+".md"), relPath, id, &fm)
	if !ok {
		return
	}

	// Strict mode: detect unknown fields. AgentFrontmatter has no catch-all
	// metadata field, so any unknown key is likely a typo.
	var strict domain.AgentFrontmatter
	dec := yaml.NewDecoder(bytes.NewReader(fmBytes))
	dec.KnownFields(true)
	if err := dec.Decode(&strict); err != nil {
		v.addFinding(relPath, FindingCategoryFrontmatter, FindingSeverityWarning,
			fmt.Sprintf("[frontmatter] %s", err.Error()))
	}

	for _, w := range fm.ValidateRefs(knownServers, knownSkills) {
		v.addFindingWithField(relPath, FindingCategoryConsistency, FindingSeverityWarning, w.Field, w.Message)
	}
}

func (v *packValidator) validateWorkflowFrontmatter(packRoot, id string) {
	var fm domain.WorkflowFrontmatter
	v.validateContentFrontmatter(
		filepath.Join(packRoot, "workflows", id+".md"), "workflows/"+id+".md", id, &fm)
}

func (v *packValidator) validateSkillFrontmatter(packRoot, id string) {
	var fm domain.SkillFrontmatter
	v.validateContentFrontmatter(
		filepath.Join(packRoot, "skills", id, "SKILL.md"), "skills/"+id+"/SKILL.md", id, &fm)
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
	if pattern, severity, ok := v.policy.firstMatchingSecretPattern(text); ok {
		v.addFinding(rel, FindingCategoryPolicy, severity, fmt.Sprintf("matches secret pattern '%s'", pattern))
	}
}

func (v *packValidator) addFinding(path string, category FindingCategory, severity FindingSeverity, message string) {
	v.findings = append(v.findings, Finding{
		Path:     path,
		Category: category,
		Severity: severity,
		Message:  message,
	})
}

func (v *packValidator) addFindingWithField(path string, category FindingCategory, severity FindingSeverity, field, message string) {
	v.findings = append(v.findings, Finding{
		Path:     path,
		Category: category,
		Severity: severity,
		Field:    field,
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

func (p packValidationPolicy) firstMatchingSecretPattern(text string) (string, FindingSeverity, bool) {
	for _, sp := range p.secretPatterns {
		if sp.pat.FindStringIndex(text) != nil {
			return sp.pat.String(), sp.severity, true
		}
	}
	return "", "", false
}

func isForbiddenEnvFile(name string) bool {
	return name == ".env" || (strings.HasPrefix(name, ".env.") && name != ".env.example")
}
