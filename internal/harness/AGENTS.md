# Harness Adapters

Each harness renders pack content into a coding assistant's native format. Four implemented: `claudecode`, `opencode`, `codex`, `cline`.

## Interface

```go
type Harness interface {
    ID() domain.Harness
    Plan(ctx engine.SyncContext) (domain.Fragment, error)     // content → harness files
    Render(ctx RenderContext) (domain.Fragment, error)        // portable rendering
    Capture(ctx CaptureContext) (CaptureResult, error)        // harness files → content
    ManagedRoots(scope, baseDir, home string) []string
    SettingsPaths(scope, baseDir, home string) []string
    StrictExtraDirs(scope, baseDir, home string) []string
    PackRelativePaths() []string
    StripManagedSettings(rendered []byte, filename string) ([]byte, error)
    CleanActions(scope, baseDir, home string) []CleanAction
}
```

The three core methods: `Plan` (forward sync), `Capture` (reverse save), `Render` (portable output). The rest handle path ownership, settings stripping, and cleanup.

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

Each harness handles scope internally. Pattern from Claude Code:

```go
func (Harness) Plan(ctx engine.SyncContext) (domain.Fragment, error) {
    // ctx.TargetDir is already resolved: project dir or $HOME
    // ctx.Home is always set (needed for global MCP paths even in project scope)
    planContent(&f, ctx.TargetDir, ctx.Profile)
    planMCPAndSettings(&f, ctx)
    return f, nil
}
```

Non-obvious: Cline MCP is always global. Even during project-scope sync, the Cline adapter writes MCP to the VS Code global storage path using `ctx.Home`.

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

Codex and Cline can't represent agents/workflows natively, so they're **promoted** to skill directories with enriched YAML frontmatter. See `promote.go`.

Forward (sync): agent/workflow → `skills/<name>/SKILL.md` with `source_type: agent` or `source_type: workflow` in frontmatter. Reverse (capture): `CapturePromotedContent()` reads `source_type` from the SKILL.md frontmatter and reconstructs the original domain type.

This round-trip is critical — the enriched frontmatter preserves enough metadata to reconstruct the original agent/workflow when saving back to packs.

## Design constraints

No concurrent/parallel harness planning or capture. Most users have 1-2 harnesses; goroutine fan-out overhead exceeds savings. Per-harness optimizations (walk caching, etc.) are the right lever.

## Consistency rules

**Plan ↔ Capture symmetry.** If Plan writes content in a particular format or layout, Capture must reverse it exactly. Changing one without the other silently breaks save round-trips — the save produces incorrect pack content with no error. When modifying Plan rendering, always update the corresponding Capture logic and verify with a round-trip test.

**ManagedRoots completeness.** Every path a harness writes to in Plan must appear in `ManagedRoots()`. Missing paths mean `clean` won't remove them and `--prune` won't detect stale files there.

**CleanActions completeness.** Every managed config key (MCP sections, tool permissions, content references) must have a corresponding removal in `CleanActions()`. The harness owns the knowledge of what to clean — `app/clean.go` only handles I/O.

**Docs.** Rendering or path changes require updating `docs/aipack.md` per-harness reference in the same change.

## Vector rendering per harness

| Vector | Claude Code | OpenCode | Codex | Cline |
|--------|-------------|----------|-------|-------|
| Rules | `.claude/rules/` | `.opencode/rules/` + `instructions` ref | Flattened `AGENTS.override.md` | `.clinerules/` |
| Agents | `.claude/agents/` | `.opencode/agents/` | Promoted to `.agents/skills/` | Promoted to `.clinerules/skills/` |
| Workflows | `.claude/commands/` | `.opencode/commands/` | Promoted to `.agents/skills/` | `.clinerules/workflows/` |
| Skills | `.claude/skills/` | `.opencode/skills/` | `.agents/skills/` | `.clinerules/skills/` |
| MCP | `.mcp.json` | `opencode.json` | `config.toml` | Global VS Code storage |

Full per-harness details including merge behavior and tool permissions: `docs/aipack.md` Per-harness reference.

## Adding a new harness

1. Create `internal/harness/<name>/harness.go` — implement full `Harness` interface
2. Add harness ID constant to `internal/domain/types.go`
3. Add to `AllHarnesses()` in `internal/domain/types.go`
4. Add normalization in `internal/cmdutil/inputs.go`
5. Register in `cmd/aipack/cli.go` `run()` function (the `harness.NewRegistry(...)` call)
6. Add rendering logic: `render.go` for MCP, settings, agent transforms
7. Add capture logic if save round-trip is needed
8. Update `docs/aipack.md` per-harness reference

## Testing

Unit tests: use stub harnesses (pattern in `internal/app/sync_test.go`):

```go
type stubHarness struct {
    id       domain.Harness
    fragment domain.Fragment
}
func (s stubHarness) Plan(engine.SyncContext) (domain.Fragment, error) {
    return s.fragment, nil
}
```

Capture tests in `capture_test.go` use real filesystem state — create temp dirs with harness-native file layouts, then verify capture round-trips correctly.
