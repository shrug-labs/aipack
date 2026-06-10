# Configuration and State

What aipack puts on your machine, how to configure it, and how to manage it. For the CLI commands that operate on these files, see the [aipack reference](./aipack.md). For profiles and composition, see [Profiles](./profiles.md). For registry schemas, see the [Pack Format Specification](./pack-format.md#112-registry). For JSON output contracts and environment variables, see the [CLI Specification](./cli-spec.md).

## Config directory layout

`aipack init` bootstraps the config directory below. All subsequent commands read from and write to this tree. The exact path depends on platform — see [Per-platform locations](#per-platform-locations) for the resolution rule and override flag.

```
~/.config/aipack/
├── sync-config.yaml          # root configuration — defaults, registry sources
├── .env                      # local values for {env:*} expansion; created empty by init
├── aipack.lock                # installed pack state (machine-managed; do not edit)
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
├── cache/                     # ephemeral user-local caches
│   └── mcp-probes.json        # live MCP tool probe results (24-hour TTL)
└── update-check.json          # cached update check result (6-hour TTL)
```

Project-scope state for the current directory also appears locally:

```
<project-dir>/
└── .aipack/                   # legacy project-scope ledger location (auto-migrated)
```

Directories are created with mode `0700`, files with `0600`.

### Per-platform locations

aipack derives the config directory from `os.UserHomeDir()` and a fixed suffix. It does **not** consult `XDG_CONFIG_HOME` or `APPDATA` — setting either of those does not move the config directory.

| Platform | Config directory |
|---|---|
| Linux | `~/.config/aipack/` |
| macOS | `~/.config/aipack/` (not `~/Library/Application Support/`) |
| Windows | `%APPDATA%\aipack\` (i.e. `<home>\AppData\Roaming\aipack\`) |
| WSL | Linux paths apply, even when invoking from a Windows shell — see the [FAQ](./faq.md). |

To use a different location, pass `--config-dir <path>` to any command. There is no environment-variable override.

## .env

`~/.config/aipack/.env` is created empty by `aipack init` and by commands that ensure the config directory exists. Populate it only when you want config-local values for `{env:VAR}` placeholders. aipack reads `.env` during profile resolution before falling back to the process environment, keeping machine-local secrets and paths out of profiles while still making sync deterministic from the config directory.

Supported lines are blank lines, comments, `KEY=value`, and `export KEY=value`. Invalid lines fail resolution with a line-numbered error that does not echo the line content.

Values are stored as raw strings — the parser does not strip surrounding quotes or interpret shell-style escape sequences. `KEY="value with spaces"` makes the literal `"value with spaces"` (quotes included) the value of `KEY`; write `KEY=value with spaces` instead. Leading and trailing whitespace on each value is trimmed; embedded `\n` and `\\` are not expanded. Files saved with a UTF-8 BOM will fail to parse the first key.

You can manage entries through `aipack config env` instead of editing the file by hand:

```bash
aipack config env list                # keys only (values masked) + total count
aipack config env list --show         # keys + values (--json supported with both forms)
aipack config env get API_TOKEN       # print one value
aipack config env set API_TOKEN abc   # write or replace, preserving comments and export prefixes
aipack config env unset API_TOKEN     # remove one entry
aipack config env path                # print absolute path to .env
aipack config env edit                # open the file in $EDITOR (or VISUAL, falling back to vi/notepad)
```

`env set` is line-preserving — comments, blank lines, and unrelated entries survive the rewrite, and existing `export KEY=...` prefixes are kept on the rewritten line. The TUI Config tab exposes profile params and config-dir `.env` entries together, with values hidden by default. Values containing newlines are rejected; key validation matches the file parser (letters, digits after the first character, and underscore).

## Profile params

Profile params are stored in `profiles/<name>.yaml` under `params:` and expanded through `{params.KEY}` references. Manage them through `aipack config params` so setup values live under one command family:

```bash
aipack config params list
aipack config params get workspace
aipack config params set workspace team-alpha
aipack config params unset workspace
aipack config params set tracker_url https://tracker.example.com --profile oncall
```

When `--profile` is omitted, aipack uses `sync-config.defaults.profile`, then `default`. Keep secrets and machine-local paths in `.env` via `{env:*}`; use profile params for stable values that should move with the profile.

## sync-config.yaml

The root configuration file. Created by `aipack init`, modified by `profile set` and `registry fetch`. You can also edit it by hand.

```yaml
schema_version: 1

defaults:
  profile: default        # active profile name
  harnesses:              # default target harnesses for sync (each synced independently)
    - codex
  scope: global           # "project" or "global" (default: global)
  auto_sync: false        # automatically sync after active-profile changes
  namespaced: false       # render pack provenance into content names

registry_sources:         # managed by registry fetch
  - name: default
    url: https://github.com/shrug-labs/packs.git
    path: registry.yaml
  - name: team-tools
    url: ssh://git@bitbucket.example.com:7999/TEAM/tools.git
    path: ops-tools/registry.yaml
```

Set scalar defaults from the CLI when you do not want to edit YAML directly:

```bash
aipack config defaults get harnesses
aipack config defaults set auto_sync true
aipack config defaults set collision_strategy error
aipack config defaults set harnesses codex,opencode
aipack config defaults set namespaced true
aipack config defaults set profile default
aipack config defaults set scope global
```

Installed pack metadata used to live here under an `installed_packs` section. It now lives in `aipack.lock` (see below). Existing installs are merged into the lockfile transparently on the first pack command (or `aipack doctor`) run after upgrade — no user action required.

### defaults

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `profile` | string | `default` | Active profile. Changed by `profile set` or `config defaults set profile <name>`. |
| `harnesses` | string[] | `[codex]` | Default target harnesses for sync. `sync` syncs each resolved harness independently (including `all`), in order. Set with a comma-separated value such as `config defaults set harnesses codex,opencode`. |
| `scope` | string | `global` | Default scope when `--scope` is not specified. Set with `config defaults set scope global` or `project`. |
| `collision_strategy` | string | `last-wins` | How content ID collisions between packs are resolved: `last-wins`, `first-wins`, or `error`. Explicit profile `overrides` always take precedence. |
| `auto_sync` | bool | `false` | When true, successful pack/profile changes that affect the active profile automatically run a normal sync using the current defaults. Set with `config defaults set auto_sync true`. |
| `namespaced` | bool | `false` | When true, rendered pack-authored markdown content uses namespaced names (`<id>__aipack__<pack>`) for path leaves and frontmatter names. Natural names remain the default. Cross-pack ID collisions for rules, agents, workflows, skills, and hooks are preserved instead of resolved by `collision_strategy`; MCP servers, plugins, and settings keys still use the configured collision behavior. Toggling this setting reconciles the target on the next sync; previous managed names are removed unless there is a user conflict. |

CLI flags override these defaults. The full resolution chain is documented in the [CLI Specification](./cli-spec.md#shared-flag-resolution).

### registry_sources

Each entry is a remote registry that `registry fetch` retrieves and caches.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Local name for this source (auto-derived from URL and path, or `--name`) |
| `url` | string | Git repository URL |
| `ref` | string | Git ref (branch or tag). Empty = git's default branch. |
| `path` | string | File path within the repo (default: `registry.yaml`) |

Sources are added automatically by `registry fetch <url>` and deleted by `registry delete`. `registry fetch` (bare) refreshes all configured sources plus compiled-in defaults. Public builds include the `shrug-labs/packs` registry by default. Distributors can prepend one additional default source by setting `github.com/shrug-labs/aipack/internal/config.AdditionalDefaultRegistryName` and `github.com/shrug-labs/aipack/internal/config.AdditionalDefaultRegistryURL` with Go ldflags. If a later distributor build changes that additional default's URL or path but keeps the same source name, bare `registry fetch` replaces the old configured source with the new compiled default.

When `ref` is empty (the default for `registry add` and `registry fetch` without `--ref`), the registry repo is cloned at its default branch. Repositories using `master`, `trunk`, or any other default branch work without configuration.

## aipack.lock

The lockfile records the resolved state of every installed pack. Created and maintained by `pack install`, `pack update`, `pack delete`, and `pack rename`. **Do not hand-edit it** — the format is machine-managed and may change between releases.

```yaml
lock_version: 1
packs:
  essentials:
    origin: https://github.com/shrug-labs/packs.git
    method: clone
    installed_at: "2026-04-12T08:30:00Z"
    sub_path: essentials
    ref: v1.2.3               # semver tag or commit hash = pinned; branch name = tracking; empty = default branch
    commit_hash: abc123def456
  local-pack:
    origin: /Users/x/src/local-pack
    method: link
    installed_at: "2026-03-12T10:00:00Z"
  archive-pack:
    origin: https://downloads.example.com/aipack/archive-pack.zip
    method: archive
    installed_at: "2026-04-12T09:00:00Z"
```

| Field | Type | Description |
|-------|------|-------------|
| `origin` | string | Absolute local path or remote URL |
| `method` | string | `link`, `copy`, `clone`, `archive`, or `local`. Legacy `http-tarball` entries are migrated to `clone` on next update. |
| `installed_at` | string | RFC 3339 timestamp |
| `ref` | string | Git ref at install time (remote only). A semver tag (`v1.2.3`), namespaced tag (`my-pack/v1.2.3`), or commit hash marks the pack as **pinned** and is preserved across `pack update`; a branch name or empty value tracks upstream. The raw remote spelling is stored unchanged so the ref can be checked out directly. `pack install --ref 1.2.3` (and its alias `--version 1.2.3`) resolve to the matching remote tag and record it here — pin state is derived from the ref's shape, not a separate field. |
| `sub_path` | string | Subdirectory within the repo (remote only) |
| `commit_hash` | string | Git HEAD SHA at install time (remote only). Enables fast-path update detection via `git ls-remote`. |
| `content_paths` | map | Maps content types to directory paths within the clone (see [Content path remapping](./pack-format.md#94-content-path-remapping)) |
| `approved` / `declined` | list | Bundled content categories the user accepted or declined at install time |

### Migration from sync-config installed_packs

The first time aipack reads pack state on a config directory that still has `installed_packs` in `sync-config.yaml` and no `aipack.lock`, it migrates the entries to the lockfile and strips `installed_packs` from sync-config. The migration is idempotent and runs from `aipack doctor` and any pack command. No user action is required — existing installs continue to work.

## Installed packs

Packs live under `~/.config/aipack/packs/<name>/`. Four install methods produce different directory types with different behaviors:

| Method | Directory type | Live edits? | Update behavior |
|--------|---------------|-------------|-----------------|
| `link` | Symlink to source directory | Yes — edits at either location hit the same files | Re-validates symlink target |
| `copy` | Full copy from local path | No — edits are local only | Re-copies from recorded origin |
| `clone` | Content-extracted from git clone | No — installed content is a static snapshot | Re-clones from origin, re-extracts content |
| `archive` | Content-extracted from zip/tar URL or file | No — installed content is a static snapshot | Re-fetches origin, full-replaces content |
| `local` | Pack already in packs directory | Yes — it's the source | Registered in-place, no fetch |

`link` is the default for local directory installs and is the best choice for pack development — you edit the source and `sync --watch` picks up changes automatically. `clone` is the default for remote git installs (SSH and HTTPS); `archive` is selected by registry entries with `method: archive`, direct installs with `--archive`, or direct `.zip`, `.tar`, `.tar.gz`, and `.tgz` sources.

Earlier versions of aipack also offered an `http-tarball` install method for GitHub HTTPS URLs. This method has been removed — clone is faster (with the local clone cache and `--reference`), gives every install a commit hash for fast-path updates, and produces consistent version handling. Existing tarball-installed packs are transparently migrated to clone on the next `pack update`. Archive installs remain supported for static distribution URLs, but they are intentionally unversioned and are re-fetched on every update.

### Integrity tracking

Each installed pack has an `.aipack-integrity.json` file recording SHA256 hashes of all files at install time. `pack update` uses this to compute and display what changed between versions.

## Profiles directory

Profiles live in `~/.config/aipack/profiles/<name>.yaml`. The active profile is set by `defaults.profile` in sync-config.yaml.

Profile files use schema version 2 and define which packs to load, what parameters to expand, and what content to include or exclude. The profile schema is documented in [Profiles](./profiles.md).

`aipack init` creates a minimal `default.yaml`:

```yaml
schema_version: 2
packs: []
```

Profiles installed via `-w all` (or `-w profiles`) from a team pack are copied here automatically.

## Registry configuration

Registries map pack names to git repositories and collection names to ordered pack install recipes. Three layers merge at resolution time:

1. **Local entries** in `~/.config/aipack/registry.yaml` — highest priority, manually maintained
2. **Cached remote sources** in `~/.config/aipack/registries/<source-name>.yaml` — one file per source, fetched by `registry fetch`
3. **Compiled-in default** pointing to `shrug-labs/packs` — used when no sources are configured

When multiple sources define the same pack or collection name, the first source in the list wins (local > cached sources in sync-config order > default). Collections do not have a separate local directory; they are part of registry YAML.

The registry YAML format is documented in the [Pack Format Specification](./pack-format.md#112-registry).

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

Cache files are keyed by `<harness>--<filename>` (e.g., `claudecode--settings.local.json`). An `index.json` manifest maps cache keys to their original file paths. Only settings and drop-in plugin files are cached — content files (rules, agents, workflows, skills, hooks, plugin descriptors) are not.

The cache is overwritten on every sync. `--dry-run` does not write cache files. `aipack restore --dry-run` previews what would be recovered.

## MCP probe cache

`~/.config/aipack/cache/mcp-probes.json` holds live tool-list results for MCP servers, populated by both the TUI picker and `aipack mcp inspect-tools` runs. Entries are keyed by the pack's absolute root path plus server name, stamped with `probed_at`, and expire after 24 hours. Expired entries are dropped on load and on save; no explicit purge is needed.

The file is pure optimization — missing, corrupt, or deleted, the next interaction simply re-probes. Safe to remove at any time. The TUI picker's header shows `probed Nh ago · r to refresh` when a cache hit seeds the initial view; pressing `r` inside the picker forces a fresh probe that overwrites the cached entry on success (and keeps the previous entry on failure).

## Search index

`~/.config/aipack/index.db` is a SQLite database (WAL mode) that powers `aipack search` and `aipack query`. It indexes resource names, descriptions, body text, tags, roles, categories, and discovery status across installed packs, registry/deep-index entries, and one-off `pack inspect` previews.

The index is rebuildable cache — not durable state. It's populated during `pack install`, `pack update`, `registry fetch --deep`, and `pack inspect`. If corrupted or deleted, the next index-building operation recreates it. Use `aipack search --status installed|registered|inspected` to filter by source state, or `aipack query --schema` to inspect the table structure.

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
