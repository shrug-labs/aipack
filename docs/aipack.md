# aipack reference

Complete CLI reference for `aipack`. For first-time setup, see [Getting Started](./getting-started.md). For pack authoring, see [Creating Packs](./creating-packs.md). For installing content from any repository, see [Installing Packs](./installing-packs.md). For profiles and composition, see [Profiles](./profiles.md). For sync workflow and save round-trips, see [Sync and Save](./sync.md). For the pack format specification, see [Pack Format](./pack-format.md). For per-harness rendering, see the [Harness Reference](./harness-reference.md). For config layout, see [Configuration and State](./configuration.md). For JSON output contracts, see the [CLI Specification](./cli-spec.md).

## Fast path

These commands cover the common first mile without adding shortcut-only aliases.

```bash
aipack pack inspect <source>     # inspect a pack source before installing it
aipack pack install <source> --add
aipack setup                     # show missing params/env values before sync
aipack sync                      # render active profile content to your harness
```

Use `pack import` for one markdown rule, prompt, or skill file; it can create a small local pack or add the content to an installed pack.

## Command map

- Setup: `init`, `doctor`, `setup`, `config defaults`, `config env`, `config params`, `mcp inspect-tools`
- Pack lifecycle: `pack create`, `pack import`, `pack install`, `pack inspect`, `pack delete`, `pack update`, `pack rename`, `pack add`, `pack remove`, `pack enable`, `pack disable`, `pack list`, `pack show`, `pack validate`
- Profiles: `profile create`, `profile delete`, `profile list`, `profile set`, `profile show`, `profile include`, `profile exclude`, `profile refs`, `profile set-param`, `profile unset-param`
- Collections: `collection list`, `collection show`, `collection install`
- Registry: `registry fetch`, `registry list`, `registry sources`, `registry delete`, `registry validate`
- Sync/Save: `sync`, `save`, `restore`, `clean`, `render`
- Discovery: `search`, `query`, `status`, `trace`
- Interactive: `manage`
- Prompts: `prompt list`, `prompt copy`, `prompt show`
- Other: `version`

## Setup

### init

Creates `~/.config/aipack/sync-config.yaml`, `~/.config/aipack/profiles/default.yaml`, and an empty `~/.config/aipack/.env` placeholder with starter content. Skips files that already exist unless `--force` is set; existing `.env` files are always preserved. (On Windows, `%APPDATA%\aipack` replaces `~/.config/aipack`.)

```bash
aipack init
aipack init --force
aipack init --config-dir /path/to/config
```

### setup

Shows the missing params and env vars needed before sync. Params are shown with `config params set` commands for the target profile, and env vars are shown with `config env set` commands that write to the active config directory's `.env` file.

```bash
aipack setup
aipack setup production
```

### config defaults

Reads and sets scalar defaults in `sync-config.yaml` from the CLI. Supported keys are `profile`, `harnesses`, `scope`, `collision_strategy`, `auto_sync`, and `namespaced`; hyphenated names and `defaults.<name>` are accepted as aliases.

```bash
aipack config defaults get harnesses
aipack config defaults set profile default
aipack config defaults set harnesses codex,opencode
aipack config defaults set scope global
aipack config defaults set collision_strategy last-wins
aipack config defaults set auto_sync true
aipack config defaults set namespaced true
```

### config params

Manages profile-scoped values used by `{params.*}` references. Values are stored in the selected profile; `--profile` defaults to `sync-config.defaults.profile`, then `default`.

```bash
aipack config params list
aipack config params list --profile production --json
aipack config params get tracker_url --profile production
aipack config params set tracker_url https://tracker.example.com --profile production
aipack config params unset tracker_url --profile production
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

### mcp inspect-tools

Connects to MCP servers and queries their live tool inventories via the MCP protocol (`initialize` → `tools/list`). Compares discovered tools against the static `available_tools` in each pack's `mcp/<server>.json` inventory, reporting additions and removals.

Without arguments, lists every MCP server found across installed packs with its pack, transport, and current inventory count. Pass a server name to inspect it. Use `--all` to inspect every server.

Server names are looked up across all installed packs. When the same name appears in multiple packs, specify `pack/server` to disambiguate. The `--profile` flag selects which profile supplies `{params.*}` values for server commands; the active profile is used by default. All three MCP transports are probed: stdio (subprocess), streamable-http (POST with `application/json` or `text/event-stream` responses), and the legacy HTTP+SSE transport (GET stream + POST). HTTP transports include the status code and response body snippet in error output so auth failures are self-describing.

With `--save`, the discovered tool list replaces `available_tools` in the pack's inventory JSON. All other metadata (`command`, `env`, `links`, `auth`, `notes`) is preserved. This is the recommended way to keep inventories current after a server update — avoids manual JSON editing and ensures the TUI tool picker and tool counts reflect reality. Combine with `--dry-run` to preview the writes without touching disk.

```bash
# List available MCP servers
aipack mcp inspect-tools

