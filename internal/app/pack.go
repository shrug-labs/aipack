package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/util"

	"gopkg.in/yaml.v3"
)

// PackAddRequest holds the inputs for installing a pack.
type PackAddRequest struct {
	// PackPath is the local path to the pack directory (mutually exclusive with URL).
	PackPath string
	// URL is a git-accessible repository or pack.json URL to clone/install from (mutually exclusive with PackPath).
	URL string
	// ConfigDir is the config directory (e.g. ~/.config/aipack).
	ConfigDir string
	// Name overrides the pack name from pack.json.
	Name string
	// Link creates a symlink instead of copying (path only; ignored for URL).
	Link bool
	// Register adds a source + pack entry to the active profile.
	Register bool
	// Profile is the profile to register in (defaults to sync-config's defaults.profile).
	Profile string

	// Seed enables auto-seeding of bundled registries and profiles for remote
	// installs. When false (default for URL installs), seeding candidates are
	// printed but not applied. Local path installs always seed regardless.
	Seed bool

	// SubPath is the subdirectory within a cloned repo where pack.json lives.
	// Set when installing from a registry entry that specifies a path.
	SubPath string
	// Ref is a git ref (branch/tag) to checkout after cloning.
	// When set alongside URL, bypasses ProbePackURL and clones directly.
	Ref string

	// Test injection points:
	RunGitFn  func(args ...string) error
	ArchiveFn func(repoURL, ref string, paths []string) ([]byte, error) // nil = config.GitArchiveFiles
	URLOKFn   func(raw string) (bool, error)
	NowFn     func() time.Time
	GitHashFn func(dir string) (string, error) // nil = config.GitHeadHash
}

// PacksDir returns the canonical pack installation directory.
func PacksDir(configDir string) string {
	return filepath.Join(configDir, "packs")
}

// PackAdd installs a pack to the canonical location and optionally registers it in a profile.
func PackAdd(req PackAddRequest, stdout io.Writer) error {
	if strings.TrimSpace(req.ConfigDir) == "" {
		return fmt.Errorf("config dir is required")
	}
	if req.URL != "" && req.PackPath != "" {
		return fmt.Errorf("--url and path argument are mutually exclusive")
	}
	if req.URL == "" && strings.TrimSpace(req.PackPath) == "" {
		return fmt.Errorf("either a path argument or --url is required")
	}

	// Validate profile exists early, before doing any work.
	if req.Register {
		profile := packProfileName(req.Profile)
		profilePath := filepath.Join(req.ConfigDir, "profiles", profile+".yaml")
		if _, err := os.Stat(profilePath); os.IsNotExist(err) {
			return fmt.Errorf("profile %q does not exist at %s (run 'aipack init' first)", profile, profilePath)
		}
	}

	if req.URL != "" {
		return packAddFromURL(req, stdout)
	}
	return packAddFromPath(req, stdout)
}

// packAddFromPath implements the local-path install flow (existing behavior + origin recording).
func packAddFromPath(req PackAddRequest, stdout io.Writer) error {
	packPath, err := filepath.Abs(req.PackPath)
	if err != nil {
		return fmt.Errorf("resolving pack path: %w", err)
	}

	// Locate pack.json — accept either the directory or a direct path to pack.json.
	packDir := packPath
	manifestPath := filepath.Join(packPath, "pack.json")
	if st, err := os.Stat(packPath); err == nil && !st.IsDir() {
		if filepath.Base(packPath) == "pack.json" {
			manifestPath = packPath
			packDir = filepath.Dir(packPath)
		} else {
			return fmt.Errorf("pack path is a file but not pack.json: %s", packPath)
		}
	}

	manifest, err := config.LoadPackManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("loading pack manifest: %w", err)
	}

	name, err := resolvePackName(req.Name, manifest.Name)
	if err != nil {
		return err
	}

	packsDir := PacksDir(req.ConfigDir)
	destDir := filepath.Join(packsDir, name)

	if err := os.MkdirAll(packsDir, 0o700); err != nil {
		return fmt.Errorf("creating packs directory: %w", err)
	}

	method := config.MethodCopy
	if req.Link {
		method = config.MethodLink
		packRemoveExisting(destDir, stdout)
		if err := os.Symlink(packDir, destDir); err != nil {
			return fmt.Errorf("creating symlink: %w", err)
		}
		fmt.Fprintf(stdout, "Linked: %s -> %s\n", destDir, packDir)
	} else {
		// Copy to a temp dir first, then atomic rename to prevent partial state.
		tmpDir, err := os.MkdirTemp(packsDir, ".copy-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		success := false
		defer func() {
			if !success {
				os.RemoveAll(tmpDir)
			}
		}()
		if err := util.CopyDir(packDir, tmpDir); err != nil {
			return fmt.Errorf("copying pack: %w", err)
		}
		packRemoveExisting(destDir, stdout)
		if err := os.Rename(tmpDir, destDir); err != nil {
			return fmt.Errorf("moving pack to %s: %w", destDir, err)
		}
		success = true
		fmt.Fprintf(stdout, "Copied: %s -> %s\n", packDir, destDir)
	}

	now := time.Now()
	if req.NowFn != nil {
		now = req.NowFn()
	}
	if err := packRecordOrigin(req.ConfigDir, name, config.InstalledPackMeta{
		Origin: packDir, Method: method, InstalledAt: now.UTC().Format(time.RFC3339),
	}); err != nil {
		fmt.Fprintf(stdout, "Warning: failed to record pack origin: %v\n", err)
	}

	// Record content integrity hashes for copy installs.
	if method == config.MethodCopy {
		if _, err := saveIntegrity(destDir); err != nil {
			fmt.Fprintf(stdout, "Warning: failed to record integrity: %v\n", err)
		}
	}

	packSeedRegistry(req.ConfigDir, destDir, stdout)
	packSeedProfiles(req.ConfigDir, destDir, manifest.Profiles, stdout)

	if req.Register {
		if err := PackRegister(req.ConfigDir, packProfileName(req.Profile), name, stdout); err != nil {
			return fmt.Errorf("registering pack in profile: %w", err)
		}
	}

	return nil
}

