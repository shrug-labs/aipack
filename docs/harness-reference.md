# Harness Reference

Four harnesses are supported: Claude Code, OpenCode, Codex, and Cline. Each renders pack content differently based on what the harness natively supports. This is the authoritative reference for rendering behavior — when other docs describe harness behavior, they should point here.

For the CLI commands that trigger sync, see the [aipack reference](./aipack.md). For the pack content format itself, see the [Pack Format Specification](./pack-format.md). For how profiles control what gets synced, see [Profiles](./profiles.md).

## Content vector rendering

| Vector | Claude Code | OpenCode | Codex | Cline |
|--------|-------------|----------|-------|-------|
| Rules | Individual files in `.claude/rules/` (frontmatter preserved, `paths:` scoping works natively) | Individual files in `.opencode/rules/` + referenced via `instructions` key in `opencode.json` | Flattened into `AGENTS.override.md` | Individual files in `.clinerules/` |
| Agents | Individual files in `.claude/agents/` (frontmatter transformed to Claude Code subagent format) | Individual files in `.opencode/agents/` | Native TOML files in `.codex/agents/` + registration in `config.toml` `[agents.<name>]` | Promoted to skill dirs in `.agents/skills/` (enriched frontmatter preserves type + metadata for round-trip) |
| Workflows | Individual files in `.claude/commands/` | Individual files in `.opencode/commands/` | Promoted to skill dirs in `.agents/skills/` (enriched frontmatter preserves type + metadata for round-trip) | Individual files in `.clinerules/workflows/` |
| Skills | Per-skill dirs in `.claude/skills/` | Per-skill dirs in `.opencode/skills/` + referenced via `skills.paths` in `opencode.json` | Per-skill dirs in `.agents/skills/` | Per-skill dirs in `.agents/skills/` |

## Scope support

| Vector | Claude Code | OpenCode | Codex | Cline |
|--------|-------------|----------|-------|-------|
| Content (rules, agents, workflows, skills) | Project + Global | Project + Global | Project + Global | Project + Global |
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

| Harness | Settings file | Plugin files | Format | Merge behavior |
|---------|--------------|-------------|--------|----------------|
| Claude Code | `.claude/settings.local.json` | `.mcp.json` | JSON | **Always three-way merge** — user permissions preserved, only `mcp__*` entries managed |
| OpenCode | `.opencode/opencode.json` | `.opencode/oh-my-opencode.json` | JSON | Template + managed keys overlay. With `--skip-settings`: MergeMode (managed keys only) |
| Codex | `.codex/config.toml` | None | TOML | Template + MCP table merge. With `--skip-settings`: MergeMode (`mcp_servers` only) |
| Cline | None | `cline_mcp_settings.json` (written to VS Code + standalone Cline global paths) | JSON | Generated from inventory (no base template). Always synced |

`--skip-settings` skips settings files but **plugins always sync** regardless.

## Environment variable expansion

Pack content uses `{env:VAR}` placeholders. All harnesses resolve them identically at sync time: the placeholder is replaced with the literal value from the process environment. If the variable is not set, the MCP server is skipped entirely and a warning is emitted.

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
| Codex | `mcp_servers`, `agents` |
| Cline | `mcpServers` |

## Harness-specific notes

