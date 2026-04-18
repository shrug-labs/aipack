package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/shrug-labs/aipack/internal/util"
)

// MCPProbeCacheTTL bounds how long a cached probe result is considered fresh.
// 24h is a balance between avoiding re-probe noise and keeping the live tool
// list broadly current; a user who wants newer data can force a re-probe via
// the TUI picker's `r` key or `aipack mcp inspect-tools <server>`.
const MCPProbeCacheTTL = 24 * time.Hour

const mcpProbeCacheSchemaVersion = 1

// MCPProbeKey identifies a cached probe result. packRoot is the absolute
// path to the pack directory so two installations of the same pack name in
// different config dirs never collide.
type MCPProbeKey struct {
	PackRoot string
	Server   string
}

// MCPProbeEntry is one cached probe result with its timestamp.
type MCPProbeEntry struct {
	Tools    []string
	ProbedAt time.Time
}

// MCPProbeCache holds the user-local probe cache. Consumers use Get/Put/
// Invalidate for individual entries; LoadMCPProbeCache / SaveMCPProbeCache
// handle the on-disk representation.
type MCPProbeCache map[MCPProbeKey]MCPProbeEntry

// Get returns the cached tools for a key if present and fresh (not past TTL).
// stale=true means the entry exists but is past TTL — callers can use this
// to decide whether to display the old list while re-probing.
func (c MCPProbeCache) Get(key MCPProbeKey) (entry MCPProbeEntry, fresh, stale bool) {
	e, ok := c[key]
	if !ok {
		return MCPProbeEntry{}, false, false
	}
	if time.Since(e.ProbedAt) > MCPProbeCacheTTL {
		return e, false, true
	}
	return e, true, false
}

// Put records a fresh probe result.
func (c MCPProbeCache) Put(key MCPProbeKey, tools []string) {
	c[key] = MCPProbeEntry{
		Tools:    slices.Clone(tools),
		ProbedAt: time.Now().UTC(),
	}
}

// Invalidate removes an entry so the next probe round hits the server.
func (c MCPProbeCache) Invalidate(key MCPProbeKey) {
	delete(c, key)
}

// mcpProbeCacheFile is the on-disk representation. Arrays (not maps) so the
// file has a stable, diffable order and keys with special characters
// (absolute paths) don't need JSON escaping.
type mcpProbeCacheFile struct {
	SchemaVersion int                 `json:"schema_version"`
	Probes        []mcpProbeEntryJSON `json:"probes"`
}

type mcpProbeEntryJSON struct {
	PackRoot string    `json:"pack_root"`
	Server   string    `json:"server"`
	Tools    []string  `json:"tools"`
	ProbedAt time.Time `json:"probed_at"`
}

// MCPProbeCachePath returns the cache file path for the given config dir.
func MCPProbeCachePath(configDir string) string {
	return filepath.Join(configDir, "cache", "mcp-probes.json")
}

// LoadMCPProbeCache reads and TTL-filters the cache file. Missing file,
// unreadable file, or parse failure returns an empty cache — the cache is
// an optimization, never load-bearing. Stale entries are dropped.
func LoadMCPProbeCache(configDir string) MCPProbeCache {
	cache := MCPProbeCache{}
	data, err := os.ReadFile(MCPProbeCachePath(configDir))
	if err != nil {
		return cache
	}
	var file mcpProbeCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		return cache
	}
	cutoff := time.Now().Add(-MCPProbeCacheTTL)
	for _, e := range file.Probes {
		if e.ProbedAt.Before(cutoff) {
			continue
		}
		cache[MCPProbeKey{PackRoot: e.PackRoot, Server: e.Server}] = MCPProbeEntry{
			Tools:    slices.Clone(e.Tools),
			ProbedAt: e.ProbedAt,
		}
	}
	return cache
}

// SaveMCPProbeCache atomically writes the cache. Stale entries are dropped
// so the file shrinks over time even if callers forget to call Invalidate.
func SaveMCPProbeCache(configDir string, cache MCPProbeCache) error {
	cutoff := time.Now().Add(-MCPProbeCacheTTL)
	file := mcpProbeCacheFile{SchemaVersion: mcpProbeCacheSchemaVersion}
	for key, entry := range cache {
		if entry.ProbedAt.Before(cutoff) {
			continue
		}
		file.Probes = append(file.Probes, mcpProbeEntryJSON{
			PackRoot: key.PackRoot,
			Server:   key.Server,
			Tools:    slices.Clone(entry.Tools),
			ProbedAt: entry.ProbedAt,
		})
	}
	// Sort for deterministic on-disk output.
	slices.SortFunc(file.Probes, func(a, b mcpProbeEntryJSON) int {
		if a.PackRoot != b.PackRoot {
			if a.PackRoot < b.PackRoot {
				return -1
			}
			return 1
		}
		if a.Server < b.Server {
			return -1
		}
		if a.Server > b.Server {
			return 1
		}
		return 0
	})
	out, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal probe cache: %w", err)
	}
	out = append(out, '\n')
	return util.WriteFileAtomic(MCPProbeCachePath(configDir), out)
}

// UpdateMCPProbeEntry is a convenience: load the cache, put one entry, save.
// Used on the CLI probe path where we don't hold a long-lived cache handle.
func UpdateMCPProbeEntry(configDir string, key MCPProbeKey, tools []string) error {
	cache := LoadMCPProbeCache(configDir)
	cache.Put(key, tools)
	return SaveMCPProbeCache(configDir, cache)
}
