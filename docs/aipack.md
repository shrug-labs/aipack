# aipack reference

Generic tool reference for `aipack` — the pack sync engine. This covers the top-level command surface, sync contract, per-harness behavior, profile configuration, and save behavior.

## Command map

Top-level commands in the current CLI surface:

- Setup: `init`, `doctor`
- Sync/Save: `sync`, `render`, `save`, `clean`, `restore`
- Pack management: `pack create`, `pack install`, `pack delete`, `pack update`, `pack rename`, `pack enable`, `pack disable`, `pack list`, `pack show`, `pack validate`
- Registry: `registry list`, `registry fetch`, `registry sources`, `registry remove`
- Profile management: `profile create`, `profile delete`, `profile list`, `profile set`, `profile show`, `manage`
- Discovery: `search`, `query`, `status`, `trace`
- Prompts: `prompt list`, `prompt copy`, `prompt show`
- Other: `version`

## Profiles

Profiles live in `${HOME}/.config/aipack/profiles/<name>.yaml` (schema v2). A profile selects which packs, content vectors, MCP servers, and harness settings to sync.

Key concepts:

- **Packs** select which installed packs to enable and which vectors to include/exclude
- **Settings** select which pack provides base harness config templates per harness
- **Overrides** allow a later pack to replace an earlier pack's content ID

Packs are referenced by name and resolved from `~/.config/aipack/packs/<name>/`. For a profile example, see the Quick Start in `README.md`.

### Profile layering

Profiles can compose multiple packs. Order matters — packs are processed in profile order, and duplicate content IDs across packs require explicit `overrides` declarations.

To create a layered profile from an existing harness config:

```bash
# 1. Capture your current state into a new pack.
aipack save --to-pack my-baseline --scope project --harness opencode

# 2. Edit the profile to add additional packs on top.
```

Profile resolution: `--profile <name>` resolves from `${HOME}/.config/aipack/profiles/<name>.yaml`. Use `--profile-path` for a direct file path.

## Sync

Applies the resolved profile to the target harnesses and writes managed content to project or global destinations.

Resolution order:

- Profile: `--profile-path` → `--profile` → `sync-config defaults.profile` → `default`
- Scope: `--scope` → `sync-config defaults.scope` → `project`
- Harness: `--harness` → `sync-config defaults.harnesses` → `AIPACK_DEFAULT_HARNESS`

Key flags:

- `--force` overrides file conflicts
- `--prune` deletes stale managed files not in the current plan
- `--skip-settings` skips harness settings files but still syncs MCP configs
- `--dry-run` previews planned changes without writing
- `--verbose` shows content diffs for changed files
- `--watch` re-syncs automatically when pack sources or config files change
- `--json` emits machine-readable output

```bash
# Sync the active/default profile to the current project directory
aipack sync

# Preview what would change without writing files
aipack sync --dry-run

# Preview with content diffs
aipack sync --dry-run --verbose

# Force-sync globally, overriding conflicts
aipack sync --profile prod --scope global --force

# Prune stale managed files
aipack sync --prune --yes

# Sync only one harness
aipack sync --harness opencode

# Watch pack sources and re-sync on every change
aipack sync --watch
```

## Sync contract

Exact sync semantics. If docs and code diverge, code is authoritative.

All managed files — content (rules, agents, workflows, skills) and config (harness settings) — go through unified diff classification:

- **Create**: no file on disk → written
- **Identical**: desired matches on-disk → no action
- **Managed**: on-disk matches ledger digest (unmodified since last sync) → updated silently
- **Conflict**: on-disk modified by user since last sync → unified diff shown, skipped without `--force`

`--force` controls conflict resolution; `--prune` controls stale file cleanup.

### Config sync (harness settings)

- Config files are computed from pack base config via `RenderBytes()`.
- `--skip-settings` skips harness settings files. Plugins (e.g. oh-my-opencode.json) and generated MCP configs (e.g. Cline) still sync.

### Content sync (rules, agents, workflows, skills)

- Content files are copied from pack directories to harness-specific locations.
- Prune of orphaned managed files only happens with `--prune`.

