package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"io"

	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/mcp"
	"github.com/shrug-labs/aipack/internal/util"
)

type InspectStatus string

const (
	InspectStatusOK      InspectStatus = "ok"
	InspectStatusSkipped InspectStatus = "skipped"
	InspectStatusError   InspectStatus = "error"
)

// MCPInspectToolsRequest holds inputs for RunMCPInspectTools.
type MCPInspectToolsRequest struct {
	ConfigDir   string
	ProfileName string // --profile: profile name to source {params.*} from
	ProfilePath string // --profile-path: explicit path; wins over ProfileName
	Home        string
	ServerRef   string // "server" or "pack/server"; empty = list mode
	All         bool
	Timeout     time.Duration
	Save        bool
	DryRun      bool // preview --save without writing
	// Stdout (when non-nil) receives live probe phase output during each
	// server probe. The human-mode single-server CLI passes os.Stdout so
	// users see live phase progression; JSON mode, --all, and the TUI
	// consumer pass nil.
	Stdout io.Writer

	// OnResult is invoked by each worker as soon as its probe completes.
	// Lets the CLI print per-server summary lines inline (rather than
	// dumping everything after the whole batch). Optional — nil skips
	// inline reporting. Called from worker goroutines, so the callback
	// must be safe for concurrent invocation; the human-mode CLI passes
	// a closure that writes through a mutex-protected writer.
	OnResult func(MCPInspectToolsServerResult)
}

// MCPServerInfo describes an MCP server discovered from installed packs.
type MCPServerInfo struct {
	ServerName    string `json:"server_name"`
	PackName      string `json:"pack_name"`
	Transport     string `json:"transport"`
	ToolCount     int    `json:"tool_count"`
	InventoryPath string `json:"inventory_path"`
}

// MCPInspectToolsServerResult holds the probe outcome for one server.
type MCPInspectToolsServerResult struct {
	ServerName    string        `json:"server_name"`
	PackName      string        `json:"pack_name"`
	Transport     string        `json:"transport"`
	Status        InspectStatus `json:"status"`
	Tools         []string      `json:"tools,omitempty"`
	ToolCount     int           `json:"tool_count"`
	PreviousTools []string      `json:"previous_tools,omitempty"`
	Added         []string      `json:"added,omitempty"`
	Removed       []string      `json:"removed,omitempty"`
	Saved         bool          `json:"saved,omitempty"`
	WouldSave     bool          `json:"would_save,omitempty"` // --save --dry-run: a write would happen but was skipped
	InventoryPath string        `json:"inventory_path,omitempty"`
	Error         string        `json:"error,omitempty"`
	Duration      string        `json:"duration,omitempty"`
}

// MCPInspectToolsResult holds the full inspection report.
type MCPInspectToolsResult struct {
	// ListMode is true when neither --server nor --all was given.
	ListMode bool                          `json:"list_mode,omitempty"`
	Servers  []MCPServerInfo               `json:"available_servers,omitempty"`
	Results  []MCPInspectToolsServerResult `json:"results,omitempty"`
	OK       bool                          `json:"ok"`
	// InputError marks a failure caused by bad user input (unknown or
	// ambiguous server name). CLI adapters map this to ExitUsage (2);
	// other !OK results map to ExitFail (1).
	InputError bool `json:"input_error,omitempty"`
	// Warnings surfaces per-pack inventory read/parse errors that were
	// previously silently skipped by discoverMCPServers. They do not fail
	// the command (the rest of the inventory still lists), but they let
	// operators know a specific pack file was unreadable or malformed.
	Warnings []string `json:"warnings,omitempty"`
}

// mcpServerRef is an internal handle to one server in one pack.
type mcpServerRef struct {
	serverName    string
	packName      string
	packRoot      string
	inventoryPath string
	server        domain.MCPServer
}