// packWarnMCPServers prints a prominent warning when a pack defines MCP servers.
func packWarnMCPServers(manifest config.PackManifest, stdout io.Writer) {
	servers := make([]string, 0, len(manifest.MCP.Servers))
	for name := range manifest.MCP.Servers {
		servers = append(servers, name)
	}
	if len(servers) == 0 {
		return
	}
	sort.Strings(servers)
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "WARNING: This pack defines MCP servers (external tool access):")
	for _, s := range servers {
		tools := manifest.MCP.Servers[s].DefaultAllowedTools
		noun := "tools"
		if len(tools) == 1 {
			noun = "tool"
		}
		fmt.Fprintf(stdout, "  %s (%d %s)\n", s, len(tools), noun)
	}
	fmt.Fprintln(stdout, "Review MCP server definitions before running 'aipack sync'.")
	fmt.Fprintln(stdout, "")
}

// packAddFromURL implements the URL-based install flow.
// Tries git archive (selective fetch) first; falls back to git clone if the
// remote does not support git archive --remote.
func packAddFromURL(req PackAddRequest, stdout io.Writer) error {
	// Resolve URL info: for SSH/git URLs or when SubPath/Ref is pre-set,
	// skip the HTTP probe (ProbePackURL) and go directly to git operations.
	var info config.PackURLInfo
	if req.SubPath != "" || req.Ref != "" || config.IsGitURL(req.URL, "") {
		info = config.PackURLInfo{RepoURL: req.URL, Ref: req.Ref, SubPath: req.SubPath}
	} else {
		var err error
		info, err = config.ProbePackURL(req.URL)
		if err != nil {
			return fmt.Errorf("probing URL: %w", err)
		}

		// Validate pack.json is accessible if we have a direct URL for it.
		if info.PackURL != "" {
			urlOKFn := config.URLOK
			if req.URLOKFn != nil {
				urlOKFn = req.URLOKFn
			}
			ok, err := urlOKFn(info.PackURL)
			if err != nil {
				return fmt.Errorf("pack.json check failed for %s: %w", info.PackURL, err)
			}
			if !ok {
				return fmt.Errorf("pack.json not found at %s", info.PackURL)
			}
		}
	}
	packsDir := PacksDir(req.ConfigDir)
	if err := os.MkdirAll(packsDir, 0o700); err != nil {
		return fmt.Errorf("creating packs directory: %w", err)
	}

	// Capture pre-install integrity so we can show a diff when replacing.
	var oldIntegrity IntegrityManifest
	if req.Name != "" {
		oldIntegrity, _ = loadIntegrity(filepath.Join(packsDir, req.Name))
	}

	// Try archive-based install first; fall back to clone.
	result, err := packTryArchive(req, info, packsDir, stdout)
	if err != nil {
		return err
	}

	now := time.Now()
	if req.NowFn != nil {
		now = req.NowFn()
	}

	name := result.name
	if err := packRecordOrigin(req.ConfigDir, name, config.InstalledPackMeta{
		Origin: req.URL, Method: result.method, InstalledAt: now.UTC().Format(time.RFC3339),
		Ref: info.Ref, SubPath: info.SubPath, CommitHash: result.commitHash,
	}); err != nil {
		fmt.Fprintf(stdout, "Warning: failed to record pack origin: %v\n", err)
	}

	packWarnMCPServers(result.manifest, stdout)

	// Record content integrity and show diff when replacing an existing pack.
	_, changed, _ := saveAndDiffIntegrity(result.destDir, oldIntegrity, stdout)
	if len(oldIntegrity.Files) > 0 && !changed {
		fmt.Fprintf(stdout, "Content unchanged.\n")
	}

	if req.Seed {
		packSeedRegistry(req.ConfigDir, result.destDir, stdout)
		packSeedProfiles(req.ConfigDir, result.destDir, result.manifest.Profiles, stdout)
	} else {
		packPreviewSeeding(result.destDir, result.manifest.Profiles, stdout)
	}

	if req.Register {
		if err := PackRegister(req.ConfigDir, packProfileName(req.Profile), name, stdout); err != nil {
			return fmt.Errorf("registering pack in profile: %w", err)
		}
	}

	return nil
}

// packInstallResult holds the output of a remote install operation.
type packInstallResult struct {
	name       string
	destDir    string
	method     string
	manifest   config.PackManifest
	commitHash string
}

