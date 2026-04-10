# aipack

A package manager for AI agent knowledge.

Rules, skills, workflows, agent definitions, MCP server configs — authored once as portable **packs**, composed through **profiles**, and synced to whatever coding assistant you use.

## The Problem

Agent knowledge exists everywhere and composes nowhere. Community skill libraries publish hundreds of capabilities. Teams build shared workflows that actually work. Individuals refine their MCP server configs and agent rules over months. All of it lives in places where it can't be combined: git repos with per-harness static renderings, chat threads that scroll off in days, personal dotfiles that never leave one machine.

The agent ecosystem has a content layer — growing fast — and no infrastructure layer to compose it. You can't install one team's deployment workflow alongside a community skill library and your personal rules, scoped to the right context, rendered for the harness you happen to use today.

## The Solution

aipack is the infrastructure layer. **Packs** are portable, versioned bundles of agent knowledge. **Profiles** compose packs and curate which content and tools are active for a given context. A **sync engine** renders the result into each harness's native format — so the same pack works whether you use Claude Code, Codex, OpenCode, or Cline.

```
┌─────────────┐     ┌──────────┐     ┌──────────────┐
│  Pack A     │     │          │     │ Claude Code  │
│ (community) │────▶│          │────▶│ .claude/     │
├─────────────┤     │          │     ├──────────────┤
│  Pack B     │────▶│  aipack  │────▶│ Codex        │
│  (team)     │     │   sync   │     │ .codex/      │
├─────────────┤     │          │     ├──────────────┤
│  Pack C     │────▶│          │────▶│ OpenCode     │
│ (personal)  │     │          │     │ .opencode/   │
└─────────────┘     └──────────┘     ├──────────────┤
                                     │ Cline        │
                                     │ .clinerules/ │
                                     └──────────────┘
```

## Supported Harnesses

| Harness | Rules | Skills | Workflows | Agents | MCP Servers | Settings |
|---------|-------|--------|-----------|--------|-------------|----------|
| Claude Code | Individual files | Skill directories | Command files | Subagent files | `.mcp.json` | `settings.local.json` |
| OpenCode | Individual files | Skill directories | Command files | Individual files | `opencode.json` | `opencode.json` |
| Codex | Flattened to `AGENTS.override.md` | Skill directories | Promoted to skill dirs | Native TOML in `.codex/agents/` | `config.toml` | `config.toml` |
| Cline | Individual files | Skill directories | Individual files | Promoted to skill dirs | Global only | N/A |

## Quick Start

### Install

On macOS, install via Homebrew:

```bash
brew install shrug-labs/tap/aipack
```

On macOS and Linux, you can also use the release-backed installer script:

```bash
curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | sh
```

On Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/shrug-labs/aipack/main/install.ps1 | iex
```

On Windows, the PowerShell installer writes `aipack.exe` to `~/.local/bin`.

If you already have Go installed and prefer a Go-native install path:

```powershell
go install github.com/shrug-labs/aipack/cmd/aipack@latest
```

Set `GOBIN` to `$HOME\.local\bin` if you want `go install` to use the same destination as the installer:

```powershell
[System.Environment]::SetEnvironmentVariable('GOBIN', "$HOME\.local\bin", 'User')
```

The installers detect your platform, download the matching release binary, verify
`SHA256SUMS`, install `aipack`, and print the installed version.

Useful overrides (macOS/Linux):

```bash
# Pin a specific release tag
curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | VERSION=v0.3.0 sh

# Install into a specific directory
curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | BIN_DIR=$HOME/.local/bin sh

# Install under a prefix
curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | PREFIX=$HOME/.local sh
```

Windows override (PowerShell):

```powershell
$env:AIPACK_VERSION = "v0.12.0"; irm https://raw.githubusercontent.com/shrug-labs/aipack/main/install.ps1 | iex
```

Release binaries are published for `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `windows/amd64`, and `windows/arm64`. Stable releases also update the Homebrew formula in `shrug-labs/homebrew-tap`. If you prefer a manual install, use the matching release asset from <https://github.com/shrug-labs/aipack/releases> together with `SHA256SUMS`.

### Build from source

```bash
git clone https://github.com/shrug-labs/aipack.git
cd aipack
make install    # builds and copies to ~/.local/bin/aipack
```

Building from source requires Go 1.25+.

### First Use

```bash
# Initialize config directory, default profile, and fetch the public registry
aipack init

# Install a pack and add it to the active profile
aipack pack install ./my-pack --add
aipack pack install --url https://github.com/org/my-pack.git --add
aipack pack install my-pack --add    # looks up name in registry

# Preview what would change
aipack sync --dry-run

# Sync to your harness config
aipack sync
```

### Full Setup (Recommended)

Packs can bundle profiles, registry entries, and dependency declarations. When
a pack includes these, the install path bootstraps your entire agent environment:

```bash
# 1. Install a pack. -w all accepts its bundled profiles, registries, and extras.
aipack pack install --url https://github.com/org/my-pack.git -w all

# 2. Activate a profile and install its dependency packs from the registry.
aipack profile set frontend-dev --install

# 3. Sync to your harness.
aipack sync
```

Three commands: install, compose, sync. The profile pulls in whatever
dependency packs it needs — community skill libraries, shared MCP configs,
project-specific workflows — and aipack renders the combined result for your
harness.

## Documentation

