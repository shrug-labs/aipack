# aipack reference

Complete CLI reference for `aipack`. For first-time setup, see [Getting Started](./getting-started.md). For pack authoring, see [Creating Packs](./creating-packs.md). For installing content from any repository, see [Installing Packs](./installing-packs.md). For profiles and composition, see [Profiles](./profiles.md). For sync workflow and save round-trips, see [Sync and Save](./sync.md). For the pack format specification, see [Pack Format](./pack-format.md). For per-harness rendering, see the [Harness Reference](./harness-reference.md). For config layout, see [Configuration and State](./configuration.md). For JSON output contracts, see the [CLI Specification](./cli-spec.md).

## Command map

- Setup: `init`, `doctor`
- Pack lifecycle: `pack create`, `pack install`, `pack delete`, `pack update`, `pack rename`, `pack add`, `pack remove`, `pack enable`, `pack disable`, `pack list`, `pack show`, `pack validate`
- Profiles: `profile create`, `profile delete`, `profile list`, `profile set`, `profile show`
- Registry: `registry fetch`, `registry list`, `registry sources`, `registry delete`
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
| `lockfile_migration` | warning | Reports failure if migrating legacy `installed_packs` from `sync-config.yaml` to `aipack.lock` failed |
| `lockfile_loaded` | warning | Reports failure if `aipack.lock` exists but cannot be parsed |
| `packs_registered` | warning | Detects pack directories not recorded in the lockfile |
| `install_entries_valid` | warning | Detects lockfile entries whose pack directory is missing (auto-fixable with `--fix`) |
| `stale_backups` | warning | Finds leftover backup and temp directories from interrupted installs/updates (auto-fixable with `--fix`) |
| `pack_version_drift` | warning | Compares installed pack versions/hashes against their origins (local checks only, no network) |
| `broken_refs` | warning | Reports profile references (includes, excludes, overrides) that were in a previous lockfile inventory but are no longer in the current pack contents — a pack update removed content the profile still references |
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

Scaffolds a new pack directory with `pack.json` manifest and standard subdirectories (`rules/`, `agents/`, `workflows/`, `skills/`, `mcp/`, `configs/`), then records it so it is immediately available for profiles and sync.

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

Bare `aipack pack install` (no arguments) reconciles the active profile — any packs referenced by the profile that aren't already on disk are fetched via the registry. This is the easiest way to catch up after setting a profile or after a shared profile gains new pack references. Pass a path, URL, or registry name to target a specific pack instead.

Supports three explicit sources:

- **Local path** (symlinked by default, `--copy` for full copy)
- **URL** (`--url` — fetched via shallow git clone)
- **Registry name** (bare name like `my-pack` — looked up in registry, then fetched)

`aipack install` is a top-level alias for `aipack pack install`. `-m`/`--missing` is an explicit alias for the bare form — useful in scripts where the intent is worth stating even when the default matches.

All remote installs use a shallow git clone (`git clone --depth 1`). Both HTTPS and SSH URLs are supported. SSH URLs (`git@host:path` or `ssh://`) avoid credential prompts. The local clone cache (`~/.config/aipack/.cache/git/`) speeds up subsequent clones via `git --reference`.

**Versioning.** Append `@version` to a pack name (or use `--version`) to install a specific semver tag or commit hash. The pack is then "pinned" — `pack update` won't change the install until you explicitly move the pin.

```bash
aipack pack install my-pack@1.2.3           # pin to exact semver tag v1.2.3
aipack pack install my-pack@v1              # partial: resolves to latest stable v1.x.x, pins to that
aipack pack install my-pack@v1.2            # partial: resolves to latest stable v1.2.x
aipack pack install my-pack@abc1234         # pin to commit hash
aipack pack install --url https://github.com/org/repo.git --version 1.2.3
```

Partial version references (`v1` or `v1.2`) query the remote tags, pick the highest matching stable tag, and pin to that exact version. Prereleases (`v1.2.0-beta.1`) are skipped during partial matching — pass an exact tag to install a prerelease. Partial installs are a discovery shortcut, not a channel: re-run `update --version v1` to move the pin when new v1.x.x tags land.

Use `aipack pack versions <name>` to discover available semver tags. Pack authors should tag their releases as `v1.2.3` (or `1.2.3` — the v-prefix is optional). The `version` field in `pack.json` is informational; git tags are authoritative.

By default, the pack is installed to disk but not added to any profile. Use `--add` to also add it to the active profile, or `--add --profile <name>` to target a specific one. Use `aipack pack add <name>` to add an installed pack to a profile later.

Core content (rules, skills, workflows, agents, prompts, mcp, configs) is always installed. Packs that bundle registries, profiles, or extras print a preview of what additional content would be applied. Use `-w all` to accept all bundled content, or apply selectively with `-w profiles`, `-w registries`, or `-w extras` (short forms: `-w p`, `-w r`, `-w e`). With `-w registries` (or `-w all`), bundled registry entries are merged into the user's local embedded registry cache (`~/.config/aipack/registries/_embedded.yaml`), making declared packs discoverable via `aipack search` and installable by name.