// packTryArchive attempts a two-phase archive fetch. On success returns the
// installed pack directory, install method, and parsed manifest. Falls back
// to git clone if the remote doesn't support git archive --remote.
func packTryArchive(req PackAddRequest, info config.PackURLInfo, packsDir string, stdout io.Writer) (packInstallResult, error) {
	subPath := info.SubPath
	archiveFn := req.ArchiveFn
	if archiveFn == nil {
		if req.RunGitFn != nil {
			// Custom git runner without an archive function — caller wants
			// clone-only behavior (common in tests). Skip archive to avoid
			// calling the real git binary.
			return packFallbackClone(req, info, packsDir, stdout)
		}
		archiveFn = config.GitArchiveFiles
	}

	// Phase 1: fetch only pack.json via archive.
	manifestRelPath := "pack.json"
	if subPath != "" {
		manifestRelPath = subPath + "/pack.json"
	}

	tarData, err := archiveFn(info.RepoURL, info.Ref, []string{manifestRelPath})
	if err != nil {
		if errors.Is(err, config.ErrArchiveNotSupported) {
			fmt.Fprintf(stdout, "Remote does not support git archive; falling back to clone\n")
			return packFallbackClone(req, info, packsDir, stdout)
		}
		return packInstallResult{}, fmt.Errorf("fetching manifest from %s: %w", info.RepoURL, err)
	}

	// Extract pack.json from the tar stream to parse manifest.
	manifest, err := parseManifestFromTar(tarData, manifestRelPath)
	if err != nil {
		return packInstallResult{}, fmt.Errorf("parsing manifest from archive: %w", err)
	}

	name, err := resolvePackName(req.Name, manifest.Name)
	if err != nil {
		return packInstallResult{}, err
	}

	// Phase 2: compute content paths and fetch all declared files.
	contentPaths := manifest.ContentPaths()
	// Prepend subPath prefix if the pack lives in a subdirectory.
	if subPath != "" {
		for i, p := range contentPaths {
			contentPaths[i] = subPath + "/" + p
		}
	}

	tarData, err = archiveFn(info.RepoURL, info.Ref, contentPaths)
	if err != nil {
		if errors.Is(err, config.ErrArchiveNotSupported) {
			fmt.Fprintf(stdout, "Remote does not support git archive for content; falling back to clone\n")
			return packFallbackClone(req, info, packsDir, stdout)
		}
		if errors.Is(err, config.ErrArchivePathNotFound) {
			return packInstallResult{}, fmt.Errorf("pack.json declares content not found in the repository — check that all listed rules/skills/workflows are committed: %w", err)
		}
		return packInstallResult{}, fmt.Errorf("fetching pack content from %s: %w", info.RepoURL, err)
	}

	// Extract into temp dir with safety validation.
	tmpDir, err := os.MkdirTemp(packsDir, ".archive-*")
	if err != nil {
		return packInstallResult{}, fmt.Errorf("creating temp dir: %w", err)
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	if err := config.ExtractArchive(bytes.NewReader(tarData), tmpDir, config.ArchiveOpts{}); err != nil {
		return packInstallResult{}, fmt.Errorf("extracting pack archive: %w", err)
	}

	// For subdirectory packs, the extracted content is under tmpDir/<subPath>/.
	// Move it up to become the pack root.
	packRoot := tmpDir
	if subPath != "" {
		subTmp, err := extractSubtree(packsDir, tmpDir, subPath)
		if err != nil {
			return packInstallResult{}, err
		}
		os.RemoveAll(tmpDir)
		tmpDir = subTmp
		packRoot = tmpDir
	}

	// Verify pack.json exists in the extracted content.
	if _, err := os.Stat(filepath.Join(packRoot, "pack.json")); err != nil {
		return packInstallResult{}, fmt.Errorf("pack.json not found in extracted archive")
	}

	destDir := filepath.Join(packsDir, name)
	packRemoveExisting(destDir, stdout)

	if err := os.Rename(tmpDir, destDir); err != nil {
		return packInstallResult{}, fmt.Errorf("moving archive to %s: %w", destDir, err)
	}
	success = true
	fmt.Fprintf(stdout, "Installed: %s -> %s\n", req.URL, destDir)

	return packInstallResult{name: name, destDir: destDir, method: config.MethodArchive, manifest: manifest}, nil
}

// packFallbackClone implements the legacy git-clone install path, used when
// git archive --remote is not supported by the remote.
func packFallbackClone(req PackAddRequest, info config.PackURLInfo, packsDir string, stdout io.Writer) (packInstallResult, error) {
	subPath := info.SubPath
	tmpDir, err := os.MkdirTemp(packsDir, ".clone-*")
	if err != nil {
		return packInstallResult{}, fmt.Errorf("creating temp dir: %w", err)
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	if req.RunGitFn != nil {
		if err := config.EnsureCloneWith(info.RepoURL, tmpDir, info.Ref, req.RunGitFn); err != nil {
			return packInstallResult{}, fmt.Errorf("cloning %s: %w", info.RepoURL, err)
		}
	} else {
		if err := config.EnsureClone(info.RepoURL, tmpDir, info.Ref); err != nil {
			return packInstallResult{}, fmt.Errorf("cloning %s: %w", info.RepoURL, err)
		}
	}

	// Capture commit hash before any subtree extraction destroys .git.
	commitHash := resolveGitHash(tmpDir, req.GitHashFn)

	packRoot := tmpDir
	if subPath != "" {
		packRoot = filepath.Join(tmpDir, subPath)
	}

	manifestPath := filepath.Join(packRoot, "pack.json")
	manifest, err := config.LoadPackManifest(manifestPath)
	if err != nil {
		return packInstallResult{}, fmt.Errorf("loading pack manifest from clone: %w", err)
	}

	name, err := resolvePackName(req.Name, manifest.Name)
	if err != nil {
		return packInstallResult{}, err
	}

	if subPath != "" {
		subTmp, err := extractSubtree(packsDir, tmpDir, subPath)
		if err != nil {
			return packInstallResult{}, err
		}
		os.RemoveAll(tmpDir)
		tmpDir = subTmp
	}

	destDir := filepath.Join(packsDir, name)
	packRemoveExisting(destDir, stdout)

	if err := os.Rename(tmpDir, destDir); err != nil {
		return packInstallResult{}, fmt.Errorf("moving clone to %s: %w", destDir, err)
	}
	success = true
	fmt.Fprintf(stdout, "Cloned: %s -> %s\n", req.URL, destDir)

	return packInstallResult{name: name, destDir: destDir, method: config.MethodClone, manifest: manifest, commitHash: commitHash}, nil
}

// parseManifestFromTar extracts and parses pack.json from tar data.
// The expectedPath is the relative path within the tar (e.g. "pack.json"
// or "subdir/pack.json").
func parseManifestFromTar(tarData []byte, expectedPath string) (config.PackManifest, error) {
	data, err := config.ExtractSingleFileFromTar(tarData, expectedPath)
	if err != nil {
		return config.PackManifest{}, err
	}
	return config.ParsePackManifest(data)
}

// packProfileName returns the trimmed profile name, defaulting to "default".
func packProfileName(raw string) string {
	if p := strings.TrimSpace(raw); p != "" {
		return p
	}
	return "default"
}

// validatePackName rejects names containing path traversal sequences,
// path separators, or null bytes.
func validatePackName(name string) error {
	if strings.Contains(name, "..") || strings.Contains(name, "/") ||
		strings.Contains(name, "\\") || strings.Contains(name, "\x00") {
		return fmt.Errorf("invalid pack name %q: must not contain path separators or traversal sequences", name)
	}
	return nil
}

// resolvePackName returns reqName if non-empty, else manifestName, else an error.
func resolvePackName(reqName, manifestName string) (string, error) {
	if n := strings.TrimSpace(reqName); n != "" {
		if err := validatePackName(n); err != nil {
			return "", err
		}
		return n, nil
	}
	if manifestName != "" {
		if err := validatePackName(manifestName); err != nil {
			return "", err
		}
		return manifestName, nil
	}
	return "", fmt.Errorf("pack has no name and --name was not provided")
}

// inferInstallMethod returns MethodLink or MethodCopy based on the file mode.
func inferInstallMethod(mode os.FileMode) string {
	if mode&os.ModeSymlink != 0 {
		return config.MethodLink
	}
	return config.MethodCopy
}

// packRemoveExisting removes an already-installed pack at destDir, printing what it replaces.
func packRemoveExisting(destDir string, stdout io.Writer) {
	if st, err := os.Lstat(destDir); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(destDir)
			fmt.Fprintf(stdout, "Replacing existing link: %s -> %s\n", destDir, target)
		} else if st.IsDir() {
			fmt.Fprintf(stdout, "Replacing existing copy: %s\n", destDir)
		}
		os.RemoveAll(destDir)
	}
}