### Safety flags

- Non-destructive by default: conflicts shown with diffs and skipped.
- `--force` overrides all conflicts.
- `--prune` deletes stale managed files not in the current plan.
- `--yes` auto-confirms prune deletions.
- `--dry-run` previews all changes without writing.
- `--verbose` (`-v`) shows content diffs for changed files.
- `--watch` enters a continuous loop: performs an initial sync, then watches pack source directories and config files for changes and re-syncs automatically. Cancel with Ctrl+C. Cannot be combined with `--dry-run`.
- `--json` emits machine-readable JSON output.

### Provenance tracking

The ledger records which pack contributed each managed file (`source_pack` field). This enables `save --to-pack` round-trips.

### Determinism

Given identical inputs + profile, generated outputs must be byte-identical across runs.

## Per-harness behavior

Four harnesses are supported. Each implements content vectors and MCP differently based on what the harness natively supports. This section is the authoritative reference — all other docs should point here.

### Content vector support

| Vector | Claude Code | OpenCode | Codex | Cline |
|--------|-------------|----------|-------|-------|
| Rules | Individual files in `.claude/rules/` (frontmatter preserved, `paths:` scoping works natively) | Individual files in `.opencode/rules/` + referenced via `instructions` key in `opencode.json` | Flattened into `AGENTS.override.md` | Individual files in `.clinerules/` |
| Agents | Individual files in `.claude/agents/` (frontmatter transformed to Claude Code subagent format) | Individual files in `.opencode/agents/` | Promoted to skill dirs in `.agents/skills/` (enriched frontmatter preserves type + metadata for round-trip) | Promoted to skill dirs in `.clinerules/skills/` (enriched frontmatter preserves type + metadata for round-trip) |
| Workflows | Individual files in `.claude/commands/` | Individual files in `.opencode/commands/` | Promoted to skill dirs in `.agents/skills/` (enriched frontmatter preserves type + metadata for round-trip) | Individual files in `.clinerules/workflows/` |
| Skills | Per-skill dirs in `.claude/skills/` | Per-skill dirs in `.opencode/skills/` + referenced via `skills.paths` in `opencode.json` | Per-skill dirs in `.agents/skills/` | Per-skill dirs in `.clinerules/skills/` |

### Scope support

| Vector | Claude Code | OpenCode | Codex | Cline |
|--------|-------------|----------|-------|-------|
| Content (rules, agents, workflows, skills) | Project + Global | Project + Global | Project + Global | Project + Global |
| MCP servers | Project + Global | Project + Global | Project + Global | **Global only** |
| Settings | Project + Global | Project + Global | Project + Global | N/A |

### MCP server configuration

| Harness | Config file | Format | Timeout |
|---------|------------|--------|---------|
| Claude Code | `.mcp.json` (project), `~/.claude.json` (global) | JSON `mcpServers` object | Global only via `MCP_TIMEOUT` env var, milliseconds (default 10000); no per-server timeout in config |
| OpenCode | `opencode.json` | JSON `mcp` key | Milliseconds (default 10000) |
| Codex | `.codex/config.toml` | TOML `[mcp_servers.<name>]` tables | Seconds (`startup_timeout_sec`, default 10) |
| Cline | VS Code extension storage `cline_mcp_settings.json` (global only) | JSON `mcpServers` object | Seconds (default 10) |

### MCP tool permissions

Each harness controls MCP tool access differently. Some harnesses store permissions separately from the server connection config (Claude Code), while others co-locate them (Codex, OpenCode, Cline).

| Harness | Permission location | Allow format | Deny format |
|---------|-------------------|-------------|-------------|
| Claude Code | `settings.local.json` `permissions.allow` / `permissions.deny` | `mcp__<server>__<tool>` patterns | `mcp__<server>__<tool>` patterns in `permissions.deny` |
| OpenCode | `opencode.json` `tools` key | `server_tool: true` per-tool | `server_*: false` wildcard deny |
| Codex | Per-server in TOML | `enabled_tools = [...]` | `disabled_tools = [...]` |
| Cline | Per-server in MCP JSON | `alwaysAllow: [...]` | Not supported |

