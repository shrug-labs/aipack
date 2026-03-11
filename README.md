# aipack

A CLI tool for authoring, composing, and syncing AI agent configuration across coding assistants.

Write your rules, skills, workflows, and MCP server configs once as portable **packs**, then sync them to every harness your team uses.

## The Problem

AI coding assistants (Claude Code, Cline, Codex, OpenCode) each store agent configuration differently — different file formats, different directory structures, different conventions. Teams using multiple assistants maintain duplicate configs that drift apart. Onboarding a new team member means manually copying dotfiles.

## The Solution

aipack introduces **packs** — portable, versioned bundles of agent configuration — and a **sync engine** that renders them into each harness's native format.

```
┌─────────────┐     ┌──────────┐     ┌──────────────┐
│  Pack A     │     │          │     │ Claude Code  │
│  (team ops) │────▶│          │────▶│ .claude/     │
├─────────────┤     │          │     ├──────────────┤
│  Pack B     │────▶│  aipack  │────▶│ Codex        │
│  (personal) │     │   sync   │     │ AGENTS.md    │
├─────────────┤     │          │     ├──────────────┤
│  Pack C     │────▶│          │────▶│ OpenCode     │
│  (org-wide) │     │          │     │ .opencode/   │
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
| Codex | Flattened to `AGENTS.override.md` | Skill directories | Flattened | Flattened | `config.toml` | `config.toml` |
| Cline | Individual files | Skill directories | Individual files | Individual files | Global only | N/A |

## Quick Start

### Install

On macOS, install via Homebrew:

```bash
brew install dfoster-oracle/tap/aipack
```

On macOS and Linux, you can also use the release-backed installer script:

```bash
curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | sh
```

The installer detects your platform, downloads the matching release binary, verifies
`SHA256SUMS`, installs `aipack`, and prints the installed version.

Useful overrides:

```bash
# Pin a specific release tag
curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | VERSION=v0.3.0 sh

# Install into a specific directory
curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | BIN_DIR=$HOME/.local/bin sh

# Install under a prefix
curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | PREFIX=$HOME/.local sh
```

Release binaries are published for `darwin/arm64`, `darwin/amd64`, and `linux/amd64`. Stable releases also update the Homebrew formula in `dfoster-oracle/homebrew-tap`. If you prefer a manual install, use the matching release asset from <https://github.com/shrug-labs/aipack/releases> together with `SHA256SUMS`.

### Build from source

```bash
git clone https://github.com/shrug-labs/aipack.git
cd aipack
make install    # builds and copies to ~/.local/bin/aipack
```

Building from source requires Go 1.24+.

### First Use

```bash
# Initialize config directory and default profile
aipack init

# Fetch the public pack catalog (may be empty in early releases)
aipack registry fetch

# Install a pack from a local directory or git URL
aipack pack install ./my-team-pack
aipack pack install --url https://github.com/org/team-pack.git

# Preview what would change
aipack sync --dry-run

# Sync to your harness config
aipack sync
```

## Key Concepts

### Packs

A pack is a directory containing agent configuration:

```
my-pack/
├── pack.json          # manifest (name, version, description)
├── rules/             # always-on constraints (<50 lines each)
├── skills/            # on-demand knowledge (SKILL.md + supporting files)
├── workflows/         # step-by-step procedures
├── agents/            # tool-using personas
├── mcp/               # MCP server configurations
└── configs/           # harness settings templates
```

### Profiles

Profiles select which packs to sync and how. Stored as YAML under `~/.config/aipack/profiles/`.

```yaml
# ~/.config/aipack/profiles/default.yaml
schema_version: 2
packs:
  - name: my-team-pack
  - name: personal
```

Packs are referenced by name and resolved from `~/.config/aipack/packs/<name>/`. Multiple packs compose in profile order. Conflicts are detected and surfaced.

### Sync

Sync reads your profile, resolves all packs, and writes to harness-native locations:

```bash
aipack sync                          # sync active profile, all scopes
aipack sync --scope project          # project-local config only
aipack sync --scope global           # user-global config only
aipack sync --harness claudecode     # one harness only
aipack sync --dry-run                # preview changes
aipack sync --force                  # overwrite conflicts
```

Sync is non-destructive by default. Modified files show a unified diff and are skipped unless `--force` is used.

## Features

| Feature | Description |
|---------|-------------|
| `aipack sync` | Render packs into harness-native config |
| `aipack save` | Capture harness config back into packs |
| `aipack pack install/list/show/update/delete` | Pack lifecycle management |
| `aipack profile create/set/show/list` | Profile management |
| `aipack registry fetch/list/search/remove` | Discover and manage pack registries |
| `aipack search` | Full-text search across all installed packs |
| `aipack render` | Generate portable pack output |
| `aipack doctor` | Validate config health and detect drift |
| `aipack manage` | Interactive terminal UI |

## Releases and Versioning

- Public installs are distributed as GitHub Release binaries.
- macOS installs are also available via `brew install dfoster-oracle/tap/aipack`.
- `install.sh` is the primary cross-platform install path for macOS and Linux.
- CLI releases use semantic versioning tags: `vMAJOR.MINOR.PATCH` and prerelease tags such as `vMAJOR.MINOR.PATCH-rc.1`.
- `aipack` remains in the `0.x` phase for now; breaking user-facing changes bump the minor version.
- The first public release line is `v0.3.0`.
- `VERSION` is the source of truth for the release line.
- Release process details live in [`RELEASING.md`](./RELEASING.md), and shipped user-facing changes are tracked in [`CHANGELOG.md`](./CHANGELOG.md).

## Registry

aipack supports multiple registry sources. Each source is cached locally and fetched independently.

- `aipack registry fetch <url>` fetches a single source and saves it for future use.
- `aipack registry fetch` (bare) fetches all configured sources.
- Sources are saved to `registry_sources` in `sync-config.yaml` and cached in `~/.config/aipack/registries/`.
- Git repos are auto-detected from `.git` suffix, or use `--ref` and `--path` for explicit git coordinates.
- The compiled-in default points at `registry.yaml` in `shrug-labs/aipack` and is used when no sources are configured.
- Even without registry entries, pack installs work via direct paths and `aipack pack install --url ...`.

## Building

```bash
make fmt       # format Go source
make fmt-check # fail if formatting is stale
make help       # show all targets
make build      # build for current platform → dist/
make test       # run Go tests
make dist       # cross-compile for darwin/arm64, darwin/amd64, linux/amd64
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