// RunMCPInspectTools discovers MCP servers from installed packs, probes them
// to discover live tool inventories, and optionally writes results back.
func RunMCPInspectTools(ctx context.Context, req MCPInspectToolsRequest) MCPInspectToolsResult {
	result := MCPInspectToolsResult{OK: true}

	configDir, syncCfg, err := loadSyncConfigForProbe(req.ConfigDir, req.Home)
	if err != nil {
		return errResult("load sync-config: %v", err)
	}

	allServers, warnings, err := discoverMCPServers(filepath.Join(configDir, "packs"))
	if err != nil {
		return errResult("discover servers: %v", err)
	}
	result.Warnings = warnings

	if req.ServerRef == "" && !req.All {
		result.ListMode = true
		for _, ref := range allServers {
			result.Servers = append(result.Servers, MCPServerInfo{
				ServerName:    ref.serverName,
				PackName:      ref.packName,
				Transport:     normalizedTransport(ref.server.Transport),
				ToolCount:     len(ref.server.AvailableTools),
				InventoryPath: ref.inventoryPath,
			})
		}
		return result
	}

	var targets []mcpServerRef
	if req.All {
		targets = allServers
	} else {
		targets, err = resolveServerRef(allServers, req.ServerRef)
		if err != nil {
			r := errResult("%v", err)
			r.InputError = true
			return r
		}
	}

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	// Profile params are loaded once across all workers. sync.Once plus two
	// plain vars (not atomics) is enough because every reader observes the
	// write through Once's happens-before; the cost is a single load regardless
	// of how many servers need params.
	var (
		paramsOnce sync.Once
		params     map[string]string
		paramsErr  error
	)
	loadParams := func() (map[string]string, error) {
		paramsOnce.Do(func() {
			params, paramsErr = loadProfileParams(configDir, syncCfg, req.ProfileName, req.ProfilePath, req.Home)
		})
		return params, paramsErr
	}

	results := make([]MCPInspectToolsServerResult, len(targets))
	concurrency := max(1, min(len(targets), 4))
	parallelBounded(ctx, len(targets), concurrency, func(i int) {
		results[i] = probeOneInspectTarget(ctx, targets[i], req, timeout, loadParams)
		if req.OnResult != nil {
			req.OnResult(results[i])
		}
	})
	result.Results = results

	// Persist every successful probe to the user-local cache so the TUI
	// (or a later CLI invocation) can skip re-probing within the TTL.
	// Batched after the parallel loop so we only hit the disk once.
	// Status can flip to error on save-failure, but sr.Tools is populated
	// before the save attempt, so len(r.Tools) > 0 is the real "probe
	// succeeded" signal — don't discard fresh probe data on unrelated
	// inventory-write failures.
	cache := LoadMCPProbeCache(configDir)
	cacheDirty := false
	for i, r := range result.Results {
		if len(r.Tools) > 0 {
			cache.Put(MCPProbeKey{PackRoot: targets[i].packRoot, Server: r.ServerName}, r.Tools)
			cacheDirty = true
		}
	}
	if cacheDirty {
		_ = SaveMCPProbeCache(configDir, cache)
	}

	anyOK := false
	for _, r := range result.Results {
		if r.Status == InspectStatusError {
			result.OK = false
		}
		if r.Status == InspectStatusOK {
			anyOK = true
		}
	}
	// A probe run that produced no successful inspections should fail even when
	// all targets were skipped (unsupported transport, unresolved refs, etc).
	if !anyOK && len(result.Results) > 0 {
		result.OK = false
	}

	return result
}