# Inspect a server
aipack mcp inspect-tools my-server

# Disambiguate when a name exists in multiple packs
aipack mcp inspect-tools my-team-pack/my-server

# Inspect and save to pack inventory
aipack mcp inspect-tools my-server --save

# Preview --save without writing
aipack mcp inspect-tools my-server --save --dry-run

# Inspect all servers
aipack mcp inspect-tools --all

# Use a different profile for {params.*} expansion
aipack mcp inspect-tools my-server --profile ops

# JSON output
aipack mcp inspect-tools my-server --json
```

## Pack lifecycle

Packs are portable, versioned bundles of AI agent configuration installed under `~/.config/aipack/packs/<name>/`. See the [Pack Format Specification](./pack-format.md) for the format itself.

### pack create

Scaffolds a new pack directory with `pack.json` manifest and standard subdirectories (`rules/`, `agents/`, `workflows/`, `skills/`, `hooks/`, `plugins/`, `mcp/`, `configs/`), then records it so it is immediately available for profiles and sync. `default` is reserved for the user's local default profile and is not a valid pack name.

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

Flags: `--rules`, `--skills`, `--agents`, `--workflows`, `--hooks`, `--prompts`. Each takes a local directory path. The source directory must exist.

### pack import

Imports one markdown file as a rule, skill, or prompt. Use `--name` to create a new installed pack, or `--pack` to add the content to an existing installed pack.

```bash
aipack pack import ./review.md --type skill --name review-pack
aipack pack import ./triage.md --type skill --pack example-pack
aipack pack import https://example.com/rule.md --type rule --name rules --id incident-rule
aipack pack import ./prompt.md --type prompt --name prompts --add
```

Flags: `--type skill|rule|prompt`, exactly one of `--name <new-pack>` or `--pack <installed-pack>`, optional `--id <id>`, optional `--add`, optional `--profile <name>`. If `--id` is omitted, aipack derives it from the source filename. If the file does not start with YAML frontmatter, aipack adds minimal frontmatter for the selected type. Imports into existing packs preserve auto-discovered manifest categories; explicit category lists are appended.

### pack install

Bare `aipack pack install` (no arguments) reconciles the active profile — any packs referenced by the profile that aren't already on disk are fetched via the registry. This is the easiest way to catch up after setting a profile or after a shared profile gains new pack references. Pass a path, URL, or registry name to target a specific pack instead. Pass multiple registry names to install them in one command.

Supports four explicit sources:

- **Local path** (symlinked by default, `--copy` for full copy)
- **Git URL** (`--url` or positional URL — fetched via shallow git clone)
- **Archive URL or file** (`.zip`, `.tar`, `.tar.gz`, `.tgz` — extracted as a static snapshot)
- **Registry name** (bare name like `my-pack` — looked up in registry, then fetched)

`aipack install` is a top-level alias for `aipack pack install`. `-m`/`--missing` is an explicit alias for the bare form — useful in scripts where the intent is worth stating even when the default matches.

Git URL installs use a shallow clone (`git clone --depth 1`). Both HTTPS and SSH URLs are supported. SSH URLs (`git@host:path` or `ssh://`) avoid credential prompts. The local clone cache (`~/.config/aipack/.cache/git/`) speeds up subsequent clones via `git --reference`. Static archive URLs and files are extracted as snapshots instead of cloned.

**Pinning.** Append `@<ref>` to a pack name (or use `--ref`, or the Kong alias `--version`) to install a specific git ref. The pack is then "pinned" — `pack update` won't change the install until you explicitly move the pin. Any git ref shape works: exact semver, partial semver (`v1`, `v1.2`), commit hash, namespaced tag (`<pack>/vX.Y.Z`), branch name, or the `latest` sentinel.

