# Engine

Core sync pipeline: building MCP server structs from packs + profiles, computing diffs, applying changes, and merging managed settings into user-owned config files.

## MCP server assembly

`buildMCPServers` in `mcp.go` assembles `domain.MCPServer` values from pack inventories and per-profile permissions. Two layers of data feed it, plus a render-time requirement check.

### Inventory (from the pack)

Each server has an inventory JSON at `<pack>/mcp/<server>.json` declaring transport, command/URL, env/headers, `available_tools` (the full surface the server exposes), and doc metadata (`links`, `auth`, `notes`). Pack authors transcribe `available_tools` from the server itself — the field is **informational**, not a permission.

### Permissions (from the profile)

Profile pack entries declare which servers are enabled and narrow the tool surface per server:

```yaml
packs:
  - name: my-pack
    mcp:
      my-server:
        allowed_tools: [...]           # effective allow list
        always_allowed_tools: [...]    # auto-approve subset (harness-specific)
        disabled_tools: [...]          # explicit deny
```

### Resolution flow

1. Collect enabled servers from all resolved packs (`enabledServers`).
2. For each, load the inventory — supplies `AvailableTools`, `Transport`, `Command`, `URL`, etc.
3. `mcpPermissionsAndSource(packs, name)` — **last pack declaring the server wins**, matching the override-resolution order.
4. Expand `{params.*}` / `{env:*}` refs in Command, Env, URL, Headers. Unresolvable refs → warning, server skipped.
5. Emit a `domain.MCPServer` carrying inventory fields + profile-resolved `AllowedTools` / `AlwaysAllowedTools` / `DisabledTools` + `RequiredRefs` + source pack.

### Required refs (orthogonal to allow lists)

`RequiredRefs` is the set of `{params.*}` / `{env:*}` references found in the raw pre-expansion Command/Env/URL/Headers. Populated by `extractRequiredRefs` using `util.WalkParamRefs` + `util.WalkEnvRefs`. Both must resolve at render time or the server is skipped.

RequiredRefs answer "can this server render at all," not "which tools is it allowed to expose." Always extract from the **raw inventory** — once expansion has substituted `{params.*}`, the refs are gone.

### Rules of thumb

- **Don't conflate `AvailableTools` with permissions.** `AvailableTools` is the full server surface; `AllowedTools` is the profile's narrowed list. Populating permissions from `AvailableTools` ships a no-op allow list.
- **`AllowedTools` is post-profile-resolution.** To know what the pack author recommends, you'd need to read the profile's `allowed_tools` or fall back to whatever the server declares.
- **Last-declaring-pack wins.** Override ordering matches the profile's pack order — if two packs declare the same server, the later pack's permissions take effect. Don't assume alphabetical or declaration order.

Harness adapters consume these structs and collapse the three permission fields to native shapes (Claude Code unions into `permissions.allow`, Cline into `alwaysAllow`, OpenCode into a boolean `tools` map, Codex keeps all three distinct). See `docs/harness-reference.md` for the full per-harness rendering.

## Settings three-way merge

aipack syncs managed settings (`~/.claude.json`, `~/.config/opencode/opencode.json`, etc.) with user-owned content via a three-way merge at apply time. Understanding the inputs is critical for harness adapter authors and for writing non-vacuous migration tests.

### The three inputs

At `ComputeSettingsDiffs` (`diff.go`), when a `SettingsAction` has `MergeMode: true`:

1. **`existing`** — the user's on-disk file bytes. May contain aipack-managed content + user-authored content + legacy entries from prior sync versions.
2. **`prevManaged`** — from `ledger.PrevManagedOverlay(dst)`. The full rendered bytes aipack wrote as the managed overlay on the **previous sync**. Stored per-file in the ledger at `Entry.ManagedOverlay`. On first-ever sync, nil.
3. **`newManaged`** — the new `s.Desired` bytes produced by the current render pipeline.

Dispatched per format in `mergeSettingsKeys` (`merge.go`) — JSON for Claude Code / Cline / OpenCode, TOML for Codex.

### Semantics

`threeWayMergeMap` / `threeWayMergeArray` in `merge.go`:

**Objects (recursive):**
- Keys in `prev` but NOT in `next` → deleted from `disk` (aipack removing a key it used to manage).
- Keys in `next` but NOT in `disk` → added.
- Keys in both → recurse for objects, three-way merge for arrays, update scalars only when `disk` still equals `prev`.
- Keys only in `disk` (not prev, not next) → preserved (user-added).

**String arrays:**
1. Items in `next` appear first, in `next` order.
2. Items only in `disk` (not in prev, not in next) appended at the end.
3. Items in `prev` but not in `next` → dropped (this is the migration mechanism).
4. Items in `next` are always present regardless of whether they were in `disk`.

**Scalars:** managed value updates only when the on-disk value still equals the previous managed value. First-sync scalar collisions and locally edited managed scalars are preserved as user-owned values.

### Implication: legacy-entry migration is free

When a harness adapter changes what it writes for a managed key (e.g. OpenCode switching from per-pack source globs to a single rendered glob), **old entries get pruned automatically** on the next sync:

- `prev` has the old entries (what last sync wrote)
- `next` has only the new entries
- `disk` has both old + any user additions
- Result: new entries + user additions; old entries dropped

Most render-pipeline migrations don't need explicit migration code — the engine's three-way merge handles it.

### Test-author pitfalls

**Vacuous "legacy leak" assertions at render time.** A test that calls `Plan()` (which calls `RenderBytes(packBase, ...)`) to produce a `SettingsAction`, then asserts legacy entries are absent from `Desired`, is **vacuous** if the legacy entries were never in the pack base template. `RenderBytes` only reads the pack base, not the user's disk file.

Correct pattern for pinning migration behavior: construct a realistic `SettingsAction` + `domain.Ledger` and call `engine.ComputeSettingsDiffs` directly. Seed the ledger with a prev-managed overlay containing the legacy entries; seed disk with user-authored entries mixed with legacy entries; assert the merged output. See `TestMigration_PrunesLegacyViaThreeWayMerge` in `internal/harness/opencode/harness_test.go` for the shape.

**Inconsistent base keys across prev/next.** Because keys in `prev` but NOT in `next` are deleted from `disk`, any test passing different top-level key sets in prev vs next will see unrelated keys disappear. Real sync doesn't hit this — the pack base template is stable across syncs and contributes the same base keys (`logLevel`, `enabled_providers`, etc.) to both prev and next.

When writing migration tests: build a `packBase` map with stable keys (no instructions/skills); `prev = packBase + legacy managed keys`; `next = RenderBytes(packBaseBytes, ..., newSpec)` so it picks up the same base keys; then merge. If you skip this (e.g. pass `nil` as base to `RenderBytes` in `next`), you'll see confusing failures where stable keys vanish from the merged output.

**TestOrderingIndependence and absolute-path outputs.** `internal/app/integration_test.go:TestOrderingIndependence` byte-compares output files across two syncs with reversed pack internals — invariant: reordering within a pack must produce identical output. For harnesses whose output embeds the target directory (e.g. OpenCode's `instructions` / `skills.paths` absolute paths), the two syncs write to different tempdirs so embedded paths legitimately differ. Fix pattern: normalize per-run `projX` / `homeX` prefixes with a placeholder before comparing (already applied). If you add a harness that embeds target dir in file content, this test will fail until you either follow the same normalization or exempt the file.
