# aipack reference

Complete CLI reference for `aipack`. For first-time setup, see [Getting Started](./getting-started.md). For pack authoring, see [Creating Packs](./creating-packs.md). For installing content from any repository, see [Installing Packs](./installing-packs.md). For profiles and composition, see [Profiles](./profiles.md). For sync workflow and save round-trips, see [Sync and Save](./sync.md). For the pack format specification, see [Pack Format](./pack-format.md). For per-harness rendering, see the [Harness Reference](./harness-reference.md). For config layout, see [Configuration and State](./configuration.md). For JSON output contracts, see the [CLI Specification](./cli-spec.md).

## Command map

- Setup: `init`, `doctor`
- Pack lifecycle: `pack create`, `pack install`, `pack delete`, `pack update`, `pack rename`, `pack enable`, `pack disable`, `pack list`, `pack show`, `pack validate`
- Profiles: `profile create`, `profile delete`, `profile list`, `profile set`, `profile show`
- Registry: `registry fetch`, `registry list`, `registry sources`, `registry remove`
- Sync/Save: `sync`, `save`, `restore`, `clean`, `render`
- Discovery: `search`, `query`, `status`, `trace`
- Interactive: `manage`
- Prompts: `prompt list`, `prompt copy`, `prompt show`
- Other: `version`

## Setup

### init

Creates `~/.config/aipack/sync-config.yaml` and `~/.config/aipack/profiles/default.yaml` with starter content. Skips files that already exist unless `--force` is set. (On Windows, `%APPDATA%\aipack` replaces `~/.config/aipack`.)

```bash
aipack init
aipack init --force
aipack init --config-dir /path/to/config
```

### doctor

Runs diagnostic checks on config, packs, and MCP servers. Overall status fails only on critical-severity checks; warnings are reported but don't cause a non-zero exit.

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
| `mcp_refs_present` | critical | Ensures required refs (env vars + params) are set for enabled MCP servers |
| `mcp_server_paths_exist` | critical | Verifies MCP server commands exist and paths are accessible |

```bash
aipack doctor
aipack doctor --fix       # auto-fix safe issues
aipack doctor --json      # machine-readable output
```

## Pack lifecycle

Packs are portable, versioned bundles of AI agent configuration installed under `~/.config/aipack/packs/<name>/`. See the [Pack Format Specification](./pack-format.md) for the format itself.

### pack create

Scaffolds a new pack directory with `pack.json` manifest and standard subdirectories (`rules/`, `agents/`, `workflows/`, `skills/`, `mcp/`, `configs/`), then registers it so it is immediately available for profiles and sync.

By default the pack is created in the current directory and symlinked into the packs directory. Use `--local` to create it directly inside the packs directory instead.

```bash
aipack pack create my-new-pack
aipack pack create my-new-pack --local
```

Content source flags create directory-level symlinks instead of empty scaffold directories:

```bash
aipack pack create my-pack --skills ./src/skills --rules ./docs/rules
aipack pack create my-pack --local --agents ./agents --workflows ./workflows
```

Flags: `--rules`, `--skills`, `--agents`, `--workflows`, `--prompts`. Each takes a local directory path. The source directory must exist.

### pack install

Installs a pack into `~/.config/aipack/packs/<name>/`. Supports three sources:

- **Local path** (symlinked by default, `--copy` for full copy)
- **URL** (`--url` — fetched via HTTP tarball for GitHub, shallow clone for everything else)
- **Registry name** (bare name like `my-team-pack` — looked up in registry, then fetched)

`aipack install` is a top-level alias for `aipack pack install`.

With `-m`/`--missing`, installs all missing packs from the active profile by looking them up in the registry. This is the easiest way to catch up after setting a profile or after new packs are added to a shared profile.

Remote packs from GitHub HTTPS URLs are fetched as HTTP tarballs (no git binary required). All other URLs use a shallow clone (`git clone --depth 1`).

Both HTTPS and SSH URLs are supported. SSH URLs (`git@host:path` or `ssh://`) avoid credential prompts.

By default, auto-registers the pack as a source in the active profile. Use `--no-register` to skip, or `--profile <name>` to target a specific profile.

Core content (rules, skills, workflows, agents, prompts, mcp, configs) is always installed. Packs that bundle registries, profiles, or extras print a preview of what additional content would be applied. Use `-w all` to accept all bundled content, or apply selectively with `-w profiles`, `-w registries`, or `-w extras` (short forms: `-w p`, `-w r`, `-w e`). With `-w registries` (or `-w all`), bundled registry entries are merged into the user's local embedded registry cache (`~/.config/aipack/registries/_embedded.yaml`), making declared packs discoverable via `aipack search` and installable by name.

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
aipack pack install --url https://github.com/org/repo.git --path team-pack -w all

# Profile and registration control
aipack pack install ./my-pack --no-register
aipack pack install ./my-pack --profile production
```

Content flags extract specific directories from a URL source into a standard pack layout. The source repo needs no `pack.json`:

```bash
# Extract skills and rules from specific directories
aipack pack install --url https://github.com/org/repo.git \
  --skills src/skills --rules docs/rules --name their-content -q
