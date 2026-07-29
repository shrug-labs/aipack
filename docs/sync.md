# Sync and Save

Sync is how pack content reaches your harness. It resolves the active profile, renders content into harness-native format, and writes managed files. Save is the reverse — capturing harness changes back into packs. Together they form a bidirectional pipeline between pack sources and harness config.

## Sync

```bash
aipack sync
```

This resolves the active profile, determines which rules, skills, workflows, agents, hooks, plugins, and MCP servers should be active, and writes them to the target harness locations. The same content renders differently per harness — rules become individual files for Claude Code but a single `AGENTS.override.md` for Codex. See the [Harness Reference](./harness-reference.md) for rendering details.

### Resolution order

When multiple sources could determine the profile, scope, or harness, sync resolves in this order:

- **Profile**: `--profile-path` → `--profile` → sync-config `defaults.profile` → `default`
- **Scope**: `--scope` → sync-config `defaults.scope` → `global`
- **Harness**: `--harness` → sync-config `defaults.harnesses` → `AIPACK_DEFAULT_HARNESS`

Sync runs each resolved harness independently — its own plan, apply, and ledger, with no cross-harness aggregation. `--harness all` and multi-entry `defaults.harnesses` sync every resolved harness one after another, in resolution order. The first harness that fails stops the run; harnesses already synced keep their changes. Human-readable output prints one self-contained block per harness, and `--json` emits one result object per harness.

### Common commands

```bash
# Preview what would change and where without writing
aipack sync --dry-run

# Preview with source packs and content diffs
aipack sync --dry-run --verbose

# Sync to project-level config (current directory)
aipack sync --scope project

# Sync a specific harness
aipack sync --harness claudecode

# Sync every supported harness, each independently
aipack sync --harness all

# Force-sync, overriding conflicts
aipack sync --force

# Watch pack sources and re-sync on every change
aipack sync --watch
```

### Auto sync

Set `defaults.auto_sync: true` in `sync-config.yaml`, or run `aipack config defaults set auto_sync true`, to run a normal sync after successful pack or profile commands that affect the active profile. The automatic sync uses the same active profile, scope, and harness defaults as `aipack sync`, syncing each resolved harness independently.

Auto sync is intentionally active-profile only. Commands that mutate an inactive profile, dry runs, failed operations, and pack updates for packs that are not enabled in the active profile do not trigger it. In the manage TUI, active-profile edits are still saved immediately; syncing waits for a seven-second idle debounce, and manual sync cancels the pending automatic sync and runs immediately.

### Flags

| Flag | Effect |
|------|--------|
| `--dry-run` | Preview planned destination changes without writing |
| `--verbose` / `-v` | With `--dry-run`, show source packs and content diffs for changed files |
| `--force` | Override file conflicts (see below) |
| `--skip-settings` | Skip base harness settings files; still syncs MCP configs, plugin references, and rendered hooks |
| `--watch` | Re-sync automatically when pack sources or config files change |
| `--json` | Machine-readable output |
| `--yes` | Auto-confirm deletions and overwrites |

Human-readable sync output labels destination changes as `[harness] /full/path` so it is clear which harness owns the target file and which exact file changed. For example, Codex skill writes appear as `[codex] /path/to/project/.codex/skills/<name>/SKILL.md`.

### Sync contract

Exact sync semantics. If docs and code diverge, code is authoritative.

All managed files — content and config — go through unified diff classification:

- **Create**: no file on disk → written.
- **Identical**: desired matches on-disk → no action.
- **Managed**: on-disk matches ledger digest (unmodified since last sync) → updated silently.
- **Conflict**: on-disk modified by user since last sync → unified diff shown, skipped without `--force`.

`--force` controls conflict resolution. Stale managed files (no longer in the profile) are removed from the current harness's owned roots and from cleanup-only stale roots declared by that harness. Stale roots exist for migrations, not current write ownership. When a stale root overlaps another active harness's current root, that active harness's ledger entries are protected; inactive ledger-managed entries under the stale root may be pruned so migrations converge without manual cleanup. User-modified stale files prompt for confirmation (or require `--yes`).

Config files are computed from pack base configs. String values in harness settings expand `{env:*}`, `{params.*}`, and `{pack:root}` references before merge. Settings merge preserves user-only keys. For scalar key collisions, aipack updates the value only when the on-disk value still matches the previous managed value; first-sync collisions and locally edited scalars are preserved. Hook descriptor command strings expand `{hook:root}`, `{pack:root}`, `{params.*}`, and `{env:*}` references before native hook rendering. `--skip-settings` skips base harness settings files but first-class plugin references, drop-in plugin files (e.g., `oh-my-opencode.json`), rendered hooks (Claude Code native hooks, OpenCode generated plugin, Codex `.codex/hooks.json` plus managed trust state, and Cline generated wrappers), and generated MCP configs (e.g., Cline) still sync.

Skill assets may contain file or directory symlinks whose resolved targets stay within the source repository. For sources without a repository marker, targets must stay within the pack root. Sync follows those links while preserving their logical paths in the rendered skill. Targets that escape the boundary, traverse `.git`, or form a directory cycle are rejected. Dry runs traverse and classify the same source files as real syncs, but do not write destinations or state.

