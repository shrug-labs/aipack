package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

// DoctorSchemaVersion is the doctor report format version.
const DoctorSchemaVersion = 1

// CheckResult is a single check outcome in a doctor report.
type CheckResult struct {
	Name        string         `json:"name"`
	OK          bool           `json:"ok"`
	Status      string         `json:"status"` // pass|fail|skip
	Severity    string         `json:"severity"`
	Message     string         `json:"message,omitempty"`
	Remediation string         `json:"remediation,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
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
}

// RunDoctor executes all doctor diagnostic checks and returns a report.
func RunDoctor(req DoctorRequest) DoctorReport {
	rep := DoctorReport{SchemaVersion: DoctorSchemaVersion, OK: false, Status: "fail", Checks: []CheckResult{}}
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
		rep.Ecosystem = buildEcosystemStatus(resolvedPacks, settingsPack, profileName, pp, configDir)
	}

	// required env vars
	envCheck := CheckResult{Name: "mcp_env_vars_present", Severity: "critical", Status: "fail", OK: false}
	missing, requiredBy := doctorRequiredMCPEnvVars(prof.Params, inventories, serverNames)
	if len(missing) > 0 {
		envCheck.Message = "missing required env vars for enabled MCP servers"
		envCheck.Remediation = "Set the missing env vars (presence only is checked); for example: export VAR=..."
		envCheck.Details = map[string]any{"missing": missing, "required_by": requiredBy}
		add(envCheck)
		add(doctorSkippedCheck("mcp_server_paths_exist", "missing required env vars"))
		return rep
	}
	envCheck.OK = true
	envCheck.Status = "pass"
	envCheck.Message = "required MCP env vars present"
	envCheck.Details = map[string]any{"required": sortedMapKeys(requiredBy)}
	add(envCheck)

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

	rep.OK = true
	rep.Status = "ok"
	return rep
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

func doctorRequiredMCPEnvVars(params map[string]string, inv map[string]domain.MCPServer, enabledServers []string) ([]string, map[string][]string) {
	requiredBy := map[string]map[string]struct{}{}
	addReq := func(varName string, server string) {
		if varName == "" {
			return
		}
		m, ok := requiredBy[varName]
		if !ok {
			m = map[string]struct{}{}
			requiredBy[varName] = m
		}
		m[server] = struct{}{}
	}
	for _, server := range enabledServers {
		entry, ok := inv[server]
		if !ok {
			continue
		}
		for _, part := range entry.Command {
			exp, err := engine.ExpandParams(params, part)
			if err != nil {
				addReq("<unresolved_params>", server)
				continue
			}
			for _, name := range doctorExtractEnvRefNames(exp) {
				addReq(name, server)
			}
		}
		for _, v := range entry.Env {
			exp, err := engine.ExpandParams(params, v)
			if err != nil {
				addReq("<unresolved_params>", server)
				continue
			}
			for _, name := range doctorExtractEnvRefNames(exp) {
				addReq(name, server)
			}
		}
	}

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
		if k == "<unresolved_params>" {
			missing = append(missing, k)
			continue
		}
		if strings.TrimSpace(os.Getenv(k)) == "" {
			missing = append(missing, k)
		}
	}
	return missing, flat
}

func doctorExtractEnvRefNames(s string) []string {
	set := map[string]struct{}{}
	for {
		start := strings.Index(s, "{env:")
		if start < 0 {
			break
		}
		rest := s[start:]
		endRel := strings.Index(rest, "}")
		if endRel < 0 {
			break
		}
		end := start + endRel
		name := strings.TrimSpace(s[start+len("{env:") : end])
		if name != "" {
			set[name] = struct{}{}
		}
		s = s[end+1:]
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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

		cmd0, err := engine.ExpandParams(params, entry.Command[0])
		if err != nil {
			failures = append(failures, map[string]any{"server": server, "error": err.Error(), "inventory_path": prov.InventoryPath})
			continue
		}
		cmd0, err = engine.ExpandEnvRefs(cmd0)
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
			part, err := engine.ExpandParams(params, raw)
			if err != nil {
				failures = append(failures, map[string]any{"server": server, "error": err.Error(), "inventory_path": prov.InventoryPath})
				continue
			}
			part, err = engine.ExpandEnvRefs(part)
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
		case "clone":
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
		case "copy":
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
	// Symbolic ref: read the ref file
	ref := strings.TrimPrefix(content, "ref: ")
	refPath := filepath.Join(dir, ".git", ref)
	data, err = os.ReadFile(refPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func buildEcosystemStatus(packs []config.ResolvedPack, settingsPack, profileName, profilePath, configDir string) *EcosystemStatus {
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
