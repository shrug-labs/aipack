# aipack reference

Generic tool reference for `aipack` — the pack sync engine. This covers sync contract, per-harness behavior, profile configuration, and save modes.

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
# 1. Snapshot your current state as a baseline pack.
aipack save --snapshot --scope project --harness opencode --to-profile my-base

# 2. Edit the profile to add additional packs on top.
```

Profile resolution: `--profile <name>` resolves from `${HOME}/.config/aipack/profiles/<name>.yaml`. Use `--profile-path` for a direct file path.

## Sync contract

Exact sync semantics. If docs and code diverge, code is authoritative.

All managed files — content (rules, agents, workflows, skills) and config (harness settings) — go through unified diff classification:

- **Create**: no file on disk → written
- **Identical**: desired matches on-disk → no action
- **Managed**: on-disk matches ledger digest (unmodified since last sync) → updated silently
- **Conflict**: on-disk modified by user since last sync → unified diff shown, skipped without `--force`

One `--force` flag controls all conflict resolution.

### Config sync (harness settings)

- Config files are computed from pack base config via `RenderBytes()`.
- `--skip-settings` skips harness settings files. Plugins (e.g. oh-my-opencode.json) and generated MCP configs (e.g. Cline) still sync.

### Content sync (rules, agents, workflows, skills)

- Content files are copied from pack directories to harness-specific locations.
- Prune of orphaned managed files only happens with `--force`.

### Safety flags

- Non-destructive by default: conflicts shown with diffs and skipped.
- `--force` overrides all conflicts and enables prune.
- `--yes` auto-confirms prune deletions.
- `--dry-run` previews all changes without writing.

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
| Agents | Individual files in `.claude/agents/` (frontmatter transformed to Claude Code subagent format) | Individual files in `.opencode/agents/` | Inlined into `AGENTS.override.md` | Individual files in `.clinerules/` (same dir as rules) |
| Workflows | Individual files in `.claude/commands/` | Individual files in `.opencode/commands/` | Inlined into `AGENTS.override.md` | Individual files in `.clinerules/workflows/` |
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
| Claude Code | `.mcp.json` (project root) | JSON `mcpServers` object | Not configurable |
| OpenCode | `opencode.json` | JSON `mcp` key | Milliseconds (default 10000) |
| Codex | `.codex/config.toml` | TOML `[mcp_servers.<name>]` tables | Not configurable |
| Cline | VS Code extension storage `cline_mcp_settings.json` (global only) | JSON `mcpServers` object | Seconds (default 10) |

### MCP tool permissions

Each harness controls MCP tool access differently. Permissions are always in a **separate location** from the server connection config.

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

Pack content uses `{env:VAR}` placeholders. Each harness expands them to its native format:

| Harness | Output format | Shell wrapping | Missing env behavior |
|---------|--------------|----------------|---------------------|
| Claude Code | `${VAR}` | No | Server skipped |
| OpenCode | Resolve if env is set; otherwise keep `{env:VAR}` literal | No | Server preserved; unresolved refs stay literal |
| Codex | `$VAR` | Yes: `bash -lc '<cmd>'` when env refs present | Written literally (never skipped) |
| Cline | `${VAR}` | No | Server skipped |

### Write targets (full path reference)

**Claude Code** (project + global)

| What | Project path | Global path |
|------|-------------|------------|
| Rules | `.claude/rules/<file>.md` | `~/.claude/rules/<file>.md` |
| Agents | `.claude/agents/<file>.md` | `~/.claude/agents/<file>.md` |
| Workflows | `.claude/commands/<file>.md` | `~/.claude/commands/<file>.md` |
| Skills | `.claude/skills/<dirname>/` | `~/.claude/skills/<dirname>/` |
| MCP servers | `.mcp.json` | `~/.claude/.mcp.json` |
| Settings | `.claude/settings.local.json` | `~/.claude/settings.json` |

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
| Rules + agents + workflows | `AGENTS.override.md` (flattened) | `~/.codex/AGENTS.override.md` |
| Skills | `.agents/skills/<dirname>/` | `~/.agents/skills/<dirname>/` |
| Settings | `.codex/config.toml` | `~/.codex/config.toml` |

**Cline** (content: project + global; MCP: global only)

| What | Project path | Global path |
|------|-------------|------------|
| Rules + agents | `.clinerules/<file>.md` | `~/Documents/Cline/Rules/aipack/<file>.md` |
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
- Agents: frontmatter transformed to Claude Code native subagent format — `name` from pack frontmatter (or derived from filename), `description`/`tools`/`skills`/`mcpServers` passed through, pack `disallowed_tools` → `disallowedTools`, pack `mcp_servers` → `mcpServers`. Non-portable fields (`mode`, `temperature`) are dropped.
- Workflows: individual command files in `.claude/commands/` only (no dual materialization).
- `CLAUDE.managed.md` is no longer written. On first sync after upgrade, it becomes a prune candidate (pruned with `--force`). `CLAUDE.md` is no longer touched.
- Global scope syncs to `~/.claude/{rules,agents,skills,commands}/`.
- `settings.local.json` always uses three-way merge, even without `--skip-settings`. User-controlled permissions (non-`mcp__` prefix) are always preserved in both `allow` and `deny` arrays.
- `permissions.deny` blocks tools entirely (deny > ask > allow precedence). Unlike OpenCode's `server_*: false` wildcard, Claude Code cannot use wildcard deny patterns because deny always takes precedence over allow regardless of specificity. Only explicit per-tool deny entries are rendered from `disabled_tools` in the profile config.

**OpenCode**
- Rules are both copied as individual files AND referenced via `instructions` globs in `opencode.json`. Skills are both copied AND referenced via `skills.paths`. These JSON references are only managed when the respective vector has `Manage: true` in the profile.
- `oh-my-opencode.json` is a plugin (pure copy from pack), always synced regardless of `--skip-settings`.
- `tools` key (MCP tool boolean map) is distinct from `permission` key (OpenCode's native harness tool access). Do not conflate them.

**Codex**
- All text content (rules, agents, workflows) is flattened into a single `AGENTS.override.md`. If an existing `AGENTS.md` exists, its content is preserved below a separator.
- Skills are the only vector that gets individual directories (`.agents/skills/`).
- Env var expansion uses shell wrapping: when a command contains `{env:VAR}` refs, the entire command becomes `bash -lc '<escaped-cmd>'`.
- Global config path is always `~/.codex/`.

**Cline**
- MCP is global-only — there is no project-level MCP settings path.
- Rules and agents share the same directory (`.clinerules/` for project, `~/Documents/Cline/Rules/aipack/` for global). There is no separate agents directory.
- The MCP settings file is generated fresh from inventory on every sync (no base template concept). Existing user-defined `mcpServers` entries are preserved during merge.
- `alwaysAllow` is allow-only — there is no mechanism to deny specific tools.

## Save modes

Three modes: **round-trip** (default), **snapshot** (`--snapshot`), **to-pack** (`--to-pack`).

- **Round-trip**: loads profile to resolve pack roots, compares harness content against ledger, saves changed files back to source packs. Use `--force` to auto-approve settings saves. Use `--dry-run` to preview.
- **Snapshot**: timestamped backup into `~/.config/aipack/saved/<timestamp>/` and installed as a pack.
- **To-pack**: saves all vectors to a specific installed pack. Scaffolds new pack if needed. No profile required.

Harness resolution: explicit `--harness` → `defaults.harnesses` in sync-config → `AIPACK_DEFAULT_HARNESS`.

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
- **URL** (`--url https://...` — cloned via git)
- **Registry name** (bare name like `my-team-pack` — looked up in registry, then cloned)

