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

Reads and sets scalar defaults in `sync-config.yaml` from the CLI. `get` without a key lists all defaults. Supported keys are `profile`, `harnesses`, `scope`, `collision_strategy`, `auto_sync`, and `namespaced`; hyphenated names and `defaults.<name>` are accepted as aliases.

```bash
aipack config defaults get
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
| `config_mutation` | critical | With `--fix`, reports refusal when another mutation or orphaned native subprocess still holds the configuration lock |
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

`doctor --fix` recovers interrupted plugin operations before repairs and preserves imported component inventories. If another operation or installer is still running, wait for it to exit and retry.

For imported plugins, `doctor` shows available components, profile selections, delivery status and setup issues. `--harness` and `--scope` use the sync-config defaults; project checks use the current directory. Manage's Sync tab shows the same summary.

```bash
aipack doctor --profile developer --harness codex,cline --scope global
aipack doctor --profile developer --harness cline --scope project --json
```

`doctor OK` means the configuration checks passed. Plugin execution and login are not tested by this command.

### mcp inspect-tools

Connects to MCP servers and queries their live tool inventories via the MCP protocol (`initialize` → `tools/list`). Compares discovered tools against the static `available_tools` in each pack's `mcp/<server>.json` inventory, reporting additions and removals.

Without arguments, lists every MCP server found across installed packs with its pack, transport, and current inventory count. Pass a server name to inspect it. Use `--all` to inspect every server.

Server names are looked up across all installed packs. When the same name appears in multiple packs, specify `pack/server` to disambiguate. The `--profile` flag selects which profile supplies `{params.*}` values for server commands; the active profile is used by default. All three MCP transports are probed: stdio (subprocess), streamable-http (POST with `application/json` or `text/event-stream` responses), and the legacy HTTP+SSE transport (GET stream + POST). HTTP transports include the status code and response body snippet in error output so auth failures are self-describing.

With `--save`, the discovered tool list replaces `available_tools` in an ordinary pack's inventory JSON. All other metadata (`command`, `env`, `links`, `auth`, `notes`) is preserved. Imported plugin inventories save to the local probe cache; their source files stay unchanged. The TUI tool picker uses the same declarations and cache. Native launcher fields or environment expansion that the probe cannot reproduce are reported explicitly. Combine with `--dry-run` to preview the writes without touching disk.

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

### Install a plugin once, use it across harnesses

Import a marketplace, install a plugin by name, and select its components through a profile. A Codex import can supply supported skills, stdio MCP servers and command hooks to all four harnesses. Update the import once, then sync each target.

This example selects one skill from the upstream Superpowers Codex plugin:

```bash
# Register the Codex marketplace.
aipack registry fetch https://github.com/obra/superpowers.git

# Install into a named profile with content initially unselected.
aipack profile create debugging
aipack pack install superpowers --add --quiet --profile debugging
aipack pack show superpowers
aipack profile include systematic-debugging --kind skill \
  --pack superpowers --profile debugging

# Preview and apply the selection to all four harnesses.
aipack sync --profile debugging --harness codex,claudecode,opencode,cline --dry-run
aipack sync --profile debugging --harness codex,claudecode,opencode,cline
```

Reload each target client after sync. Credentials and executable approvals belong to each host. Use separate profiles when targets need different components.

Change the selection or update the source, then preview and sync the affected targets again:

```bash
aipack profile exclude systematic-debugging --kind skill \
  --pack superpowers --profile debugging
aipack pack update superpowers --dry-run
aipack pack update superpowers
aipack sync --profile debugging --harness codex,claudecode,opencode,cline --dry-run
aipack sync --profile debugging --harness codex,claudecode,opencode,cline
aipack trace skill systematic-debugging --pack superpowers \
  --profile debugging --harness codex --json
