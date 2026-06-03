# Harness Adapters

Each harness renders pack content into a coding assistant's native format. Four implemented: `claudecode`, `opencode`, `codex`, `cline`.

## Interface

```go
type Harness interface {
    ID() domain.Harness
    Layout(scope, baseDir, home string) Layout    // filesystem footprint
    Plan(ctx engine.SyncContext) (domain.Fragment, error)  // content → harness files
    Render(ctx RenderContext) (domain.Fragment, error)      // portable rendering
    Capture(ctx CaptureContext) (CaptureResult, error)      // harness files → content
}
```

`Layout` describes what the harness owns: path roots, partially-owned files, and how to strip or reset managed content. `Plan`, `Capture`, and `Render` are the content operations.

## Layout

```go
type Layout struct {
    ValidationRoots []string    // destinations this harness may write under
    RemovePaths     []string    // fully-owned paths safe to delete wholesale
    OwnedFiles      []OwnedFile // files with partial key ownership
}
```

`OwnedFile` carries two closures: `Strip` (selectively remove managed content for capture/save) and `Reset` (aggressively clear managed sections for clean). Both operate on `map[string]any`. The `Format` field (JSON or TOML) drives parse/serialize in the `StripManaged` helper.

`ValidationRoots`, `RemovePaths`, and `OwnedFiles` are intentionally different:

- `ValidationRoots`: path allowlist for sync destinations, stale-file scoping, and ledger routing.
- `RemovePaths`: whole directories/files aipack owns and may remove outright during `clean`.
- `OwnedFiles`: partially-managed files that must be edited surgically, never deleted wholesale.

Clean is derived from Layout: `RemovePaths` are deleted wholesale, `OwnedFiles` are reset via `OwnedFile.Reset`, and ledger-tracked leaf files inside `ValidationRoots` are removed when they are not an `OwnedFile` and not already covered by `RemovePaths`. This is how mixed containers such as `.opencode/` can keep a partially-owned `opencode.json` while still cleaning fully-owned drop-ins like `oh-my-opencode.json`.

## Fragment pattern

`Plan()` returns a `Fragment`, not a `Plan`. The caller accumulates fragments:

```go
func (Harness) Plan(ctx engine.SyncContext) (domain.Fragment, error) {
    var f domain.Fragment
    // add to f.Writes, f.Copies, f.Settings, f.MCP
    return f, nil
}
// caller: fragment.Apply(&plan)
```

Use `f.MCP` for MCP configs, `f.Settings` for harness settings. `--skip-settings` gates Settings but never MCP — putting MCP output in Settings breaks skip-settings behavior.

## Scope branching

Each harness handles scope internally via `Layout()`. Pattern from Claude Code:

```go
func (Harness) Layout(scope domain.Scope, baseDir, home string) harness.Layout {
    // Single scope switch, returns ValidationRoots + RemovePaths + OwnedFiles
}
```

Non-obvious: Cline MCP is always global. Even during project-scope sync, the Cline adapter writes MCP to the VS Code global storage path using `home`.

## Content helpers

`PlanStandardContent()` handles the common case — rules as files, agents as files, workflows as files, skills as directories. Most harnesses call it with a `ContentDirs` struct:

```go
harness.PlanStandardContent(&f, profile, harness.ContentDirs{
    Rules:     filepath.Join(base, "rules"),
    Agents:    filepath.Join(base, "agents"),
    Workflows: filepath.Join(base, "commands"),
    Skills:    filepath.Join(base, "skills"),
}, transformAgent)
```

The optional `transformAgent` callback lets harnesses modify agent output (Claude Code transforms tools to PascalCase, filters MCP-prefixed tools from the allowlist).

## Promote pattern

Codex and Cline can't represent some content types natively, so they're **promoted** to skill directories with enriched YAML frontmatter. See `promote.go`.

Forward (sync): agent/workflow → `skills/<rendered-name>/SKILL.md` with matching `name: <rendered-name>` and `source_type: agent` or `source_type: workflow` in frontmatter. In natural mode `<rendered-name>` is the source ID; when `SyncContext.Namespaced` is true, it is `<id>__aipack__<pack>`. Reverse (capture): `CapturePromotedContent()` reads `source_type` from the SKILL.md frontmatter and reconstructs the original domain type.

Rendered path leaves and rendered frontmatter names must match for rules rendered as individual files, agents, skills, workflows/commands, and promoted content. Namespaced mode is mutually exclusive with natural mode in the desired set for a single harness target/scope; sync should converge to one spelling and prune the other through ledger cleanup. Capture strips `<id>__aipack__<pack>` back to the source ID before save writes pack content.

This round-trip is critical — the enriched frontmatter preserves enough metadata to reconstruct the original agent/workflow when saving back to packs.