```

Flags: `--rules`, `--skills`, `--agents`, `--workflows`, `--prompts` (directory paths within the repo). `--quiet` / `-q` registers the pack as quiet in the profile (omitted selectors include nothing). Content flags require `--url` and `--name`.

For the full guide on installing from non-pack repositories, see [Installing Packs](./installing-packs.md).

### pack list

Lists all installed packs with name, install method (link/copy/clone/http-tarball), version, origin, content summary, and broken-link status.

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

### pack update

Updates installed pack(s) to latest version from their origin. For cloned packs, re-clones from origin and re-extracts content (content path mappings from the original install are preserved). For HTTP-tarball packs, re-downloads and re-extracts. For copied packs, re-copies from the recorded origin. For symlinked packs, re-validates the link target. Exactly one of `<name>` or `--all` is required.

When an update brings new bundled content categories that weren't previously approved, they're surfaced for review — printed in the CLI, shown as a checklist dialog in the TUI. Use `-w` to approve specific categories or `-w all` to accept everything.

```bash
aipack pack update my-pack
aipack pack update --all
aipack pack update my-pack -w profiles    # also apply bundled profiles on this update
aipack pack update my-pack -w all         # accept all new bundled content
```

### pack delete

Deletes an installed pack from disk and deregisters it from all profiles.

```bash
aipack pack delete my-pack
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
aipack pack enable my-pack -q           # register as quiet
aipack pack disable my-pack
aipack pack disable my-pack --profile production
```

`--quiet` / `-q` sets `quiet: true` on the profile entry (omitted selectors include nothing). See [Profiles — Quiet packs](./profiles.md#quiet-packs).

### pack validate

Read-only validation of a single pack source tree. Checks pack structure, manifest inventory, frontmatter correctness, and content policy (secrets, forbidden paths) without installing or syncing anything. Exit code 0 if clean, 1 if findings are reported.

Each finding includes a severity (`error` or `warning`), a category (`frontmatter`, `policy`, `consistency`, or `inventory`), the file path, and a message. In human output, findings are printed as `- [severity] path: message`. For the JSON output shape, see the [CLI Specification](./cli-spec.md#aipack-pack-validate).

```bash
aipack pack validate ./my-pack
aipack pack validate ./my-pack --json
```

## Profile management

Profiles define which packs, content, and settings to sync. Stored as YAML under `~/.config/aipack/profiles/`. For the profile schema and composition model, see [Profiles](./profiles.md).

Resolution order when multiple sources specify a profile:

- `--profile-path` → `--profile` → `sync-config defaults.profile` → `default`

### profile list

Lists all profiles. The active profile (from `defaults.profile` in sync-config) is marked with `(active)`.

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
  --ref team/ops-tools --path ops-tools/registry.yaml

# Override the cached source name
aipack registry fetch https://bitbucket.example.com/scm/TEAM/tools.git --name my-tools

# Fetch from an HTTP URL
aipack registry fetch https://example.com/registry.yaml

# Fetch all configured sources
aipack registry fetch

# Deep-index for resource-level search
aipack registry fetch --deep
```

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

### registry remove

Removes a registry source from sync-config and deletes its cache file.

```bash
aipack registry remove my-tools
```

## Sync, Save, Restore, Clean, Render

For the sync workflow, save round-trips, restore, clean, and render, see [Sync and Save](./sync.md).

## Discovery

### status

Shows ecosystem status: active profile, installed packs with content inventories, and totals.

```bash
aipack status
aipack status --profile production
aipack status --profile-path /path/to/profile.yaml
aipack status --json
```

### trace

Traces a single resource through the sync pipeline, showing where it comes from (pack source) and where it would land in each harness location. Useful for debugging why a rule isn't showing up or which harness file contains a given resource.

Valid resource types: `rule`, `agent`, `workflow`, `skill`, `mcp`.

The output shows the source pack, source file path, and each destination with its harness, file path, and on-disk state (`create`, `identical`, `managed`, `conflict`, `untracked`, or `error`). Use `--harness` to filter output to a single harness. Destinations where the resource is composited into a multi-resource file (e.g. Codex flattening rules into `AGENTS.override.md`) are flagged as embedded separately from the state.

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

### search

Full-text search (FTS5 with BM25 ranking) across resource names, descriptions, and body text. The SQLite index is built automatically during `registry fetch --deep` and pack install.

Filters: `--tags` (comma-separated), `--role`, `--kind` (rule/skill/workflow/agent/pack), `--category` (ops/dev/infra/governance/meta), `--pack`, `--installed`, `--available`.

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

Tabs: Profiles, Packs, Save, Sync, Search.

Key bindings: `tab` switch tabs, `j/k` navigate, `enter` expand, `space` toggle, `l` list profiles, `n` new profile, `d` delete, `D` duplicate, `a` activate, `p` add pack, `r` remove pack, `s` sync, `esc` quit (auto-saves).

```bash
aipack manage
```

## Prompts

Browse and copy prompts from installed packs. Prompts are opaque text blobs (no frontmatter validation) declared in a pack's `prompts/` directory.

```bash
aipack prompt list
aipack prompt show my-prompt
aipack prompt copy my-prompt   # copies to clipboard
```

## Version

Prints the CLI version string. Also checks for newer releases in the background (cached for 6 hours, disable with `AIPACK_NO_UPDATE_CHECK=1`).

```bash
aipack version
```

---

## Per-harness reference

For rendering behavior, write targets, MCP configuration differences, and harness-specific notes, see the [Harness Reference](./harness-reference.md).