For global-scope sync, aipack honors harness-native config-root environment variables where the harness defines them: `CODEX_HOME` for Codex, `OPENCODE_CONFIG_DIR` for OpenCode, and `CLINE_DIR` / `CLINE_DATA_DIR` for Cline. Cline global hook wrappers are an exception: they use `~/Documents/Cline/Hooks/`, not the Cline env-var roots.

Plugin references are additive-only in v1. Sync writes the enablement entries required by supported harnesses, but removing a plugin from a profile or pack does not disable or uninstall it from the harness.

The ledger records which pack contributed each managed file (`source_pack` field), enabling save round-trips.

Given identical inputs and profile, sync produces byte-identical outputs across runs.

### Content drift detection

Each successful sync records a per-pack content inventory in `aipack.lock`:
rule, agent, workflow, and plugin IDs; skill snapshots (description + the sorted list
of files bundled under the skill directory other than `SKILL.md`); and MCP
server snapshots (available tools and the `{params.*}` / `{env:*}` references
the raw server strings make).

On the next sync, before the plan runs, aipack diffs the previous inventory
against the freshly resolved pack contents and prints a per-pack report for
anything that changed. Removed items that your active profile still references
appear under **"Removed (affects your profile)"** so you see the actionable
drift first. Other removals, additions, and per-item changes follow.

Skills and MCP servers have a richer **"Changed"** section because they carry
more than a name:

- **Skills** report description changes (from the SKILL.md frontmatter) and
  added/removed bundled assets — helper scripts, templates, data files a skill
  ships alongside `SKILL.md`.
- **MCP servers** report added/removed `available_tools` and added/removed
  required references. A new required `{params.*}` or `{env:*}` that the
  profile doesn't satisfy is the classic "pack update that would break sync"
  signal, and drift output surfaces it before the plan runs so you can
  reconcile before sync completes.

Drift detection runs in dry-run too, so `aipack sync --dry-run` shows the
report without writing the new inventory back. On a real sync, the new
inventory is recorded only after the apply step succeeds — if a fatal error
interrupts the sync, the previous inventory is preserved so the next attempt
sees the same drift.

A profile `include`, `exclude`, or `overrides` entry that no longer resolves
against the current pack contents — but was present in the previous inventory
— is surfaced as a `broken-ref` warning on every sync (and by the `broken_refs`
check in `aipack doctor`). Drift detection promotes these from silent drops
to visible, actionable output while preserving the hard-error behavior for
typos (IDs that were never in the pack).

## Save

Save captures harness content back into packs. Two modes.

### Round-trip (default)

Compares harness content against the ledger from the last sync and saves changed files back to their source packs. This is how you edit content in-place (in the harness) and push it back to the pack source.

```bash
aipack save --profile default                    # save changed files
aipack save --profile default --dry-run          # preview first
aipack save --profile default --force            # include settings changes
```

When `defaults.namespaced: true` renders pack provenance into markdown content names, save strips those rendered names back to source IDs before comparing and writing pack content.

**Tool permissions are not captured.** If a harness-side MCP `allow` / `deny` list has drifted from what the profile declares, `save` ignores the drift. The profile is the sole source of truth for MCP tool permission policy; the harness allow list is a render target, not an input. Adjust permissions through `aipack manage`'s tool picker (`t` on an MCP entry) so the profile YAML stays the canonical record.

### To-pack

Captures harness content and writes it into a named installed pack. If the pack doesn't exist, it's scaffolded and registered automatically. Use `--types` and `--harness` to narrow capture.

```bash
aipack save --to-pack my-pack                                    # capture all
aipack save --to-pack my-pack --harness claudecode --types rules,skills   # narrow
aipack save --to-pack new-pack --scope global                    # from global config
```

## Restore

Restores settings files from the pre-sync cache. Each `aipack sync` snapshots existing settings files before overwriting them, stored alongside the ledger in a `presync/` directory. Restore copies them back.

```bash
aipack restore --yes                              # undo last sync's settings changes
aipack restore --dry-run                          # preview first
aipack restore --harness claudecode --yes         # one harness only
aipack restore --scope global --yes               # global scope
```

For cache layout details, see [Configuration and State](./configuration.md#pre-sync-cache).

## Clean

Removes all sync-managed content from harness file locations. Preserves unrelated harness settings (model choice, provider config, etc.) by targeting only paths the harness adapter declares as managed. Prompts for confirmation unless `--yes` is set.

```bash
aipack clean --scope project                      # current project, all harnesses
aipack clean --scope project --dry-run            # preview first
aipack clean --scope global --harness cline --yes # one harness, skip confirmation
aipack clean --scope project --ledger --yes       # also remove .aipack/ ledger
```

## Render

Resolves the profile and renders all pack content into a self-contained output directory. The output is harness-independent — merged pack content without targeting any specific harness's file layout.

```bash
aipack render --profile default
aipack render --profile default --out-dir ./rendered-output
```

This is useful for inspecting resolved content, generating portable output, or debugging profile resolution issues.

## What to read next

- [Getting Started](./getting-started.md) — install packs and sync for the first time
- [Profiles](./profiles.md) — control what content syncs and how
- [Harness Reference](./harness-reference.md) — per-harness rendering, write targets, and settings merge behavior
- [Configuration and State](./configuration.md) — ledger, pre-sync cache, and state management