By default, auto-registers the pack as a source in the active profile. Use `--no-register` to skip, or `--profile <name>` to target a specific profile.

```bash
aipack pack install ./my-pack
aipack pack install ./my-pack --copy --name custom-name
aipack pack install --url https://github.com/org/pack-repo
aipack pack install my-team-pack              # registry name
aipack pack install ./my-pack --no-register
aipack pack install ./my-pack --profile production
```

### pack list

Lists all installed packs with name, install method (link/copy/clone), version, origin, and broken-link status.

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

Updates installed pack(s) to latest version from their origin. For git-cloned packs, runs `git pull`. For symlinked packs, re-validates the link target. Exactly one of `<name>` or `--all` is required.

```bash
aipack pack update my-pack
aipack pack update --all
```

### pack add / pack remove

Adds or removes a pack from the active profile without installing or deleting it.

```bash
aipack pack add my-pack
aipack pack add my-pack --profile production
aipack pack remove my-pack
aipack pack remove my-pack --profile production
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

Sets the active profile by updating `defaults.profile` in `sync-config.yaml`.

```bash
aipack profile set ocm
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

### registry list / registry search

Browse or search the merged registry by name/description.

```bash
aipack registry list
aipack registry list --json
aipack registry search openshift
aipack registry search api --json
```

