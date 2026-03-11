package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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
	findings []string
}

func ValidatePackRoot(packRoot string) []string {
	root := strings.TrimSpace(packRoot)
	if root == "" {
		return []string{"pack root must be set"}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return []string{err.Error()}
	}
	v := packValidator{root: absRoot, policy: defaultPackValidationPolicy}
	v.validateManifestAndInventory()
	v.walkPackFiles()
	sort.Strings(v.findings)
	return v.findings
}

func (v *packValidator) validateManifestAndInventory() {
	manifestPath := filepath.Join(v.root, "pack.json")
	st, err := os.Stat(manifestPath)
	if err != nil || !st.Mode().IsRegular() {
		v.findings = append(v.findings, fmt.Sprintf("pack.json not found in %s", v.root))
		return
	}
	manifest, err := LoadPackManifest(manifestPath)
	if err != nil {
		v.findings = append(v.findings, fmt.Sprintf("pack.json: %v", err))
		return
	}
	resolvedRoot := ResolvePackRoot(manifestPath, manifest.Root)
	if resolvedRoot == "" {
		v.findings = append(v.findings, fmt.Sprintf("pack %q root could not be resolved", manifest.Name))
		return
	}
	if err := validatePackInventory(manifest.Name, resolvedRoot, manifest); err != nil {
		v.findings = append(v.findings, err.Error())
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
		v.addFinding(rel, "forbidden .env file")
		return
	}
	for _, snippet := range v.policy.forbiddenPathSnippets {
		if strings.Contains(rel, snippet) {
			v.addFinding(rel, fmt.Sprintf("filename contains forbidden snippet '%s'", snippet))
			break
		}
	}
	b, err := os.ReadFile(fullPath)
	if err != nil {
		return
	}
	if !domain.HasFrontmatterPrefix(b) {
		if _, _, ok := domain.MatchPrimaryContentFile(rel); ok {
			v.addFinding(rel, "missing YAML frontmatter")
		}
	}
	if v.policy.shouldSkipSecretScan(rel) {
		return
	}
	text := string(b)
	if pattern, ok := v.policy.firstMatchingSecretPattern(text); ok {
		v.addFinding(rel, fmt.Sprintf("matches secret pattern '%s'", pattern))
	}
}

func (v *packValidator) addFinding(rel string, message string) {
	v.findings = append(v.findings, fmt.Sprintf("%s: %s", rel, message))
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
