package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/update"
	"github.com/shrug-labs/aipack/internal/util"
)

// DoctorSchemaVersion is the doctor report format version.
const DoctorSchemaVersion = 1

// CheckResult is a single check outcome in a doctor report.
type CheckResult struct {
	Name        string         `json:"name"`
	OK          bool           `json:"ok"`
	Status      string         `json:"status"` // pass|fail|skip|warn|fixed
	Severity    string         `json:"severity"`
	Message     string         `json:"message,omitempty"`
	Remediation string         `json:"remediation,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	Fixed       bool           `json:"fixed,omitempty"`
	FixAction   string         `json:"fix_action,omitempty"`
}

// DoctorReport is the full doctor output.
type DoctorReport struct {
	SchemaVersion int              `json:"schema_version"`
	OK            bool             `json:"ok"`
	Status        string           `json:"status"` // ok|fail
	ProfilePath   string           `json:"profile_path,omitempty"`
	Checks        []CheckResult    `json:"checks"`
	Ecosystem     *EcosystemStatus `json:"ecosystem,omitempty"`
}

// EcosystemStatus summarizes the resolved profile, packs, and content vectors.
type EcosystemStatus struct {
	Profile        string       `json:"profile"`
	ProfilePath    string       `json:"profile_path"`
	ConfigDir      string       `json:"config_dir"`
	Packs          []PackStatus `json:"packs"`
	TotalRules     int          `json:"total_rules"`
	TotalAgents    int          `json:"total_agents"`
	TotalWorkflows int          `json:"total_workflows"`
	TotalSkills    int          `json:"total_skills"`
	TotalMCP       int          `json:"total_mcp_servers"`
	SettingsPack   string       `json:"settings_pack,omitempty"`
}

// PackStatus describes a single pack's content contribution.
type PackStatus struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	Rules      int    `json:"rules"`
	Agents     int    `json:"agents"`
	Workflows  int    `json:"workflows"`
	Skills     int    `json:"skills"`
	MCPServers int    `json:"mcp_servers"`
	Settings   bool   `json:"settings"`
}

// PackInfo describes a resolved pack for doctor output.
type PackInfo struct {
	Name         string   `json:"name"`
	ManifestPath string   `json:"manifest_path"`
	PackRoot     string   `json:"pack_root"`
	MCPServers   []string `json:"mcp_servers"`
}

// ServerProvider describes the origin of an MCP server entry.
type ServerProvider struct {
	Name          string `json:"name"`
	Pack          string `json:"pack"`
	PackRoot      string `json:"pack_root"`
	InventoryPath string `json:"inventory_path"`
}

// DoctorRequest holds the inputs for RunDoctor.
type DoctorRequest struct {
	ConfigDir   string
	ProfilePath string
	ProfileName string
	Home        string // $HOME — threaded explicitly for testability
	Status      bool   // populate Ecosystem in the report
	Fix         bool   // auto-fix safe issues
	Version     string // current CLI version for update check
}

// RunDoctor executes all doctor diagnostic checks and returns a report.
func RunDoctor(req DoctorRequest) (rep DoctorReport) {
	rep = DoctorReport{SchemaVersion: DoctorSchemaVersion, OK: false, Status: "fail", Checks: []CheckResult{}}
	add := func(cr CheckResult) {
		rep.Checks = append(rep.Checks, cr)
	}

	// sync-config
	syncCheck := CheckResult{Name: "sync_config_loaded", Severity: "critical", Status: "fail", OK: false}
	configDir, syncCfgPath, syncCfg, syncErr := doctorLoadSyncConfig(req.ConfigDir, req.Home)
	if syncErr != nil {
		syncCheck.Message = syncErr.Error()
		syncCheck.Remediation = "Set HOME or pass --config-dir, then ensure sync-config.yaml exists"
		add(syncCheck)
		add(doctorSkippedCheck("profile_loaded", "sync-config not loaded"))
		add(doctorSkippedCheck("packs_resolved", "sync-config not loaded"))
		add(doctorSkippedCheck("mcp_env_vars_present", "sync-config not loaded"))
		add(doctorSkippedCheck("mcp_server_paths_exist", "sync-config not loaded"))
		return rep
	}
	syncCheck.OK = true
	syncCheck.Status = "pass"
	syncCheck.Message = "sync-config loaded"
	syncCheck.Details = map[string]any{"config_dir": configDir, "path": syncCfgPath}
	add(syncCheck)

	// CLI update check (informational, warning-only) — run async so the HTTP
	// call doesn't block subsequent file-based checks.
	updateIdx := len(rep.Checks)
	rep.Checks = append(rep.Checks, CheckResult{}) // placeholder
	updateCh := make(chan CheckResult, 1)
	go func() { updateCh <- doctorCheckUpdate(req.Version, configDir) }()
	defer func() {
		select {
		case result := <-updateCh:
			rep.Checks[updateIdx] = result
		case <-time.After(5 * time.Second):
			rep.Checks[updateIdx] = CheckResult{
				Name: "cli_update", Severity: "warning", Status: "skip", OK: true,
				Message: "skipped: update check timed out",
			}
		}
	}()

	// git availability (warning-only — registry fetch and pack install need git)
	add(doctorCheckGit())

	// unregistered packs (warning-only, does not block subsequent checks)
	add(doctorCheckUnregisteredPacks(configDir, syncCfg))

	// pack version drift (warning-only)
	add(doctorCheckPackDrift(configDir, syncCfg))

	// profile
	profileCheck := CheckResult{Name: "profile_loaded", Severity: "critical", Status: "fail", OK: false}
	profileName := strings.TrimSpace(req.ProfileName)
	if profileName == "" {
		profileName = strings.TrimSpace(syncCfg.Defaults.Profile)
	}
	if profileName == "" {
		profileName = "default"
	}
	pp, err := config.ResolveProfilePath(req.ProfilePath, configDir, profileName, req.Home)
	if err != nil {
		profileCheck.Message = err.Error()
		profileCheck.Remediation = "Pass --profile-path or ensure HOME is set and the requested profile exists"
		add(profileCheck)
		add(doctorSkippedCheck("packs_resolved", "profile not loaded"))
		add(doctorSkippedCheck("mcp_env_vars_present", "profile not loaded"))
		add(doctorSkippedCheck("mcp_server_paths_exist", "profile not loaded"))
		return rep
	}
	prof, err := config.LoadProfile(pp)
	if err != nil {
		profileCheck.Message = err.Error()
		profileCheck.Remediation = "Fix the profile YAML or pass a different --profile-path"
		profileCheck.Details = map[string]any{"profile_path": pp}
		add(profileCheck)
		add(doctorSkippedCheck("packs_resolved", "profile not loaded"))
		add(doctorSkippedCheck("mcp_env_vars_present", "profile not loaded"))
		add(doctorSkippedCheck("mcp_server_paths_exist", "profile not loaded"))
		return rep
	}
	profileCheck.OK = true
	profileCheck.Status = "pass"
	profileCheck.Message = "profile loaded"
	profileCheck.Details = map[string]any{"profile": profileName, "profile_path": pp}
	add(profileCheck)
	rep.ProfilePath = pp

	// Profile structure validation (warning-level).
	add(doctorCheckProfileValidation(prof))

	// packs + MCP inventory — reuse config.ResolveProfile + engine.LoadMCPInventoryForPacks
	packsCheck := CheckResult{Name: "packs_resolved", Severity: "critical", Status: "fail", OK: false}
	resolvedPacks, settingsPack, rcErr := config.ResolveProfile(prof, pp, configDir)
	if rcErr != nil {
		packsCheck.Message = rcErr.Error()
		packsCheck.Remediation = "Fix profile sources/packs and ensure all referenced pack.json and mcp inventory files exist locally (URL sources must already be cached)"
		add(packsCheck)
		add(doctorSkippedCheck("mcp_env_vars_present", "packs not resolved"))
		add(doctorSkippedCheck("mcp_server_paths_exist", "packs not resolved"))
		return rep
	}
	inventories, invErr := engine.LoadMCPInventoryForPacks(resolvedPacks)
	if invErr != nil {
		packsCheck.Message = invErr.Error()
		packsCheck.Remediation = "Fix profile sources/packs and ensure all referenced pack.json and mcp inventory files exist locally (URL sources must already be cached)"
		add(packsCheck)
		add(doctorSkippedCheck("mcp_env_vars_present", "packs not resolved"))
		add(doctorSkippedCheck("mcp_server_paths_exist", "packs not resolved"))
		return rep
	}
	packInfos, providers := doctorBuildPackInfoAndProviders(resolvedPacks, inventories, configDir)
	providersList := make([]ServerProvider, 0, len(providers))
	serverNames := make([]string, 0, len(providers))
	for name, p := range providers {
		serverNames = append(serverNames, name)
		providersList = append(providersList, p)
	}
	sort.Strings(serverNames)
	sort.Slice(providersList, func(i, j int) bool { return providersList[i].Name < providersList[j].Name })
	packsCheck.OK = true
	packsCheck.Status = "pass"
	packsCheck.Message = "packs resolved"
	packsCheck.Details = map[string]any{"packs": packInfos, "enabled_mcp_servers": serverNames, "mcp_server_providers": providersList}
	add(packsCheck)

	if req.Status {
		rep.Ecosystem = BuildEcosystemStatus(resolvedPacks, settingsPack, profileName, pp, configDir)
	}

	// required refs (params + env vars)
	refsCheck := CheckResult{Name: "mcp_refs_present", Severity: "critical", Status: "fail", OK: false}
	missing, requiredBy := doctorRequiredMCPRefs(prof.Params, inventories, serverNames)
	if len(missing) > 0 {
		refsCheck.Message = "missing required refs for enabled MCP servers"
		refsCheck.Remediation = "Set missing env vars (export VAR=...) or add missing params to the profile"
		refsCheck.Details = map[string]any{"missing": missing, "required_by": requiredBy}
		add(refsCheck)
		add(doctorSkippedCheck("mcp_server_paths_exist", "missing required refs"))
		return rep
	}
	refsCheck.OK = true
	refsCheck.Status = "pass"
	refsCheck.Message = "required MCP refs present"
	refsCheck.Details = map[string]any{"required": sortedMapKeys(requiredBy)}
	add(refsCheck)

	// MCP server path checks
	pathsCheck := CheckResult{Name: "mcp_server_paths_exist", Severity: "critical", Status: "fail", OK: false}
	failures := doctorCheckMCPServerPaths(inventories, prof.Params, []string{"bitbucket", "atlassian"}, providers)
	if len(failures) > 0 {
		pathsCheck.Message = "MCP server paths missing"
		pathsCheck.Remediation = "Ensure the MCP server command exists (install missing tool or clone/build the MCP server repo to the expected path)"
		pathsCheck.Details = map[string]any{"failures": failures}
		add(pathsCheck)
		return rep
	}
	pathsCheck.OK = true
	pathsCheck.Status = "pass"
	pathsCheck.Message = "MCP server paths OK"
	add(pathsCheck)

	// Ledger health checks (warning-level, fixable).
	add(doctorCheckLedgerHealth(configDir, resolvedPacks, req.Fix))

	// Stale ledger check: warn about ledger files for the other scope.
	add(doctorCheckStaleLedgers(configDir, syncCfg))

	// Manifest/disk drift check (warning-level, fixable).
	add(doctorCheckManifestDrift(configDir, resolvedPacks, req.Fix))

	// Recompute overall OK: only critical failures set OK=false.
	rep.OK = true
	rep.Status = "ok"
	for _, c := range rep.Checks {
		if !c.OK && c.Severity == "critical" {
			rep.OK = false
			rep.Status = "fail"
			break
		}
	}
	return rep
}

// doctorCheckUpdate checks if a newer CLI version is available.
func doctorCheckUpdate(currentVersion, configDir string) CheckResult {
	check := CheckResult{Name: "cli_update", Severity: "warning", Status: "pass", OK: true}
	r := update.Check(currentVersion, configDir)
	if r == nil {
		check.Message = "up to date"
		return check
	}
	check.Status = "warn"
	check.OK = false
	check.Message = fmt.Sprintf("newer version available: %s (current: %s)", r.Latest, r.Current)
	check.Remediation = "Run: brew upgrade aipack"
	if r.UpdateURL != "" {
		check.Details = map[string]any{"update_url": r.UpdateURL}
	}
	return check
}

// doctorCheckGit verifies git is available. Registry fetch and pack install
// require git for cloning, and on macOS git requires Xcode Command Line Tools.
func doctorCheckGit() CheckResult {
	check := CheckResult{Name: "git_available", Severity: "warning", Status: "pass", OK: true}
	if err := config.CheckGit(); err != nil {
		check.Status = "warn"
		check.OK = false
		check.Message = err.Error()
		check.Remediation = "Install git to enable registry fetch and pack install from git URLs"
		return check
	}
	check.Message = "git available"
	return check
}

func doctorSkippedCheck(name string, reason string) CheckResult {
	return CheckResult{Name: name, Severity: "critical", Status: "skip", OK: false, Message: "skipped: " + reason}
}

func doctorLoadSyncConfig(configDirFlag string, home string) (string, string, config.SyncConfig, error) {
	configDir := strings.TrimSpace(configDirFlag)
	if configDir == "" {
		d, err := config.DefaultConfigDir(home)
		if err != nil {
			return "", "", config.SyncConfig{}, fmt.Errorf("HOME is not set; pass --config-dir")
		}
		configDir = d
	}
	if !filepath.IsAbs(configDir) {
		abs, err := filepath.Abs(configDir)
		if err != nil {
			return "", "", config.SyncConfig{}, err
		}
		configDir = abs
	}
	path := config.SyncConfigPath(configDir)
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", config.SyncConfig{}, fmt.Errorf("sync-config missing: %s", path)
		}
		return "", "", config.SyncConfig{}, err
	}
	if !st.Mode().IsRegular() {
		return "", "", config.SyncConfig{}, fmt.Errorf("sync-config is not a file: %s", path)
	}
	cfg, err := config.LoadSyncConfig(path)
	if err != nil {
		return "", "", config.SyncConfig{}, err
	}
	return configDir, path, cfg, nil
}

// doctorBuildPackInfoAndProviders constructs PackInfo and ServerProvider slices
// from resolved packs and an MCP inventory map.
func doctorBuildPackInfoAndProviders(packs []config.ResolvedPack, inventory map[string]domain.MCPServer, configDir string) ([]PackInfo, map[string]ServerProvider) {
	var packInfos []PackInfo
	providers := map[string]ServerProvider{}

	for _, rp := range packs {
		manifestPath := filepath.Join(configDir, "packs", rp.Name, "pack.json")
		mcpNames := make([]string, 0, len(rp.MCP))
		for name := range rp.MCP {
			mcpNames = append(mcpNames, name)
		}
		sort.Strings(mcpNames)
		packInfos = append(packInfos, PackInfo{
			Name:         rp.Name,
			ManifestPath: manifestPath,
			PackRoot:     rp.Root,
			MCPServers:   mcpNames,
		})
		for _, name := range mcpNames {
			if _, ok := inventory[name]; !ok {
				continue
			}
			invPath := filepath.Join(rp.Root, "mcp", name+".json")
			providers[name] = ServerProvider{Name: name, Pack: rp.Name, PackRoot: rp.Root, InventoryPath: invPath}
		}
	}
	return packInfos, providers
}

// doctorRequiredMCPRefs scans enabled MCP servers for {params.*} and {env:VAR}
// references, checks which are available, and returns missing ones.
func doctorRequiredMCPRefs(params map[string]string, inv map[string]domain.MCPServer, enabledServers []string) ([]string, map[string][]string) {
	requiredBy := map[string]map[string]struct{}{}
	addReq := func(ref string, server string) {
		if ref == "" {
			return
		}
		m, ok := requiredBy[ref]
		if !ok {
			m = map[string]struct{}{}
			requiredBy[ref] = m
		}
		m[server] = struct{}{}
	}

	// Scan a string for all unresolved param and env references.
	scanRefs := func(s string, server string) {
		_ = util.WalkParamRefs(s, func(ref util.ParamRef) error {
			if _, ok := params[ref.Name]; !ok {
				addReq("param:"+ref.Name, server)
			}
			return nil
		})
		_ = util.WalkEnvRefs(s, func(ref util.EnvRef) error {
			addReq("env:"+ref.Name, server)
			return nil
		})
	}

	for _, server := range enabledServers {
		entry, ok := inv[server]
		if !ok {
			continue
		}
		for _, part := range entry.Command {
			scanRefs(part, server)
		}
		for _, v := range entry.Env {
			scanRefs(v, server)
		}
		if entry.URL != "" {
			scanRefs(entry.URL, server)
		}
		for _, v := range entry.Headers {
			scanRefs(v, server)
		}
	}

	// Check which refs are actually missing.
	missing := []string{}
	flat := map[string][]string{}
	keys := make([]string, 0, len(requiredBy))
	for k := range requiredBy {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		servers := make([]string, 0, len(requiredBy[k]))
		for s := range requiredBy[k] {
			servers = append(servers, s)
		}
		sort.Strings(servers)
		flat[k] = servers

		if strings.HasPrefix(k, "param:") {
			// Already confirmed missing during scan (only added when not in params map).
			missing = append(missing, k)
		} else if strings.HasPrefix(k, "env:") {
			envName := strings.TrimPrefix(k, "env:")
			if strings.TrimSpace(os.Getenv(envName)) == "" {
				missing = append(missing, k)
			}
		}
	}
	return missing, flat
}

func doctorCheckMCPServerPaths(inv map[string]domain.MCPServer, params map[string]string, servers []string, providers map[string]ServerProvider) []map[string]any {
	failures := []map[string]any{}
	for _, server := range servers {
		entry, ok := inv[server]
		if !ok {
			continue
		}
		prov, _ := providers[server]
		if len(entry.Command) == 0 {
			failures = append(failures, map[string]any{"server": server, "error": "missing command", "inventory_path": prov.InventoryPath})
			continue
		}

		cmd0, err := engine.ExpandRefs(params, entry.Command[0])
		if err != nil {
			failures = append(failures, map[string]any{"server": server, "error": err.Error(), "inventory_path": prov.InventoryPath})
			continue
		}
		if filepath.IsAbs(cmd0) {
			if _, err := os.Stat(cmd0); err != nil {
				failures = append(failures, map[string]any{"server": server, "path": cmd0, "error": err.Error(), "inventory_path": prov.InventoryPath})
				continue
			}
		} else {
			if _, err := exec.LookPath(cmd0); err != nil {
				failures = append(failures, map[string]any{"server": server, "executable": cmd0, "error": "not found in PATH", "inventory_path": prov.InventoryPath})
				continue
			}
		}

		for _, raw := range entry.Command[1:] {
			part, err := engine.ExpandRefs(params, raw)
			if err != nil {
				failures = append(failures, map[string]any{"server": server, "error": err.Error(), "inventory_path": prov.InventoryPath})
				continue
			}
			if strings.HasPrefix(part, "http://") || strings.HasPrefix(part, "https://") {
				continue
			}
			if !filepath.IsAbs(part) && !strings.HasPrefix(part, "./") && !strings.HasPrefix(part, "../") {
				continue
			}
			if _, err := os.Stat(part); err != nil {
				failures = append(failures, map[string]any{"server": server, "path": part, "error": err.Error(), "inventory_path": prov.InventoryPath})
			}
		}
	}
	sort.Slice(failures, func(i, j int) bool {
		is, _ := failures[i]["server"].(string)
		js, _ := failures[j]["server"].(string)
		if is != js {
			return is < js
		}
		pi, _ := failures[i]["path"].(string)
		pj, _ := failures[j]["path"].(string)
		return pi < pj
	})
	return failures
}

// doctorCheckUnregisteredPacks warns about pack directories that exist in
// configDir/packs/ but are not tracked in sync-config's installed_packs map.
func doctorCheckUnregisteredPacks(configDir string, syncCfg config.SyncConfig) CheckResult {
	check := CheckResult{Name: "packs_registered", Severity: "warning", Status: "pass", OK: true}

	packsDir := PacksDir(configDir)
	entries, err := os.ReadDir(packsDir)
	if err != nil {
		if os.IsNotExist(err) {
			check.Message = "no packs directory"
			return check
		}
		check.Status = "warn"
		check.OK = false
		check.Message = fmt.Sprintf("cannot read packs directory: %s", err)
		return check
	}

	var unregistered []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !e.IsDir() {
			continue
		}
		if _, ok := syncCfg.InstalledPacks[name]; !ok {
			unregistered = append(unregistered, name)
		}
	}
	sort.Strings(unregistered)

	if len(unregistered) > 0 {
		check.Status = "warn"
		check.OK = false
		check.Message = fmt.Sprintf("%d pack(s) in packs/ not in installed_packs", len(unregistered))
		check.Remediation = "Run 'aipack pack install' to register, or remove unused pack directories"
		check.Details = map[string]any{"unregistered": unregistered}
	} else {
		check.Message = "all packs registered"
	}

	return check
}

// PackDrift describes version drift for a single pack.
type PackDrift struct {
	Name             string `json:"name"`
	Method           string `json:"method"`
	InstalledVersion string `json:"installed_version"`
	OriginVersion    string `json:"origin_version,omitempty"`
	InstalledHash    string `json:"installed_hash,omitempty"`
	CurrentHash      string `json:"current_hash,omitempty"`
	Reason           string `json:"reason"`
}

// doctorCheckPackDrift compares installed pack versions/hashes against their
// origins using local filesystem reads only (no network).
func doctorCheckPackDrift(configDir string, syncCfg config.SyncConfig) CheckResult {
	check := CheckResult{Name: "pack_version_drift", Severity: "warning", Status: "pass", OK: true}

	packsDir := PacksDir(configDir)
	var drifted []PackDrift

	for name, meta := range syncCfg.InstalledPacks {
		packDir := filepath.Join(packsDir, name)

		switch meta.Method {
		case config.MethodClone:
			// Compare recorded commit hash against current git HEAD in pack dir.
			if meta.CommitHash == "" {
				continue
			}
			head, err := gitHeadHash(packDir)
			if err != nil {
				continue
			}
			if head != meta.CommitHash {
				drifted = append(drifted, PackDrift{
					Name:          name,
					Method:        meta.Method,
					InstalledHash: meta.CommitHash[:min(len(meta.CommitHash), 8)],
					CurrentHash:   head[:min(len(head), 8)],
					Reason:        "git HEAD differs from recorded commit_hash",
				})
			}
		case config.MethodCopy:
			// Compare installed version against origin's pack.json version (local paths only).
			if meta.Origin == "" || !filepath.IsAbs(meta.Origin) {
				continue
			}
			installedManifest, err := config.LoadPackManifest(filepath.Join(packDir, "pack.json"))
			if err != nil {
				continue // pack not readable, other checks will catch this
			}
			originManifestPath := filepath.Join(meta.Origin, "pack.json")
			if meta.SubPath != "" {
				originManifestPath = filepath.Join(meta.Origin, meta.SubPath, "pack.json")
			}
			originManifest, err := config.LoadPackManifest(originManifestPath)
			if err != nil {
				continue // origin not accessible
			}
			if installedManifest.Version != "" && originManifest.Version != "" && installedManifest.Version != originManifest.Version {
				drifted = append(drifted, PackDrift{
					Name:             name,
					Method:           meta.Method,
					InstalledVersion: installedManifest.Version,
					OriginVersion:    originManifest.Version,
					Reason:           "installed version differs from origin",
				})
			}
		}
		// link: no drift possible (symlink points to source)
	}

	sort.Slice(drifted, func(i, j int) bool { return drifted[i].Name < drifted[j].Name })

	if len(drifted) > 0 {
		check.Status = "warn"
		check.OK = false
		check.Message = fmt.Sprintf("%d pack(s) have version drift", len(drifted))
		check.Remediation = "Run 'aipack pack update' to refresh, or reinstall with 'aipack pack install'"
		check.Details = map[string]any{"drifted": drifted}
	} else {
		check.Message = "no version drift detected"
	}
	return check
}

// gitHeadHash reads the current HEAD commit hash from a git repo without shelling out.
// Handles both loose refs (.git/refs/heads/...) and packed refs (.git/packed-refs).
func gitHeadHash(dir string) (string, error) {
	headPath := filepath.Join(dir, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(string(data))
	// Detached HEAD: raw hash
	if !strings.HasPrefix(content, "ref: ") {
		return content, nil
	}
	// Symbolic ref: try the loose ref file first.
	ref := strings.TrimPrefix(content, "ref: ")
	refPath := filepath.Join(dir, ".git", ref)
	data, err = os.ReadFile(refPath)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	// Loose ref missing — check packed-refs (created by git gc / git pack-refs).
	packedPath := filepath.Join(dir, ".git", "packed-refs")
	packed, err := os.ReadFile(packedPath)
	if err != nil {
		return "", fmt.Errorf("ref %s not found in loose refs or packed-refs", ref)
	}
	for _, line := range strings.Split(string(packed), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 && parts[1] == ref {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("ref %s not found in packed-refs", ref)
}

// BuildEcosystemStatus constructs an EcosystemStatus summary from resolved packs.
func BuildEcosystemStatus(packs []config.ResolvedPack, settingsPack, profileName, profilePath, configDir string) *EcosystemStatus {
	es := &EcosystemStatus{
		Profile:      profileName,
		ProfilePath:  profilePath,
		ConfigDir:    configDir,
		SettingsPack: settingsPack,
	}
	for _, rp := range packs {
		ps := PackStatus{
			Name:       rp.Name,
			Version:    rp.Manifest.Version,
			Rules:      len(rp.Rules),
			Agents:     len(rp.Agents),
			Workflows:  len(rp.Workflows),
			Skills:     len(rp.Skills),
			MCPServers: len(rp.MCP),
			Settings:   rp.Name == settingsPack,
		}
		es.TotalRules += ps.Rules
		es.TotalAgents += ps.Agents
		es.TotalWorkflows += ps.Workflows
		es.TotalSkills += ps.Skills
		es.TotalMCP += ps.MCPServers
		es.Packs = append(es.Packs, ps)
	}
	return es
}

// ---------------------------------------------------------------------------
// Ledger health check
// ---------------------------------------------------------------------------

func ledgerFilesUnder(configDir string) ([]string, error) {
	ledgerDir := filepath.Join(configDir, "ledger")
	var files []string
	err := filepath.WalkDir(ledgerDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// doctorCheckLedgerHealth scans all ledger files for orphaned entries (paths
// that no longer exist on disk) and entries with missing SourcePack. With fix=true,
// prunes orphans and fills SourcePack when a single pack is resolved.
func doctorCheckLedgerHealth(configDir string, packs []config.ResolvedPack, fix bool) CheckResult {
	check := CheckResult{Name: "ledger_health", Severity: "warning", Status: "pass", OK: true}

	files, err := ledgerFilesUnder(configDir)
	if err != nil {
		if os.IsNotExist(err) {
			check.Message = "no ledger directory"
			return check
		}
		check.Status = "warn"
		check.OK = false
		check.Message = fmt.Sprintf("cannot read ledger directory: %s", err)
		return check
	}

	// Determine the single pack name for SourcePack fill (if unambiguous).
	singlePack := ""
	if len(packs) == 1 {
		singlePack = packs[0].Name
	}

	totalOrphaned := 0
	totalMissingSP := 0
	totalFixed := 0

	for _, path := range files {
		lg, warn, lerr := engine.LoadLedger(path)
		if lerr != nil || warn != "" {
			continue
		}

		modified := false
		fileFixes := 0
		for k, entry := range lg.Managed {
			// Check for orphaned entries.
			if _, serr := os.Lstat(k); os.IsNotExist(serr) {
				totalOrphaned++
				if fix {
					delete(lg.Managed, k)
					modified = true
					fileFixes++
				}
				continue
			}
			// Check for missing SourcePack.
			if entry.SourcePack == "" {
				totalMissingSP++
				if fix && singlePack != "" {
					entry.SourcePack = singlePack
					lg.Managed[k] = entry
					modified = true
					fileFixes++
				}
			}
		}
		if fix && modified {
			if serr := engine.SaveLedger(path, lg, false); serr != nil {
				fileFixes = 0 // save failed: none of this file's fixes persisted
			}
		}
		totalFixed += fileFixes
	}

	issues := totalOrphaned + totalMissingSP
	if issues == 0 {
		check.Message = "ledger entries healthy"
		return check
	}

	check.Status = "warn"
	check.OK = false
	check.Details = map[string]any{}

	parts := []string{}
	if totalOrphaned > 0 {
		parts = append(parts, fmt.Sprintf("%d orphaned entries", totalOrphaned))
		check.Details["orphaned"] = totalOrphaned
	}
	if totalMissingSP > 0 {
		parts = append(parts, fmt.Sprintf("%d missing SourcePack", totalMissingSP))
		check.Details["missing_source_pack"] = totalMissingSP
	}
	check.Message = strings.Join(parts, ", ")

	if fix && totalFixed > 0 {
		check.Fixed = true
		check.FixAction = fmt.Sprintf("fixed %d entries", totalFixed)
		check.Status = "fixed"
		check.OK = true
	} else {
		check.Remediation = "Run 'aipack doctor --fix' to auto-repair, or 'aipack sync' to rebuild the ledger"
	}
	return check
}

// ---------------------------------------------------------------------------
// Manifest / disk drift check
// ---------------------------------------------------------------------------

// doctorCheckManifestDrift compares each pack's manifest-declared content
// against what actually exists on disk. Reports files on disk not in the
// manifest ("undeclared") and manifest entries with no corresponding file
// ("missing"). With fix=true, updates each pack's pack.json to match disk
// (adds undeclared IDs, removes missing IDs).
type driftItem struct {
	Pack      string `json:"pack"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	DriftType string `json:"drift_type"` // "undeclared" or "missing"
}