```

Profile selections survive updates. `profile include` restores excluded content; `aipack manage` provides the same controls interactively. Trace shows the original source and why a component is selected or blocked.

#### Delivery behavior

Compatible skills and stdio MCP servers use ordinary pack delivery on Claude Code, OpenCode and Cline. Claude/OpenCode use plugin delivery where supported native features require it. Source assets and runtime data remain available across selection changes and updates. Ordinary delivery follows the existing [collision and override rules](profiles.md#layering-multiple-packs).

`pack inspect` and `pack show` report component support for each target. Sync checks every requested target before writing; exclude incompatible components or choose a supported target. Unavailable hook events produce warnings automatically. See [imported plugin support](#imported-plugin-support) for limits, authentication and ownership.

### Git authentication

Git-backed commands use your normal Git/SSH authentication when stdin and stderr are terminals. Native prompts remain available when stdout is redirected. Prompt-capable Git operations within a command run one at a time.

JSON output, background operations, and commands without terminal stdin and stderr disable Git authentication prompts. Use `--non-interactive` to disable these prompts explicitly, including in terminal-based automation. This flag does not answer other AIPack confirmations.

AIPack does not store passphrases or modify your SSH configuration. Unattended use requires credentials that work without a prompt, such as an unlocked SSH agent. External credential agents may still request hardware interaction. Separate required Git connections may each need authentication.

Packs are portable, versioned bundles of AI agent configuration installed under `~/.config/aipack/packs/<name>/`. See the [Pack Format Specification](./pack-format.md) for the format itself.

### pack create

Scaffolds a new pack directory with `pack.json` manifest and standard subdirectories (`rules/`, `agents/`, `workflows/`, `skills/`, `hooks/`, `mcp/`, `configs/`), then records it so it is immediately available for profiles and sync. `default` is reserved for the user's local default profile and is not a valid pack name.

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

`--dry-run` previews per-pack outcomes and file-level content changes without touching installed packs, bundled content, profiles, registries, the lockfile, or the local git cache. Archive checks may refresh disposable validator and semantic-result observations under `.cache/archive-observations/` so repeated startup checks can use conditional requests. Use dry-run before a real update to check the commit-hash transition, changed files, and any new bundled categories that would land.

For startup hooks and automation, `pack update --all --dry-run --json` emits versioned check output and suppresses progress text from stdout. `--json` requires `--dry-run`. Statuses describe the check (`update-available`, `up-to-date`, `skipped`, or `error`), and bundled availability is separate from persisted preferences. Dry-run never adopts registry coordinates: the installed lockfile origin remains authoritative.

When an update brings bundled content categories that were never reviewed, they're labeled new. Previously declined categories are reported separately instead of repeatedly appearing new. Use `-w` to approve specific categories or `-w all` to accept everything.

With `defaults.auto_sync: true`, successful updates automatically sync only when at least one updated pack is enabled in the active profile. `--dry-run`, failed updates, and updates for inactive-profile-only packs do not auto-sync.

```bash
aipack pack update                         # update all installed packs
aipack pack update my-pack                 # update one specific pack
aipack pack update --all                   # explicit alias for the bare form (scripts)
aipack pack update --all --dry-run --json  # structured check without installed/configured state changes
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

Shared OpenCode and Cline hook wrappers retain the other packs' handlers when a pack is deleted. Deletion checks recorded destinations before writes and restores shared hook changes if persistence fails. If a shared wrapper containing that pack has local edits, preserve or revert those edits before deletion.

```bash
aipack pack delete my-pack
aipack pack delete my-pack --keep-rendered
aipack pack delete my-pack --dry-run
aipack pack delete my-pack --json
```

### pack rename

Renames an installed pack across all configuration: the pack directory, `pack.json` manifest, `sync-config.yaml`, all profiles, and all ledger files.

Imported packs also update managed skill asset paths, hook commands and MCP launchers to the renamed source, including recorded custom config roots whose environment variables are no longer set. Preserve or revert local edits to those references before renaming.

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

Toggles exact content IDs in a profile without hand-editing YAML. Bare IDs are matched across the profile's enabled pack entries for rules, agents, workflows, skills, hooks, and MCP servers. If a name appears in more than one place, rerun with `--kind` or `--pack` to choose the target. If the only match is in a disabled pack entry, enable the pack first with `aipack pack enable <pack> --profile <profile>`. MCP support is server-level only; keep per-tool allowlists in profile YAML or the TUI tool picker.

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

The registry maps pack names to source repositories and collection names to ordered pack install recipes. The unified view merges all cached sources in `~/.config/aipack/registries/` in `registry_sources` order from sync-config (first-seen wins for pack and collection name conflicts). Registry listing exposes the winning source and any lower-priority sources shadowed for the same name. Sources include remote registries fetched via `registry fetch` and embedded entries bundled inside installed packs; synthetic `embedded://` sources are local materializations and are skipped by `registry fetch`.

