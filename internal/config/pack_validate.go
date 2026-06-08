package config

import (
	"bytes"
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
)

type packValidator struct {
	root     string
	packRoot string
	manifest PackManifest
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
	v := packValidator{root: absRoot}
	v.validateManifestAndInventory()
	slices.SortFunc(v.findings, func(a, b Finding) int {
		return cmp.Compare(a.String(), b.String())
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
	// Populate resolvedPaths so validation can find files authored under
	// organizational subdirectories (agents/workflows/skills).
	if err := DiscoverContent(&manifest, resolvedRoot); err != nil {
		v.addFinding("pack.json", FindingCategoryInventory, FindingSeverityError, err.Error())
		return
	}
	if err := validatePackInventory(manifest.Name, resolvedRoot, manifest); err != nil {
		v.addFinding("pack.json", FindingCategoryInventory, FindingSeverityError, err.Error())
	}
	v.packRoot = resolvedRoot
	v.manifest = manifest

	if len(manifest.Extras) > MaxExtras {
		v.addFinding("pack.json", FindingCategoryPolicy, FindingSeverityError,
			fmt.Sprintf("extras declares %d entries (max %d)", len(manifest.Extras), MaxExtras))
	}
	ev := NewExtrasValidator()
	for _, ext := range manifest.Extras {
		if _, err := ev.ValidateEntry(ext); err != nil {
			v.addFinding("pack.json", FindingCategoryPolicy, FindingSeverityError, err.Error())
			continue
		}
		extPath := filepath.Join(resolvedRoot, ext)
		if _, err := os.Stat(extPath); err != nil {
			v.addFinding("pack.json", FindingCategoryInventory, FindingSeverityError,
				fmt.Sprintf("extras path %q not found in %s", ext, resolvedRoot))
		}
	}

	v.validateBundledProfiles()
	v.validateFrontmatter()
}

func (v *packValidator) validateBundledProfiles() {
	for _, id := range v.manifest.Profiles {
		if !IsProtectedPackProfileID(id) {
			continue
		}
		v.addFinding(filepath.ToSlash(filepath.Join("profiles", id+".yaml")), FindingCategoryPolicy, FindingSeverityError,
			fmt.Sprintf("profile name %q is reserved for the user's local default profile", DefaultProfileName))
	}
}

func (v *packValidator) validateFrontmatter() {
	// Build known-ID sets for cross-reference checks.
	knownServers := map[string]struct{}{}
	for _, name := range v.manifest.MCP {
		knownServers[name] = struct{}{}
	}
	knownSkills := map[string]struct{}{}
	for _, id := range v.manifest.Skills {
		knownSkills[id] = struct{}{}
	}

	for _, id := range v.manifest.Rules {
		v.validateRuleFrontmatter(id)
	}
	for _, id := range v.manifest.Agents {
		v.validateAgentFrontmatter(id, knownServers, knownSkills)
	}
	for _, id := range v.manifest.Workflows {
		v.validateWorkflowFrontmatter(id)
	}
	for _, id := range v.manifest.Skills {
		v.validateSkillFrontmatter(id)
	}
	// Prompts are intentionally skipped: PromptFrontmatter has no Validate()
	// method because prompts are opaque text blobs.
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
	if !domain.HasFrontmatterPrefix(raw) {
		v.addFinding(relPath, FindingCategoryFrontmatter, FindingSeverityError, "missing YAML frontmatter")
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

func (v *packValidator) validateManifestFrontmatter(cat domain.PackCategory, id string, target frontmatterValidator) (string, []byte, bool) {
	relPath := v.manifest.RelPath(cat, id)
	fmBytes, ok := v.validateContentFrontmatter(
		filepath.Join(v.packRoot, filepath.FromSlash(relPath)), relPath, id, target)
	return relPath, fmBytes, ok
}

func (v *packValidator) validateRuleFrontmatter(id string) {
	var fm domain.RuleFrontmatter
	v.validateManifestFrontmatter(domain.CategoryRules, id, &fm)
}

func (v *packValidator) validateAgentFrontmatter(id string, knownServers, knownSkills map[string]struct{}) {
	var fm domain.AgentFrontmatter
	relPath, fmBytes, ok := v.validateManifestFrontmatter(domain.CategoryAgents, id, &fm)
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

func (v *packValidator) validateWorkflowFrontmatter(id string) {
	var fm domain.WorkflowFrontmatter
	v.validateManifestFrontmatter(domain.CategoryWorkflows, id, &fm)
}

func (v *packValidator) validateSkillFrontmatter(id string) {
	var fm domain.SkillFrontmatter
	v.validateManifestFrontmatter(domain.CategorySkills, id, &fm)
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
