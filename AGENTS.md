# aipack

Go 1.26 module. Pack sync engine — author agent configuration once, render to any supported harness (Claude Code, OpenCode, Codex, Cline).

## Architecture

Three layers enforced by `cmd/aipack/architecture_test.go`:

| Layer | Path | Role |
|-------|------|------|
| CLI | `cmd/aipack/` | Kong adapters — parse flags, delegate to app |
| Service | `internal/app/` | Request → Run → Result. No CLI deps, no I/O globals |
| Domain | `internal/` (config, domain, engine, harness, render) | Business logic, no upward imports |

Import rules (test-enforced, violations fail `go test`):
- `internal/` NEVER imports `cmd/`
- `harness/` and `render/` NEVER import `config/` (depend on domain + engine only)
- No imports of deleted v1 packages (full blocklist in `architecture_test.go`)

## Key abstractions

Services in `internal/app/` follow a Request → Run → Result pattern with injected I/O — no global state. See the existing services for the shape.

Sync produces a **Plan** by accumulating **Fragments** from each harness adapter. Settings and MCP are separate plan vectors with different gating behavior — details in `internal/harness/AGENTS.md`.

Four harness adapters (`claudecode`, `opencode`, `codex`, `cline`) handle both forward sync (Plan) and reverse save (Capture). Scope branching is per-harness. Interface contract and patterns: `internal/harness/AGENTS.md`.

## Conventions

- Wrap errors with `%w` — always preserve context
- Exit codes: `cmdutil.ExitOK` (0), `ExitFail` (1), `ExitUsage` (2)
- Tests: `t.Parallel()` where safe, `t.TempDir()` for isolation, NEVER `t.Parallel()` with `t.Setenv()` or `t.Chdir()`
- Use `make fmt` (not raw `gofmt -w`) for formatting
- `--skip-settings` skips settings only; MCP configs and plugins always sync
- Version injected via ldflags at build time (`-X main.version`, `-X main.commit`)
- All commits require `Signed-off-by` — use `git commit --signoff`

## Directory map

| Path | Contents |
|------|----------|
| `cmd/aipack/` | CLI adapters (Kong) — see `cmd/aipack/AGENTS.md` |
| `cmd/aipack/tui/` | Bubbletea TUI for `aipack manage` |
| `internal/app/` | Service layer: sync, save, clean, doctor, pack, trace, etc. |
| `internal/config/` | Config parsing, profile resolution, sync-config, manifest, discovery |
| `internal/domain/` | Core types: Plan, Fragment, Content, Profile, Ledger, Actions |
| `internal/engine/` | Sync pipeline: parse, resolve, plan, diff, apply, merge |
| `internal/harness/` | Per-harness adapters — see `internal/harness/AGENTS.md` |
| `internal/index/` | SQLite FTS5 search index |
| `internal/render/` | Portable pack rendering (harness-independent) |
| `internal/cmdutil/` | CLI utilities: exit codes, flag resolution, harness normalization |
| `internal/util/` | Shared utilities: file I/O, digests |
| `schemas/` | Embedded JSON Schemas (pack.json, MCP server) |
| `docs/` | User documentation — `docs/aipack.md` is authoritative for sync behavior |

## Workflow

- Before editing: read nearby code and related tests
- After editing: `go test ./...`, then `make lint`
- Before declaring done: `make build && make test && make lint` must all pass
- `make lint` runs `go vet`, `staticcheck` (if installed), and `go fix ./...` (applies Go modernization fixes in-place).
- Pre-commit: `go build ./...` → `go test ./...` → `make fmt` → check `git diff` for fmt changes → stage any → commit
- Feature work going to main: single atomic commit, not per-task intermediaries
- Commits use `git commit --signoff`

### What to update when

| You changed... | Also update |
|---------------|-------------|
| CLI flags or behavior | Help text in the same file |
| Sync behavior or per-harness rendering | `docs/aipack.md` (authoritative reference) |
| JSON output shape | `docs/cli-spec.md` (output contracts) |
| Pack format or profile schema | `docs/pack-format.md` |
| Any user-visible feature, fix, or breaking change | `CHANGELOG.md` Unreleased section |
| Config directory layout or sync-config schema | `docs/configuration.md` |

### Release process

`VERSION` is the source of truth. Full process in `RELEASING.md`: bump VERSION → update CHANGELOG → verify (`make fmt-check && make test && go vet ./... && make dist`) → commit → tag `vX.Y.Z` → push tag. The tag triggers CI to build and publish release assets.