```bash
# Reconcile the active profile — install any missing packs (default)
aipack pack install
aipack pack install -m                      # explicit equivalent, same behavior

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

# Add to profile at install time
aipack pack install ./my-pack --add
aipack pack install ./my-pack --add --profile my-profile
```

Content flags extract specific directories from a URL source into a standard pack layout. The source repo needs no `pack.json`:

```bash
# Extract skills and rules from specific directories
aipack pack install --url https://github.com/org/repo.git \
  --skills src/skills --rules docs/rules --name their-content -q
```

Flags: `--rules`, `--skills`, `--agents`, `--workflows`, `--prompts` (directory paths within the repo). `--quiet` / `-q` marks the pack as quiet in the profile (omitted selectors include nothing). Content flags require `--url` and `--name`.

For the full guide on installing from non-pack repositories, see [Installing Packs](./installing-packs.md).

### pack list

Lists all installed packs with name, install method (link/copy/clone/local), version, origin, content summary, and broken-link status. Pinned packs show their version pin label inline (e.g. `v1.2.3 (pinned)`).

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

Updates installed pack(s) to latest version from their origin. By default, updates every installed pack; pass a name to target one. For cloned packs, re-clones from origin and re-extracts content (content path mappings from the original install are preserved). For copied packs, re-copies from the recorded origin. For symlinked packs, re-validates the link target.

When an update brings new bundled content categories that weren't previously approved, they're surfaced for review — printed in the CLI, shown as a checklist dialog in the TUI. Use `-w` to approve specific categories or `-w all` to accept everything.

```bash
aipack pack update                         # update all installed packs
aipack pack update my-pack                 # update one specific pack
aipack pack update --all                   # explicit alias for the bare form (scripts)
aipack pack update my-pack -w profiles     # also apply bundled profiles on this update
aipack pack update my-pack -w all          # accept all new bundled content
```

**Pinned packs stay pinned.** A bare `pack update` on a pack that was installed with a `--version` does not change the installed version. Instead, it checks the remote and reports the latest available version. Use `--version` to explicitly move or clear the pin:

```bash
aipack pack update my-pack --version 2.0.0    # move pin to a new tag
aipack pack update my-pack --version latest   # clear pin, track default branch HEAD again
```

Legacy packs installed via the (now-removed) `http-tarball` method are transparently migrated to the `clone` method on next update.

**Concurrent updates.** When refreshing multiple packs (bare `pack update` or `--all`), up to three packs update in parallel — bounded to stay within typical git-host connection limits. Per-pack stdout is buffered and flushed in input order after the parallel phase, and bundled profile and registry installs still run sequentially, so the transcript stays coherent and last-writer-wins semantics for shared profile IDs are deterministic. Concurrent clones for the same origin URL (for example, several packs installed from the same monorepo) serialize on the local bare-clone cache at `~/.config/aipack/.cache/git/` so only one remote fetch runs per origin. Ctrl-C mid-update stops dispatching new packs while letting in-flight workers finish on their own cancellation-aware operations.

### pack versions

Lists available semver tags for a pack from its remote git origin. Resolves the origin from the lockfile (if installed) or the registry (if not installed). Only tags that parse as valid semver are shown. The currently installed version is marked with a star.

```bash
aipack pack versions my-team-pack
aipack pack versions my-team-pack --json
```

### pack delete

Deletes an installed pack from disk and removes it from all profiles.

```bash
aipack pack delete my-pack
```

### pack rename

Renames an installed pack across all configuration: the pack directory, `pack.json` manifest, `sync-config.yaml`, all profiles, and all ledger files.

```bash
aipack pack rename old-name new-name
```

### pack add / pack remove

Adds or removes a pack entry from a profile. The pack must be installed on disk first (see `pack install`).

```bash
aipack pack add my-pack
aipack pack add my-pack --profile my-profile
aipack pack add my-pack -q              # add as quiet
aipack pack remove my-pack
aipack pack remove my-pack --profile my-profile
```

`--quiet` / `-q` sets `quiet: true` on the profile entry (omitted selectors include nothing). See [Profiles — Quiet packs](./profiles.md#quiet-packs).

### pack enable / pack disable

Toggles a pack's `enabled` field in a profile without removing the entry. Useful for temporarily deactivating a pack while preserving its selectors and overrides.

```bash
aipack pack enable my-pack
aipack pack enable my-pack --profile my-profile
aipack pack disable my-pack
aipack pack disable my-pack --profile my-profile
```

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

The registry maps pack names to source repositories. The unified view merges all cached sources in `~/.config/aipack/registries/` in `registry_sources` order from sync-config (first-seen wins for pack name conflicts). Sources include remote registries fetched via `registry fetch` and embedded entries bundled inside installed packs.

### registry fetch

Fetches remote registries and caches them locally. Each source is cached as a separate file and saved to `registry_sources` in sync-config for future fetches.

With an explicit URL, fetches that single source. Without a URL, fetches all configured sources (or the compiled-in default from `shrug-labs/packs`).

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

### registry delete

Deletes a registry source from sync-config and removes its cache file.

```bash
aipack registry delete my-tools
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
aipack --version   # same output; -V also works
```

---

## Per-harness reference

For rendering behavior, write targets, MCP configuration differences, and harness-specific notes, see the [Harness Reference](./harness-reference.md).
