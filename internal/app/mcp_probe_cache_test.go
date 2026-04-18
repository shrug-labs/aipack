package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestMCPProbeCache_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache := MCPProbeCache{}
	k1 := MCPProbeKey{PackRoot: "/packs/a", Server: "srv-a"}
	k2 := MCPProbeKey{PackRoot: "/packs/b", Server: "srv-b"}
	cache.Put(k1, []string{"tool-a", "tool-b"})
	cache.Put(k2, []string{"tool-x"})

	if err := SaveMCPProbeCache(dir, cache); err != nil {
		t.Fatalf("SaveMCPProbeCache: %v", err)
	}

	loaded := LoadMCPProbeCache(dir)
	if len(loaded) != 2 {
		t.Fatalf("loaded %d entries, want 2", len(loaded))
	}
	e1, fresh, stale := loaded.Get(k1)
	if !fresh || stale {
		t.Fatalf("k1 Get: fresh=%v stale=%v, want fresh=true", fresh, stale)
	}
	if !slices.Equal(e1.Tools, []string{"tool-a", "tool-b"}) {
		t.Errorf("k1 tools = %v", e1.Tools)
	}
	if e1.ProbedAt.IsZero() {
		t.Error("k1 probedAt is zero — Put should have stamped time.Now")
	}
}

func TestMCPProbeCache_TTLDropsStaleOnLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write a cache file by hand with one fresh and one stale entry.
	// Using the raw file schema pins the on-disk format in test as well.
	freshAt := time.Now().UTC()
	staleAt := time.Now().UTC().Add(-MCPProbeCacheTTL - time.Hour)
	raw := map[string]any{
		"schema_version": mcpProbeCacheSchemaVersion,
		"probes": []map[string]any{
			{"pack_root": "/p", "server": "fresh", "tools": []string{"t"}, "probed_at": freshAt.Format(time.RFC3339Nano)},
			{"pack_root": "/p", "server": "stale", "tools": []string{"t"}, "probed_at": staleAt.Format(time.RFC3339Nano)},
		},
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(MCPProbeCachePath(dir), data, 0o644); err != nil {
		t.Fatal(err)
	}

	loaded := LoadMCPProbeCache(dir)
	if _, ok := loaded[MCPProbeKey{PackRoot: "/p", Server: "fresh"}]; !ok {
		t.Error("fresh entry should be loaded")
	}
	if _, ok := loaded[MCPProbeKey{PackRoot: "/p", Server: "stale"}]; ok {
		t.Error("stale entry should be dropped on load")
	}
}

func TestMCPProbeCache_GetReportsStale(t *testing.T) {
	t.Parallel()
	cache := MCPProbeCache{}
	k := MCPProbeKey{PackRoot: "/p", Server: "s"}
	cache[k] = MCPProbeEntry{
		Tools:    []string{"t"},
		ProbedAt: time.Now().Add(-MCPProbeCacheTTL - time.Hour),
	}
	_, fresh, stale := cache.Get(k)
	if fresh {
		t.Error("expected !fresh for expired entry")
	}
	if !stale {
		t.Error("expected stale=true so callers can surface last-known tools")
	}
}

func TestMCPProbeCache_InvalidateRemoves(t *testing.T) {
	t.Parallel()
	cache := MCPProbeCache{}
	k := MCPProbeKey{PackRoot: "/p", Server: "s"}
	cache.Put(k, []string{"t"})
	cache.Invalidate(k)
	if _, ok := cache[k]; ok {
		t.Error("Invalidate did not remove the entry")
	}
}

func TestLoadMCPProbeCache_MissingFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	cache := LoadMCPProbeCache(t.TempDir())
	if len(cache) != 0 {
		t.Errorf("missing file should yield empty cache, got %d entries", len(cache))
	}
}

func TestLoadMCPProbeCache_CorruptFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(MCPProbeCachePath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := LoadMCPProbeCache(dir)
	if len(cache) != 0 {
		t.Errorf("corrupt file should yield empty cache, got %d entries", len(cache))
	}
}

func TestSaveMCPProbeCache_DropsStaleEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache := MCPProbeCache{}
	cache[MCPProbeKey{PackRoot: "/p", Server: "stale"}] = MCPProbeEntry{
		Tools:    []string{"t"},
		ProbedAt: time.Now().Add(-MCPProbeCacheTTL - time.Hour),
	}
	cache.Put(MCPProbeKey{PackRoot: "/p", Server: "fresh"}, []string{"u"})

	if err := SaveMCPProbeCache(dir, cache); err != nil {
		t.Fatal(err)
	}
	loaded := LoadMCPProbeCache(dir)
	if _, ok := loaded[MCPProbeKey{PackRoot: "/p", Server: "stale"}]; ok {
		t.Error("stale entry should be pruned on save")
	}
	if _, ok := loaded[MCPProbeKey{PackRoot: "/p", Server: "fresh"}]; !ok {
		t.Error("fresh entry missing after save/load")
	}
}

func TestUpdateMCPProbeEntry_IdempotentLoadModifySave(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	k := MCPProbeKey{PackRoot: "/p", Server: "s"}
	if err := UpdateMCPProbeEntry(dir, k, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := UpdateMCPProbeEntry(dir, k, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	loaded := LoadMCPProbeCache(dir)
	if !slices.Equal(loaded[k].Tools, []string{"a", "b"}) {
		t.Errorf("tools = %v, want [a b]", loaded[k].Tools)
	}
}
