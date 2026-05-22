# Harness Reference

Four harnesses are supported: Claude Code, OpenCode, Codex, and Cline. Each renders pack content differently based on what the harness natively supports. This is the authoritative reference for rendering behavior — when other docs describe harness behavior, they should point here.

For the CLI commands that trigger sync, see the [aipack reference](./aipack.md). For the pack content format itself, see the [Pack Format Specification](./pack-format.md). For how profiles control what gets synced, see [Profiles](./profiles.md).

## Content vector rendering

| Vector | Claude Code | OpenCode | Codex | Cline |
|--------|-------------|----------|-------|-------|
| Rules | Individual files in `.claude/rules/` (frontmatter preserved, `paths:` scoping works natively) | Individual files in `.opencode/rules/` + referenced via `instructions` key in `opencode.json` | Flattened into `AGENTS.override.md` | Individual files in `.clinerules/` |
| Agents | Individual files in `.claude/agents/` (frontmatter transformed to Claude Code subagent format) | Individual files in `.opencode/agents/` | Native TOML files in `.codex/agents/` + registration in `config.toml` `[agents.<name>]` | Promoted to skill dirs in `.agents/skills/` for round-trip capture |
| Workflows | Individual files in `.claude/commands/` | Individual files in `.opencode/commands/` | Promoted to skill dirs in `.agents/skills/` for round-trip capture | Individual files in `.clinerules/workflows/` |
| Skills | Per-skill dirs in `.claude/skills/` | Per-skill dirs in `.opencode/skills/` + referenced via `skills.paths` in `opencode.json` | Per-skill dirs in `.agents/skills/` | Per-skill dirs in `.agents/skills/` |
| Plugins | `enabledPlugins` in `.claude/settings.json`; source marketplaces in `~/.claude/plugins/known_marketplaces.json` | Not supported by first-class plugin references | `[plugins."<id>@<marketplace>"] enabled = true` in `config.toml` | Not supported |

## Rendered content identity

By default, rendered pack-authored markdown keeps the source content name in both the rendered path leaf and the rendered frontmatter `name`. For example, a skill named `deploy` renders as `skills/deploy/SKILL.md` with `name: deploy`.

When `defaults.namespaced: true` is set in `sync-config.yaml`, aipack adds pack provenance to both the rendered path leaf and the rendered frontmatter name using `<id>__aipack__<pack>`. This applies to rules rendered as individual files, rule identities flattened into Codex `AGENTS.override.md`, agents, skills, workflows/commands, hooks, and native Codex agent registration names. For example, `deploy` from `my-pack` renders as `deploy__aipack__my-pack`.

Namespaced mode also changes collision resolution for those rendered content vectors. Same-ID rules, agents, workflows, skills, and hooks from different packs are kept because their rendered names differ by pack. MCP server names, plugins, and settings keys are not namespaced and still follow `defaults.collision_strategy`.

The `__aipack__` sentinel is reserved in pack names and in agent, workflow, skill, and hook IDs so save/capture can distinguish rendered identities from source IDs. Rule IDs still reserve literal `__` because rendered rule filenames use it as the escape for `/`. Natural and namespaced names are mutually exclusive for a single harness target and scope; a successful sync keeps only the active managed spelling unless a user conflict blocks cleanup.

MCP server names, plugins, and settings keys are not rewritten because they are live wiring keys. Save and capture strip namespaced rendered names back to the source ID before writing pack content. In the write-target tables below, `<name>`, `<dirname>`, and `<file>` refer to the active rendered identity where applicable.

## Scope support

| Vector | Claude Code | OpenCode | Codex | Cline |
|--------|-------------|----------|-------|-------|
| Content (rules, agents, workflows, skills, hooks) | Project + Global | Project + Global | Project + Global | Project + Global |
| Plugin references | Project + Global | N/A | Project + Global | N/A |
| MCP servers | Project + Global | Project + Global | Project + Global | **Global only** |
| Settings | Project + Global | Project + Global | Project + Global | N/A |