## Design constraints

No concurrent/parallel harness planning or capture. Most users have 1-2 harnesses; goroutine fan-out overhead exceeds savings. Per-harness optimizations (walk caching, etc.) are the right lever.

## Consistency rules

**Plan ↔ Capture symmetry.** If Plan writes content in a particular format or layout, Capture must reverse it exactly. Changing one without the other silently breaks save round-trips — the save produces incorrect pack content with no error. When modifying Plan rendering, always update the corresponding Capture logic and verify with a round-trip test.

**ValidationRoots completeness.** Every path a harness writes to in Plan must appear under `Layout().ValidationRoots`. Missing paths mean destination validation, `clean`, ledger routing, and stale file pruning can all misclassify ownership.

**RemovePaths precision.** Put only fully-owned paths in `Layout().RemovePaths`. Do not include mixed containers that also hold partially-owned files. Example: OpenCode uses `.opencode/` as a validation root, but only `agents/`, `commands/`, `rules/`, and `skills/` are wholesale remove paths because `opencode.json` is partially owned.

**OwnedFiles completeness.** Every managed config key (MCP sections, tool permissions, content references) must have a corresponding entry in `Layout().OwnedFiles` with both `Strip` and `Reset` closures. Clean and capture both derive behavior from these entries.

**Docs.** Rendering or path changes require updating `docs/aipack.md` per-harness reference in the same change.

## Settings

Multiple packs can contribute harness settings via `configs/harness_settings` in their manifest. Settings from all contributing packs are deep-merged in profile order (first pack wins at leaf conflicts). `settings.enabled: false` on a pack entry opts it out. `--skip-settings` skips base template keys but still writes computed managed keys (MCP configs, tool permissions, agent registrations).

Plugin files (`configs/harness_plugins`) are pure copies. Same-filename plugins from different packs produce an error (collision detection).

Hooks are first-class pack content under `hooks/<id>/HOOK.yaml`. Harness adapters render native hook configuration from typed hook descriptors. Claude Code merges native hook groups into `settings.local.json`, OpenCode writes a generated `plugins/aipack-hooks.js` server plugin, Codex writes `.codex/hooks.json` plus `hooks.state` trust hashes in `config.toml`, and Cline writes generated wrappers in its hooks directory.

## Vector rendering per harness

| Vector | Claude Code | OpenCode | Codex | Cline |
|--------|-------------|----------|-------|-------|
| Rules | `.claude/rules/` | `.opencode/rules/` + `instructions` ref | Flattened `AGENTS.override.md` | `.clinerules/` |
| Agents | `.claude/agents/` | `.opencode/agents/` | Native TOML in `codex.toml` | Promoted to `.agents/skills/` |
| Workflows | `.claude/commands/` | `.opencode/commands/` | Promoted to `.agents/skills/` | `.clinerules/workflows/` |
| Skills | `.claude/skills/` | `.opencode/skills/` | `.agents/skills/` | `.agents/skills/` (shared with Codex) |
| MCP | `.mcp.json` | `opencode.json` | `config.toml` | Global VS Code storage |
| Settings | `settings.local.json` | `opencode.json` | `config.toml` | N/A |
| Hooks | `settings.local.json` native `hooks` | `plugins/aipack-hooks.js` | `.codex/hooks.json` + `config.toml` trust state | Generated wrappers in hooks dir |

Full per-harness details including merge behavior and tool permissions: `docs/aipack.md` Per-harness reference.

## Adding a new harness

1. Create `internal/harness/<name>/harness.go` — implement full `Harness` interface (5 methods)
2. Add harness ID constant to `internal/domain/types.go`
3. Add to `AllHarnesses()` in `internal/domain/types.go`
4. Add normalization in `internal/cmdutil/inputs.go`
5. Register in `cmd/aipack/cli.go` `run()` function (the `harness.NewRegistry(...)` call)
6. Add rendering logic: `render.go` for MCP, settings, agent transforms
7. Add capture logic if save round-trip is needed
8. Update `docs/aipack.md` per-harness reference

## Testing

**Integration tests** in `internal/app/integration_test.go` run full sync cycles with real harness adapters across all four harnesses. Use the `contractEnv` helper for new cross-harness behavioral tests.

**Per-harness tests** in each harness package test rendering and capture for that harness specifically.

**Stub harnesses** for unit tests that need a harness but don't care about rendering:

```go
type stubHarness struct {
    id       domain.Harness
    fragment domain.Fragment
}
func (s stubHarness) Layout(domain.Scope, string, string) harness.Layout {
    return harness.Layout{}
}
func (s stubHarness) Plan(engine.SyncContext) (domain.Fragment, error) {
    return s.fragment, nil
}
```