func doctorCheckManifestDrift(_ string, packs []config.ResolvedPack, fix bool) CheckResult {
	check := CheckResult{Name: "manifest_drift", Severity: "warning", Status: "pass", OK: true}

	var drifts []driftItem

	for _, rp := range packs {
		packRoot := rp.Root
		manifest := rp.Manifest

		// Check each content vector.
		type vectorCheck struct {
			kind     string
			dir      string
			suffix   string
			declared []string
		}
		checks := []vectorCheck{
			{"rules", filepath.Join(packRoot, "rules"), ".md", manifest.Rules},
			{"agents", filepath.Join(packRoot, "agents"), ".md", manifest.Agents},
			{"workflows", filepath.Join(packRoot, "workflows"), ".md", manifest.Workflows},
		}

		for _, vc := range checks {
			onDisk, err := config.DiscoverIDs(vc.dir, vc.suffix)
			if err != nil {
				continue
			}
			drifts = appendDrift(drifts, rp.Name, vc.kind, onDisk, vc.declared)
		}

		// Skills.
		onDiskSkills, err := config.DiscoverSkills(filepath.Join(packRoot, "skills"))
		if err == nil {
			drifts = appendDrift(drifts, rp.Name, "skills", onDiskSkills, manifest.Skills)
		}
	}

	if len(drifts) == 0 {
		check.Message = "no manifest drift detected"
		return check
	}

	check.Status = "warn"
	check.OK = false

	undeclared := 0
	missing := 0
	for _, d := range drifts {
		if d.DriftType == "undeclared" {
			undeclared++
		} else {
			missing++
		}
	}

	parts := []string{}
	if undeclared > 0 {
		parts = append(parts, fmt.Sprintf("%d on disk but not in manifest", undeclared))
	}
	if missing > 0 {
		parts = append(parts, fmt.Sprintf("%d in manifest but not on disk", missing))
	}
	check.Message = strings.Join(parts, ", ")
	check.Details = map[string]any{"drift": drifts}

	if fix {
		totalFixed := fixManifestDrift(packs, drifts)
		if totalFixed > 0 {
			check.Fixed = true
			check.FixAction = fmt.Sprintf("updated %d pack manifest(s)", totalFixed)
			check.Status = "fixed"
			check.OK = true
			return check
		}
	}

	check.Remediation = "Run 'aipack doctor --fix' to update pack.json manifests, or remove nil content fields to enable auto-discovery"
	return check
}