```bash
aipack pack install my-pack@1.2.3                     # pin to exact semver tag v1.2.3
aipack pack install my-pack@v1                        # partial: resolves to latest stable v1.x.x, pins to that
aipack pack install my-pack@v1.2                      # partial: resolves to latest stable v1.2.x
aipack pack install my-pack@abc1234                   # pin to commit hash
aipack pack install my-pack@my-pack/v0.3.0            # pin to namespaced tag (multi-pack monorepo)
aipack pack install my-pack --ref main                # track a branch (no pin)
aipack pack install --url https://github.com/org/repo.git --ref 1.2.3
```

`--version` is a Kong alias for `--ref` kept for historical scripts; new content should prefer `--ref`.

Partial version references (`v1` or `v1.2`) query the remote tags, pick the highest matching stable tag, and pin to that exact version. Prereleases (`v1.2.0-beta.1`) are skipped during partial matching — pass an exact tag to install a prerelease. Partial installs are a discovery shortcut, not a channel: re-run `update --ref v1` to move the pin when new v1.x.x tags land.

Namespaced tags unblock multi-pack monorepos where flat `v1.2.3` tags would be ambiguous across sibling packs. The convention is `<pack-name>/vX.Y.Z` (Go-module style). Once installed, subsequent `pack update` and `pack versions` commands derive the prefix from the lockfile, so users don't have to re-type it — `pack update my-pack --ref 0.3.1` resolves against `my-pack/v0.3.1` automatically.

Use `aipack pack versions <name>` to discover available semver tags. Pack authors should tag their releases as `v1.2.3` (or `1.2.3` — the v-prefix is optional), or as `<pack-name>/v1.2.3` for multi-pack repos. The `version` field in `pack.json` is informational; git tags are authoritative.

By default, the pack is installed to disk but not added to any profile. Use `--add` to also add it to the active profile, or `--add --profile <name>` to target a specific one. Use `aipack pack add <name>` to add an installed pack to a profile later.

Multiple positional sources are supported for registry pack names only. Shared flags such as `--add`, `--profile`, `--with`, `--quiet`, and `--no-quiet` apply to every pack in the batch. Use `name@<ref>` per pack when refs differ; use a single-pack install for local paths, direct URLs, `--path`, `--name`, `--copy`, `--ref`, or content-path flags.

When `defaults.auto_sync: true` is set in `sync-config.yaml`, installs that add content to the active profile automatically run `aipack sync` after the install succeeds. Installs that target another profile do not auto-sync.

Core content (rules, skills, workflows, agents, hooks, prompts, mcp, configs) is always installed. Packs that bundle registries, profiles, or extras print a preview of what additional content would be applied. Use `-w all` to accept all bundled content, or apply selectively with `-w profiles`, `-w registries`, or `-w extras` (short forms: `-w p`, `-w r`, `-w e`). With `-w registries` (or `-w all`), bundled registry entries are merged into the user's local embedded registry cache (`~/.config/aipack/registries/_embedded.yaml`), making declared packs discoverable via `aipack search` and installable by name. A bundled profile named `default` is reserved for the user's local default profile; installs skip it with a warning and continue.

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

# Static archives
aipack pack install https://example.com/team-pack.zip
aipack pack install ./team-pack.zip

# Subdirectory within a mono-repo
aipack pack install --url https://github.com/org/shared-repo.git --path team-pack

# Registry name
aipack pack install my-team-pack
aipack pack install essentials aipack-core memory --add -w all
aipack pack install my-pack@v1 other-pack@main --add

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

Flags: `--rules`, `--skills`, `--agents`, `--workflows`, `--hooks`, `--prompts` (directory paths within the repo). `--quiet` / `-q` marks the pack as quiet in the profile (omitted selectors include nothing). Content flags require `--url` and `--name`.

For the full guide on installing from non-pack repositories, see [Installing Packs](./installing-packs.md).

### pack list

Lists all installed packs with name, install method (link/copy/clone/archive/local), version, origin, content summary, and broken-link status. Pinned packs show their version pin label inline (e.g. `v1.2.3 (pinned)`).

```bash
aipack pack list
aipack pack list --json
```

### pack show