// PackList returns all installed packs. It delegates to PackListDetailed,
// which populates content inventory fields (Rules, Agents, etc.) as well.
func PackList(configDir string) ([]PackShowEntry, error) {
	return PackListDetailed(configDir)
}

// PackRemove uninstalls a pack and deregisters it from all profiles.
func PackRemove(configDir string, name string, stdout io.Writer) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("pack name is required")
	}
	packsDir := PacksDir(configDir)
	destDir := filepath.Join(packsDir, name)

	if _, err := os.Lstat(destDir); os.IsNotExist(err) {
		return fmt.Errorf("pack %q is not installed", name)
	}

	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("removing pack: %w", err)
	}
	fmt.Fprintf(stdout, "Removed: %s\n", destDir)

	// Best-effort origin cleanup.
	_ = packClearOrigin(configDir, name)

	// Best-effort deregister from all profiles.
	packDeregisterFromAllProfiles(configDir, name, stdout)

	return nil
}

// packDeregisterFromAllProfiles removes source and pack entries for a given pack
// name from every profile YAML in configDir/profiles/.
func packDeregisterFromAllProfiles(configDir, packName string, stdout io.Writer) {
	profilesDir := filepath.Join(configDir, "profiles")
	names, err := config.ListProfileNames(profilesDir)
	if err != nil {
		return
	}
	for _, name := range names {
		profilePath := filepath.Join(profilesDir, name+".yaml")
		if packDeregister(profilePath, packName) {
			fmt.Fprintf(stdout, "Deregistered pack %q from profile %q\n", packName, name)
		}
	}
}

// packDeregister removes pack entries matching packName from a single profile file.
// Returns true if the profile was modified.
func packDeregister(profilePath, packName string) bool {
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return false
	}

	modified := false

	// Remove matching pack entries.
	filteredPacks := cfg.Packs[:0]
	for _, p := range cfg.Packs {
		if p.Name == packName {
			modified = true
			continue
		}
		filteredPacks = append(filteredPacks, p)
	}
	cfg.Packs = filteredPacks

	if !modified {
		return false
	}

	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return false
	}
	_ = util.WriteFileAtomicWithPerms(profilePath, out, 0o700, 0o600)
	return true
}

// extractSubtree copies the subtree at cloneDir/subPath into a new temp dir
// under parentDir. Returns the temp dir path. Caller is responsible for
// cleanup on error.
func extractSubtree(parentDir, cloneDir, subPath string) (string, error) {
	src := filepath.Join(cloneDir, subPath)
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("sub-path %q not found in clone: %w", subPath, err)
	}
	tmp, err := os.MkdirTemp(parentDir, ".subdir-*")
	if err != nil {
		return "", err
	}
	if err := util.CopyDir(src, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("copying pack subtree: %w", err)
	}
	return tmp, nil
}