**Claude Code**
- Rules: copied as individual files to `.claude/rules/`. `paths:` frontmatter scoping works natively in Claude Code; unknown frontmatter fields (`title`, `audience`, `last_updated`) are ignored.
- Agents: frontmatter transformed to Claude Code native subagent format — `name` from pack frontmatter (or derived from filename), `description`/`skills`/`mcpServers` passed through. `tools` and `disallowed_tools` are mapped to PascalCase (`read` → `Read`, `bash` → `Bash`) and converted from YAML lists to comma-separated strings. When `mcpServers` is present, MCP-prefixed tools are filtered out of `tools:` (Claude Code's tools field creates a hard allowlist that would block MCP server access). Pack `disallowed_tools` → `disallowedTools`, pack `mcp_servers` → `mcpServers`. Non-portable fields (`mode`, `temperature`) are dropped.
- Workflows: individual command files in `.claude/commands/` only (no dual materialization).
- `CLAUDE.managed.md` is no longer written. On first sync after upgrade, it is automatically removed as a stale managed file. `CLAUDE.md` is no longer touched.
- Global scope syncs to `~/.claude/{rules,agents,skills,commands}/`.
- `settings.local.json` always uses three-way merge, even without `--skip-settings`. User-controlled permissions (non-`mcp__` prefix) are always preserved in both `allow` and `deny` arrays.
- `permissions.deny` blocks tools entirely (deny > ask > allow precedence). Unlike OpenCode's `server_*: false` wildcard, Claude Code cannot use wildcard deny patterns because deny always takes precedence over allow regardless of specificity. Only explicit per-tool deny entries are rendered from `disabled_tools` in the profile config.

**OpenCode**
- Rules are both copied as individual files AND referenced via `instructions` globs in `opencode.json`. Skills are both copied AND referenced via `skills.paths`. These JSON references are only managed when the respective vector has `Manage: true` in the profile.
- `oh-my-opencode.json` is a plugin (pure copy from pack), always synced regardless of `--skip-settings`.
- `tools` key (MCP tool boolean map) is distinct from `permission` key (OpenCode's native harness tool access). Do not conflate them.

**Codex**
- Rules are flattened into a single `AGENTS.override.md`. If an existing `AGENTS.md` exists, its content is preserved below a separator.
- Agents are rendered as native Codex TOML files in `.codex/agents/<name>.toml`, each containing `name`, `description`, `developer_instructions` (from the agent body), and any `harness.codex` overrides as top-level TOML keys. A registration entry (`[agents.<name>]` with `description` and an absolute `config_file`) is merged into `config.toml`. Referenced MCP servers are resolved from the profile and embedded in the agent TOML. Referenced skills become `skills.config` entries with paths to the rendered skill directories. The `harness` frontmatter block is stripped — it does not appear in the rendered TOML.
- Workflows are promoted to `.agents/skills/<name>/SKILL.md` with enriched YAML frontmatter that preserves the original type (`source_type: workflow`) for round-trip capture. Skills are copied as directories under the same path.
- Capture reads `.codex/agents/*.toml` to reconstruct pack agents: `developer_instructions` becomes the agent body, known Codex fields (`model`, `model_reasoning_effort`, etc.) populate `harness.codex` in frontmatter, and embedded MCP server names are extracted to `mcp_servers`.
- Global config path is always `~/.codex/`.

**Cline**
- MCP is global-only — there is no project-level MCP settings path.
- Sync writes Cline MCP settings to both the VS Code global-storage path and the standalone Cline path (`~/.cline/data/settings/cline_mcp_settings.json`).
- Save/capture prefers the canonical VS Code path, falls back to the standalone path when the canonical file is missing, and warns when another discovered file differs from the capture source.
- Agents (but not workflows) are promoted to skill directories in `.agents/skills/` (project) or `~/.agents/skills/` (global), since Cline natively reads both `.clinerules/` and `.agents/`. Enriched YAML frontmatter (`source_type: agent`) preserves agent metadata for round-trip capture. Workflows remain individual files in `.clinerules/workflows/`. Codex no longer shares this promotion path — Codex agents render as native TOML files in `.codex/agents/`.
- The MCP settings file is generated fresh from inventory on every sync (no base template concept). Existing user-defined `mcpServers` entries are preserved during merge.
- `alwaysAllow` is allow-only — there is no mechanism to deny specific tools.

## Implementation references

- Claude Code: `internal/harness/claudecode/harness.go`, `internal/harness/claudecode/render.go`
- OpenCode: `internal/harness/opencode/harness.go`, `internal/harness/opencode/render.go`
- Codex: `internal/harness/codex/harness.go`, `internal/harness/codex/render.go`, `internal/harness/codex/agent_render.go`
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