## MCP server configuration

| Harness | Config file | Format | Timeout |
|---------|------------|--------|---------|
| Claude Code | `.mcp.json` (project), `~/.claude.json` (global) | JSON `mcpServers` object | Global only via `MCP_TIMEOUT` env var, milliseconds (default 10000); no per-server timeout in config |
| OpenCode | `opencode.json` | JSON `mcp` key | Milliseconds (default 10000) |
| Codex | `.codex/config.toml` | TOML `[mcp_servers.<name>]` tables | Seconds (`startup_timeout_sec`, default 10) |
| Cline | VS Code extension storage `cline_mcp_settings.json` + standalone `~/.cline/data/settings/cline_mcp_settings.json` (global only) | JSON `mcpServers` object | Seconds (default 10) |

## MCP tool permissions

Each harness controls MCP tool access differently. Some harnesses store permissions separately from the server connection config (Claude Code), while others co-locate them (Codex, OpenCode, Cline). Profiles carry three parallel fields per server — `allowed_tools` (visible/callable), `always_allowed_tools` (visible AND auto-approved without prompt), and `disabled_tools` (blocked) — and each harness adapter renders them into its native model.

| Harness | Permission location | Visibility/allow format | Per-tool auto-approve | Deny format |
|---------|-------------------|-------------------------|-----------------------|-------------|
| Claude Code | `settings.local.json` `permissions.allow` / `permissions.deny` | `mcp__<server>__<tool>` patterns in `permissions.allow` (allow is already auto-approve) | same as allow — both `allowed_tools` and `always_allowed_tools` render to `permissions.allow` | `mcp__<server>__<tool>` patterns in `permissions.deny` |
| Cline | Per-server in MCP JSON | `alwaysAllow: [...]` (Cline's only concept) | same as allow — both fields union into `alwaysAllow` | Not supported |
| Codex | Per-server in TOML | `enabled_tools = [...]` (union of both fields) | `[mcp_servers.<name>.tools.<tool>] approval_mode = "approve"` — nested per-tool stanza emitted for each `always_allowed_tools` entry | `disabled_tools = [...]` |
| OpenCode | `opencode.json` `tools` key (legacy, still supported) | `server_tool: true` per-tool (union of both fields) | not yet rendered — warning emitted, awaiting upstream syntax confirmation | `server_*: false` wildcard deny |

**Allow semantics differ per harness.** Not all "allow" mechanisms restrict tool visibility:

| Harness | `allow` means | `always_allow` distinct from `allow`? | `deny` means |
|---------|--------------|--------------------------------------|-------------|
| Claude Code | Auto-approve (tools not in allow are visible but prompt per call) | No — `permissions.allow` already means auto-approve | Block entirely |
| Cline | Auto-approve (`alwaysAllow`) — only per-server concept | No — `alwaysAllow` is Cline's only allow mechanism | N/A |
| OpenCode | Enable tool (boolean `true` in `tools` map) | Not yet — rendering pending upstream syntax | Wildcard disable (`false`) |
| Codex | Restrict to listed tools (`enabled_tools`) | **Yes** — nested `[mcp_servers.X.tools.Y] approval_mode = "approve"` is distinct from visibility | Block listed tools (`disabled_tools`) |

**Inventory policy:** when a server has a curated `AllowedTools` list, unspecified tools should be explicitly denied where the harness supports it. This requires the pack manifest to carry complete per-server tool inventories. Without complete inventories, only explicitly listed `disabled_tools` are denied; unlisted tools default to harness-specific behavior (ask/prompt for Claude Code and Cline, unrestricted for others).

## Settings and merge behavior

| Harness | Settings file | Other config files | Format | Merge behavior |
|---------|--------------|-------------|--------|----------------|
| Claude Code | `.claude/settings.local.json`; `.claude/settings.json` for first-class plugins | `.mcp.json` | JSON | **Always three-way merge** — user permissions preserved, only `mcp__*` entries managed. Plugin enablement is additive-only. |
| OpenCode | `.opencode/opencode.json` | `.opencode/oh-my-opencode.json` | JSON | Template + managed keys overlay. With `--skip-settings`: MergeMode (managed keys only) |
| Codex | `.codex/config.toml` | `.codex/hooks.json` | TOML settings + rendered JSON hooks | Template + MCP/plugin/hook trust-state merge. With `--skip-settings`: MergeMode (`mcp_servers`, agents, plugins, hooks.state managed keys only). Plugin enablement is additive-only. |
| Cline | None | `cline_mcp_settings.json` (written to VS Code + standalone Cline global paths) | JSON | Generated from inventory (no base template). Always synced |

`--skip-settings` skips settings files but MCP configs, drop-in plugins, and first-class plugin references always sync regardless.

## Environment variable expansion

Pack content uses `{env:VAR}` and `{env:VAR:-default}` placeholders. All harnesses resolve them identically at sync time: the placeholder is replaced with the literal value from the active config directory's `.env` file first, then the process environment, then any inline default. If the variable is not set and no default is provided, the MCP server is skipped entirely and a warning is emitted.

## Write targets

**Claude Code** (project + global)

| What | Project path | Global path |
|------|-------------|------------|
| Rules | `.claude/rules/<file>.md` | `~/.claude/rules/<file>.md` |
| Agents | `.claude/agents/<file>.md` | `~/.claude/agents/<file>.md` |
| Workflows | `.claude/commands/<file>.md` | `~/.claude/commands/<file>.md` |
| Skills | `.claude/skills/<dirname>/` | `~/.claude/skills/<dirname>/` |
| MCP servers | `.mcp.json` | `~/.claude.json` |
| Settings | `.claude/settings.local.json` | `~/.claude/settings.local.json` |
| Plugins | `.claude/settings.json`; source marketplaces in `~/.claude/plugins/known_marketplaces.json` | `~/.claude/settings.json`; source marketplaces in `~/.claude/plugins/known_marketplaces.json` |

Claude Code remote MCP transport rendering follows Claude's current config vocabulary: aipack `streamable-http` servers render as `type: "http"`, while `sse` servers render as `type: "sse"`.

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
| Agents | `.codex/agents/<name>.toml` (native) + registered in `.codex/config.toml` | `~/.codex/agents/<name>.toml` + registered in `~/.codex/config.toml` |
| Workflows | `.agents/skills/<name>/SKILL.md` (promoted) | `~/.agents/skills/<name>/SKILL.md` |
| Skills | `.agents/skills/<dirname>/` | `~/.agents/skills/<dirname>/` |
| Settings | `.codex/config.toml` | `~/.codex/config.toml` |
| Plugins | `.codex/config.toml` | `~/.codex/config.toml` |
| Hooks | `.codex/hooks.json` + trust state in `.codex/config.toml` | `~/.codex/hooks.json` + trust state in `~/.codex/config.toml` |

**Cline** (content: project + global; MCP: global only)

| What | Project path | Global path |
|------|-------------|------------|
| Rules | `.clinerules/<file>.md` | `~/Documents/Cline/Rules/<file>.md` |
| Agents | `.agents/skills/<name>/SKILL.md` (promoted) | `~/.agents/skills/<name>/SKILL.md` (promoted) |
| Workflows | `.clinerules/workflows/<file>.md` | `~/Documents/Cline/Workflows/<file>.md` |
| Skills | `.agents/skills/<dirname>/` | `~/.agents/skills/<dirname>/` |
| MCP | N/A | `~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/settings/cline_mcp_settings.json` (macOS VS Code) + `~/.cline/data/settings/cline_mcp_settings.json` |

## Managed keys

Keys stripped on save round-trip:

| Harness | Keys stripped |
|---------|-------------|
| Claude Code | `mcp__*` entries in `permissions.allow` and `permissions.deny` |
| OpenCode | `mcp`, `tools`, `instructions`, `skills` |
| Codex | `mcp_servers`, `agents`, AIPack-owned `hooks.state` entries for `.codex/hooks.json` |
| Cline | `mcpServers` |

First-class plugin references are additive-only. Save and clean do not remove plugin enablement from harness files.

## Harness-specific notes

**Claude Code**
- Rules: copied as individual files to `.claude/rules/`. `paths:` frontmatter scoping works natively in Claude Code; unknown frontmatter fields (`title`, `audience`, `last_updated`) are ignored.
- Agents: frontmatter transformed to Claude Code native subagent format — `name` from pack frontmatter (or derived from filename), `description`/`skills`/`mcpServers` passed through. `tools` and `disallowed_tools` are mapped to PascalCase (`read` → `Read`, `bash` → `Bash`) and converted from YAML lists to comma-separated strings. When `mcpServers` is present, MCP-prefixed tools are filtered out of `tools:` (Claude Code's tools field creates a hard allowlist that would block MCP server access). Pack `disallowed_tools` → `disallowedTools`, pack `mcp_servers` → `mcpServers`. Non-portable fields (`mode`, `temperature`) are dropped.
- Workflows: individual command files in `.claude/commands/` only (no dual materialization). Rule, agent, workflow, and skill path leaves and frontmatter names include rendered content identity.
- `CLAUDE.managed.md` is no longer written. On first sync after upgrade, it is automatically removed as a stale managed file. `CLAUDE.md` is no longer touched.
- Global scope syncs to `~/.claude/{rules,agents,skills,commands}/`.
- Save/capture normalizes Claude Code's native `type: "http"` MCP entries back to aipack `streamable-http`.
- `settings.local.json` always uses three-way merge, even without `--skip-settings`. User-controlled permissions (non-`mcp__` prefix) are always preserved in both `allow` and `deny` arrays.
- Plugin references write `enabledPlugins` in `.claude/settings.json`. Source-prefixed marketplaces such as `github:owner/marketplace` are registered in `~/.claude/plugins/known_marketplaces.json`.
- `permissions.deny` blocks tools entirely (deny > ask > allow precedence). Unlike OpenCode's `server_*: false` wildcard, Claude Code cannot use wildcard deny patterns because deny always takes precedence over allow regardless of specificity. Only explicit per-tool deny entries are rendered from `disabled_tools` in the profile config.

**OpenCode**
- Rules are both copied as individual files AND referenced via `instructions` globs in `opencode.json`. Skills are both copied AND referenced via `skills.paths`. These JSON references are only managed when the respective vector has `Manage: true` in the profile. Rule, agent, workflow, and skill path leaves and frontmatter names include rendered content identity.
- `oh-my-opencode.json` is a plugin (pure copy from pack), always synced regardless of `--skip-settings`.
- `tools` key (MCP tool boolean map) is distinct from `permission` key (OpenCode's native harness tool access). Do not conflate them.

**Codex**
- Rules are flattened into a single `AGENTS.override.md`. If an existing `AGENTS.md` exists, its content is preserved below a separator.
- Agents are rendered as native Codex TOML files in `.codex/agents/<name>.toml`, each containing `name`, `description`, `developer_instructions` (from the agent body), and any `harness.codex` overrides as top-level TOML keys. A registration entry (`[agents.<name>]` with `description` and an absolute `config_file`) is merged into `config.toml`. Referenced MCP servers are resolved from the profile and embedded in the agent TOML. Referenced skills become `skills.config` entries with paths to the rendered skill directories. The `harness` frontmatter block is stripped — it does not appear in the rendered TOML.
- Workflows are promoted to `.agents/skills/<name>/SKILL.md` for round-trip capture. Workflow and skill directory names and frontmatter names include rendered content identity. Codex flattened rules are generated into one `AGENTS.override.md`; their source comments and frontmatter names use the rendered identity when namespacing is enabled.
- Plugin references merge `[plugins."<id>@<marketplace>"] enabled = true` into `config.toml`. The default marketplace is `openai-curated`.
- Pack hooks declared under `hooks/<id>/HOOK.yaml` are rendered into one `.codex/hooks.json` file in profile order. aipack maps portable lifecycle events to Codex native hook events, renders the pack-authored command directly, writes trust-state hashes for rendered command hooks into `config.toml` under `hooks.state`, and removes only those AIPack-owned state entries during save/clean.
- Capture reads `.codex/agents/*.toml` to reconstruct pack agents: `developer_instructions` becomes the agent body, known Codex fields (`model`, `model_reasoning_effort`, etc.) populate `harness.codex` in frontmatter, and embedded MCP server names are extracted to `mcp_servers`.
- Global config path is always `~/.codex/`.

**Cline**
- MCP is global-only — there is no project-level MCP settings path.
- Sync writes Cline MCP settings to both the VS Code global-storage path and the standalone Cline path (`~/.cline/data/settings/cline_mcp_settings.json`).
- Cline remote transport names are adapter-specific: aipack `streamable-http` renders as `type: "streamableHttp"` in `cline_mcp_settings.json`, while `sse` remains `type: "sse"`.
- Save/capture prefers the canonical VS Code path, falls back to the standalone path when the canonical file is missing, and warns when another discovered file differs from the capture source.
- Agents (but not workflows) are promoted to skill directories in `.agents/skills/` (project) or `~/.agents/skills/` (global), since Cline natively reads both `.clinerules/` and `.agents/`. Rule, promoted agent, workflow, and skill path leaves and frontmatter names include rendered content identity. Codex no longer shares this promotion path — Codex agents render as native TOML files in `.codex/agents/`.
- The MCP settings file is generated fresh from inventory on every sync (no base template concept). Existing user-defined `mcpServers` entries are preserved during merge.
- `alwaysAllow` is allow-only — there is no mechanism to deny specific tools.

## Implementation references

- Claude Code: `internal/harness/claudecode/harness.go`, `internal/harness/claudecode/render.go`
- OpenCode: `internal/harness/opencode/harness.go`, `internal/harness/opencode/render.go`
- Codex: `internal/harness/codex/harness.go`, `internal/harness/codex/render.go`, `internal/harness/codex/hooks.go`, `internal/harness/codex/agent_render.go`
- Cline: `internal/harness/cline/harness.go`, `internal/harness/cline/render.go`
- Sync engine: `internal/engine/`
- Config resolution: `internal/config/profile_resolve.go`

If docs and code diverge, the code is authoritative.

## Upstream harness docs

- OpenCode: [MCP](https://opencode.ai/docs/mcp-servers/#enable), [Config](https://opencode.ai/docs/config/#instructions), [Agents](https://opencode.ai/docs/agents/#markdown), [Commands](https://opencode.ai/docs/commands/#markdown)
- Codex: [AGENTS.md](https://developers.openai.com/codex/guides/agents-md/), [Skills](https://developers.openai.com/codex/skills/), [Subagents](https://developers.openai.com/codex/subagents), [Config Reference](https://developers.openai.com/codex/config-reference/), [MCP](https://developers.openai.com/codex/mcp)
- Claude Code: [Memory/Rules](https://code.claude.com/docs/en/memory), [Subagents](https://code.claude.com/docs/en/sub-agents), [Skills](https://code.claude.com/docs/en/skills)
- Cline: [Storage](https://docs.cline.bot/customization/overview#storage-locations), [MCP Config](https://docs.cline.bot/mcp/adding-and-configuring-servers#editing-configuration-files)

## What to read next

- [aipack Reference](./aipack.md) — CLI commands and flags
- [Pack Format Specification](./pack-format.md) — content format and manifest schema
- [Profiles](./profiles.md) — composition, selectors, and MCP overrides