### registry fetch

Fetches remote registries and caches them locally. Each source is cached as a separate file and saved to `registry_sources` in sync-config for future fetches.

With an explicit URL, fetches that single source. Without a URL, fetches all configured sources plus any compiled-in default sources. Public builds include the `shrug-labs/packs` registry; distributor builds may prepend one additional default registry.

Git detection: URL ending in `.git`, `git@host:path`, `ssh://`, an explicit `--ref`, or a repository-relative `--path` selects Git acquisition. The default is the remote's default branch and `path=registry.yaml`. Refs accept branches, tags and commit hashes. A standalone HTTP catalog URL uses HTTP GET. Local directories accept `--path` to identify their catalog.

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

`--deep` shallow-clones each registered pack and indexes resource-level metadata for search. Indexed kinds: rules, agents, workflows, skills, hooks, prompts, imported plugin identities, and MCP server inventories. Already-installed packs are skipped because the installed pack source remains authoritative. Deep-indexed resources show up under `aipack search --status registered` so users can search a registered pack's content before deciding to install.

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

Shows ecosystem status: active profile, enabled profile packs with content inventories, disabled profile packs, and enabled-content totals. Disabled packs appear in a separate section so installed-but-inactive content is visible without changing profile behavior.

```bash
aipack status
aipack status --profile production
aipack status --profile-path /path/to/profile.yaml
aipack status --json
```

### trace

Traces a single resource through the sync pipeline, showing where it comes from (pack source) and where it would land in each harness location. Useful for debugging why a rule isn't showing up or which harness file contains a given resource.

Trace reports source, selection and delivery state. It does not record execution or count usage.

If the resource name is unique in the active profile, the type can be omitted. If the active profile does not contain the resource, `trace` checks disabled profile packs, excluded profile content, and installed packs that are not in the profile. Inactive resources show no destinations and include exact next commands such as `aipack pack enable`, `aipack profile include`, `aipack pack add`, then `aipack sync`. If multiple active or inactive resources share the same name, `trace` prints the explicit commands to disambiguate.

Valid resource types: `rule`, `agent`, `workflow`, `skill`, `hook`, `plugin`, `mcp`.

JSON output always includes the profile state; human output prints it for inactive resources. The output also shows the source pack, source file path, and each destination with its harness, file path, and on-disk state (`create`, `identical`, `managed`, `conflict`, `untracked`, or `error`). Use `--harness` to filter output to a single harness. Destinations where the resource is composited into a multi-resource file (e.g. Codex flattening rules into `AGENTS.override.md`) are flagged as embedded separately from the state.

```bash
# Trace a rule named "anti-slop"
aipack trace anti-slop
aipack trace rule anti-slop

# Trace a skill named "oncall"
aipack trace skill oncall --scope global

# Trace an MCP server named "issue-tracker"
aipack trace mcp issue-tracker

# Resolve an observed OpenCode tool to its original MCP server
aipack trace mcp issue_tracker_search --tool --harness opencode --json

# Filter to a single harness
aipack trace rule anti-slop --harness claudecode

# JSON output for tooling
aipack trace rule anti-slop --json
```

MCP tool lookup requires one explicit target: Claude Code, OpenCode or Codex. It reports the original server ID and source pack. If the tool name matches multiple servers, trace reports the ambiguity. Destination states show whether the content is planned or already delivered.

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

### Imported plugin support

| Source | Native target | Other targets |
| --- | --- | --- |
| Claude marketplace | Claude Code | Unsupported |
| Codex legacy marketplace | Codex | Supported skills, stdio MCP servers and synchronous command hooks on Claude Code, OpenCode and Cline |
| Agent Plugins v1 | Codex | Supported skills and stdio MCP servers on Claude Code, OpenCode and Cline |

Use the component IDs shown by `pack show` for profile selections. The source format determines those IDs, and one selector can cover several source files. Codex 0.159.2 does not activate Agent Plugins v1 hooks, commands or apps.

Cross-harness MCP delivery supports a subset of stdio declarations. Remote transports and unsupported fields require the plugin's native target or exclusion from the profile. Targets use their own startup budget by default; [timeout policies](profiles.md#imported-mcp-startup-timeouts) can adjust separate startup deadlines. Native plugin installation and cross-harness stdio MCP delivery are currently unsupported on Windows.