Displays detailed metadata for an installed pack: name, version, path, install method, origin, git ref, commit hash, install timestamp, and content inventory (rules, agents, workflows, skills, hooks, MCP servers).

```bash
aipack pack show my-pack
aipack pack show my-pack --json
```

### pack inspect

Inspects a local path, registry pack name, git URL, or archive URL/file without installing the pack, changing the lockfile, or adding it to a profile. The output shows source metadata, discovered content counts, content IDs, bundled profiles/registries/extras, plugins, MCP servers, and trust warnings such as external-tool MCP access. Inspected resources are written to the search index with status `inspected`, so you can search a previewed pack before deciding to trust or install it.

```bash
aipack pack inspect ./my-pack
aipack pack inspect team-pack
aipack pack inspect --url https://github.com/org/repo.git --path packs/team
aipack pack inspect https://example.com/team-pack.zip
aipack pack inspect ./team-pack.zip
aipack pack inspect team-pack --json
aipack pack inspect --clear              # wipe inspected rows from the index
aipack search --status inspected
```

Inspected rows are not durable cache — they live alongside installed and registered content in the search index so you can search a preview before installing. Each new inspect drops inspected rows older than 30 days automatically; `aipack pack inspect --clear` removes them on demand and never touches installed or registered packs.

### pack update

Updates installed pack(s) to latest version from their origin. By default, updates every installed pack; pass a name to target one. For cloned packs, re-clones from origin and re-extracts content (content path mappings from the original install are preserved). For copied packs, re-copies from the recorded origin. For symlinked packs, re-validates the link target.

`--dry-run` previews per-pack outcomes and file-level content changes without touching installed packs, bundled content, the lockfile, or the local git cache. Use it before a real update to check the commit-hash transition, changed files, and any new bundled categories that would land.

When an update brings new bundled content categories that weren't previously approved, they're surfaced for review — printed in the CLI, shown as a checklist dialog in the TUI. Use `-w` to approve specific categories or `-w all` to accept everything.

With `defaults.auto_sync: true`, successful updates automatically sync only when at least one updated pack is enabled in the active profile. `--dry-run`, failed updates, and updates for inactive-profile-only packs do not auto-sync.

```bash
aipack pack update                         # update all installed packs
aipack pack update my-pack                 # update one specific pack
aipack pack update --all                   # explicit alias for the bare form (scripts)
aipack pack update my-pack -w profiles     # also apply bundled profiles on this update
aipack pack update my-pack -w all          # accept all new bundled content
```

**Pinned packs stay pinned.** A bare `pack update` on a pack that was installed with a `--ref` does not change the installed version. Instead, it checks the remote and reports the latest available version. Use `--ref` to explicitly move or clear the pin:

```bash
aipack pack update my-pack --ref 2.0.0     # move pin to a new tag
aipack pack update my-pack --ref latest    # clear pin, track default branch HEAD again
aipack pack update my-pack --ref main      # switch to tracking a branch
```

Legacy packs installed via the (now-removed) `http-tarball` method are transparently migrated to the `clone` method on next update. In dry-run mode the migration is only previewed; no pack files, lockfile metadata, bundled content, or git cache entries are written.

**Concurrent updates.** When refreshing multiple packs (bare `pack update` or `--all`), up to three packs update in parallel — bounded to stay within typical git-host connection limits. Per-pack stdout is buffered and flushed in input order after the parallel phase, and bundled profile and registry installs still run sequentially, so the transcript stays coherent and last-writer-wins semantics for shared profile IDs are deterministic. Concurrent clones for the same origin URL (for example, several packs installed from the same monorepo) serialize on the local bare-clone cache at `~/.config/aipack/.cache/git/` so only one remote fetch runs per origin. Ctrl-C mid-update stops dispatching new packs while letting in-flight workers finish on their own cancellation-aware operations.

### pack versions

Lists available semver tags for a pack from its remote git origin. Resolves the origin from the lockfile (if installed) or the registry (if not installed). Only tags that parse as valid semver are shown. The currently installed version is marked with a star.

```bash
aipack pack versions my-team-pack
aipack pack versions my-team-pack --json
```

### pack delete