// probeOneInspectTarget runs the full per-server inspection flow: stdio
// gate → param load → expansion → probe → optional save. Extracted so the
// parallel worker fits into parallelBounded's callback shape, and so the
// sequential serial path stays testable via the same code.
func probeOneInspectTarget(
	ctx context.Context,
	ref mcpServerRef,
	req MCPInspectToolsRequest,
	timeout time.Duration,
	loadParams func() (map[string]string, error),
) MCPInspectToolsServerResult {
	sr := MCPInspectToolsServerResult{
		ServerName:    ref.serverName,
		PackName:      ref.packName,
		Transport:     normalizedTransport(ref.server.Transport),
		PreviousTools: slices.Clone(ref.server.AvailableTools),
	}

	var serverParams map[string]string
	if serverNeedsProfileParams(ref.server) {
		p, err := loadParams()
		if err != nil {
			sr.Status = InspectStatusSkipped
			sr.Error = fmt.Sprintf("profile params unavailable: %v", err)
			return sr
		}
		serverParams = p
	}

	expanded, expErr := engine.ExpandSingleMCPServer(serverParams, ref.server)
	if expErr != nil {
		sr.Status = InspectStatusSkipped
		// The engine's error carries its own sanitized message (templates
		// only, no post-expansion content — see internal/util/envref.go
		// and internal/engine/envref.go).
		sr.Error = fmt.Sprintf("expand server %q: %v", ref.serverName, expErr)
		return sr
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	probeResult, probeErr := mcp.Probe(probeCtx, expanded, req.Stdout, nil)
	cancel()

	if probeErr != nil {
		sr.Status = InspectStatusError
		sr.Error = probeErr.Error()
		return sr
	}

	names := probeResult.ToolNames()
	slices.Sort(names)
	sr.Status = InspectStatusOK
	sr.Tools = names
	sr.ToolCount = len(names)
	sr.Duration = probeResult.Duration.Round(time.Millisecond).String()

	slices.Sort(sr.PreviousTools)
	sr.Added, sr.Removed = engine.DiffStrings(sr.PreviousTools, names)

	if req.Save {
		if req.DryRun {
			sr.WouldSave = true
			sr.InventoryPath = ref.inventoryPath
		} else if saveErr := saveInventoryTools(ref.inventoryPath, names); saveErr != nil {
			sr.Status = InspectStatusError
			sr.Error = fmt.Sprintf("save failed: %v", saveErr)
		} else {
			sr.Saved = true
			sr.InventoryPath = ref.inventoryPath
		}
	}

	return sr
}

// SaveMCPInventoryTools writes a discovered MCP tool inventory back to an
// existing mcp/<server>.json file while preserving unrelated fields.
func SaveMCPInventoryTools(inventoryPath string, tools []string) error {
	return saveInventoryTools(inventoryPath, tools)
}

// discoverMCPServers scans all installed packs for MCP server definitions.
// warnings accumulates per-file read/parse failures so the caller can surface
// them instead of silently reporting an incomplete inventory.
func discoverMCPServers(packsDir string) (refs []mcpServerRef, warnings []string, err error) {
	entries, err := os.ReadDir(packsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read packs dir: %w", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || util.IsBackupDir(e.Name()) {
			continue
		}
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		packRoot := filepath.Join(packsDir, e.Name())
		mcpDir := filepath.Join(packRoot, "mcp")
		mcpFiles, readErr := os.ReadDir(mcpDir)
		if readErr != nil {
			// Missing mcp/ directory is expected for packs that ship no MCP
			// servers. Surface other errors (permission denied, I/O) because
			// they masquerade as "this pack has no servers" to the caller.
			if !os.IsNotExist(readErr) {
				warnings = append(warnings, fmt.Sprintf("read %s: %v", mcpDir, readErr))
			}
			continue
		}
		for _, f := range mcpFiles {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			invPath := filepath.Join(mcpDir, f.Name())
			b, readErr := os.ReadFile(invPath)
			if readErr != nil {
				warnings = append(warnings, fmt.Sprintf("read %s: %v", invPath, readErr))
				continue
			}
			var srv domain.MCPServer
			if parseErr := json.Unmarshal(b, &srv); parseErr != nil {
				warnings = append(warnings, fmt.Sprintf("parse %s: %v", invPath, parseErr))
				continue
			}
			serverName := strings.TrimSuffix(f.Name(), ".json")
			srv.Name = serverName
			srv.PackRoot = packRoot
			refs = append(refs, mcpServerRef{
				serverName:    serverName,
				packName:      e.Name(),
				packRoot:      packRoot,
				inventoryPath: invPath,
				server:        srv,
			})
		}
	}
	slices.SortFunc(refs, func(a, b mcpServerRef) int {
		if a.packName != b.packName {
			return strings.Compare(a.packName, b.packName)
		}
		return strings.Compare(a.serverName, b.serverName)
	})
	return refs, warnings, nil
}