// fixManifestDrift applies drift fixes to pack manifests: adds undeclared IDs
// and removes missing IDs. Returns the number of packs whose manifests were
// successfully updated.
func fixManifestDrift(packs []config.ResolvedPack, drifts []driftItem) int {
	// Index packs by name for lookup.
	packByName := make(map[string]config.ResolvedPack, len(packs))
	for _, rp := range packs {
		packByName[rp.Name] = rp
	}

	// Group drifts by pack.
	driftsByPack := make(map[string][]driftItem)
	for _, d := range drifts {
		driftsByPack[d.Pack] = append(driftsByPack[d.Pack], d)
	}

	totalFixed := 0
	for packName, items := range driftsByPack {
		rp, ok := packByName[packName]
		if !ok {
			continue
		}
		manifest := rp.Manifest

		for _, d := range items {
			cat := kindToCategory(d.Kind)
			if cat == "" {
				continue
			}
			ptr := manifest.ContentIDsPtr(cat)
			if ptr == nil {
				continue
			}
			switch d.DriftType {
			case "undeclared":
				*ptr = append(*ptr, d.ID)
			case "missing":
				*ptr = slices.DeleteFunc(*ptr, func(s string) bool { return s == d.ID })
			}
		}

		// Sort each slice for deterministic output.
		sort.Strings(manifest.Rules)
		sort.Strings(manifest.Agents)
		sort.Strings(manifest.Workflows)
		sort.Strings(manifest.Skills)

		manifestPath := filepath.Join(rp.Root, "pack.json")
		if err := config.SavePackManifest(manifestPath, manifest); err == nil {
			totalFixed++
		}
	}
	return totalFixed
}