Codex enforces its source and installation policies. An explicit source-policy refusal includes the generated local marketplace path and administrator allow rule.

#### Authentication

Use the destination assistant's normal login flow for protected services and apps. Sync delivers their configuration; authentication remains with that assistant.

#### Ownership and scopes

Existing native installations remain managed by their assistant. If one conflicts with an import, sync identifies it in the error. Remove the conflicting installation or registration through that assistant before syncing. Renaming a pack keeps its plugin identity.

For Codex, remove each plugin you are moving, then its marketplace:

```bash
codex plugin remove example-plugin@example-marketplace
codex plugin marketplace remove example-marketplace
```

Replace both names with the existing native identities before installing through AIPack.

Shared native installations and overlapping OpenCode scopes use matching sources and component selections. If selections conflict, sync the other scope with the plugin disabled or clean that scope first. Profile edits take effect at sync. Independent installations can use separate AIPack and assistant configuration directories.

#### Command hooks

Imported Codex command hooks use the profile's existing `hooks` selectors. Commands retain their assets, root/data variables and timeouts. OpenCode and Cline stop the shell and its subprocesses when a hook times out. Claude supplies native event input; Codex-only fields such as `turn_id` and per-handler context spilling are unavailable.

OpenCode and Cline map SessionStart, UserPromptSubmit, PreToolUse, PostToolUse and PreCompact to their hook adapters, forwarding supported context and blocking output. Tool names and input come from the destination client. Missing events, non-command handlers, inactive asynchronous handlers and trigger-filtered compaction groups produce warnings. Startup/resume behavior and input fields can differ from Codex; scripts retain their external prerequisites. Cline also requires native hook enablement.

OpenCode and Cline can render imported hooks for Windows. Cline requires Node on `PATH`; commands run in the default shell and retain any Bash or PowerShell dependency. Native Windows execution has not been tested.

Cline's PowerShell wrappers limit each handler's captured stdout to 65,536 characters and stderr to 4,096 characters. Oversized output produces a diagnostic and the handler's JSON result is discarded.

#### Updates and recovery

Updates preserve profile selections and refresh marketplace catalogs. If a selected component disappears, update its selector before syncing; the previous delivery stays active until sync succeeds. Version and commit pins retain their recorded source when the importer is updated. If the catalog changes the plugin's repository or package source, reinstall to use that source.

Customize imports through profiles. Updates protect locally edited imported files and permissions. Save those edits separately and restore the installed source before updating. If the recorded baseline is missing, back up the pack and selections, then delete and reinstall it.

Failed plugin operations restore prior delivery. After an interruption, retry the operation to recover and finish; sync recovers replacements before loading the profile, and dry-run reports pending recovery. If an installer is still running, wait for it to exit. `doctor --fix` can recover interrupted operations and clean staging files. Runtime data survives updates, clean and pack deletion.

Claude sync reports incomplete Node dependency setup and retries it on a later sync with lifecycle scripts disabled. For additional setup required by the plugin author, use the delivered package. Reconnect a repaired server through the assistant.

#### Marketplace sources

`registry fetch <url> --format claude|codex-legacy|agent-plugins` selects a marketplace format when the catalog cannot identify it. The choice survives refreshes; `--format auto` clears it. Codex uses a root Agent Plugins manifest in preference to a legacy manifest. Claude catalogs can describe packages without a plugin manifest.

Local and Git catalogs use the usual pack installation methods and Git credentials. Local catalogs are saved with absolute source paths so refresh works from any directory. Plugin repositories do not need an AIPack manifest. Use a repository-relative `--path` for catalogs in Git URLs without `.git`, and `git-subdir` for Claude plugin subdirectories. Codex relative Git sources require a local or Git-backed parent catalog.

npm sources use normal npm credentials and acquire packages without lifecycle scripts or dependency installation, including on Windows. `pack versions` lists registry package versions; `--ref` selects a version, range or tag, and `--ref latest` resumes tracking. The package version may differ from the plugin version. Claude imports also accept npm aliases and tarball URLs; tarballs refresh their URL and do not support registry version selectors.

If shared Claude catalog metadata changes, update all affected packs before syncing. Conflicting snapshots or selections that would expose excluded content are refused.