**Allow semantics differ per harness.** Not all "allow" mechanisms restrict tool visibility:

| Harness | `allow` means | `deny` means |
|---------|--------------|-------------|
| Claude Code | Auto-approve (tool still usable without it, just prompts) | Block entirely |
| Cline | Auto-approve (`alwaysAllow`) | N/A |
| OpenCode | Enable tool (boolean `true` in `tools` map) | Wildcard disable (`false`) |
| Codex | Restrict to listed tools (`enabled_tools`) | Block listed tools (`disabled_tools`) |

**Inventory policy:** when a server has a curated `AllowedTools` list, unspecified tools should be explicitly denied where the harness supports it. This requires the pack manifest to carry complete per-server tool inventories. Without complete inventories, only explicitly listed `disabled_tools` are denied; unlisted tools default to harness-specific behavior (ask/prompt for Claude Code and Cline, unrestricted for others).

### Settings files and merge behavior

| Harness | Settings file | Plugin files | Format | Merge behavior |
|---------|--------------|-------------|--------|----------------|
| Claude Code | `.claude/settings.local.json` | `.mcp.json` | JSON | **Always three-way merge** — user permissions preserved, only `mcp__*` entries managed |
| OpenCode | `.opencode/opencode.json` | `.opencode/oh-my-opencode.json` | JSON | Template + managed keys overlay. With `--skip-settings`: MergeMode (managed keys only) |
| Codex | `.codex/config.toml` | None | TOML | Template + MCP table merge. With `--skip-settings`: MergeMode (`mcp_servers` only) |
| Cline | None | `cline_mcp_settings.json` | JSON | Generated from inventory (no base template). Always synced |

`--skip-settings` skips settings files but **plugins always sync** regardless.

### Environment variable expansion

Pack content uses `{env:VAR}` placeholders. All harnesses resolve them identically at sync time: the placeholder is replaced with the literal value from the process environment. If the variable is not set, the MCP server is skipped entirely and a warning is emitted.

### Write targets (full path reference)

**Claude Code** (project + global)

| What | Project path | Global path |
|------|-------------|------------|
| Rules | `.claude/rules/<file>.md` | `~/.claude/rules/<file>.md` |
| Agents | `.claude/agents/<file>.md` | `~/.claude/agents/<file>.md` |
| Workflows | `.claude/commands/<file>.md` | `~/.claude/commands/<file>.md` |
| Skills | `.claude/skills/<dirname>/` | `~/.claude/skills/<dirname>/` |
| MCP servers | `.mcp.json` | `~/.claude.json` |
| Settings | `.claude/settings.local.json` | `~/.claude/settings.local.json` |

**OpenCode** (project + global)

| What | Project path | Global path |
|------|-------------|------------|
| Rules | `.opencode/rules/<file>.md` | `~/.config/opencode/rules/<file>.md` |
| Agents | `.opencode/agents/<file>.md` | `~/.config/opencode/agents/<file>.md` |
| Workflows | `.opencode/commands/<file>.md` | `~/.config/opencode/commands/<file>.md` |
| Skills | `.opencode/skills/<dirname>/` | `~/.config/opencode/skills/<dirname>/` |
| Settings | `.opencode/opencode.json` | `~/.config/opencode/opencode.json` |
| Plugin | `.opencode/oh-my-opencode.json` | `~/.config/opencode/oh-my-opencode.json` |

**Codex** (project + global)

| What | Project path | Global path |
|------|-------------|------------|
| Rules | `AGENTS.override.md` (flattened) | `~/.codex/AGENTS.override.md` |
| Agents + workflows | `.agents/skills/<name>/SKILL.md` (promoted) | `~/.agents/skills/<name>/SKILL.md` |
| Skills | `.agents/skills/<dirname>/` | `~/.agents/skills/<dirname>/` |
| Settings | `.codex/config.toml` | `~/.codex/config.toml` |

**Cline** (content: project + global; MCP: global only)