func kindToCategory(kind string) domain.PackCategory {
	cat := domain.PackCategory(kind)
	switch cat {
	case domain.CategoryRules, domain.CategoryAgents, domain.CategoryWorkflows, domain.CategorySkills:
		return cat
	default:
		return ""
	}
}

func toStringSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}

// appendDrift compares onDisk vs declared IDs and appends drift items.
func appendDrift(drifts []driftItem, pack, kind string, onDisk, declared []string) []driftItem {
	declaredSet := toStringSet(declared)
	diskSet := toStringSet(onDisk)
	for _, id := range onDisk {
		if _, ok := declaredSet[id]; !ok {
			drifts = append(drifts, driftItem{Pack: pack, Kind: kind, ID: id, DriftType: "undeclared"})
		}
	}
	for _, id := range declared {
		if _, ok := diskSet[id]; !ok {
			drifts = append(drifts, driftItem{Pack: pack, Kind: kind, ID: id, DriftType: "missing"})
		}
	}
	return drifts
}

func doctorCheckProfileValidation(prof config.ProfileConfig) CheckResult {
	check := CheckResult{Name: "profile_validated", Severity: "warning", Status: "pass", OK: true}
	errs := config.ValidateProfileConfig(prof)
	if len(errs) > 0 {
		check.Status = "warn"
		check.OK = false
		check.Message = fmt.Sprintf("%d profile validation issue(s)", len(errs))
		check.Remediation = "Fix the issues in the profile YAML"
		check.Details = map[string]any{"issues": errs}
		return check
	}
	check.Message = "profile structure valid"
	return check
}