// packSeedRegistry copies a registry.yaml bundled in the installed pack to
// the user's config directory if no registry exists there yet.
func packSeedRegistry(configDir, packDir string, stdout io.Writer) {
	src := filepath.Join(packDir, "registry.yaml")
	if !util.PathExists(src) {
		return // no bundled registry
	}
	dest := filepath.Join(configDir, "registry.yaml")
	if util.PathExists(dest) {
		// Registry already exists — merge new entries rather than overwriting.
		packMergeRegistry(src, dest, stdout)
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	if err := util.WriteFileAtomicWithPerms(dest, data, 0o700, 0o600); err != nil {
		fmt.Fprintf(stdout, "Warning: failed to seed registry: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Seeded registry from pack: %s\n", dest)
}

// packSeedProfiles copies profile YAML files from the installed pack to
// configDir/profiles/ if they don't already exist there.
func packSeedProfiles(configDir, packDir string, profiles []string, stdout io.Writer) {
	if len(profiles) == 0 {
		return
	}
	profilesDir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		fmt.Fprintf(stdout, "Warning: failed to create profiles dir: %v\n", err)
		return
	}
	for _, relPath := range profiles {
		src := filepath.Join(packDir, relPath)
		base := filepath.Base(relPath)
		name := strings.TrimSuffix(base, filepath.Ext(base))
		dest := filepath.Join(profilesDir, base)
		if _, err := os.Stat(dest); err == nil {
			continue // never overwrite
		}
		data, err := os.ReadFile(src)
		if err != nil {
			fmt.Fprintf(stdout, "Warning: failed to read seed profile %s: %v\n", relPath, err)
			continue
		}
		if err := util.WriteFileAtomicWithPerms(dest, data, 0o700, 0o600); err != nil {
			fmt.Fprintf(stdout, "Warning: failed to write seed profile %s: %v\n", dest, err)
			continue
		}
		fmt.Fprintf(stdout, "Seeded profile %q from pack\n", name)
		fmt.Fprintf(stdout, "  To activate: aipack profile set %s\n", name)
	}
}

// packPreviewSeeding prints what would be seeded without applying changes.
// Used for remote installs when --seed is not specified.
func packPreviewSeeding(packDir string, profiles []string, stdout io.Writer) {
	// Collect candidates first, then print once.
	var lines []string
	if util.PathExists(filepath.Join(packDir, "registry.yaml")) {
		lines = append(lines, "  registry: registry.yaml")
	}
	for _, relPath := range profiles {
		if util.PathExists(filepath.Join(packDir, relPath)) {
			lines = append(lines, fmt.Sprintf("  profile:  %s", relPath))
		}
	}
	if len(lines) > 0 {
		fmt.Fprintln(stdout, "This pack bundles registry/profile content (use --seed to apply):")
		for _, l := range lines {
			fmt.Fprintln(stdout, l)
		}
	}
}

// packMergeRegistry merges entries from src into dest, adding only packs
// that don't already exist in dest.
func packMergeRegistry(src, dest string, stdout io.Writer) {
	srcReg, err := config.LoadRegistry(src)
	if err != nil {
		return
	}
	destReg, err := config.LoadRegistry(dest)
	if err != nil {
		return
	}
	added := 0
	for name, entry := range srcReg.Packs {
		if _, exists := destReg.Packs[name]; !exists {
			destReg.Packs[name] = entry
			added++
		}
	}
	if added == 0 {
		return
	}
	out, err := yaml.Marshal(&destReg)
	if err != nil {
		return
	}
	if err := util.WriteFileAtomicWithPerms(dest, out, 0o700, 0o600); err != nil {
		fmt.Fprintf(stdout, "Warning: failed to merge registry entries: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Merged %d new pack(s) into registry: %s\n", added, dest)
}

// packRecordOrigin saves the install metadata for a pack in sync-config.
func packRecordOrigin(configDir, name string, meta config.InstalledPackMeta) error {
	scPath := config.SyncConfigPath(configDir)
	sc, err := config.LoadSyncConfig(scPath)
	if err != nil {
		return err
	}
	if sc.InstalledPacks == nil {
		sc.InstalledPacks = make(map[string]config.InstalledPackMeta)
	}
	sc.InstalledPacks[name] = meta
	return config.SaveSyncConfig(scPath, sc)
}

// packClearOrigin removes the install metadata for a pack from sync-config.
func packClearOrigin(configDir, name string) error {
	scPath := config.SyncConfigPath(configDir)
	sc, err := config.LoadSyncConfig(scPath)
	if err != nil {
		return err
	}
	if sc.InstalledPacks == nil {
		return nil
	}
	delete(sc.InstalledPacks, name)
	return config.SaveSyncConfig(scPath, sc)
}

// PackRegister adds a pack entry to the named profile.
func PackRegister(configDir string, profileName string, packName string, stdout io.Writer) error {
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")

	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("profile %q does not exist at %s (run 'aipack init' first)", profileName, profilePath)
		}
		return fmt.Errorf("parsing profile: %w", err)
	}

	// Check if pack already exists.
	packExists := false
	for _, p := range cfg.Packs {
		if p.Name == packName {
			packExists = true
			break
		}
	}
	if !packExists {
		enabled := true
		cfg.Packs = append(cfg.Packs, config.PackEntry{
			Name:    packName,
			Enabled: &enabled,
		})
	}

	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshalling profile: %w", err)
	}

	if err := util.WriteFileAtomicWithPerms(profilePath, out, 0o700, 0o600); err != nil {
		return err
	}

	if !packExists {
		fmt.Fprintf(stdout, "Registered pack %q in profile %q\n", packName, profileName)
	} else {
		fmt.Fprintf(stdout, "Pack %q already registered in profile %q\n", packName, profileName)
	}
	return nil
}

// PackDeregister removes a pack entry from the named profile.
func PackDeregister(configDir string, profileName string, packName string, stdout io.Writer) error {
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")

	if packDeregister(profilePath, packName) {
		fmt.Fprintf(stdout, "Removed pack %q from profile %q\n", packName, profileName)
		return nil
	}

	// Check if profile exists at all.
	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		return fmt.Errorf("profile %q does not exist at %s", profileName, profilePath)
	}

	fmt.Fprintf(stdout, "Pack %q not found in profile %q\n", packName, profileName)
	return nil
}

// PackUpdateRequest holds the inputs for updating pack(s).
type PackUpdateRequest struct {
	ConfigDir string
	Name      string // empty when All=true
	All       bool
	RunGitFn  func(args ...string) error                                // test injection; nil = real git
	NowFn     func() time.Time                                          // test injection; nil = time.Now
	GitHashFn func(dir string) (string, error)                          // test injection; nil = config.GitHeadHash
	ArchiveFn func(repoURL, ref string, paths []string) ([]byte, error) // test injection; nil = config.GitArchiveFiles
}

// PackUpdateResult describes the outcome of updating a single pack.
type PackUpdateResult struct {
	Name       string `json:"name"`
	Method     string `json:"method"`
	Status     string `json:"status"` // "updated", "up-to-date", "skipped", "error"
	Message    string `json:"message"`
	CommitHash string `json:"commit_hash,omitempty"`
}

// PackShowEntry describes detailed information about an installed pack.
type PackShowEntry struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Path        string   `json:"path"`
	Method      string   `json:"method"`
	Origin      string   `json:"origin"`
	Ref         string   `json:"ref,omitempty"`
	CommitHash  string   `json:"commit_hash,omitempty"`
	InstalledAt string   `json:"installed_at,omitempty"`
	Rules       []string `json:"rules"`
	Agents      []string `json:"agents"`
	Workflows   []string `json:"workflows"`
	Skills      []string `json:"skills"`
	Prompts     []string `json:"prompts"`
	MCPServers  []string `json:"mcp_servers"`
}