### registry fetch

Fetches remote registries and caches them locally. Each source is cached as a separate file and saved to `registry_sources` in sync-config for future fetches.

With an explicit URL, fetches that single source. Without a URL, fetches all configured sources (or the compiled-in default from `shrug-labs/aipack`).

Git detection: URL ending in `.git` → git mode (defaults: `ref=main`, `path=registry.yaml`). `--ref` provided → git mode. Otherwise → HTTP GET.

```bash
# Fetch from a git repo (auto-detected from .git suffix)
aipack registry fetch https://bitbucket.example.com/scm/TEAM/tools.git

# Fetch with explicit ref and path
aipack registry fetch https://bitbucket.example.com/scm/TEAM/tools.git \
  --ref team/ai-runbooks --path ai-runbooks/registry.yaml

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
aipack registry remove ocm-ops-tools
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

## Interactive TUI

`aipack manage` opens a terminal UI for managing profiles and packs. Requires a TTY.

Tabs: Profiles, Packs, Registry, Sync, Search.

Key bindings: `tab` switch tabs, `j/k` navigate, `enter` expand, `space` toggle, `l` list profiles, `n` new profile, `d` delete, `D` duplicate, `a` activate, `p` add pack, `r` remove pack, `s` sync, `esc` quit (auto-saves).

```bash
aipack manage
```

## Implementation references

- Claude Code: `internal/harness/claudecode/harness.go`, `internal/harness/claudecode/render.go`
- OpenCode: `internal/harness/opencode/harness.go`, `internal/harness/opencode/render.go`
- Codex: `internal/harness/codex/harness.go`, `internal/harness/codex/render.go`
- Cline: `internal/harness/cline/harness.go`, `internal/harness/cline/render.go`
- Sync engine: `internal/engine/`
- Config resolution: `internal/config/resolve.go`

If docs and code diverge, the code is authoritative.

### Upstream harness docs

- OpenCode: [MCP](https://opencode.ai/docs/mcp-servers/#enable), [Config](https://opencode.ai/docs/config/#instructions), [Agents](https://opencode.ai/docs/agents/#markdown), [Commands](https://opencode.ai/docs/commands/#markdown)
- Codex: [AGENTS.md](https://developers.openai.com/codex/guides/agents-md/), [Skills](https://developers.openai.com/codex/skills/), [MCP](https://developers.openai.com/codex/mcp)
- Claude Code: [Memory/Rules](https://code.claude.com/docs/en/memory), [Subagents](https://code.claude.com/docs/en/sub-agents), [Skills](https://code.claude.com/docs/en/skills)
- Cline: [Storage](https://docs.cline.bot/customization/overview#storage-locations), [MCP Config](https://docs.cline.bot/mcp/adding-and-configuring-servers#editing-configuration-files)