// doctorCheckStaleLedgers warns about ledger files that may be orphaned from
// a previous scope configuration. For example, if the user switched from
// project scope to global scope, old project ledgers remain on disk.
func doctorCheckStaleLedgers(configDir string, syncCfg config.SyncConfig) CheckResult {
	check := CheckResult{Name: "stale_ledgers", Severity: "warning", Status: "pass", OK: true}

	ledgerDir := filepath.Join(configDir, "ledger")
	files, err := ledgerFilesUnder(configDir)
	if err != nil {
		if os.IsNotExist(err) {
			check.Message = "no ledger directory"
			return check
		}
		check.Status = "warn"
		check.OK = false
		check.Message = fmt.Sprintf("cannot read ledger directory: %s", err)
		return check
	}

	// Build expected per-harness ledger filenames.
	currentHarnesses := syncCfg.Defaults.Harnesses
	if len(currentHarnesses) == 0 {
		check.Message = "no harnesses configured"
		return check
	}
	expected := map[string]struct{}{}
	for _, h := range currentHarnesses {
		expected[strings.ToLower(h)+".json"] = struct{}{}
	}
	scope := strings.TrimSpace(syncCfg.Defaults.Scope)

	var stale []string
	for _, path := range files {
		rel, rerr := filepath.Rel(ledgerDir, path)
		if rerr != nil {
			continue
		}
		base := filepath.Base(path)
		_, expectedName := expected[base]
		isProjectLedger := strings.Contains(rel, string(filepath.Separator))

		if scope == string(domain.ScopeProject) {
			if isProjectLedger {
				continue
			}
		} else if !isProjectLedger && expectedName {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		age := fmt.Sprintf("%.0f days old", time.Since(info.ModTime()).Hours()/24)
		stale = append(stale, rel+" ("+age+", "+path+")")
	}

	if len(stale) > 0 {
		check.Status = "warn"
		check.OK = false
		check.Message = fmt.Sprintf("%d ledger file(s) not matching current harness config", len(stale))
		check.Remediation = "These may be from a previous harness configuration. Remove them if no longer needed."
		check.Details = map[string]any{"stale": stale}
	} else {
		check.Message = "no stale ledgers"
	}
	return check
}
