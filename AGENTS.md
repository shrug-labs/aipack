# aipack

Go 1.24 module. Generic pack sync engine — works with any content pack installed via `aipack pack install`.

## Terminology

| Term | Definition |
|------|-----------|
| **Pack** | Portable, versioned bundle of AI agent configuration (rules, agents, workflows, skills, MCP servers, harness configs) |
| **Harness** | Tool wrapping an LLM with context/tool/orchestration (Cline, Codex, OpenCode, Claude Code, Cursor, Windsurf, Aider, Amp) |
| **Profile** | Composition layer selecting which packs, content, and settings to sync to which harnesses |
| **Sync** | Writing pack content to harness-specific locations and formats |
| **Render** | Generating portable, self-contained pack output (not tied to a harness) |

## Capability vector mapping

Each harness implements vectors differently. Quick reference (see `docs/aipack.md` for full details including scope, MCP formats, merge behavior, env var expansion, and limitations):

| Vector | Claude Code | OpenCode | Codex | Cline |
|--------|-------------|----------|-------|-------|
| Rules | `.claude/rules/<file>.md` | Individual files + `instructions` ref | Flattened into `AGENTS.override.md` | Individual files in `.clinerules/` |
| Agents | `.claude/agents/<file>.md` | Individual files in `.opencode/agents/` | Promoted to `.agents/skills/<name>/SKILL.md` | Promoted to `.clinerules/skills/<name>/SKILL.md` |
| Workflows | `.claude/commands/<file>.md` | `.opencode/commands/` | Promoted to `.agents/skills/<name>/SKILL.md` | `.clinerules/workflows/` |
| Skills | `.claude/skills/` | `.opencode/skills/` + `skills.paths` ref | `.agents/skills/` | `.clinerules/skills/` |
| MCP | `.mcp.json` + `settings.local.json` permissions | `opencode.json` `mcp` key | `config.toml` `[mcp_servers]` | Global VS Code storage only |
| Settings | `settings.local.json` (always merge) | `opencode.json` (template) | `config.toml` (template) | N/A |

## Architecture constraints

Three-layer structure enforced by `cmd/aipack/architecture_test.go`:

- `cmd/aipack/` → CLI adapters: thin wrappers that parse flags and delegate
- `internal/app/` → Services: `Request` struct → `Run()` → `Result` or error
- `internal/` → Domain packages

**Import rules:**
- NEVER import `cmd/` from `internal/`
- `app` MUST NOT import `internal/render`
- Domain packages: no upward imports, only peer domain packages + stdlib + third-party

## Conventions

- Wrap errors with `%w` — always preserve context
- Use `cmdutil.ExitOK` (0), `ExitFail` (1), `ExitUsage` (2)
- CLI adapters: Kong `Run(g *Globals) error` pattern
- Tests: `t.Parallel()` where safe, `t.TempDir()` for isolation, NEVER `t.Parallel()` with `t.Setenv()`
- `--skip-settings` skips harness settings only; MCP configs still sync
- Plan has two non-content vectors: Settings (gated by `--skip-settings`), MCP (never gated)
- Drop-in config files (any non-base config in a pack's harness directory) are settings, not a separate vector

## Directory map

- `cmd/aipack/` — CLI entry + command adapters
- `internal/app/` — service layer (sync, save, clean, doctor, init, pack)
- `internal/config/` — config parsing, profile resolution, sync config
- `internal/domain/` — domain types (plan, content, profile, ledger, settings)
- `internal/engine/` — sync engine (parse, resolve, plan, diff, apply, merge, MCP)
- `internal/harness/` — per-harness plan/render/capture (claudecode, cline, codex, opencode)
- `internal/render/` — pack rendering (portable output)
- `internal/update/` — CLI version update checking
- `schemas/` — embedded JSON Schemas for pack.json and MCP server validation
- `internal/cmdutil/` — CLI utilities (flag resolution, harness/scope normalization)
- `internal/util/` — shared utilities (file I/O, digests)
- `docs/aipack.md` — tool reference (sync contract, per-harness behavior)

## Workflow

- Before editing: read nearby code and related tests
- After editing: `go test ./...`, then `go vet ./...`
- Pre-commit gate: `go build ./...` → `go test ./...` → `make fmt` → check `git diff` for fmt changes → stage any fmt changes → commit
- Use `make fmt` (not raw `gofmt -w`) — it's the canonical formatting target
- If CLI behavior changed: update CLI help text in the same change
- If sync behavior changed: update `docs/aipack.md`