// resolveServerRef resolves a server reference ("name" or "pack/name") to
// concrete targets. Errors on ambiguity with a helpful message.
func resolveServerRef(all []mcpServerRef, ref string) ([]mcpServerRef, error) {
	// pack/server form
	if parts := strings.SplitN(ref, "/", 2); len(parts) == 2 {
		pack, name := parts[0], parts[1]
		for _, r := range all {
			if r.packName == pack && r.serverName == name {
				return []mcpServerRef{r}, nil
			}
		}
		return nil, fmt.Errorf("server %q not found in pack %q", name, pack)
	}

	// bare server name
	var matches []mcpServerRef
	for _, r := range all {
		if r.serverName == ref {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("server %q not found in any installed pack", ref)
	case 1:
		return matches, nil
	default:
		var packs []string
		for _, m := range matches {
			packs = append(packs, m.packName)
		}
		return nil, fmt.Errorf("server %q found in multiple packs: %s\nSpecify pack/server: aipack mcp inspect-tools %s/%s",
			ref, strings.Join(packs, ", "), packs[0], ref)
	}
}

// loadProfileParams loads just the params map from the active profile.
// Resolution chain:
//   - profilePathFlag wins if non-empty
//   - otherwise, the name falls back to explicit flag → sync-config default →
//     hardcoded "default" (see cmdutil.ResolveProfileName), and the path is
//     resolved via config.ResolveProfilePath.
func loadProfileParams(configDir string, syncCfg config.SyncConfig, profileName, profilePathFlag, home string) (map[string]string, error) {
	name := cmdutil.ResolveProfileName(profileName, syncCfg)
	pp, err := config.ResolveProfilePath(profilePathFlag, configDir, name, home)
	if err != nil {
		return nil, err
	}
	prof, err := config.LoadProfile(pp)
	if err != nil {
		return nil, err
	}
	return prof.Params, nil
}

func normalizedTransport(transport string) string {
	if transport == "" {
		return domain.TransportStdio
	}
	return transport
}

func serverNeedsProfileParams(server domain.MCPServer) bool {
	if slices.ContainsFunc(server.Command, util.HasParamRef) {
		return true
	}
	for _, value := range server.Env {
		if util.HasParamRef(value) {
			return true
		}
	}
	if util.HasParamRef(server.URL) {
		return true
	}
	for _, value := range server.Headers {
		if util.HasParamRef(value) {
			return true
		}
	}
	return false
}

func errResult(format string, args ...any) MCPInspectToolsResult {
	return MCPInspectToolsResult{
		OK: false,
		Results: []MCPInspectToolsServerResult{{
			Status: InspectStatusError,
			Error:  fmt.Sprintf(format, args...),
		}},
	}
}

func saveInventoryTools(path string, tools []string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return err
	}
	raw["available_tools"] = toolsJSON
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return util.WriteFileAtomic(path, out)
}

func loadSyncConfigForProbe(configDirFlag, home string) (string, config.SyncConfig, error) {
	configDir, err := cmdutil.ResolveConfigDir(configDirFlag, home)
	if err != nil {
		return "", config.SyncConfig{}, err
	}
	if !filepath.IsAbs(configDir) {
		abs, absErr := filepath.Abs(configDir)
		if absErr != nil {
			return "", config.SyncConfig{}, absErr
		}
		configDir = abs
	}
	syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(configDir))
	if err != nil {
		return "", config.SyncConfig{}, fmt.Errorf("load sync-config: %w", err)
	}
	return configDir, syncCfg, nil
}