// packUpdateContext holds resolved dependencies for updating packs.
// Built once per PackUpdate call via newPackUpdateContext.
type packUpdateContext struct {
	packsDir  string
	configDir string
	sc        config.SyncConfig
	registry  config.Registry // loaded once, reused across all packs
	runGitFn  func(args ...string) error
	nowFn     func() time.Time
	gitHashFn func(string) (string, error)
	archiveFn func(repoURL, ref string, paths []string) ([]byte, error)
	stdout    io.Writer
}

// newPackUpdateContext resolves defaults from the request and returns the
// dependency context used by packUpdateOne.
func newPackUpdateContext(req PackUpdateRequest, sc config.SyncConfig, stdout io.Writer) packUpdateContext {
	runGitFn := req.RunGitFn
	if runGitFn == nil {
		runGitFn = config.RunGit
	}
	nowFn := req.NowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	reg, _ := config.LoadMergedRegistry(req.ConfigDir)

	return packUpdateContext{
		packsDir:  PacksDir(req.ConfigDir),
		configDir: req.ConfigDir,
		sc:        sc,
		registry:  reg,
		runGitFn:  runGitFn,
		nowFn:     nowFn,
		gitHashFn: req.GitHashFn,
		archiveFn: req.ArchiveFn,
		stdout:    stdout,
	}
}

// PackUpdate refreshes one or all installed packs.
func PackUpdate(req PackUpdateRequest, stdout io.Writer) ([]PackUpdateResult, error) {
	if strings.TrimSpace(req.ConfigDir) == "" {
		return nil, fmt.Errorf("config dir is required")
	}

	scPath := config.SyncConfigPath(req.ConfigDir)
	sc, _ := config.LoadSyncConfig(scPath)
	ctx := newPackUpdateContext(req, sc, stdout)

	var names []string
	if req.All {
		entries, err := os.ReadDir(ctx.packsDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
				names = append(names, e.Name())
			}
		}
	} else {
		names = []string{req.Name}
	}

	var results []PackUpdateResult
	for _, name := range names {
		r := packUpdateOne(name, ctx)
		results = append(results, r)
	}
	return results, nil
}