Deletes an installed pack from disk, removes it from all profiles, clears its lockfile and ledger entries, removes clean rendered harness files that aipack can safely attribute to the pack, and strips pack-managed keys from shared settings files. Files with user modifications, unknown ledger paths, and shared settings user keys are preserved and left unmanaged. Use `--keep-rendered` to stop managing the pack while leaving all rendered harness files in place as unmanaged content.

```bash
aipack pack delete my-pack
aipack pack delete my-pack --keep-rendered
aipack pack delete my-pack --dry-run
aipack pack delete my-pack --json
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

Read-only validation of a single pack source tree. Checks pack structure, manifest inventory, bundled profile names, frontmatter correctness, extras shape, and cross-reference consistency without installing, syncing, or scanning authored content bodies. Exit code 0 when there are no error-severity findings, 1 otherwise.

Each finding includes a severity (`error` or `warning`), a category (`frontmatter`, `policy`, `consistency`, or `inventory`), the file path, and a message. Warnings are reported but do not make the command fail. In human output, findings are printed as `- [severity] path: message`. For the JSON output shape, see the [CLI Specification](./cli-spec.md#aipack-pack-validate).

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

### profile include / profile exclude

Toggles exact content IDs in a profile without hand-editing YAML. Bare IDs are matched across the profile's enabled pack entries for rules, agents, workflows, skills, hooks, plugins, and MCP servers. If a name appears in more than one place, rerun with `--kind` or `--pack` to choose the target. If the only match is in a disabled pack entry, enable the pack first with `aipack pack enable <pack> --profile <profile>`. MCP support is server-level only; keep per-tool allowlists in profile YAML or the TUI tool picker.

```bash
aipack profile include jira
aipack profile exclude anti-slop
aipack profile include datetime-injector --kind hook --pack team-pack
aipack profile exclude jira --kind mcp
```

### profile refs

Reports the detailed `{params.*}` and `{env:*}` reference data behind `aipack setup`. Use `setup` for first-time remediation and `profile refs --json` when scripts or diagnostics need the full reference inventory. Param refs are marked `set`, `defaulted`, or `missing`; env refs are marked `dotenv`, `env`, `defaulted`, or `missing` depending on whether the value comes from the config directory's `.env` file, the process environment, an inline default, or neither.

```bash
aipack profile refs
aipack profile refs production
aipack profile refs production --json
```

### profile set-param / profile unset-param

Compatibility aliases for editing the `params` map in a profile without hand-editing YAML. Prefer `aipack config params` for new usage. Machine-local secrets should still use `{env:*}` plus `.env` or the process environment.

```bash
aipack profile set-param production tracker_url https://tracker.example.com
aipack profile unset-param production tracker_url
```

## Collections

Collections are registry-defined install recipes for multiple packs. Use them for onboarding sets such as "install the team starter packs." Profiles still decide which installed packs are active for a harness context.

Collections come from fetched registry sources. The merged registry view resolves collection names in source order, the same as pack names.

### collection list

Lists available collections from the merged registry.

```bash
aipack collection list
aipack collection list --registry /path/to/registry.yaml
aipack collection list --json
```

### collection show

Shows one collection's ordered pack recipe, including per-pack refs and bundled-content choices.

```bash
aipack collection show team-dev
aipack collection show team-dev --json
```

### collection install

Installs every pack referenced by the collection. By default, packs are installed to disk but not added to a profile. Use `--add` to add each installed pack to the active profile, or `--add --profile <name>` to target another profile. Use `-w all` or another `--with` value to override bundled-content choices for every pack in the collection.

If the named collection is not found in the cached registry view, `collection install` fetches configured/default registries once and retries.

```bash
aipack collection install team-dev
aipack collection install team-dev --add
aipack collection install team-dev --add -w all
```

## Registry

The registry maps pack names to source repositories and collection names to ordered pack install recipes. The unified view merges all cached sources in `~/.config/aipack/registries/` in `registry_sources` order from sync-config (first-seen wins for pack and collection name conflicts). Sources include remote registries fetched via `registry fetch` and embedded entries bundled inside installed packs.

### registry fetch

Fetches remote registries and caches them locally. Each source is cached as a separate file and saved to `registry_sources` in sync-config for future fetches.

With an explicit URL, fetches that single source. Without a URL, fetches all configured sources plus any compiled-in default sources. Public builds include the `shrug-labs/packs` registry; distributor builds may prepend one additional default registry.

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

`--deep` shallow-clones each registered pack and indexes resource-level metadata for search. Indexed kinds: rules, agents, workflows, skills, hooks, prompts, plugin descriptors, and MCP server inventories. Already-installed packs are skipped because the installed pack source remains authoritative. Deep-indexed resources show up under `aipack search --status registered` so users can search a registered pack's content before deciding to install.

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

### registry validate

Validates a registry YAML file without fetching, installing, or merging it. The command reports all semantic validation errors and supports JSON output for CI.

```bash
aipack registry validate ./registry.yaml
aipack registry validate ./registry.yaml --json
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