- **[Getting Started](docs/getting-started.md)** — install packs and sync to your harness in five minutes
- **[Installing Packs](docs/installing-packs.md)** — all installation methods, content_paths, quiet packs, registry entries
- **[Creating Packs](docs/creating-packs.md)** — author your own pack from scratch or existing content
- **[Profiles](docs/profiles.md)** — compose packs, filter content, expand parameters, scope to roles
- **[Pack Format Specification](docs/pack-format.md)** — format reference for content vectors, MCP servers, environment references, and manifests
- **[Harness Reference](docs/harness-reference.md)** — per-harness rendering behavior, write targets, and configuration differences
- **[Sync and Save](docs/sync.md)** — sync workflow, save round-trips, restore, and clean
- **[aipack Reference](docs/aipack.md)** — complete CLI reference
- **[Configuration and State](docs/configuration.md)** — config directory layout, sync-config, ledger and state management
- **[CLI Specification](docs/cli-spec.md)** — JSON output contracts and enumerations for tooling integration

## Key Concepts

A **pack** is a directory of agent configuration — rules, skills, workflows, agent definitions, MCP server configs, and harness settings — with a `pack.json` manifest. Content is markdown with YAML frontmatter. Drop files into the conventional directories (`rules/`, `skills/`, `workflows/`, `agents/`, `mcp/`) and the sync engine discovers them automatically. Any git repository can also be consumed as a pack by mapping its directories to content types — no `pack.json` required in the source. See [Installing Packs](docs/installing-packs.md).

**Profiles** control which packs to sync and how — content filtering, parameter expansion, per-pack content overrides, context-based scoping. Packs can bundle profiles so setup is `pack install -w all` + `profile set` + `sync`. See [Profiles](docs/profiles.md) for the full guide.

**Sync** resolves the active profile and writes to harness-native locations. Non-destructive by default — user modifications are detected via content digest and shown as diffs rather than overwritten.

## Features

| Feature | Description |
|---------|-------------|
| `aipack init` | Bootstrap config directory, default profile, and registry |
| `aipack sync` | Render packs into harness-native config |
| `aipack save` | Capture harness config back into packs |
| `aipack restore` | Undo the last sync's settings changes |
| `aipack clean` | Remove all managed files from harness locations |
| `aipack pack create/install/list/show/update/delete` | Pack lifecycle management |
| `aipack pack add/remove/enable/disable` | Profile membership and activation |
| `aipack pack rename/validate` | Pack configuration and validation |
| `aipack profile create/set/show/list/delete` | Profile management |
| `aipack registry fetch/list/sources/delete` | Discover and manage pack registries |
| `aipack search` | Full-text search across all installed packs |
| `aipack query` | Run raw SQL against the pack index |
| `aipack status` | Show ecosystem status: profile, packs, and content inventories |
| `aipack trace` | Trace a resource from pack source to harness destination |
| `aipack render` | Generate portable pack output |
| `aipack doctor` | Validate config health and detect drift |
| `aipack manage` | Interactive terminal UI |
| `aipack prompt list/show/copy` | Browse prompts from installed packs |
| `aipack version` | Print the CLI version |

## Releases and Versioning

- Public installs are distributed as GitHub Release binaries.
- macOS installs are also available via `brew install shrug-labs/tap/aipack`.
- `install.sh` is the primary cross-platform install path for macOS and Linux.
- CLI releases use semantic versioning tags: `vMAJOR.MINOR.PATCH` and prerelease tags such as `vMAJOR.MINOR.PATCH-rc.1`.
- `aipack` remains in the `0.x` phase for now; breaking user-facing changes bump the minor version.
- The first public release line is `v0.3.0`.
- `VERSION` is the source of truth for the release line.
- Release process details live in [`RELEASING.md`](./RELEASING.md), and shipped user-facing changes are tracked in [`CHANGELOG.md`](./CHANGELOG.md).

## Registry

aipack supports multiple registry sources. Each source is cached locally and fetched independently.

- `aipack registry fetch <url>` adds and fetches a single source.
- `aipack registry fetch` (bare) fetches all configured sources.
- `aipack registry sources` lists configured sources and their cache status.
- Sources are saved to `registry_sources` in `sync-config.yaml` and cached in `~/.config/aipack/registries/`.
- Both HTTPS and SSH git URLs are supported. SSH URLs (`git@host:path` or `ssh://`) avoid credential prompts.
- Git repos are auto-detected from `.git` suffix, `git@` prefix, `ssh://` scheme, or `--ref`/`--path` flags.
- The compiled-in default points at `registry.yaml` in `shrug-labs/packs` and is used when no sources are configured.
- Even without registry entries, pack installs work via direct paths and `aipack pack install --url ...`.

## Building

```bash
make fmt       # format Go source
make fmt-check # fail if formatting is stale
make help       # show all targets
make build      # build for current platform → dist/
make test       # run Go tests
make dist       # cross-compile for darwin/arm64, darwin/amd64, linux/amd64, windows/amd64, windows/arm64
make install    # build + copy to ~/.local/bin/
make clean      # remove build artifacts
```

## Contributing

This project welcomes contributions from the community. Before submitting a pull
request, please [review our contribution guide](./CONTRIBUTING.md).

## Security

Please consult the [security guide](./SECURITY.md) for our responsible security
vulnerability disclosure process.

## License

Copyright (c) 2025, 2026 The aipack Authors.

Released under the Universal Permissive License v1.0. See [LICENSE.txt](./LICENSE.txt).