func packUpdateOne(name string, ctx packUpdateContext) PackUpdateResult {
	packDir := filepath.Join(ctx.packsDir, name)
	meta, hasMeta := ctx.sc.InstalledPacks[name]

	info, err := os.Lstat(packDir)
	if err != nil {
		return PackUpdateResult{Name: name, Status: "error", Message: fmt.Sprintf("not installed: %v", err)}
	}

	method := meta.Method
	if method == "" {
		method = inferInstallMethod(info.Mode())
	}

	switch method {
	case config.MethodClone:
		ref := meta.Ref
		oldIntegrity, _ := loadIntegrity(packDir)
		// hashDir tracks which directory to read HEAD from (may differ for subpath packs).
		hashDir := packDir
		if meta.SubPath != "" {
			// Subdirectory pack: re-clone full repo, extract subtree.
			tmpDir, err := os.MkdirTemp(ctx.packsDir, ".clone-*")
			if err != nil {
				return PackUpdateResult{Name: name, Method: method, Status: "error", Message: err.Error()}
			}
			defer os.RemoveAll(tmpDir)
			if err := config.EnsureCloneWith(meta.Origin, tmpDir, ref, ctx.runGitFn); err != nil {
				return PackUpdateResult{Name: name, Method: method, Status: "error", Message: err.Error()}
			}
			hashDir = tmpDir // capture hash from full clone before subtree extraction
			subTmp, err := extractSubtree(ctx.packsDir, tmpDir, meta.SubPath)
			if err != nil {
				return PackUpdateResult{Name: name, Method: method, Status: "error", Message: err.Error()}
			}
			if err := os.RemoveAll(packDir); err != nil {
				os.RemoveAll(subTmp)
				return PackUpdateResult{Name: name, Method: method, Status: "error", Message: err.Error()}
			}
			if err := os.Rename(subTmp, packDir); err != nil {
				return PackUpdateResult{Name: name, Method: method, Status: "error", Message: err.Error()}
			}
		} else if ref != "" {
			if err := ctx.runGitFn("-C", packDir, "fetch", "--depth", "1", "origin", ref); err != nil {
				return PackUpdateResult{Name: name, Method: method, Status: "error", Message: err.Error()}
			}
			if err := ctx.runGitFn("-C", packDir, "checkout", ref); err != nil {
				return PackUpdateResult{Name: name, Method: method, Status: "error", Message: err.Error()}
			}
		} else {
			if err := ctx.runGitFn("-C", packDir, "pull", "--ff-only"); err != nil {
				return PackUpdateResult{Name: name, Method: method, Status: "error", Message: err.Error()}
			}
		}
		newHash := resolveGitHash(hashDir, ctx.gitHashFn)
		if newHash != "" && newHash == meta.CommitHash {
			fmt.Fprintf(ctx.stdout, "Up-to-date (clone): %s @ %s\n", name, shortHash(newHash))
			return PackUpdateResult{Name: name, Method: method, Status: "up-to-date", Message: "already at " + shortHash(newHash), CommitHash: newHash}
		}
		_ = packRecordOrigin(ctx.configDir, name, config.InstalledPackMeta{
			Origin: meta.Origin, Method: method, InstalledAt: ctx.nowFn().UTC().Format(time.RFC3339),
			Ref: ref, SubPath: meta.SubPath, CommitHash: newHash,
		})
		packSeedRegistry(ctx.configDir, packDir, ctx.stdout)
		_, _, _ = saveAndDiffIntegrity(packDir, oldIntegrity, ctx.stdout)
		msg := "pulled latest"
		if newHash != "" {
			msg = shortHash(newHash)
			if meta.CommitHash != "" {
				msg = shortHash(meta.CommitHash) + " -> " + shortHash(newHash)
			}
		}
		fmt.Fprintf(ctx.stdout, "Updated (clone): %s %s\n", name, msg)
		return PackUpdateResult{Name: name, Method: method, Status: "updated", Message: msg, CommitHash: newHash}

	case config.MethodCopy:
		origin := meta.Origin
		if origin == "" {
			return PackUpdateResult{Name: name, Method: method, Status: "skipped", Message: "no origin recorded; cannot re-copy"}
		}
		if _, err := os.Stat(origin); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: "error", Message: fmt.Sprintf("origin not found: %s", origin)}
		}
		oldIntegrity, _ := loadIntegrity(packDir)
		if err := os.RemoveAll(packDir); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: "error", Message: err.Error()}
		}
		if err := util.CopyDir(origin, packDir); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: "error", Message: err.Error()}
		}
		_ = packRecordOrigin(ctx.configDir, name, config.InstalledPackMeta{
			Origin: origin, Method: method, InstalledAt: ctx.nowFn().UTC().Format(time.RFC3339),
		})
		_, _, _ = saveAndDiffIntegrity(packDir, oldIntegrity, ctx.stdout)
		packSeedRegistry(ctx.configDir, packDir, ctx.stdout)
		fmt.Fprintf(ctx.stdout, "Updated (copy): %s from %s\n", name, origin)
		return PackUpdateResult{Name: name, Method: method, Status: "updated", Message: "re-copied from " + origin}

	case config.MethodArchive:
		origin := meta.Origin
		if origin == "" {
			return PackUpdateResult{Name: name, Method: method, Status: "skipped", Message: "no origin recorded; cannot re-fetch"}
		}

		// Re-resolve through the registry to pick up ref/origin changes
		// that happened after the pack was initially installed.
		ref := meta.Ref
		subPath := meta.SubPath
		if entry, ok := ctx.registry.Packs[name]; ok {
			if entry.Repo != "" {
				origin = entry.Repo
			}
			if entry.Ref != "" {
				ref = entry.Ref
			}
			if entry.Path != "" {
				subPath = entry.Path
			}
		}

		// Capture pre-update integrity for diff.
		oldIntegrity, _ := loadIntegrity(packDir)

		// Re-run the two-phase archive fetch (discard install chatter).
		archiveReq := PackAddRequest{
			URL:       origin,
			ConfigDir: ctx.configDir,
			Ref:       ref,
			SubPath:   subPath,
			Name:      name,
			ArchiveFn: ctx.archiveFn,
		}
		archiveInfo := config.PackURLInfo{RepoURL: origin, Ref: ref, SubPath: subPath}
		result, err := packTryArchive(archiveReq, archiveInfo, ctx.packsDir, io.Discard)
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: "error", Message: err.Error()}
		}

		_, changed, _ := saveAndDiffIntegrity(result.destDir, oldIntegrity, ctx.stdout)
		if !changed && len(oldIntegrity.Files) > 0 {
			fmt.Fprintf(ctx.stdout, "Up-to-date (archive): %s\n", name)
			return PackUpdateResult{Name: name, Method: method, Status: "up-to-date", Message: "content unchanged"}
		}

		_ = packRecordOrigin(ctx.configDir, name, config.InstalledPackMeta{
			Origin: origin, Method: method, InstalledAt: ctx.nowFn().UTC().Format(time.RFC3339),
			Ref: ref, SubPath: subPath, CommitHash: result.commitHash,
		})
		packSeedRegistry(ctx.configDir, result.destDir, ctx.stdout)
		fmt.Fprintf(ctx.stdout, "Updated (archive): %s from %s\n", name, origin)
		return PackUpdateResult{Name: name, Method: method, Status: "updated", Message: "re-fetched from " + origin}

	case config.MethodLink:
		target, err := os.Readlink(packDir)
		if err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: "error", Message: fmt.Sprintf("readlink: %v", err)}
		}
		if _, err := os.Stat(target); err != nil {
			return PackUpdateResult{Name: name, Method: method, Status: "error", Message: fmt.Sprintf("symlink target missing: %s", target)}
		}
		fmt.Fprintf(ctx.stdout, "OK (link): %s -> %s\n", name, target)
		return PackUpdateResult{Name: name, Method: method, Status: "up-to-date", Message: "symlink target exists"}

	default:
		if !hasMeta {
			return PackUpdateResult{Name: name, Method: method, Status: "skipped", Message: "no install metadata; cannot determine update method"}
		}
		return PackUpdateResult{Name: name, Method: method, Status: "skipped", Message: fmt.Sprintf("unknown method %q", method)}
	}
}

// PackShow returns detailed information about an installed pack.
func PackShow(configDir string, name string) (PackShowEntry, error) {
	if strings.TrimSpace(name) == "" {
		return PackShowEntry{}, fmt.Errorf("pack name is required")
	}
	packsDir := PacksDir(configDir)

	scPath := config.SyncConfigPath(configDir)
	sc, _ := config.LoadSyncConfig(scPath)

	return packShowCore(packsDir, name, sc.InstalledPacks)
}

// PackListDetailed returns detailed information for all installed packs,
// loading sync-config once instead of per-pack.
func PackListDetailed(configDir string) ([]PackShowEntry, error) {
	packsDir := PacksDir(configDir)
	entries, err := os.ReadDir(packsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	scPath := config.SyncConfigPath(configDir)
	sc, _ := config.LoadSyncConfig(scPath)

	var result []PackShowEntry
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		entry, err := packShowCore(packsDir, name, sc.InstalledPacks)
		if err != nil {
			continue
		}
		result = append(result, entry)
	}
	return result, nil
}