If the resource name is unique in the active profile, the type can be omitted. If multiple active resources share the same name, `trace` prints the explicit commands to disambiguate.

Valid resource types: `rule`, `agent`, `workflow`, `skill`, `hook`, `plugin`, `mcp`.

The output shows the source pack, source file path, and each destination with its harness, file path, and on-disk state (`create`, `identical`, `managed`, `conflict`, `untracked`, or `error`). Use `--harness` to filter output to a single harness. Destinations where the resource is composited into a multi-resource file (e.g. Codex flattening rules into `AGENTS.override.md`) are flagged as embedded separately from the state.

```bash
# Trace a rule named "anti-slop"
aipack trace anti-slop
aipack trace rule anti-slop

# Trace a skill named "oncall"
aipack trace skill oncall --scope global

# Trace an MCP server named "issue-tracker"
aipack trace mcp issue-tracker

# Filter to a single harness
aipack trace rule anti-slop --harness claudecode

# JSON output for tooling
aipack trace rule anti-slop --json
```

### search

Opens the manage TUI on the Search tab for interactive search and install flows. Search terms plus `--kind`, `--category`, `--status`, `--installed`, and `--available` are carried into the TUI, so `aipack search deploy --kind workflow` opens Search with that query ready. Advanced CLI-only filters (`--tags`, `--role`, `--pack`) still use the text search output. Use `--json` for the machine-readable CLI search output.

Full-text search uses FTS5 with BM25 ranking across resource names, descriptions, and body text. The SQLite index is built automatically during `registry fetch --deep`, pack install/update, and `pack inspect`. Search reconciles installed status against `aipack.lock` and installed pack directories before applying installed/status filters, so stale registry rows do not make installed packs appear available.

Filters: `--tags` (comma-separated), `--role`, `--kind` (rule/skill/workflow/agent/prompt/plugin/mcp/pack), `--category` (ops/dev/infra/governance/meta), `--pack`, `--status installed|registered|inspected`, `--installed`, `--available`. `--available` is retained as a compatibility alias for uninstalled results; use `--status registered` for registry/deep-index content and `--status inspected` for pack previews created by `pack inspect`.

```bash
aipack search 5xx triage
aipack search --category ops
aipack search --tags observability --role oncall-operator
aipack search deploy --kind workflow --category infra
aipack search 5xx --installed
aipack search --status registered
aipack search --status inspected
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

Tabs: Profiles, Packs, Sync, Save, Search, Config.

Mouse clicks work for tab content, overlay actions, and modal selections when the terminal reports mouse events; action menu highlights follow mouse hover, mouse-wheel scrolling works in the Profiles content tree and Packs content list, and large sync-plan diff overlays retain their bottom border above the help bar. Every interaction remains reachable from the keyboard.

Key bindings: `tab` switch tabs, `j/k` navigate, `enter` expand, `space` toggle, `l` list profiles, `n` new profile, `d` delete, `D` duplicate, `a` activate, `p` add pack, `r` remove pack, `s` sync, `t` MCP tool picker (on an MCP entry in the profiles tree), `.` context actions, `esc` quit or back out of the active overlay/subscreen. Double-clicking a pack row in the Packs tab opens the same action menu, where uninstalled packs can be inspected before install and installed packs can dry-run update with file-level changes before mutating local state. Config settings live on the Config tab; profile content actions stay on the Profiles tab.

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

For rendering behavior, rendered content identity, write targets, global config-root environment variables, MCP configuration differences, and harness-specific notes, see the [Harness Reference](./harness-reference.md). Codex skills and promoted workflows render under Codex-owned skill directories (`.codex/skills/` for project scope and `~/.codex/skills/` for default global scope).
