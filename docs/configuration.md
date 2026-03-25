# Configuration and State

What aipack puts on your machine, how to configure it, and how to manage it. For the CLI commands that operate on these files, see the [aipack reference](./aipack.md). For profile and registry schemas, see the [Pack Format Specification](./pack-format.md#8-composition). For JSON output contracts and environment variables, see the [CLI Specification](./cli-spec.md).

## Config directory layout

`aipack init` bootstraps `~/.config/aipack/`. All subsequent commands read from and write to this tree. On Windows the config directory is `%APPDATA%\aipack` instead. In WSL, Linux paths apply — see the [FAQ](./faq.md) for WSL-specific guidance.

```
~/.config/aipack/
├── sync-config.yaml          # root configuration — defaults, installed packs, registry sources
├── profiles/                  # profile YAML files
│   ├── default.yaml
│   └── oncall.yaml
├── packs/                     # installed pack directories
│   ├── my-pack/               # archive or clone (full copy)
│   └── local-pack → /src/…   # symlink (local install)
├── registries/                # cached remote registry sources
│   └── <source-name>.yaml
├── registry.yaml              # local registry entries (manual, highest priority)
├── ledger/                    # sync state (global scope + project scope)
│   ├── claudecode.json        # global-scope ledger for Claude Code
│   ├── <encoded-project>/     # project-scope ledgers, keyed by project path
│   │   ├── claudecode.json
│   │   └── presync/           # pre-sync settings cache for this project
│   └── presync/               # pre-sync settings cache (global scope)
├── index.db                   # SQLite search index (rebuildable)
└── update-check.json          # cached update check result (6-hour TTL)
```

Project-scope state for the current directory also appears locally:

```
<project-dir>/
└── .aipack/                   # legacy project-scope ledger location (auto-migrated)
```

Directories are created with mode `0700`, files with `0600`.

## sync-config.yaml

The root configuration file. Created by `aipack init`, modified by pack install/delete, profile set, and registry fetch. You can also edit it by hand.

```yaml
schema_version: 1

defaults:
  profile: default        # active profile name
  harnesses:              # target harnesses for sync (list)
    - cline
  scope: project          # default scope: "project" or "global"

installed_packs:          # managed by pack install/delete/update
  essentials:
    origin: "https://github.com/shrug-labs/packs.git"
    method: http-tarball
    installed_at: "2026-03-10T08:30:00Z"
    ref: main
    sub_path: essentials
    commit_hash: abc123def456
  local-pack:
    origin: /Users/x/src/local-pack
    method: link
    installed_at: "2026-03-12T10:00:00Z"

registry_sources:         # managed by registry fetch
  - name: default
    url: https://github.com/shrug-labs/packs.git
    ref: main
    path: registry.yaml
  - name: team-tools
    url: ssh://git@bitbucket.example.com:7999/TEAM/tools.git
    ref: main
    path: ops-tools/registry.yaml
```

### defaults

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `profile` | string | `default` | Active profile. Changed by `profile set`. |
| `harnesses` | string[] | `[cline]` | Target harnesses for sync. Multiple harnesses sync in one pass. |
| `scope` | string | `project` | Default scope when `--scope` is not specified. |

CLI flags override these defaults. The full resolution chain is documented in the [CLI Specification](./cli-spec.md#shared-flag-resolution).

### installed_packs

Each entry records how a pack was installed. Keys are pack names.

| Field | Type | Description |
|-------|------|-------------|
| `origin` | string | Absolute local path or remote URL |
| `method` | string | `link`, `copy`, `clone`, `http-tarball`, or `archive` (legacy) |
| `installed_at` | string | RFC 3339 timestamp |
| `ref` | string | Git ref used at install time (remote only) |
| `sub_path` | string | Subdirectory within the repo (remote only) |
| `commit_hash` | string | Git HEAD SHA at install time (remote only) |

Don't edit these entries by hand — use `pack install`, `pack delete`, `pack update`, and `pack rename`.

### registry_sources

Each entry is a remote registry that `registry fetch` retrieves and caches.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Local name for this source (auto-derived from URL or `--name`) |
| `url` | string | Git repository URL |
| `ref` | string | Git ref (branch or tag) |
| `path` | string | File path within the repo (default: `registry.yaml`) |

Sources are added automatically by `registry fetch <url>` and removed by `registry remove`. `registry fetch` (bare) refreshes all configured sources.

## Installed packs

Packs live under `~/.config/aipack/packs/<name>/`. Four install methods produce different directory types with different behaviors:

| Method | Directory type | Live edits? | Update behavior |
|--------|---------------|-------------|-----------------|
| `link` | Symlink to source directory | Yes — edits at either location hit the same files | Re-validates symlink target |
| `copy` | Full copy from local path | No — edits are local only | Re-copies from recorded origin |
| `clone` | Shallow git clone | Yes (with `git pull`) | `git pull` in the clone |
| `http-tarball` | Downloaded from GitHub | No — edits are local only | Re-downloads tarball, shows file-level diff |
| `local` | Pack already in packs directory | Yes — it's the source | Registered in-place, no fetch |

`link` is the default for local installs and is the best choice for pack development — you edit the source and `sync --watch` picks up changes automatically. `clone` is the default for SSH remote installs; `http-tarball` is used for GitHub HTTPS URLs.

### Integrity tracking

Each installed pack has an `.aipack-integrity.json` file recording SHA256 hashes of all files at install time. `pack update` uses this to compute and display what changed between versions.

## Profiles directory

Profiles live in `~/.config/aipack/profiles/<name>.yaml`. The active profile is set by `defaults.profile` in sync-config.yaml.

Profile files use schema version 2 and define which packs to load, what parameters to expand, and what content to include or exclude. The profile schema is documented in the [Pack Format Specification](./pack-format.md#81-profile-structure).

`aipack init` creates a minimal `default.yaml`:

```yaml
schema_version: 2
packs: []
```

Profiles installed via `--seed` from a team pack are copied here automatically.

## Registry configuration

Registries map pack names to git repositories. Three layers merge at resolution time:

1. **Local entries** in `~/.config/aipack/registry.yaml` — highest priority, manually maintained
2. **Cached remote sources** in `~/.config/aipack/registries/<source-name>.yaml` — one file per source, fetched by `registry fetch`
3. **Compiled-in default** pointing to `shrug-labs/aipack` — used when no sources are configured

When multiple sources define the same pack name, the first source in the list wins (local > cached sources in sync-config order > default).

The registry YAML format is documented in the [Pack Format Specification](./pack-format.md#92-registry).

## Ledger

The ledger tracks every file that `aipack sync` writes. It enables conflict detection (has the user modified a managed file since the last sync?), provenance tracking (which pack contributed this file?), and safe pruning (which files are stale?).

Each harness in each scope gets its own ledger file:

- **Global scope:** `~/.config/aipack/ledger/<harness>.json`
- **Project scope:** `~/.config/aipack/ledger/<encoded-project-path>/<harness>.json`

Each entry in the ledger records a content digest (SHA256), the sync timestamp, the file's mtime, and which pack contributed it. During sync, aipack compares the on-disk file's digest against the ledger to classify it as identical, managed (safe to update), or conflicted (user-modified).

### Ledger diagnostics

`aipack doctor` runs two ledger-related checks:

- **`ledger_health`** — detects orphaned entries (files no longer on disk) and missing `source_pack` fields. Repairable with `--fix`.
- **`stale_ledgers`** — detects ledger files left behind from a previous scope or harness configuration.

`aipack clean --ledger` removes ledger files entirely, resetting sync state for a fresh start.

## Pre-sync cache

Each time `aipack sync` writes a settings file, it first snapshots the existing content into a `presync/` directory alongside the ledger. This enables `aipack restore` to undo the last sync's settings changes.

Cache files are keyed by `<harness>--<filename>` (e.g., `claudecode--settings.local.json`). An `index.json` manifest maps cache keys to their original file paths. Only settings and plugin files are cached — content files (rules, agents, workflows, skills) are not.

The cache is overwritten on every sync. `--dry-run` does not write cache files. `aipack restore --dry-run` previews what would be recovered.

## Search index

`~/.config/aipack/index.db` is a SQLite database (WAL mode) that powers `aipack search` and `aipack query`. It indexes resource names, descriptions, body text, tags, roles, and categories across all installed packs.

The index is rebuildable cache — not durable state. It's populated during `pack install`, `pack update`, and `registry fetch --deep`. If corrupted or deleted, the next index-building operation recreates it. Use `aipack query --schema` to inspect the table structure.

## State management

| I want to... | Command |
|---|---|
| Bootstrap config directory and default profile | `aipack init` |
| Check config health and detect drift | `aipack doctor` |
| Auto-repair safe ledger and manifest issues | `aipack doctor --fix` |
| Undo the last sync's settings changes | `aipack restore` |
| Remove all managed files from harness locations | `aipack clean` |
| Remove managed files and reset sync state | `aipack clean --ledger` |
| Preview what sync would change | `aipack sync --dry-run` |
| See ecosystem status at a glance | `aipack status` |
| Trace a resource from pack source to harness destination | `aipack trace <type> <name>` |