// packShowCore builds a PackShowEntry for a single pack using pre-loaded metadata.
func packShowCore(packsDir, name string, meta map[string]config.InstalledPackMeta) (PackShowEntry, error) {
	packDir := filepath.Join(packsDir, name)

	info, err := os.Lstat(packDir)
	if err != nil {
		if os.IsNotExist(err) {
			return PackShowEntry{}, fmt.Errorf("pack %q is not installed", name)
		}
		return PackShowEntry{}, fmt.Errorf("stat pack %q: %w", name, err)
	}

	entry := PackShowEntry{Name: name}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(packDir)
		if err == nil {
			entry.Path = target
		} else {
			entry.Path = packDir
		}
	} else {
		entry.Path = packDir
	}

	manifestPath := filepath.Join(packDir, "pack.json")
	if m, err := config.LoadPackManifest(manifestPath); err == nil {
		entry.Version = m.Version
		entry.Rules = m.Rules
		entry.Agents = m.Agents
		entry.Workflows = m.Workflows
		entry.Skills = m.Skills
		entry.Prompts = m.Prompts
		servers := make([]string, 0, len(m.MCP.Servers))
		for k := range m.MCP.Servers {
			servers = append(servers, k)
		}
		entry.MCPServers = servers
	}

	if m, ok := meta[name]; ok {
		entry.Origin = m.Origin
		entry.Method = m.Method
		entry.Ref = m.Ref
		entry.CommitHash = m.CommitHash
		entry.InstalledAt = m.InstalledAt
	}

	if entry.Method == "" {
		entry.Method = inferInstallMethod(info.Mode())
	}

	if entry.Rules == nil {
		entry.Rules = []string{}
	}
	if entry.Agents == nil {
		entry.Agents = []string{}
	}
	if entry.Workflows == nil {
		entry.Workflows = []string{}
	}
	if entry.Skills == nil {
		entry.Skills = []string{}
	}
	if entry.Prompts == nil {
		entry.Prompts = []string{}
	}
	if entry.MCPServers == nil {
		entry.MCPServers = []string{}
	}

	return entry, nil
}

// resolveGitHash returns the HEAD commit hash for dir, or "" if unavailable.
// Uses gitHashFn if non-nil, otherwise falls back to config.GitHeadHash.
func resolveGitHash(dir string, gitHashFn func(string) (string, error)) string {
	fn := gitHashFn
	if fn == nil {
		fn = config.GitHeadHash
	}
	h, err := fn(dir)
	if err != nil {
		return ""
	}
	return h
}

// shortHash returns the first 12 characters of a commit hash for display.
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// --- profile-aware pack install ---

// PackInstallMissingRequest holds the inputs for installing packs missing from a profile.
type PackInstallMissingRequest struct {
	ConfigDir   string
	ProfileName string

	// PackAddFn overrides PackAdd for testing. nil = use PackAdd.
	PackAddFn func(PackAddRequest, io.Writer) error
}

// PackInstallMissingResult describes the outcome for a single pack in the profile.
type PackInstallMissingResult struct {
	Pack   string // pack name from profile
	Status string // "installed", "present", "not-in-registry", "error"
	Detail string // install method or error message
}

// ProfileMissingPacks returns the names of enabled packs in a profile whose
// directories do not exist under configDir/packs/.
func ProfileMissingPacks(configDir, profileName string) ([]string, error) {
	profilePath := filepath.Join(configDir, "profiles", profileName+".yaml")
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return nil, fmt.Errorf("loading profile %q: %w", profileName, err)
	}

	packsDir := PacksDir(configDir)
	var missing []string
	for _, pe := range cfg.Packs {
		name := strings.TrimSpace(pe.Name)
		if name == "" {
			continue
		}
		// Skip disabled packs (nil Enabled = enabled).
		if pe.Enabled != nil && !*pe.Enabled {
			continue
		}
		packDir := filepath.Join(packsDir, name)
		if _, err := os.Stat(packDir); os.IsNotExist(err) {
			missing = append(missing, name)
		}
	}
	return missing, nil
}

// PackInstallMissing installs packs that a profile declares but that are not
// present on disk. Each missing pack is looked up in the merged registry and
// installed via PackAdd. Packs not found in the registry are reported but do
// not cause a failure.
func PackInstallMissing(req PackInstallMissingRequest, stdout io.Writer) ([]PackInstallMissingResult, error) {
	profilePath := filepath.Join(req.ConfigDir, "profiles", req.ProfileName+".yaml")
	cfg, err := config.LoadProfile(profilePath)
	if err != nil {
		return nil, fmt.Errorf("loading profile %q: %w", req.ProfileName, err)
	}

	packsDir := PacksDir(req.ConfigDir)
	regReq := RegistryListRequest{ConfigDir: req.ConfigDir}
	addFn := req.PackAddFn
	if addFn == nil {
		addFn = PackAdd
	}

	var results []PackInstallMissingResult
	for _, pe := range cfg.Packs {
		name := strings.TrimSpace(pe.Name)
		if name == "" {
			continue
		}
		if pe.Enabled != nil && !*pe.Enabled {
			continue
		}

		packDir := filepath.Join(packsDir, name)
		if _, err := os.Stat(packDir); err == nil {
			results = append(results, PackInstallMissingResult{
				Pack: name, Status: "present",
			})
			continue
		}

		// Look up in registry.
		entry, err := RegistryLookup(regReq, name)
		if err != nil {
			fmt.Fprintf(stdout, "  %s: not in registry — install manually or check 'aipack registry list'\n", name)
			results = append(results, PackInstallMissingResult{
				Pack: name, Status: "not-in-registry", Detail: err.Error(),
			})
			continue
		}

		// Install via PackAdd.
		addReq := PackAddRequest{
			ConfigDir: req.ConfigDir,
			URL:       entry.Repo,
			Ref:       entry.Ref,
			SubPath:   entry.Path,
			Name:      name,
			Register:  false, // already in the profile
		}
		if installErr := addFn(addReq, stdout); installErr != nil {
			fmt.Fprintf(stdout, "  %s: install failed: %v\n", name, installErr)
			results = append(results, PackInstallMissingResult{
				Pack: name, Status: "error", Detail: installErr.Error(),
			})
			continue
		}

		results = append(results, PackInstallMissingResult{
			Pack: name, Status: "installed",
		})
	}

	return results, nil
}