| What | Project path | Global path |
|------|-------------|------------|
| Rules | `.clinerules/<file>.md` | `~/Documents/Cline/Rules/aipack/<file>.md` |
| Agents | `.clinerules/skills/<name>/SKILL.md` (promoted) | `~/.cline/skills/<name>/SKILL.md` (promoted) |
| Workflows | `.clinerules/workflows/<file>.md` | `~/Documents/Cline/Workflows/aipack/<file>.md` |
| Skills | `.clinerules/skills/<dirname>/` | `~/.cline/skills/<dirname>/` |
| MCP | N/A | `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json` (macOS) |

### Managed keys (stripped on save round-trip)

| Harness | Keys stripped |
|---------|-------------|
| Claude Code | `mcp__*` entries in `permissions.allow` and `permissions.deny` |
| OpenCode | `mcp`, `tools`, `instructions`, `skills` |
| Codex | `mcp_servers` |
| Cline | `mcpServers` |

### Harness-specific notes

**Claude Code**
- Rules: copied as individual files to `.claude/rules/`. `paths:` frontmatter scoping works natively in Claude Code; unknown frontmatter fields (`title`, `audience`, `last_updated`) are ignored.
- Agents: frontmatter transformed to Claude Code native subagent format — `name` from pack frontmatter (or derived from filename), `description`/`skills`/`mcpServers` passed through. `tools` and `disallowed_tools` are mapped to PascalCase (`read` → `Read`, `bash` → `Bash`) and converted from YAML lists to comma-separated strings. When `mcpServers` is present, MCP-prefixed tools are filtered out of `tools:` (Claude Code's tools field creates a hard allowlist that would block MCP server access). Pack `disallowed_tools` → `disallowedTools`, pack `mcp_servers` → `mcpServers`. Non-portable fields (`mode`, `temperature`) are dropped.
- Workflows: individual command files in `.claude/commands/` only (no dual materialization).
- `CLAUDE.managed.md` is no longer written. On first sync after upgrade, it becomes a prune candidate (pruned with `--prune`). `CLAUDE.md` is no longer touched.
- Global scope syncs to `~/.claude/{rules,agents,skills,commands}/`.
- `settings.local.json` always uses three-way merge, even without `--skip-settings`. User-controlled permissions (non-`mcp__` prefix) are always preserved in both `allow` and `deny` arrays.
- `permissions.deny` blocks tools entirely (deny > ask > allow precedence). Unlike OpenCode's `server_*: false` wildcard, Claude Code cannot use wildcard deny patterns because deny always takes precedence over allow regardless of specificity. Only explicit per-tool deny entries are rendered from `disabled_tools` in the profile config.

**OpenCode**
- Rules are both copied as individual files AND referenced via `instructions` globs in `opencode.json`. Skills are both copied AND referenced via `skills.paths`. These JSON references are only managed when the respective vector has `Manage: true` in the profile.
- `oh-my-opencode.json` is a plugin (pure copy from pack), always synced regardless of `--skip-settings`.
- `tools` key (MCP tool boolean map) is distinct from `permission` key (OpenCode's native harness tool access). Do not conflate them.

**Codex**
- Rules are flattened into a single `AGENTS.override.md`. If an existing `AGENTS.md` exists, its content is preserved below a separator.
- Agents and workflows are promoted to `.agents/skills/<name>/SKILL.md` with enriched YAML frontmatter that preserves the original type (`source_type: agent` or `source_type: workflow`) and metadata for round-trip capture. Skills are copied as directories under the same path.
- Global config path is always `~/.codex/`.

**Cline**
- MCP is global-only — there is no project-level MCP settings path.
- Agents (but not workflows) are promoted to skill directories in `.clinerules/skills/` (project) or `~/.cline/skills/` (global), with enriched YAML frontmatter (`source_type: agent`) that preserves agent metadata for round-trip capture. Workflows remain individual files in `.clinerules/workflows/`. The promotion mechanism uses the same enriched-frontmatter approach as Codex, but Codex also promotes workflows.
- The MCP settings file is generated fresh from inventory on every sync (no base template concept). Existing user-defined `mcpServers` entries are preserved during merge.
- `alwaysAllow` is allow-only — there is no mechanism to deny specific tools.

## Save

Two modes: **round-trip** (default) and **to-pack** (`--to-pack`).

- **Round-trip**: loads a profile, compares harness content against the ledger from the last sync, and saves changed files back to their source packs. Settings files require `--force`.
- **To-pack**: captures harness content and writes it into the named installed pack. If the pack does not exist, it is scaffolded and registered automatically. Use `--types` and `--harness` to narrow capture.

Harness resolution: `--harness` → `sync-config defaults.harnesses` → `AIPACK_DEFAULT_HARNESS`.

Key flags:

- `--to-pack <name>` switches to capture-to-pack mode
- `--types rules,agents,...` filters saved categories in `--to-pack` mode
- `--force` auto-approves settings saves and overwrites file conflicts
- `--dry-run` previews changes without writing

```bash
# Round-trip: save changed files back to source packs
aipack save --profile default

# Round-trip: preview changes without writing
aipack save --profile default --dry-run

# Round-trip: include settings changes
aipack save --profile default --force

# To-pack: capture current harness content into an installed pack
aipack save --to-pack my-pack

# To-pack: save only rules and skills from one harness
aipack save --to-pack my-pack --harness claudecode --types rules,skills

# To-pack: create a new pack from current global harness state
aipack save --to-pack new-pack --scope global
```

## Pack management

Packs are portable, versioned bundles of AI agent configuration installed under `~/.config/aipack/packs/<name>/`.

### pack create

Scaffolds a new pack directory with `pack.json` manifest and standard subdirectories (`rules/`, `agents/`, `workflows/`, `skills/`, `mcp/`, `configs/`).

```bash
aipack pack create ./my-new-pack
aipack pack create ./path/to/dir --name custom-pack-name
```

### pack install

Installs a pack into `~/.config/aipack/packs/<name>/`. Supports three sources:

- **Local path** (symlinked by default, `--copy` for full copy)
- **URL** (`--url` — fetched via `git archive` with automatic fallback to shallow clone)
- **Registry name** (bare name like `my-team-pack` — looked up in registry, then fetched)

With `-m`/`--missing`, installs all missing packs from the active profile by looking them up in the registry. This is the easiest way to catch up after setting a profile or after new packs are added to a shared profile.

Remote packs are fetched using a two-phase process: first the manifest (`pack.json`) is retrieved to determine declared content, then only the declared files are fetched. This avoids downloading the full repository. When the remote doesn't support `git archive --remote` (e.g. GitHub), aipack falls back to a shallow clone automatically.

Both HTTPS and SSH URLs are supported. SSH URLs (`git@host:path` or `ssh://`) avoid credential prompts.

By default, auto-registers the pack as a source in the active profile. Use `--no-register` to skip, or `--profile <name>` to target a specific profile.

Packs that bundle registries or profiles print a preview of what would be seeded. Use `--seed` to apply them, or review the output and seed manually.

```bash
# Install all missing packs from the active profile
aipack pack install -m

# Local installs
aipack pack install ./my-pack
aipack pack install ./my-pack --copy --name custom-name

# Remote installs (HTTPS and SSH)
aipack pack install --url https://github.com/org/pack-repo.git
aipack pack install --url git@github.com:org/pack-repo.git --ref main

# Subdirectory within a mono-repo
aipack pack install --url https://github.com/org/shared-repo.git --path team-pack

# Registry name
aipack pack install my-team-pack

# Apply bundled registries and profiles
aipack pack install --url https://github.com/org/repo.git --path team-pack --seed

# Profile and registration control
aipack pack install ./my-pack --no-register
aipack pack install ./my-pack --profile production
```

### pack list

Lists all installed packs with name, install method (link/copy/clone/archive), version, origin, and broken-link status.

```bash
aipack pack list
aipack pack list --json
```

### pack show

Displays detailed metadata for an installed pack: name, version, path, install method, origin, git ref, commit hash, install timestamp, and content inventory (rules, agents, workflows, skills, MCP servers).

```bash
aipack pack show my-pack
aipack pack show my-pack --json
```

### pack delete

Deletes an installed pack from disk and deregisters it from all profiles.

```bash
aipack pack delete my-pack
```

### pack update

Updates installed pack(s) to latest version from their origin. For archive-installed packs, re-fetches declared content and shows a file-level diff of changes. For git-cloned packs, runs `git pull`. For copied packs, re-copies from the recorded origin. For symlinked packs, re-validates the link target. Exactly one of `<name>` or `--all` is required.

```bash
aipack pack update my-pack
aipack pack update --all
```

### pack rename

Renames an installed pack across all configuration: the pack directory, `pack.json` manifest, `sync-config.yaml`, all profiles, and all ledger files.

```bash
aipack pack rename old-name new-name
```

### pack enable / pack disable

Enables or disables a pack in the active profile without installing or deleting it from disk.

```bash
aipack pack enable my-pack
aipack pack enable my-pack --profile production
aipack pack disable my-pack
aipack pack disable my-pack --profile production
```

## Profile management

Profiles define which packs, content, and settings to sync. Stored as YAML under `~/.config/aipack/profiles/`.

### profile list

Lists all profiles. The active profile (from `defaults.profile` in sync-config) is marked with `*`.

```bash
aipack profile list
```

### profile create / profile delete

Creates an empty profile or deletes an existing one. Deleting the active profile clears the active setting.

```bash
aipack profile create staging
aipack profile delete staging
```

### profile set

Sets the active profile by updating `defaults.profile` in `sync-config.yaml`. Reports any packs declared in the profile that are not installed.

Use `--install` to automatically install missing packs from the registry after setting the profile.

```bash
aipack profile set my-team
aipack profile set my-team --install
```

### profile show

Loads and fully resolves a profile — packs with content inventories, MCP servers, and settings.

```bash
aipack profile show
aipack profile show production
aipack profile show --json
aipack profile show --profile-path /path/to/profile.yaml
```

## Registry

The registry maps pack names to source repositories. The unified view merges:

1. **Local entries** from `~/.config/aipack/registry.yaml` (highest priority, manual edits)
2. **Cached remote sources** in `~/.config/aipack/registries/` (in source order from sync-config)

### registry list

Browse the merged registry.

```bash
aipack registry list
aipack registry list --registry /path/to/registry.yaml
aipack registry list --json
```

### registry sources

Lists all configured registry sources from sync-config, showing name, URL, git ref, and cache status.

```bash
aipack registry sources
aipack registry sources --json
```

### registry fetch

Fetches remote registries and caches them locally. Each source is cached as a separate file and saved to `registry_sources` in sync-config for future fetches.

With an explicit URL, fetches that single source. Without a URL, fetches all configured sources (or the compiled-in default from `shrug-labs/aipack`).

Git detection: URL ending in `.git` → git mode (defaults: `ref=main`, `path=registry.yaml`). `git@host:path` or `ssh://` → git mode. `--ref` provided → git mode. Otherwise → HTTP GET.

```bash
# Fetch from a git repo (HTTPS)
aipack registry fetch https://bitbucket.example.com/scm/TEAM/tools.git

# Fetch from a git repo (SSH — avoids credential prompts)
aipack registry fetch git@bitbucket.example.com:TEAM/tools.git

# Fetch with explicit ref and path
aipack registry fetch https://bitbucket.example.com/scm/TEAM/tools.git \
  --ref team/ai-runbooks --path ai-runbooks/registry.yaml

# Override the cached source name
aipack registry fetch https://bitbucket.example.com/scm/TEAM/tools.git --name my-tools

# Fetch from an HTTP URL
aipack registry fetch https://example.com/registry.yaml

# Fetch all configured sources
aipack registry fetch

# Deep-index for resource-level search
aipack registry fetch --deep
```

### registry remove

Removes a registry source from sync-config and deletes its cache file.

```bash
aipack registry remove my-tools
```

## Discovery (search index)

SQLite-backed search index across all installed packs and registry entries. Built automatically during `registry fetch --deep` and pack install.

### search

Full-text search (FTS5 with BM25 ranking) across resource names, descriptions, and body text. Supports filtering by `--tags`, `--role`, `--kind` (rule/skill/workflow/agent/pack), `--category` (ops/dev/infra/governance/meta), `--pack`, `--installed`, `--available`.

```bash
aipack search 5xx triage
aipack search --category ops
aipack search --tags observability --role oncall-operator
aipack search deploy --kind workflow --category infra
aipack search 5xx --installed
aipack search --available
aipack search 5xx --json
```

### query

Raw SQL against the index database. Returns JSON. Use `--schema` to inspect tables.

```bash
aipack query --schema
aipack query "SELECT r.name, r.description FROM resources r JOIN tags t ON t.resource_id = r.id WHERE r.kind = 'skill' AND t.tag = '5xx'"
aipack query "SELECT tag, COUNT(*) as count FROM tags GROUP BY tag ORDER BY count DESC"
```

## Restore

Restores settings files from the pre-sync cache. Each `aipack sync` snapshots existing settings files before overwriting them, stored alongside the ledger in a `presync/` directory. Restore copies them back.

```bash
# Undo the last sync's settings changes
aipack restore --yes

# Preview what would be restored
aipack restore --dry-run

# Restore only claudecode settings
aipack restore --harness claudecode --yes

# Restore global-scope settings
aipack restore --scope global --yes

# Machine-readable JSON output
aipack restore --json
```

### Settings cache behavior

During sync, aipack caches each settings file's pre-sync content:

- **presync cache**: overwritten on every sync, enables undo-last-sync via `aipack restore`
- Cache files are keyed by `<harness>--<filename>` with an `index.json` manifest
- Only settings and plugin files are cached (not content files like rules/agents)
- `--dry-run` sync does not write cache files

## Init

Creates `~/.config/aipack/sync-config.yaml` and `~/.config/aipack/profiles/default.yaml` with starter content. Skips files that already exist unless `--force` is set.

```bash
aipack init
aipack init --force
aipack init --config-dir /path/to/config
```

## Pack validate

Read-only validation of a single pack source tree. Checks pack structure, manifest inventory, frontmatter correctness, and content policy (secrets, forbidden paths) without installing or syncing anything. Exit code 0 if clean, 1 if findings are reported.

```bash
aipack pack validate ./my-pack
aipack pack validate ./my-pack --json
```

Each finding includes a severity (`error` or `warning`), a category (`frontmatter`, `policy`, `consistency`, or `inventory`), the file path, and a message. In human output, findings are printed as `- [severity] path: message`. The `--json` output returns a `{"ok": bool, "findings": [...]}` object where each finding has `path`, `category`, `severity`, `field` (optional, for frontmatter issues), and `message` keys.

## Clean

Removes all sync-managed content from harness file locations: rules, agents, workflows, skills, MCP server configs, and tool allowlists. Preserves unrelated harness settings (model choice, provider config, etc.) by only targeting paths the harness adapter declares as managed. Prompts for confirmation unless `--yes` is set.

```bash
# Clean managed files from the current project (all harnesses)
aipack clean --scope project

# Preview what would be removed
aipack clean --scope project --dry-run

# Clean only the cline harness globally, skip confirmation
aipack clean --scope global --harness cline --yes

# Also remove the .aipack/ ledger directory
aipack clean --scope project --ledger --yes
```

## Render

Resolves the profile and renders all pack content (rules, agents, workflows, skills, MCP configs) into a self-contained output directory. The output is harness-independent — merged pack content without targeting any specific harness's file layout. Prints the output directory path to stdout.

```bash
aipack render --profile default
aipack render --profile default --out-dir ./rendered-output
aipack render --profile-path /path/to/profile.yaml --out-dir ./out
aipack render --profile default --json
```

## Prompt

Browse and copy prompts from installed packs. Prompts are opaque text blobs (no frontmatter validation) declared in a pack's `prompts/` directory.

```bash
aipack prompt list
aipack prompt show my-prompt
aipack prompt copy my-prompt   # copies to clipboard
```

## Trace

Traces a single resource through the sync pipeline, showing where it comes from (pack source) and where it would land in each harness location. Useful for debugging why a rule isn't showing up or which harness file contains a given resource.

Valid resource types: `rule`, `agent`, `workflow`, `skill`, `mcp`.

```bash
# Trace a rule named "anti-slop"
aipack trace rule anti-slop

# Trace a skill named "oncall"
aipack trace skill oncall --scope global

# Trace an MCP server named "atlassian"
aipack trace mcp atlassian

# Filter to a single harness
aipack trace rule anti-slop --harness claudecode

# JSON output for tooling
aipack trace rule anti-slop --json
```

The output shows the source pack, source file path, and each destination with its harness, file path, and on-disk state (`create`, `identical`, `managed`, `conflict`, `untracked`, or `error`). Use `--harness` to filter output to a single harness. Destinations where the resource is composited into a multi-resource file (e.g. Codex flattening rules into `AGENTS.override.md`) are flagged as embedded separately from the state.

## Doctor checks

`aipack doctor` runs diagnostic checks and reports issues. In addition to the core checks (sync-config, profile, packs, MCP, env vars, ledger), the following checks are included:

| Check | Severity | What it does |
|-------|----------|-------------|
| `cli_update` | warning | Checks if a newer CLI version is available |
| `git_available` | warning | Verifies git is installed (needed for registry fetch and pack install) |
| `profile_validated` | warning | Validates profile YAML structure |
| `packs_registered` | warning | Detects pack directories not in `installed_packs` |
| `pack_version_drift` | warning | Compares installed pack versions/hashes against their origins (local checks only, no network) |
| `stale_ledgers` | warning | Detects ledger files orphaned from a previous scope or harness configuration |
| `ledger_health` | warning | Checks for orphaned entries and missing `source_pack` fields (auto-fixable with `--fix`) |
| `manifest_drift` | warning | Detects undeclared or missing content in pack manifests (auto-fixable with `--fix`) |

```bash
aipack doctor
aipack doctor --fix       # auto-fix safe issues
aipack doctor --json      # machine-readable output
```

## Status

Shows ecosystem status: active profile, installed packs with content inventories, and totals.

```bash
aipack status
aipack status --profile production
aipack status --profile-path /path/to/profile.yaml
aipack status --json
```

## Interactive TUI

`aipack manage` opens a terminal UI for managing profiles and packs. Requires a TTY.

Tabs: Profiles, Packs, Save, Sync, Search.

Key bindings: `tab` switch tabs, `j/k` navigate, `enter` expand, `space` toggle, `l` list profiles, `n` new profile, `d` delete, `D` duplicate, `a` activate, `p` add pack, `r` remove pack, `s` sync, `esc` quit (auto-saves).

```bash
aipack manage
```

## Version

Prints the CLI version string.

```bash
aipack version
```

## Implementation references

- Claude Code: `internal/harness/claudecode/harness.go`, `internal/harness/claudecode/render.go`
- OpenCode: `internal/harness/opencode/harness.go`, `internal/harness/opencode/render.go`
- Codex: `internal/harness/codex/harness.go`, `internal/harness/codex/render.go`
- Cline: `internal/harness/cline/harness.go`, `internal/harness/cline/render.go`
- Sync engine: `internal/engine/`
- Config resolution: `internal/config/profile_resolve.go`

If docs and code diverge, the code is authoritative.

### Upstream harness docs

- OpenCode: [MCP](https://opencode.ai/docs/mcp-servers/#enable), [Config](https://opencode.ai/docs/config/#instructions), [Agents](https://opencode.ai/docs/agents/#markdown), [Commands](https://opencode.ai/docs/commands/#markdown)
- Codex: [AGENTS.md](https://developers.openai.com/codex/guides/agents-md/), [Skills](https://developers.openai.com/codex/skills/), [MCP](https://developers.openai.com/codex/mcp)
- Claude Code: [Memory/Rules](https://code.claude.com/docs/en/memory), [Subagents](https://code.claude.com/docs/en/sub-agents), [Skills](https://code.claude.com/docs/en/skills)
- Cline: [Storage](https://docs.cline.bot/customization/overview#storage-locations), [MCP Config](https://docs.cline.bot/mcp/adding-and-configuring-servers#editing-configuration-files)
