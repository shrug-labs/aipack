# Changelog

All notable user-facing changes to `aipack` will be documented in this file.

The format is based on Keep a Changelog, and releases use semantic versioning tags.

## [Unreleased]

## [0.33.1]

### Fixed

- **JSON output detects incomplete writes.** Machine-readable commands preserve their existing indented, escaped output while returning an error when the destination accepts only part of the payload.

## [0.33.0] - 2026-07-23

### Added

- **Bulk pack update checks support machine-readable output.** `aipack pack update --all --dry-run --json` emits versioned results with per-pack statuses, summary counts, bundled-content availability, and registry source differences.
- **Registry listings show source precedence.** `aipack registry list` identifies the source that supplied each pack and any lower-priority definitions it shadowed.

### Changed

- **Pack updates keep using the source recorded at install time.** A registry entry that later points elsewhere is reported but does not silently redirect an installed pack.
- **Declined bundled content is reported separately.** Previously declined profiles, registries, and extras are no longer presented as newly available.

### Fixed

- **Archive updates no longer report repackaged-but-unchanged packs as updates.** ZIP and tar archives are compared by extracted pack content. HTTP validators and local file hashes avoid redundant work without determining the update result.
- **Registry refresh skips embedded entries.** `aipack registry fetch` no longer tries to fetch synthetic `embedded://` sources.

## [0.32.3]

### Fixed

- **Claude Code settings documentation now distinguishes project and global targets.** Project sync writes settings and hooks to `.claude/settings.local.json`, while global sync writes them to `~/.claude/settings.json`, matching the v0.32.2 global hook and permission behavior.

## [0.32.2]

### Fixed

- **Claude Code hooks and permissions now load when synced at global scope.** They were written to `~/.claude/settings.local.json`, which Claude Code reads only at project scope, so globally synced hooks and managed permissions never took effect. They now sync to `~/.claude/settings.json` — the user-scope file Claude Code reads — three-way merged with `enabledPlugins` and your existing settings.

## [0.32.1]

### Changed

- **`aipack config defaults get` without a key now lists all sync defaults.** The keyed form still prints the selected default value.

## [0.32.0]

### Added

- **`aipack status` and `aipack trace` now diagnose inactive profile content.** Status lists disabled profile packs separately while keeping totals scoped to enabled content, and status JSON adds `disabled_packs`. Trace now reports inactive matches from disabled packs, excluded content, and installed packs outside the profile with `profile_state`, blockers, and exact remediation commands.

## [0.31.1]

### Added

- **The manage TUI Packs tab now browses a pack's Settings.** Harness config files shipped under a pack's `configs/` directory appear as a Settings section in the content browser alongside rules, skills, MCP servers, and the other content vectors, with file preview and size. `aipack pack show --json` gains a `settings` array listing them as `<harness>/<file>` IDs.

### Fixed

- **Skill helper scripts keep executable permissions during sync.** Copied skill assets with source execute bits now render as executable files and resync repairs mode drift when the file content is unchanged.

## [0.31.0]

### Added

- **`aipack config params` manages profile-scoped `{params.*}` values.** `list`, `get`, `set`, and `unset` use the active profile by default and accept `--profile` for explicit targeting. Existing `profile set-param` and `profile unset-param` commands remain available as compatibility aliases, while `aipack setup` now prints `config params set` remediation commands.

## [0.30.1]

### Changed

- **`default` is now protected for pack-provided profiles.** Pack installs skip `profiles/default.yaml` instead of copying it over the user's local default profile, emit a warning, and continue installing the rest of the pack. `aipack pack validate` now reports an error when a pack declares a bundled profile named `default`, and pack names of `default` are rejected so `pack create default` cannot scaffold a reserved bundled profile.

## [0.30.0]

### Changed

- **Codex skills now render under Codex-owned directories.** Project Codex sync writes skills and promoted workflows to `.codex/skills/`, and default global sync writes them to `~/.codex/skills/`; `CODEX_HOME` sync continues to write directly under `$CODEX_HOME/skills/`. Cline remains the owner of `.agents/skills/`, so Codex no longer shares that render path.
- **`aipack sync` now reports each target harness independently.** Existing multi-harness defaults and `--harness all` still run the resolved target set, but each harness now gets its own plan, apply, and ledger. Human-readable sync output reports one `sync OK [<harness>]` line per completed harness, and dry-run/apply output labels file changes as `[<harness>] /full/path`.
- **`aipack sync --json` now emits per-harness results.** Sync JSON output is now an array of result objects, including for single-harness sync. Content counts are still profile-level, while `settings` and `warnings` describe the individual harness result. A multi-harness failure emits no partial JSON array.
- **The manage TUI previews the same target set that default sync will run.** Active-profile sync prompts and pending-change previews aggregate all configured harnesses. Pack updates that affect the active profile also trigger a fresh sync-status check, even if an older check is already loading.
- **The Packs screen shows clearer source and progress state.** Remote clone/archive/tarball installs show their origin URL as `Source` instead of the installed cache path, active installs show their current phase in the list/details/status areas, and remote install display names strip a trailing `.git`.

### Fixed

- **Codex upgrade cleanup protects active Cline skills.** During the next Codex sync, ledger-managed stale Codex entries under the old `.agents/skills/` root are cleaned up without treating that shared directory as a current Codex write root. Active Cline-owned entries are protected; inactive ledger-managed entries under the legacy root may be pruned so Codex stops discovering duplicate skills. For unattended upgrades, run `aipack sync --harness codex --yes` to auto-confirm deletion of managed stale entries.
- **Legacy ledger migration now converges.** Entries migrated into current per-harness ledgers are pruned from old combined/project ledgers, so repeated syncs do not replay already-migrated stale entries.
- **Legacy shared-root migration no longer lets Codex claim Cline-owned paths.** When migrating a legacy combined ledger, each tracked file is attributed to the harness that currently writes to its location. A Codex sync does not claim `.agents/skills/` entries as Codex-owned.
- **Pack deletion keeps registered packs discoverable.** Deleting an installed pack that still exists in a registry now restores its search index status to `registered` instead of dropping it from search results. Pack deletion also recognizes Codex rendered skills in `.codex/skills/` and `$CODEX_HOME/skills/`.

## [0.29.2]

### Fixed

- **Compiled additional default registry sources now replace stale configured sources with the same name.** Distributor builds that move their extra default registry to a new URL or path no longer leave the old source ahead of the current default after bare `aipack registry fetch`.
- **Symlinked content roots now sync correctly.** When the content path being rendered is itself a directory symlink, aipack resolves the root and walks the target directory so nested files are planned and written normally.

## [0.29.1]

### Fixed

- **JSON output now preserves string content through the streaming encoder.** Machine-readable CLI output keeps the same indented shape while escaping indexed search text and other string values as data.

## [0.29.0]

### Added

- **Portable hooks now render across all supported harnesses.** Claude Code merges hook groups into `settings.local.json`, OpenCode emits an auto-discovered `plugins/aipack-hooks.js` server plugin, Codex writes `.codex/hooks.json` plus trust state, and Cline emits generated wrappers in its hooks directory. `compact.before` is now part of the portable hook event set; hook matchers are regular expressions; handlers can provide `command_windows` for Windows-specific commands.

### Fixed

- **Global sync now respects harness config-root environment variables.** Codex global sync honors `CODEX_HOME`, OpenCode honors `OPENCODE_CONFIG_DIR`, and Cline honors `CLINE_DIR` / `CLINE_DATA_DIR` instead of always writing to the default home-based locations. Cline global hook wrappers remain rooted in `~/Documents/Cline/Hooks/`, matching Cline's native hook discovery.
- **Settings sync now preserves local scalar values on collision.** When a pack ships scalar settings such as a Codex `profile`, sync updates the value only if the local file still contains the previous managed value; first-time collisions and local edits stay under local control and are reported as conflicts.
- **Git URL installs with explicit content paths no longer require a root `pack.json`.** Commands such as `aipack pack install --url https://github.com/org/repo --skills path/to/skills --name team-skills` now clone and extract the requested content path directly instead of probing `HEAD/pack.json` first.

## [0.28.2]

### Changed

- **`pack validate` now focuses on pack shape instead of content scanning.** Validation still checks manifests, inventory, frontmatter, extras, and cross-references, but no longer flags authored body text, `.env` files, SSH key text, AWS-looking strings, or OCID-like identifiers as validation findings.

### Fixed

- **Git installs now resolve safe directory symlinks inside pack content.** Subpath packs can expose shared skill directories or declared extra directories via repo-local symlinks; installs copy the resolved contents into the installed pack instead of failing with `symlink to directory not allowed`.
- **Removing managed MCP servers now preserves user-added servers in shared config files.** Sync, clean, and save strip only ledger-tracked MCP entries instead of clearing the whole MCP server map.
- **Settings and stale-file handling now preserve user data more reliably.** Three-way merges keep non-string user array entries and emit empty arrays as `[]` instead of `null`.
- **Dry-run summaries now count settings and MCP merges as file operations.** Settings-only and MCP-only sync plans no longer appear as zero-op dry runs when they would modify files.
- **`pack validate` now validates nested authored content at its discovered path.** Agents, workflows, and skills organized in subdirectories no longer produce false frontmatter read errors from their canonical leaf paths.

## [0.28.1]

### Fixed

- **Repo-relative content-path CLI installs and inspections now stay relative.** `pack install --url ... --skills skills` and `pack inspect --url ... --skills skills` no longer expand content paths against the current working directory before extraction.
- **Numeric-looking short commit hashes now resolve as commit pins.** Seven-character all-digit short SHAs such as `1366620` are no longer misclassified as partial semver versions.

## [0.28.0]

### Added

- **Registry collections install groups of packs.** Registries can define `collections:` recipes, and `aipack collection list/show/install` can inspect or install them. `aipack collection install <name> --add -w all` installs the ordered pack set, adds it to the target profile, and applies bundled-content choices across the collection.
- **`pack install` accepts multiple registry pack names.** Commands such as `aipack pack install essentials aipack-core memory --add -w all` install each named registry pack with shared profile and bundled-content flags. Batch installs are registry-name only; path, URL, archive, and content-path installs remain single-source.
- **Packs can ship portable hooks.** Hook descriptors live in `hooks/<id>/HOOK.yaml`, participate in profile selection and pack content paths, and can reference `{hook:root}`, `{pack:root}`, `{env:*}`, and `{params.*}` from command handlers. Codex renders supported lifecycle events to `.codex/hooks.json` and syncs the matching trust state in `config.toml`; profiles can disable hook content with `hooks.enabled: false`.
- **Profiles can include or exclude content by exact ID from the CLI.** `aipack profile include <id>` and `aipack profile exclude <id>` search enabled profile packs across rules, agents, workflows, skills, hooks, plugins, and MCP servers. Use `--kind` or `--pack` to resolve ambiguity; MCP is toggled at server level.

### Changed

- **Profile content selection is consistent across CLI, TUI, and sync.** Quiet packs, disabled packs, glob selectors, hook enablement, and MCP inclusive maps now resolve the same way when shown, synced, or written back.
- **Namespaced rendering can keep same-ID content from multiple packs active.** Set `defaults.namespaced: true` or run `aipack config defaults set namespaced true` to render harness-visible rules, skills, workflows/commands, agents, and hooks as `<id>__aipack__<pack>`. MCP servers, plugins, and settings still follow the configured collision behavior. Natural source names remain the default; the manage TUI Config tab exposes the toggle, sync reconciles renamed managed files when the setting changes, and save/capture writes back to source IDs.
- **`__aipack__` is reserved for rendered namespaced identities.** Pack names and agent, workflow, skill, and hook IDs cannot contain the sentinel; rule IDs still reserve literal `__` for nested-rule filename escaping.
- **Active-profile edits make the next sync step explicit.** When auto-sync is off, profile and pack edits that affect the active profile print or show the `aipack sync` hint after saving.

## [0.27.7]

### Changed

- **`aipack search` now opens the manage TUI Search tab by default on interactive terminals.** Search terms plus common filters seed the TUI, while `--json` and advanced filters such as `--tags`, `--role`, and `--pack` keep the machine-readable or text CLI output path.

### Fixed

- **Search now handles hyphenated terms literally.** Queries such as `alpha-beta` and `content-pack` no longer fail from SQLite FTS5 parsing the hyphen as query syntax.

## [0.27.6]

### Fixed

- **The manage TUI now accepts terminal paste in text inputs.** Bracketed paste works in modal text dialogs, Search query input, and the Save tab's inline new-pack field, with pasted line endings normalized for single-line controls.

## [0.27.5]

### Fixed

- **Codex settings diff previews now describe MCP permission merges accurately.** `sync --dry-run --verbose` and the manage TUI diff view label merged settings as "after merge", preserve runtime tool approvals outside the displayed hunks, and avoid showing TOML formatting-only changes as approval removals.

## [0.27.4]

### Fixed

- **Cline MCP sync now preserves literal shell operators and renders Streamable HTTP with Cline's native type.** Settings merges no longer rewrite `<`, `>`, or `&` as JSON unicode escapes, aipack `streamable-http` MCP servers now render as Cline `type: "streamableHttp"`, and Claude Code capture normalizes native `type: "http"` back to aipack `streamable-http`.
- **Profile MCP disable-only entries no longer drop sibling servers from normal packs.** A non-quiet profile map that only contains `enabled: false` MCP entries now keeps the pack's default server set and applies those entries as exclusions; maps with enabled servers or tool policy keep their existing inclusive-selection behavior.

## [0.27.3]

### Added

- **Distributors can compile in an additional default registry source.** Bare `aipack registry fetch` and `aipack init` still include the public `shrug-labs/packs` registry, and distribution builds can prepend one extra registry by setting `internal/config.AdditionalDefaultRegistryName` and `internal/config.AdditionalDefaultRegistryURL` via Go ldflags.
- **`aipack config defaults get/set` manages sync-config defaults from the CLI.** The command covers `profile`, `harnesses`, `scope`, `collision_strategy`, and `auto_sync`; profile changes reuse active-profile validation, while registry sources remain under `registry fetch/sources/delete`.
- **`defaults.auto_sync` can sync active-profile changes automatically.** When enabled in `sync-config.yaml` or with `aipack config defaults set auto_sync true`, successful pack/profile mutations that affect the active profile run a normal sync using the current sync defaults. The manage TUI saves active-profile edits immediately and debounces the automatic sync for seven seconds.

## [0.27.2]

### Fixed

- **Codex MCP cleanup now removes runtime tool approvals for servers removed by profile switches.** In-place `aipack sync` transitions no longer leave orphaned `[mcp_servers.<name>.tools.*]` tables that make Codex reject `config.toml` with an invalid transport error.

## [0.27.1]

### Fixed

- **Archive-shaped `pack install` and `pack inspect` inputs now infer archive mode.** `.zip`, `.tar`, `.tar.gz`, and `.tgz` URLs and local files no longer need `--archive`; positional archive URLs avoid the git-clone path, and local archive files are extracted through the archive installer instead of being rejected as non-`pack.json` files.

## [0.27.0]

### Added

- **The manage TUI now supports mouse clicks** for tabs, screen content, overlays, selectable rows, and modal actions in terminals that report mouse events.
- **The manage TUI adds a Config tab** for sync defaults, profile params, config-dir `.env` values, and harness defaults instead of hiding those workflows in profile and sync action menus. The tab's `.` action menu can add/edit/delete profile params and add/edit/delete `.env` entries directly.
- **The manage TUI can drill from Search results into pack details before install.** Search `enter` opens the matching pack and content row in the Packs tab, while installation stays in the Packs tab. Inspected/deep-indexed packs now show indexed metadata and content summaries in Packs even without a local install.
- **`aipack setup [profile]` shows a missing-param/env checklist** for first-time setup. Params print `profile set-param` commands, env vars print `config env set` commands, and empty profiles print a `pack install <source> --add` hint instead of erroring out.
- **Reference expansion supports `:-default` fallbacks and config-dir `.env` values.** `{params.workspace:-default}` falls back only when the profile omits `workspace`; `{env:API_BASE_URL:-https://api.example.com}` falls back only when `API_BASE_URL` is unset; `{env:VAR}` reads the active config directory's `.env` before the process environment.
- **`aipack config env` manages the config-dir `.env` file** that backs `{env:*}` resolution, including list/get/set/unset/path/edit commands with masked list output by default.
- **`aipack profile refs` reports profile `{params.*}` and `{env:*}` references**, and `profile set-param` / `profile unset-param` edit profile params with warnings before mutating pack-provided profiles.
- **`aipack pack inspect <source>` previews pack trust signals before install**, including warnings for declared MCP server definitions so external-tool access is visible before install/sync decisions. Discovered resources are indexed as `inspected` for `aipack search --status inspected`; inspected rows expire after 30 days, and `pack inspect --clear` removes them on demand.
- **`aipack pack import` imports one markdown file into a new or existing pack** as a skill, rule, or prompt, with generated frontmatter when needed.
- **`aipack pack delete` gained safer planning and removal flags.** The command removes clean rendered files and managed shared settings by default while preserving user-modified paths; `--dry-run`, `--json`, and `--keep-rendered` preview or stop managing the pack without deleting rendered content.
- **`aipack pack update --dry-run` previews updates with file-level content changes** without mutating disk or lockfile.
- **Plugin references are first-class pack content.** Packs can declare plugin descriptors, filter them through profile selectors, surface them in show/status/search/trace output, and sync references to Codex and Claude Code.
- **Static archive pack installs are supported.** Direct installs can pass `--archive`, registries can declare `method: archive`, and updates re-fetch and replace archive installs safely.
- **`aipack search --kind` accepts `mcp` and `plugin` kinds.**
- **`aipack registry validate <file>` validates registry YAML** with text or JSON output.

### Changed

- **The manage TUI now runs on Bubble Tea v2** and the screen code is split into dedicated tab and overlay packages behind the shared router path.
- **The manage TUI tab order is now Profiles, Packs, Sync, Save, Search, Config.** Sync shows the current plan and status together; Profiles keeps pack attribution visible in the content tree and status line.
- **Search exposes discovery status.** Search results include `status` (`installed`, `registered`, or `inspected`), and the CLI/TUI can filter by those states. Existing `--installed` and `--available` flags remain compatible.
- **Search reconciles installed state from the lockfile and installed pack directories** before filtering, so stale registry or deep-index rows do not make installed content appear available.
- **Initialization now prepares config-local `.env`.** `aipack init` and commands that ensure config exists create an empty `.env` placeholder beside `sync-config.yaml`; existing `.env` files are preserved.
- **Doctor and reference diagnostics now read config-local `.env`.** Missing MCP reference remediation points at `aipack config env set`, and env reference status distinguishes `.env` values from process environment values.
- **Registry lookup errors now include first-run recovery hints** for foundational public packs such as `aipack-core` and `essentials` when the default public registry has not been fetched.
- **`aipack trace` no longer requires a resource type for unique active resources.** `trace <name>` resolves exact active-profile matches across rules, agents, workflows, skills, plugins, and MCP servers.
- **`aipack registry fetch --deep` now indexes prompts, plugin descriptors, and MCP server inventories** alongside rules, agents, workflows, and skills.

### Fixed

- **`aipack pack delete` now clears deleted packs from installed search results** and preserves sibling MCP settings when removing a pack that contributed shared harness settings.
- **The manage TUI mouse/action surfaces are less surprising.**
  - Config row hit areas now align with the rendered rows.
  - Action menu highlights follow mouse hover.
  - The Profiles content tree and Packs content list support mouse-wheel scrolling.
  - Sync-plan diff overlays retain their bottom border above the help bar.
  - Unset/defaulted params no longer offer a no-op delete action.
  - Indexed-only pack content no longer offers local file actions before install.
  - Profiles rows now support pack checkbox clicks plus double-click profile/pack action menus.
  - Packs rows now double-click pack rows into the action menu.
  - Packs actions can preview installs and dry-run updates with file-level changes before mutating state.
  - The Sync tab's `.` menu exposes plan, sync, and sync-config actions.
- **Truncated styled text in the manage TUI no longer leaks ANSI styles into adjacent rows.**
- **Claude Code streamable HTTP MCP servers render with `type: "http"`.** `sse` remains unchanged.

## [0.26.0]

### Added

- **Harness settings templates expand `{env:*}`, `{params.*}`, and `{pack:root}` references.** Pack-provided files like `configs/codex/config.toml` can use machine-local paths; unresolved references fail sync. Codex and opencode now write base-only settings (claudecode already did); `--skip-settings` still suppresses them.

### Changed

- **Live progress for slow operations.** `pack update`, `pack install`, `mcp inspect-tools`, and `registry fetch` stream phase lines (cloning, extracting, probing, connected, listing tools, fetching, deep-indexing) to stdout. `pack update --all` suppresses phase lines so concurrent workers don't interleave; sync output is unchanged.
- **TUI live progress on the packs tab.** Pack updates show a per-row spinner, ✓/✗ glyph, batch status during `update --all`, and a `Checked` timestamp on the detail panel; pack installs show a status-bar phase spinner. Esc cancels in-flight operations. Bundled-content checklist Esc cancels the whole install/update; space toggles, enter confirms. MCP tool picker also gains a live probe spinner.

### Fixed

- **`aipack update` on Homebrew-managed binaries** points at `brew upgrade aipack` instead of failing with `creating temp file in /usr/local/bin: ... permission denied`.
- **`pack install` no longer prints a `Changes:` block or `Content unchanged.` line.** Re-installing with different `-w` flags was leaking bundled-content filter side effects as fake content drift; integrity is still recorded for `pack update`'s drift detection.

## [0.25.2]

### Added

- **`o` opens the focused file with the OS default app.** The manage TUI binds `o` alongside `e:edit` across browsing panels and the preview overlay, and adds an `Open file` entry to every context action menu (`.`) that already offered `Edit file` / `Edit manifest` / `Edit sync-config`. `o` invokes `open` on macOS, `xdg-open` on Linux, and `cmd /c start` on Windows; the launched app runs detached so the TUI does not suspend, and Start failures surface in the status bar. Customize what opens by setting OS-level file associations (Finder "Open With → Change All", `xdg-mime default`, Windows "Always use this app").

### Fixed

- **TUI file editing now has a Windows fallback and supports editor arguments.** The manage TUI previously fell back to `vi` everywhere and passed `$EDITOR` as a single executable name, so Windows users often saw nothing happen and values like `code --wait` failed. The editor launcher now falls back to `notepad.exe` on Windows, keeps `vi` on Unix-like systems, splits quoted editor commands into executable plus args, and reports a clear status-bar error when the editor cannot be launched.

## [0.25.1]

### Fixed

- **Codex native agent registrations now use absolute `config_file` paths.** `aipack sync` previously wrote `[agents.<name>] config_file = './agents/<name>.toml'` in `.codex/config.toml`. Some Codex clients deserialize the agents table without a base path and reject that relative path with `AbsolutePathBuf deserialized without a base path in agents`. Sync now registers the resolved `.codex/agents/<name>.toml` destination path instead.

## [0.25.0]

### Changed

- **`pack install -q` is now a pack property, not a per-add flag.** The lockfile records `install_quiet: true` at install time, and every subsequent profile-add of that pack (via `pack install --add`, `pack add`, registry auto-add, TUI) defaults to `quiet: true` without re-specifying the flag. Previously `-q` only affected the one profile add at install time; removing the pack from a profile and re-adding silently demoted it to non-quiet, and a `pack install --add` after a TUI wipe of the profile entry did the same. Quiet intent now persists across profile edits, removals, and cross-profile adds. An explicit `--no-quiet` on `pack install` or `pack add` overrides the lockfile hint when you genuinely want a quiet-installed pack to be non-quiet in one specific profile. `pack update` and re-install preserve the existing lockfile `install_quiet` unless a new explicit flag is passed — your quiet state survives version bumps.

### Added

- **`install_quiet` field on lockfile `InstalledPackMeta`.** Source of truth for a pack's quiet-by-nature state. `omitempty` so old lockfiles round-trip unchanged; packs installed before this release read as `install_quiet: false` and inherit the v0.24.x semantics until the next `pack install -q`.
- **`--no-quiet` flag on `pack install` and `pack add`.** Explicit override that forces the profile entry to non-quiet (and for `pack install`, stamps `install_quiet: false` on the lockfile). Mutually exclusive with `-q`.

## [0.24.1]

### Fixed

- **TUI MCP tool picker now advertises navigation, adds fast-nav keys, and keeps the counts footer on-screen.** The v0.23.0 picker over-budgeted its visible-row count by five rows — `contentStyle.Padding(1, 2)` (2) and the wrapping `"\n" + View() + "\n"` (2) weren't accounted for in the terminal-height reservation — so on long tool lists the cursor scrolled past the viewport and the `N off · N ask · N auto │ N/N enabled` counts footer clipped off the bottom. The reservation is now centralized in a `pickerMaxVisible()` helper that documents every row of chrome. Separately, the help bar listed only cycle/shortcut keys with no hint of how to scroll, so users reported they "couldn't scroll to the bottom" even though `j`/`k` worked — the bar now spells navigation out (`j/k:move  g/G:top/bot`) and the picker handles `PgDn`/`PgUp`/`Ctrl+F`/`Ctrl+B` (page by viewport) plus `g`/`G`/`Home`/`End` (jump to first/last).
- **TUI content tree now honors `quiet: true` for both content vectors and MCP servers.** `BuildContentTree` called the lower-level `ResolveCurrentVector`, which doesn't know about quiet — a quiet pack with `rules: include: null` resolved to "include everything" instead of the sync-time resolver's "include nothing," producing a tree full of `[x]` rows for items sync would never actually deliver. Same gap for MCP servers with no explicit `mcp:` entry. The tree now mirrors `internal/config/profile_resolve.go`: quiet packs default every vector and server to off, and only an explicit non-empty `include` list or `enabled: true` activates items. Discoverability is preserved — every declared item still appears in the tree; only the `Enabled` flag flips.

## [0.24.0]

### Added

- **Subdirectory authoring for all content vectors.** Rules: `rules/team-a/style.md` → id `team-a/style`; the harness filename escapes `/` to `__` (`team-a__style.md`), so same-leaf rules in different folders coexist within one pack. Agents, workflows, and skills: subdirectories are organizational only — the id is the file basename (or the skill's parent directory name), and same-leaf entries within one pack are rejected at discovery. `prompts/`, `mcp/`, `profiles/`, and `registries/` also autodiscover recursively. `aipack save` round-trips nested content back to the authored pack-source path for every capture shape — per-file copies (rules/agents/workflows), skill directory copies, and content-writes (native-TOML agents on Codex, promoted agents/workflows on Cline and OpenCode-promote) — so edits made in the harness never leave a flat duplicate alongside the original.

### Changed

- **`__` is reserved in authored rule ids** (harness escape for `/`); manifest validation rejects the literal.
- **Agent, workflow, and skill ids may not contain `/`** — the id is always the leaf; subdirectories under those category roots are filesystem-only organization, never part of the id.
- **Skills capture requires `SKILL.md`.** Bare subdirectories are skipped; previously captured as empty skills.
- **Quiet packs now opt out of MCP servers and settings by default.** A quiet profile entry with no explicit `mcp:` map no longer falls back to the manifest-derived default (all declared servers enabled), and a quiet pack with config files no longer contributes settings unless `settings.enabled: true` is set. Explicit profile entries still override — `mcp: {srv: {enabled: true}}` and `settings.enabled: true` work as escape hatches, matching existing quiet semantics for content vectors (opt-in only). Previously `aipack pack add -q` on a pack declaring multiple MCP servers would trigger last-wins collisions and unresolved-ref warnings against other packs' configs.

### Fixed

- **`pack install` of a v1 pack no longer writes an inconsistent manifest.** `SavePackManifest` always stamps `schema_version: 2` — the in-memory `PackManifest.MCP` is a flat `[]string` regardless of source shape, so the serialized form is always v2. Previously, installing a valid v1 pack (`schema_version: 1` + nested `mcp: {servers: {...}}`) rewrote the manifest as `schema_version: 1` + flat `mcp` array, which the strict parser then rejected on the next read ("v1 expects nested object; got flat array"). Install, update, and extract all re-save the manifest, so consumers of v1 packs hit this on every sync after install.

## [0.23.0]

### Added

- **`always_allowed_tools` profile field for per-tool auto-approve.** Parallel to `allowed_tools` at `packs.<p>.mcp.<srv>.always_allowed_tools`. Claude Code and Cline union it with `allowed_tools` into their native auto-approve field; Codex emits a per-tool `approval_mode = "approve"` stanza; OpenCode unions into its legacy `tools` boolean map with a single aggregated sync-time warning listing the affected servers. A tool in `always_allowed_tools` is implicitly visible — no matching `allowed_tools` entry required.
- **TUI tri-state tool picker for MCP servers.** Cursor over an MCP entry in the profiles tab's content tree and press `t`. Each tool cycles through off / ask / auto on `<space>` (shortcuts `x` / `a` / `A`); enter confirms. Silent profiles render every probed tool as *ask* — matching the effective "grant all, prompt per call" state. Pressing `.` on an MCP entry in the tree opens a small menu (Edit file, Tool list); inside the picker `.` opens bulk actions (enable all, always-allow all, disable/enable server, reset, save inventory). Picker writes are literal sorted lists and collapse to silent when the net state matches harness defaults.
- **Persistent MCP probe cache.** Live-probe results from both the TUI picker and `aipack mcp inspect-tools` are cached at `~/.config/aipack/cache/mcp-probes.json` with a 24-hour TTL, so reopening the picker or re-running the CLI skips re-probing within the window. The picker header shows a "probed Nh ago · r to refresh" hint; pressing `r` inside the picker invalidates the cached entry and fires a fresh probe. Stale entries are dropped on load and on save. Cache file is user-local — safe to delete at any time.
- **`aipack mcp inspect-tools` probes MCP servers for their live tool list.** Bare: list every server across installed packs. Named: connect, list, diff against the pack's `available_tools` inventory. `--save` writes the live list back; pair with `--save --dry-run` to preview writes without touching disk. `--all` inspects every server. Use `pack/server` to disambiguate when names collide. `--profile` / `--profile-path` select where `{params.*}` values come from (full resolution chain: `--profile-path` → `--profile` → sync-config default → `"default"`). All three MCP transports are probed — stdio (subprocess over JSON-RPC), streamable-http (single POST endpoint with `application/json` or `text/event-stream` responses; session threaded via `Mcp-Session-Id`), and the legacy HTTP+SSE transport (long-lived GET stream carries responses for each POST request, correlated by ID). HTTP probes include status code and response-body snippet in errors so auth failures are actionable. The TUI tool picker's async probe uses the same dispatcher — pressing `t` on an MCP entry works regardless of transport. Unknown or ambiguous server names exit with code 2 (`ExitUsage`), probe failures with code 1 (`ExitFail`). Per-file read/parse errors encountered while scanning `packs/*/mcp/*.json` surface via a new top-level `warnings` array instead of being silently dropped.

### Changed

- **`pack.json` `schema_version: 2` introduces a flat `mcp` array.** The new shape is `"mcp": ["server-a", "server-b"]` — a list of server IDs, matching every other content vector; auto-discovered from `mcp/*.json` when the field is omitted. Tool policy (`allowed_tools`, `always_allowed_tools`, `disabled_tools`) lives entirely in profiles, not in the manifest. Schema 1 (pre-v0.23 packs with the nested `mcp` object and pack-level `default_allowed_tools` / `default_always_allowed_tools`) remains fully supported and loads with no warnings — the parser routes on `schema_version` and runs a dedicated v1 path that extracts server IDs for the runtime. Pack-level tool policy in v1 manifests is read but inert; authors who want tool policy should pair the schema 2 bump with a bundled profile. Shape is strictly tied to version: a v1 manifest with a flat `mcp` array (or a v2 manifest with the nested form) is rejected at parse time rather than silently coerced. `aipack pack create` and `aipack save --to-pack` now emit `schema_version: 2`.
- **`aipack save` no longer captures tool permissions from the harness.** Copying a new MCP server still copies `mcp/<name>.json` and appends the server ID to `manifest.MCP`. Tool-list permissions observed in the harness config are discarded — the harness allow list is a render target, not a source. The `aipack save --help` text now calls this out alongside the profile pointer. Adjust permissions through the TUI tri-state picker.

### Security

- **MCP probe hardens against hostile servers across every transport.** All three transports enforce a 10 MiB per-message read cap: a stdio server declaring `Content-Length: 1073741824` is rejected before allocation, a newline-framed stdio server that streams non-newline bytes forever errors at the cap instead of growing the parent's memory, and HTTP responses (both `application/json` and individual SSE events) trigger the same cap when oversized. On Unix, the stdio probe also places the server in its own process group and signals the full group on cleanup, so subprocesses spawned by the server (node worker pools, Python multiprocessing) no longer leak as orphans when the probe times out. Error messages from `{params.*}` / `{env:*}` expansion failures no longer echo post-expansion strings, so profile param values (including secrets) can't land in `inspect-tools` output or logs.

## [0.22.0]

### Added

- **Namespaced git tags unblock multi-pack monorepos.** Tags of the form `<pack-name>/vX.Y.Z` (Go-module convention) are now recognized by the resolver. Repos that ship multiple packs in subdirectories can version sibling packs independently without flat-tag collisions. The convention is opt-in per pack — single-pack repos keep their flat `vX.Y.Z` tags with zero change. Authors release with `git tag my-pack/v1.2.3`; consumers install with `pack install my-pack --ref my-pack/v1.2.3` or the positional shorthand `pack install my-pack@my-pack/v1.2.3`. Once installed, `pack update` and `pack versions` auto-derive the prefix from the lockfile, so users can pass bare semver on update (`--ref 1.2.4`) and aipack resolves against `my-pack/v1.2.4` automatically. No registry schema changes required.
- **`pack update` accepts any git ref.** Previously `--version` rejected anything that didn't parse as semver, a commit hash, or `latest`. Now `pack update my-pack --ref main` switches to tracking a branch, `--ref release-2026-04-01` pins to a non-semver tag, and `--ref <namespaced-tag>` works on multi-pack repos. The update path and the install path now share one classification layer — any ref shape is a legitimate pin attempt, and the resolver dispatches on the shape.

### Changed

- **`--ref` is the primary flag for pinning on both `pack install` and `pack update`.** `--version` stays as a Kong alias — every `--version X` invocation resolves identically to `--ref X` — so existing scripts keep working unchanged. The unification drops the `--version`/`--ref` mutual-exclusivity check and removes the up-front validation that rejected non-semver specs; any git ref shape (exact semver, partial semver, namespaced semver, commit hash, `latest`, branch, non-semver tag) is now a legitimate ref attempt dispatched by shape. Help text, examples, and docs lead with `--ref`.

### Fixed

- **`aipack pack install` (bare and `-m/--missing`) and `aipack profile set <name> --install` exit non-zero when reconciliation hits an install error or a profile-referenced pack missing from every registry.** Both surfaces previously printed the failure and returned 0.
- **`aipack pack update <name> --ref <older-commit>` no longer fails with `pack has N unresolved deltas`** when the initial install was a shallow clone. `UpdateBareCache` now skips seeding the bare reference cache from a shallow local — a shallow seed produces an incomplete object database that later `clone --reference` calls can't resolve. Existing poisoned caches recover via a one-shot retry that invalidates the cache and clones without `--reference`.
- **`pack install`, `pack update`, and `pack rename` populate the drift-detection baseline at install time** instead of waiting for the first `sync`. `aipack doctor`'s broken-refs check and sync's drift report now work against a just-installed pack. `pack update` also backfills the baseline opportunistically on the up-to-date fast path, so packs installed before this release recover the gap without manual intervention.

### Documentation

- **Clarified the pack versioning model: installable versions are git tags, not `pack.json:version`.** The manifest field is author metadata for `pack show`, `pack list`, and `doctor`'s drift warning. `aipack pack install my-pack@1.2.3` resolves against the git tag on the remote. "Releasing a new version" in `docs/creating-packs.md` documents the bump → commit → tag → push ritual with rationale (immutable, distributed, branch-conflict-free, Go modules precedent). A new "Releasing a namespaced version" section covers the multi-pack-monorepo ritual. Cross-links from `docs/installing-packs.md` and `docs/pack-format.md`.

## [0.21.2]

### Added

- **`aipack --version` / `-V`** now print the same output as `aipack version`.
- **`r` on the `aipack manage` packs tab** also re-checks remote versions for the pack under the cursor. Useful after fixing credentials or pushing a new tag upstream.

### Fixed

- **`aipack manage` packs tab no longer hangs** when SSH keys are passphrase-locked and not loaded in `ssh-agent`. Cursor navigation is debounced and aipack-spawned git commands run non-interactively; cached credential sources (ssh-agent, credential helpers, askpass) still work as configured.
- **Pack version lookup failures caused by SSH auth issues show a recovery hint** in the details panel instead of a generic `(unavailable)` row.

## [0.21.1]

### Fixed

- **`aipack prompt list` / `show` / `copy` autodiscover prompts from `prompts/`** even when the manifest omits the `prompts` field, matching every other content type. The install-time and sync-time search indexers get the same fix.
- **`aipack manage` Packs tab and `aipack pack show` list prompts** alongside rules, agents, workflows, skills, and MCP servers.

## [0.21.0]

### Added

- **Pack versioning.** `pack install my-pack@1.2.3` installs and pins a specific semver tag; `@abc1234` pins a commit, `@v1`/`@v1.2` resolve to the highest matching stable. `pack versions <name>` lists available tags. `pack update <name> --version <X>` moves a pin; `--version latest` clears it. One version per pack per machine — use `--name` for parallel installs.
- **Lockfile (`aipack.lock`)** records every installed pack and replaces the `installed_packs` block in `sync-config.yaml`. Migration is automatic.
- **Sync-time content drift detection.** `aipack sync` now reports per-pack added/removed/changed content before applying the plan, with a separate "Removed (affects your profile)" section for items your active profile references.
- **TUI packs tab is versioning-aware.** Pack details show pin/commit/latest, the pack list flags drift, registry packs show available tags inline with a version picker on install, and clone-pack actions gain "Pin to version..." and "Unpin".
- **`aipack doctor` broken-refs check.** New warning when a profile references content the pack no longer ships.

### Changed

- **`pack install` and `pack update` work without arguments** — `install` reconciles the active profile (fetching missing referenced packs), `update` refreshes every installed pack. `-m/--missing` and `--all` are accepted as explicit aliases.
- **`pack update` is concurrent** — up to three packs in parallel, output flushed in input order.
- **`aipack doctor` detects clone-pack drift via `git ls-remote`** with graceful offline degradation.
- **Profile references that drift out of a pack are warnings, not fatal errors.** Typos (IDs never in the pack) still hard-error.
- **HTTP tarball install method removed.** All remote installs use shallow git clone; tarball-installed packs migrate on next `pack update`.
- **Registry sources without an explicit `--ref` use the remote's default branch.**

## [0.20.2]

### Changed

- Homebrew tap renamed from `dfoster-oracle/homebrew-tap` to `shrug-labs/homebrew-tap`. New installations: `brew install shrug-labs/tap/aipack`. Existing `dfoster-oracle/tap/aipack` installs continue working via GitHub's automatic redirect, but re-tapping is recommended: `brew untap dfoster-oracle/tap && brew tap shrug-labs/tap`.

## [0.20.1]

### Fixed

- OpenCode profile filtering now takes effect at runtime. `opencode.json`'s `instructions` array and `skills.paths` previously pointed at each pack's source directory, so OpenCode loaded every rule and skill in the pack regardless of the profile's `rules.exclude` / `skills.exclude` selectors. They now point at aipack's rendered managed directories (`.config/opencode/rules/` + `.config/opencode/skills/` for global scope, `.opencode/rules/` + `.opencode/skills/` for project scope), which contain only profile-selected content. Existing users upgrading from 0.20.0 have their legacy pack-source entries pruned on next sync via the settings three-way merge; user-added entries are preserved.
- TUI profiles content tree no longer clips every row with `…` at narrow pane widths. When the pane is too narrow for the full label + pack attribution + size columns, the tree drops whole optional columns (size first, then pack attribution) instead of truncating every line mid-word. Wide layouts render the full three-column attribution as before.

## [0.20.0]

### Added

- `pack add` / `pack remove` commands for adding and removing pack entries from profiles. `pack install --add` combines install with profile addition.
- `pack disable` now sets `enabled: false` in-place, preserving the entry's selectors and overrides. `pack enable` sets it back to `true`. Previously both commands added/removed the entry entirely.

### Changed

- **Breaking:** `pack install` no longer adds the pack to the active profile by default. Use `--add` to add at install time (e.g. `aipack pack install ./my-pack --add`), or `aipack pack add <name>` after install. The `--no-register` flag has been removed.
- **Breaking:** `pack enable` / `pack disable` now toggle the `enabled` field on an existing profile entry instead of adding/removing it. Use `pack add` / `pack remove` for entry creation and deletion.
- **Breaking:** `registry remove` renamed to `registry delete` for consistency with `pack delete` and `profile delete`.

## [0.19.1]

### Added

- TUI pack install and update show a bundled content checklist (`--with` equivalent) for selecting profiles, registries, and extras.
- Init template includes `collision_strategy: last-wins` in sync-config defaults, making the default visible.

### Fixed

- TUI save pipeline no longer requires a non-empty profile — saving to a newly created pack works even when no packs are configured.
- TUI empty-profile UX: hint instead of red error, "Create new pack..." always available in the add-pack dialog.

## [0.19.0]

### Added

- `defaults.collision_strategy` in sync-config.yaml: `last-wins` (default), `first-wins`, or `error`. Explicit `overrides` always take precedence. In `error` mode, all collisions are reported in one message with remediation YAML.
- Git clone cache at `configDir/.cache/git/` — `pack install` and `pack update` maintain bare-repo caches and use `--reference` to reduce network transfer on subsequent clones.
- `pack update` uses `git ls-remote` to skip cloning when the remote HEAD is unchanged and no new `--with` categories are requested.
- TUI profiles tab: `J`/`K` reorders packs in the roster. Content tree shows conflict markers (`⚠` unresolved, `⬡` override winner, dim strikethrough for losers) with a `.` action menu for setting/removing overrides.

### Changed

- Content collisions default to `last-wins` instead of erroring. Set `defaults.collision_strategy: error` to restore the previous behavior.
- Collision errors now report all collisions in a single message with copy-pasteable remediation YAML, instead of failing on the first one.

### Fixed

- Init template defaults to `scope: global` and `harnesses: [codex]`, matching current conventions.
- `pack update` for symlinked packs now re-reads the manifest and re-installs bundled content.
- `pack update` error messages were silently swallowed — now prints to stderr.
- Shallow bare-repo caches rejected by `git --reference` — cache now removes the shallow marker after seeding.

## [0.18.3]

### Fixed

- Fetching a second registry file from the same git repo overwrote the first registry source instead of creating a separate entry. Registry source identity now uses the (URL, path) pair — two files from one repo are correctly tracked as distinct sources with independent cache files and sync-config entries.
- Docs: corrected stale description of registry merge behavior that referenced a nonexistent `~/.config/aipack/registry.yaml` local file.

## [0.18.2]

### Fixed

- Pack install and update now use atomic backup-and-swap for directory replacement, preventing pack loss if the final move fails. Consolidated from per-call-site implementations into `ReplaceDirAtomic`.
- Removed dead registry-copy code in pack extraction (superseded by extras).
- Docs: default registry pointer corrected to `shrug-labs/packs`.

### Added

- `doctor` checks `install_entries_valid` and `stale_backups`: detect orphaned sync-config entries and leftover temp directories from interrupted operations. Both auto-fixable with `--fix`.

## [0.18.1]

### Fixed

- Disabling all MCP-providing packs could delete `.claude.json` (or other shared config files) entirely instead of stripping only the managed keys. Stale reconciliation now uses the harness `OwnedFile` strip functions for merge-mode files, preserving user-owned content. Affects all four harnesses with shared config files: Claude Code, OpenCode, Codex, and Cline.

## [0.18.0]

### Added

- **Pack extras and `{pack:root}` expansion.** Packs can declare an `extras` field in pack.json listing supporting files — wrapper scripts, data files, helper source — that are preserved through install and update. MCP server definitions reference these via `{pack:root}`, which resolves to the installed pack's absolute path before `{params.*}` and `{env:*}` expansion. Enables self-contained packs that ship executable helpers alongside their MCP configs.
- **Selective content installation (`--with` / `-w`).** Replaces `--seed` on `pack install` and `pack update`. Controls bundled content — `profiles`(`p`), `registries`(`r`), `extras`(`e`), or `all`. Core content (rules, skills, workflows, agents, prompts, mcp, configs) is always installed. Without `--with`, remote installs preview available bundled content; local installs default to all.
- **Registry install at pack install time.** `pack install -w registries` and `pack update -w registries` merge pack-bundled registry entries into the local embedded registry cache immediately, rather than waiting for the next sync.
- **Bundled content candidates on update.** When `pack update` finds new content categories that weren't previously approved, they appear as candidates — printed in the CLI, shown as a checklist dialog in the TUI (space to toggle, enter to confirm).
- TUI profiles tab: "Update" (per-pack) and "Update all" actions via the `.` action menu.
- `pack show` includes extras in output.

### Changed

- **Profiles and registries are now ID-based.** *(Breaking)* Manifest fields use bare IDs (`"profiles": ["dev"]`, `"registries": ["team-tools"]`) discovered from standard `profiles/` and `registries/` directories, matching rules, skills, and other content types. Old relative-path format (`"profiles/ops.yaml"`, `"registry.yaml"`) is deprecated — old-format entries in pack.json are automatically normalized, but files must live in the standard directories.
- **Default scope is now `global`.** When `--scope` is not specified and sync-config has no `defaults.scope`, CLI and TUI default to `global` instead of `project`. Fixes confusion when running from `$HOME` where project and global paths coincide. Set `defaults.scope: project` in sync-config or pass `--scope project` for the old behavior.
- **Pack-bundled profiles are managed content.** On install or update, profiles from the pack always overwrite the local copy. Copy to a new name to preserve customizations.
- **Content approval tracking.** Sync-config records which bundled content categories the user approved per pack. On update, previously approved categories carry forward automatically (profiles re-copied, registries re-merged); new categories surface as candidates. Pre-`--with` installs are treated as fully approved for backward compatibility.
- Content vector fields (`rules`, `agents`, `workflows`, `skills`, `prompts`) no longer distinguish `null` from `[]` for discovery — both trigger auto-discovery from the conventional directory.
- Save and TUI discovery skip project scope when the working directory is `$HOME`.
- CLI help text and config-path references show platform-aware defaults (`%APPDATA%\aipack` on Windows, `~/.config/aipack` elsewhere).
- Windows installer (`install.ps1`) installs to `~/.local/bin`, matching macOS/Linux. Update your PATH if upgrading from a previous Windows install.
- Build system uses a cross-platform Go task runner (`go run ./tools/task`) instead of shell-dependent Makefile recipes. `make` targets still work.

### Fixed

- `--config-dir` override was not respected for ledger operations — reads and writes always derived from `$HOME`. Now threaded end-to-end through TargetSpec, PlanRequest, and ledger resolution.
- `aipack version` shows correct version and commit when installed via `go install` (previously "dev (unknown)"). Falls back to Go build info metadata when linker flags are absent.
- `aipack trace` misattributed shared-path destinations (e.g., `.agents/skills/`) to the wrong harness when multiple harnesses target the same directory.

## [0.17.0]

### Changed

- **Multi-pack settings.** Any pack with harness config files now contributes base settings automatically — no `settings.enabled: true` required. Multiple packs' settings are deep-merged in profile order (first pack wins at leaf conflicts, warning emitted). Set `settings.enabled: false` to opt a pack out. Existing profiles with `settings.enabled: true` continue to work unchanged.
- `--skip-settings` now correctly skips only base template keys while still writing all computed managed keys (MCP permissions, agent registrations). Previously, Claude Code leaked the base template through in skip mode, and Codex dropped agent registrations.
- Sync summary no longer shows settings count — settings are infrastructure, not user-authored content.
- Plugin files (`harness_plugins`) from multiple packs are no longer silently merged. Same-filename collisions across packs now produce an error.

### Added

- TUI pack actions: "Disable settings" / "Enable settings" toggle replaces the old single-pack "Settings source" selector.

## [0.16.0]

### Added

- Content type flags (`--rules`, `--skills`, `--agents`, `--workflows`, `--prompts`) on `pack install` and `pack create`. Install with `--url` extracts a clean content-only slice from the repo. Create with local paths produces directory-level symlinks for live editing.
- Content path remapping (`content_paths`) in registry entries: declarative version of content flags for team distribution. Consumed repos need no `pack.json` — aipack generates one at install time.
- Quiet packs (`quiet: true` on profile pack entries, `--quiet`/`-q` install flag, or `quiet: true` registry hint): omitted vector selectors resolve to nothing instead of all content, enabling opt-in inclusion from large catalogs.
- Prompts as a content category: `--prompts` flag on `pack install` and `pack create`, `prompt` as a search kind, prompt content indexed at sync time.
- `pack list` shows multi-line output with origin, ref, and content summary per pack.
- `search` shows a summary header with result counts grouped by kind. Single-pack results omit the pack prefix. Descriptions truncate at sentence boundaries for readability.

### Changed

- All remote pack installs (clone and HTTP tarball) now produce clean content-only packs. Installed packs no longer contain `.git/`, test directories, or other non-pack files. `pack update` for clone-method packs re-clones and re-extracts instead of `git pull`.
- `profile list` shows `(active)` marker instead of `*` for the current profile.
- `doctor` renames `mcp_env_vars_present` check to `mcp_refs_present`. MCP ledger entries with missing SourcePack are now detected and auto-fixable.
- Documentation restructured into focused guides: [Installing Packs](docs/installing-packs.md), [Creating Packs](docs/creating-packs.md), [Profiles](docs/profiles.md), [Sync and Save](docs/sync.md), and [Harness Reference](docs/harness-reference.md). Existing docs slimmed with cross-references to the new guides.

## [0.15.2]

### Fixed

- `aipack doctor` false positives on presync cache directories and MCP synthetic ledger keys.
- Codex `AGENTS.override.md` now carries SourcePack attribution (`(composite)` for multi-pack profiles).

## [0.15.1]

### Fixed

- Three-way settings merge preserved disk array order instead of managed order, causing positional arrays like MCP server `args` to retain a wrong element order indefinitely — even across `--force` re-syncs. The merge now emits managed items in managed order first, appending user-only items afterward.

## [0.15.0]

### Changed

- Engine apply pipeline decomposed into focused units: file application (`apply.go`), stale-file reconciliation (`stale.go`), and interactive I/O (`interact.go`). Interactive prompting is now injectable via the `Interactor` interface on `ApplyRequest`, enabling tests for previously uncoverable deletion paths.
- `shouldDelete` returns a `DeleteDecision` type (`DeleteYes`, `DeleteNo`, `DeleteSkippedNonInteractive`) instead of `(bool, error)`, distinguishing user-declined from non-interactive-skipped from I/O failure. Non-interactive skips produce an actionable summary warning with count.
- Stale-file reconciliation no longer tracks stat/remove failure counts or emits per-file or aggregate warnings for filesystem errors on managed files. These files are created by aipack in directories it controls, so permission-based failures are not a realistic scenario.
- `LoadLedger` and `SnapshotSettingsFiles` now return `[]domain.Warning` instead of ad-hoc warning strings, making the warning pipeline consistent across the engine.
- Engine package introduces an `Engine` struct with injectable `FS` and `Interactor` dependencies. All filesystem-touching functions (14 exported, plus internal helpers) are now methods on `*Engine`, replacing direct `os.*` calls with the `FS` interface. This makes the sync/apply/diff/ledger/cache pipeline testable without touching the real filesystem. A `MemFS` in-memory implementation is exported for test use. The `Interactor` (previously on `ApplyRequest`) moves to the Engine, giving callers a single construction site for all I/O dependencies.
- Pack source acquisition (git clone, HTTP tarball, archive extraction, URL probing) extracted from `internal/config` to `internal/source`. The config package drops from 19 files / 7k lines to 15 / 4.5k.
- `OSFS.WriteFile` fallback directory permission changed from `0o755` to `0o700`, matching aipack's convention for user-private config directories.
- TUI threads a single `*Engine` instance through the model hierarchy instead of creating separate instances per async operation.

### Security

- Pack URL probing rejects URLs containing embedded credentials.
- `urlOK` validates URL scheme before making requests, rejects non-HTTP(S) schemes.
- `urlOK` uses HEAD instead of GET for URL probing (falls back to GET on 405 for servers that don't support HEAD), draining response bodies for connection reuse.

## [0.14.0]

### Added

- Native Codex agent rendering. Pack agents with `harness.codex` frontmatter overrides now render as self-contained TOML files in `.codex/agents/` with registration entries in `config.toml`, matching Codex's native multi-agent format. Agents can declare per-harness config (model, reasoning effort, service tier) under a `harness:` namespace in frontmatter.
- General frontmatter stripping for cross-harness rendering. Harness-specific frontmatter fields (e.g., `harness:` on agents, `paths:` on rules) are automatically stripped when content is rendered as markdown for harnesses that don't support them.
- Promotion collision detection for Cline harness. Skills, workflows, and agents that would collide in the same skill directory are now caught during sync with a descriptive error.

### Changed

- Codex Layout now manages `.codex/agents/` alongside `.agents/skills/` for validation, cleanup, and stale-file detection.
- Codex `config.toml` Strip/Reset functions now handle both `mcp_servers` and `agents` managed keys.

### Fixed

- OpenCode capture now correctly parses disabled tools from settings, populating `DisabledTools` on captured MCP servers instead of silently dropping them.

## [0.13.1]

### Changed

- Dry-run output uses consistent `create:`/`update:` verbs instead of mixed `write:`/`copy:` for new files.
- Dry-run plan summary now shows source content counts (e.g., `plan: 3 file ops from 2 rules, 1 skills, 2 identical`) instead of bare change counts.
- MCP server count included in `ContentCounts` and the sync summary line, matching the JSON output.
- Sync OK line drops the redundant `mcp` count (now included in the content summary).

### Fixed

- Docs: `pack create` examples updated from removed directory-path syntax to current name-based syntax.
- Docs: `sync --json` field descriptions now correctly describe profile source counts, not plan file counts.

## [0.13.0]

### Changed

- **`pack create` redesigned.** Takes a pack name instead of a directory path. Packs are created in the current directory and symlinked into the packs directory by default (`--link` behavior). Use `--local` to create directly inside the packs directory. Registration in sync-config is now automatic.
- Registry search and list output uses a structured multi-line format with labeled fields (Name, Description, Owner, Repo, Path, Ref, Contact).
- `make lint` now runs `go fix ./...` alongside `go vet` and `staticcheck`.

### Fixed

- Disabled packs no longer participate in override conflict resolution during profile resolution. Previously, a disabled pack declaring overrides could incorrectly suppress duplicate-resource errors between enabled packs.
- TUI text input handles multi-byte key events correctly, preventing phantom characters from special key presses.
- TUI pack name input no longer leaks keypresses to global hotkeys. Previously, typing characters that matched global shortcuts (e, s, r, w, digits) while entering a new pack name would trigger tab switches, sync, or other actions instead of appending to the input.
- TUI "Save to pack" action (`.` menu) now scopes to the cursor item instead of carrying forward all selected files.

## [0.12.1]

### Changed

- `IgnoredName` now excludes `.venv`, `.git`, and `node_modules` in addition to `__pycache__` and `.DS_Store`. Packs developed inside git repositories, Python virtualenvs, or Node projects no longer include these directories in sync, digest computation, or file watching.

## [0.12.0]

### Added

- **Windows support (amd64 + arm64).** Cross-platform config paths (`%APPDATA%\aipack` on Windows, `~/.config/aipack` elsewhere), PowerShell installer (`install.ps1`) with `AIPACK_VERSION` support, PowerShell shell completion, Windows self-update with locked-executable handling, `clip.exe` clipboard support, and `windows/amd64` + `windows/arm64` release binaries.
- **CI Windows test runner.** Tests now run on both Ubuntu and Windows in the validate pipeline.
- **WSL detection.** `aipack doctor` warns when running in WSL with Cline configured, since global-scope Cline rules target the Windows filesystem which WSL cannot reach.
- **Symlink test portability.** Tests that create symlinks skip gracefully on Windows without Developer Mode instead of failing.

### Changed

- Home directory resolution uses `os.UserHomeDir()` instead of `$HOME`, which works across all platforms (HOME on Unix, USERPROFILE on Windows).
- Cline Documents folder is resolved via the Windows shell API (`SHGetKnownFolderPath`) to handle OneDrive folder redirection. Non-Windows platforms use the conventional `~/Documents` path.
- `pack install --link` falls back to a directory junction (`mklink /J`) on Windows when symlinks require elevated privileges.
- Git error hints are platform-aware: credential helper suggestions use `manager` on Windows, `store` on Linux, `osxkeychain` on macOS. Git-not-found on Windows suggests `winget install Git.Git`.
- Cline global paths changed from a package-level variable to a `GlobalPathsFor(home)` function to support platform-dependent Documents folder resolution.
- Ledger path encoding handles Windows drive letters and backslashes.
- Test assertions use `filepath.Join` and `t.TempDir()` instead of hardcoded Unix path literals.

### Known limitations

- **WSL + Cline global scope:** aipack in WSL writes to the Linux filesystem, but Cline reads from the Windows filesystem. Use `aipack sync --scope project` in WSL, or run aipack natively on Windows for global scope.
- **OpenCode harness:** Global paths still use `.config/opencode` (Unix convention). Windows-specific resolution is not yet implemented.

## [0.11.7]

### Added

- Symlinks in pack content are now resolved at install time when the target is within the source repository boundary. This enables packs in monorepos to share content via symlinks (e.g. `rules/shared.md -> ../../shared/base.md`). The installed pack always contains regular files. Symlinks that escape the repo boundary, point to directories, traverse `.git/`, or use absolute targets are rejected. The `--link` install method is unchanged.

### Changed

- Bitbucket Server SSH URLs (port 7999) now use shallow clone instead of `git archive --remote`. This fixes auto-discovery for packs with incomplete manifests and simplifies the install pipeline. Packs previously installed via archive will update via clone on next `pack update`.

## [0.11.6]

### Changed

- `pack install` and `pack update` for GitHub HTTPS URLs now download tarballs directly instead of trying git-archive then falling back to clone. Removes the git binary requirement for GitHub-hosted packs and avoids protocol-level rejections from GitHub's git-archive endpoint.
- URL-based fetch strategy selection replaces the try-then-fallback cascade. Bitbucket Server SSH uses git archive, GitHub HTTPS uses HTTP tarball, everything else uses shallow clone. Each strategy's errors are terminal rather than triggering fallback chains.
- Version update check uses redirect parsing (`/releases/latest` → `Location` header) instead of the GitHub releases JSON API, avoiding the 60 req/hr unauthenticated rate limit.

### Fixed

- `adopt` and `save` now discover existing pack content before writing the manifest, preventing previously auto-discovered rules/skills/workflows from being silently dropped when adding a single new item.

## [0.11.5]

### Changed

- Default registry moved from `shrug-labs/aipack` to `shrug-labs/packs`. The registry now lives alongside the packs it indexes, decoupling content changes from tool releases and eliminating a redundant clone during `pack install`.

## [0.11.4]

### Added

- FAQ document (`docs/faq.md`) covering basics, setup, usage, and pack creation/sharing.

### Fixed

- Pack install temp directories (clone, archive, copy, subtree extraction) are now created in a dedicated staging area (`<configDir>/.tmp/pack-staging/`) instead of inside the `packs/` directory. Orphaned staging dirs from interrupted installs no longer appear as installed packs or interfere with pack enumeration.

## [0.11.3]

### Fixed

- Default registry no longer evicted when custom registry sources are added. The compiled-in shrug-labs registry is always included during `registry fetch`, keeping aipack-core and essentials resolvable.
- `pack install` no longer fails when the default profile is missing. Config files are auto-created on first use, removing the need to run `aipack init` first.
- Profile override resolution is now order-independent. The pack that declares `overrides` wins regardless of its position in the profile's pack list. Typos in override IDs now produce an error instead of being silently ignored.
- `--seed` on reinstall now overwrites existing profiles with the pack version. Without `--seed`, an interactive prompt asks whether to replace each existing profile.
- Dry-run output classifies skill directory copies as `copy`, `update`, or `skip(conflict)` instead of treating all existing destinations as overwrites.
- Cline MCP settings sync no longer requires `--force` when the settings file already exists. Uses three-way merge to preserve user-added servers while applying pack changes.
- MCP ledger reconciliation after sync now uses injectable time for testability.

## [0.11.2]

### Changed

- TUI achromatic color tokens (dim text, help bar, headers, summaries) now use 4-tier background detection instead of 2-tier adaptive colors. Gray terminals that are neither dark nor light get tinted gray palettes with WCAG 3:1+ contrast ratios. Adds a contrast ratio test suite covering all palettes.

### Fixed

- `pack install` with an incorrect sub-path now lists available packs in the repository instead of a bare "not found" error.

## [0.11.1]

### Changed

- `install-completions` replaced third-party kongplete implementation with a custom command supporting `--uninstall`, `--print`, `--yes`, and `--rc-file` flags. Install and uninstall are idempotent, use marker-delimited blocks in the rc file, and detect non-interactive sessions.

### Fixed

- Tab completions for pack names now include symlinked packs. Previously `pack delete`, `pack show`, and other pack-argument commands only completed directory-based packs, omitting symlinks.

## [0.11.0]

### Added

- `aipack install-completions` command registers tab completions for bash, zsh, and fish. Dynamic predictors suggest pack names, profile names, harness names, resource types, and other arguments based on current config state.
- `pack delete` detects profiles seeded by the removed pack and offers to clean them up. Use `--yes` to auto-confirm; the TUI auto-removes seeded profiles without prompting.

### Changed

- Cline skills and agents now write to `.agents/skills/` (shared with Codex) instead of `.clinerules/skills/` and `~/.cline/skills/`. Cline reads both locations natively, so sharing a single canonical skills directory prevents duplication when multiple harnesses target the same project.
- TUI colors use adaptive tokens that auto-select light or dark variants based on terminal background. Light-background terminals now get readable contrast instead of washed-out colors.
- Confirmation prompts in `clean` and `sync --yes` now respect context cancellation (Ctrl-C exits immediately instead of blocking on stdin).

## [0.10.2]

### Changed

- Cline MCP sync now writes `cline_mcp_settings.json` to both the VS Code global-storage path and the standalone Cline path (`~/.cline/data/settings/cline_mcp_settings.json`).
- Cline MCP capture prefers the VS Code path, falls back to the standalone path when needed, and emits a warning when the secondary file differs from the capture source.
- Go minimum version lowered to 1.25.

### Fixed

- Watch loop now drains in-progress syncs before exiting and refuses to start new syncs after context cancellation, preventing races on shutdown.

## [0.10.1]

### Changed

- Centralized harness path construction into per-scope `Paths` structs; `LedgerPathForScope` now takes typed `domain.Harness` instead of `string`.
- Threaded `context.Context` from CLI root through engine, config, and update layers to all network and exec call sites. Signal cancellation now propagates to HTTP requests, git operations, and update downloads.

## [0.10.0]

### Added

- `aipack update` command downloads the latest release binary from GitHub, verifies its SHA256 checksum, and atomically replaces the current executable. Includes ad-hoc codesign on macOS.
- Stale managed files are now removed automatically during sync. Files no longer in the profile are cleaned up without requiring `--prune`. User-modified stale files prompt for confirmation (or `--yes` to skip).
- macOS ad-hoc codesign for built binaries in `make install` and `make dist`, preventing quarantine issues.
- `make lint` target runs `go vet` and `staticcheck`.

### Changed

- Removed `--prune` flag from `aipack sync`. Stale file cleanup is now always-on — sync converges to the profile's desired state by default.
- Cline global rules and workflows write to `~/Documents/Cline/Rules/` and `~/Documents/Cline/Workflows/` directly instead of an `aipack/` subdirectory. Re-sync to pick up the new paths; the old subdirectories are cleaned automatically.
- `RunSync` and `ApplyPlan` return structured `[]domain.Warning` instead of printing to stderr. Warnings are included in `--json` output as a `"warnings"` array and no longer shadow each other in the TUI.
- Harness interface consolidated from six path/settings/clean methods into a single `Layout()` returning a `Layout` struct with `ValidationRoots`, `RemovePaths`, and `OwnedFiles`. Clean, save, and trace all derive behavior from Layout.
- Go version bumped from 1.24 to 1.26.

### Fixed

- `WriteFileAtomic` uses unique temp files via `os.CreateTemp`, preventing data loss when concurrent syncs race on the same destination.
- `clean` properly handles mixed containers like `.opencode/` — partially-owned settings files are surgically reset while fully-owned content and ledger-tracked drop-in files are removed.
- Cline project-scope `clean` no longer deletes the entire `.clinerules/` directory — only aipack-managed subdirectories (`workflows/`, `skills/`) are removed as RemovePaths. User-created files in `.clinerules/` root are preserved; aipack-managed rule files are cleaned individually via the ledger.
- Pack list, pack update, and prompt list skip hidden (dot-prefixed) directories.
- `ResolveProfile` no longer reads implicit environment state (`os.Getwd`, `os.Getenv`); callers pass `ProjectDir` and `Home` via `ResolveRequest`.

## [0.9.4]

### Fixed

- Sync summary counts content from the profile's typed collections instead of inferring from plan destination paths. Fixes double-counting when targeting multiple harnesses and misclassification of workflows promoted to skill directories by Codex.
- MCP server count in sync output reflects the number of configured servers, not the number of config files written.
- Removed dead `CountContentTypes` function.

## [0.9.3]

### Fixed

- Save TUI now shows proper names for all file types instead of raw filenames (e.g. "execute-plan" not "SKILL.md").
- Skill preview loads content from directory-based skills instead of showing "(empty)".
- Packs tab vertical separators highlight when navigating between columns.

## [0.9.2]

### Added

- **Number-key tab switching**: Press 1-5 to jump directly to any tab. Help bar updated to show `1-5/tab:switch`.
- **Persistent status line**: Shows active profile name, sync state, and pack count below the content area. Transient messages appear on the right and auto-clear after 3 seconds.
- **Destructive confirm dialogs**: Delete and remove confirmations now default to "No", preventing accidental data loss from a stray Enter.

### Improved

- **Pack info panel**: Simplified layout — registry details flow directly below pack details separated by a horizontal rule instead of being pinned to the bottom with variable gaps.
- **Help bar readability**: Related key groups separated by `│` dividers.

### Fixed

- Removed unused TUI styles, dead prompt helpers, and stale test utilities.
- Genericized internal team references in documentation examples.

## [0.9.1]

### Fixed

- **Content discovery in status/show**: `aipack status`, `aipack manage`, and `aipack pack show` now run content auto-discovery, so packs relying on convention-based layout (no explicit content lists in `pack.json`) display their rules, skills, agents, and workflows correctly.

## [0.9.0]

### Added

- **`aipack restore`**: Restores settings files from the pre-sync cache. Each sync snapshots existing settings before overwriting; restore copies them back. Supports `--harness`, `--scope`, `--dry-run`, `--json`.
- **`aipack status`**: Shows ecosystem status — active profile, installed packs with content inventories, and totals. Supports `--profile`, `--profile-path`, `--json`.
- **`aipack trace`**: Traces a resource from pack source through the sync pipeline to each harness destination, showing file path and on-disk state. Valid types: `rule`, `agent`, `workflow`, `skill`, `mcp`. Supports `--harness`, `--scope`, `--json`.
- **`pack enable` / `pack disable`**: Enable or disable a pack in a profile without installing or deleting it from disk. Replaces the previous `pack add` / `pack remove`.
- **`sync --watch`**: Continuous sync mode — performs an initial sync, then watches pack source directories and config files for changes and re-syncs automatically.
- **`sync --verbose` (`-v`)**: Shows content diffs for changed files during sync.
- **`sync --json`**: Machine-readable JSON output for sync operations.
- **Pre-sync settings cache**: Sync now snapshots each settings file before overwriting, stored in a `presync/` directory alongside the ledger. Enables `aipack restore`.
- **Codex agent/workflow promotion**: Agents and workflows are now promoted to `.agents/skills/<name>/SKILL.md` with enriched YAML frontmatter (`source_type: agent` or `source_type: workflow`) that preserves metadata for round-trip capture. Previously these were flattened into `AGENTS.override.md`.
- **Cline agent promotion**: Agents are promoted to skill directories in `.clinerules/skills/` with enriched frontmatter, matching the Codex promotion approach.
- **Doctor checks**: `cli_update` (newer version available), `profile_validated` (YAML structure), `packs_registered` (unregistered pack dirs), `pack_version_drift` (local origin comparison), `stale_ledgers` (orphaned ledger files).
- **Automatic update checking**: `aipack sync` and `aipack version` check GitHub for newer releases in the background (cached for 6 hours, no blocking). Disable with `AIPACK_NO_UPDATE_CHECK=1`. Skipped for dev builds.

### Changed

- **Env var expansion unified**: All harnesses now resolve `{env:VAR}` identically at sync time — the placeholder is replaced with the literal value from the process environment. If the variable is not set, the MCP server is skipped entirely and a warning is emitted. Previously each harness translated to its own native syntax.
- **`--prune` separated from `--force`**: `--force` only overrides file conflicts. `--prune` independently controls deletion of stale managed files not in the current plan. Previously `--force` implied prune.
- **`pack install` missing-pack behavior**: Running `pack install` with no arguments no longer installs all missing packs by default. Use `-m`/`--missing` to install missing packs from the active profile.
- **`registry fetch` absorbs `registry add`**: Fetching a URL now auto-saves it as a source in sync-config, removing the need for a separate add step.
- **Claude Code global MCP path**: Changed from `~/.claude/.mcp.json` to `~/.claude.json`.
- **Claude Code global settings path**: Changed from `~/.claude/settings.json` to `~/.claude/settings.local.json`.
- **Save modes**: Reduced from three (round-trip, snapshot, to-pack) to two (round-trip and to-pack). `--to-pack` now supports `--types` filter.
- **TUI tabs**: Registry tab replaced with Save tab in `aipack manage`.
- **Go version**: Bumped from 1.23 to 1.24.

### Removed

- **`--snapshot` save mode**: Use `--to-pack <name>` instead.
- **`registry add`**: Absorbed into `registry fetch`, which now auto-saves sources.
- **`registry search`**: Use `aipack search` for full-text search across packs and registries.
- **`pack add` / `pack remove`**: Replaced by `pack enable` / `pack disable`.

## [0.8.0]

### Added

- **Content auto-discovery**: Manifests with nil content fields (rules, agents, workflows, skills) now auto-discover from the conventional directory structure at profile resolve time. Explicit lists — including empty `[]` — are preserved. Removes the need to enumerate every resource in `pack.json`.
- **Glob selectors in profiles**: Include/exclude selectors now support glob patterns (e.g., `anti-*`, `experimental-*`). Exact IDs still error on unknown references; globs silently match zero items.
- **`pack rename`**: Renames a pack across directory, manifest, sync-config, all profiles, and all ledger files with rollback on failure.
- **`doctor --fix`**: Auto-repairs safe ledger issues: prunes orphaned entries (paths no longer on disk) and fills missing `SourcePack` when a single pack is resolved.
- **`doctor` ledger health check**: New `ledger_health` diagnostic detects orphaned entries and missing `SourcePack` fields.
- **`doctor` manifest drift check**: New `manifest_drift` diagnostic compares manifest-declared content against what exists on disk, reporting undeclared and missing resources.
- **`aipack install` alias**: Top-level shorthand for `aipack pack install`.

### Changed

- Content discovery functions (`DiscoverIDs`, `DiscoverSkills`) extracted to `config/pack_discover.go` as public API, replacing private duplicates in `save.go`.
- `doctor` overall status now only fails on critical-severity checks; warning-level checks (ledger health, manifest drift) do not cause a non-zero exit.

## [0.7.2]

### Added

- **Content diff on pack add**: Re-installing a pack now shows what changed (added, removed, modified files) or prints "Content unchanged" when content is identical.
- **Registry re-resolution on pack update**: `pack update` re-resolves origin, ref, and subpath from the cached registry before fetching, picking up registry changes (e.g. branch moves) made after initial install.
- **MCP server name validation**: Pack inventory validation now checks that the `"name"` field inside each MCP server JSON file matches the manifest key. A mismatch — which causes silent sync failures — is caught at validation time with a clear error.

### Fixed

- **Pack update N+1 registry reads**: `pack update --all` was re-reading and re-parsing all registry files from disk for each pack. The merged registry is now loaded once and reused.

## [0.7.1]

### Fixed

- **Archive fallback for missing `git-upload-archive`**: GitHub may also reject `git archive --remote` with `"Invalid command: git-upload-archive"` (distinct from the HTTP 422 fixed in v0.7.0). This error is now classified as unsupported archive, triggering the shallow clone fallback for both `registry fetch` and `pack install`.

## [0.7.0]

### Fixed

- **Registry fetch on GitHub HTTPS**: `git archive --remote` over HTTPS returns HTTP 422 on GitHub, but the error was not recognized as an unsupported-archive signal. The shallow clone fallback now triggers correctly for this case.

### Added

- **`pack install` (no args)**: Installs all missing packs from the active profile by looking them up in the registry. Enables one-command dependency resolution after setting a profile.
- **`profile set --install`**: Sets the active profile and installs missing packs from the registry in one step.
- **`profile set` missing-pack detection**: Reports packs declared in the profile that are not installed and suggests `aipack pack install`.

## [0.6.0]

### Added

- **Git archive install**: `pack install --url` now uses `git archive --remote` for selective fetch (manifest + declared content only), with automatic fallback to shallow clone when the remote doesn't support it. New install method `"archive"` recorded in metadata.
- **`pack install --path`**: Install a pack from a subdirectory within a git repo.
- **`pack install --seed`**: Opt-in flag to apply bundled registries and profiles from remote packs. Without `--seed`, candidates are printed but not applied.
- **Pack name sanitization**: Pack names containing `..`, `/`, `\`, or null bytes are rejected.
- **MCP server warning**: `pack install` prints a warning when a pack defines MCP servers, listing server names and tool counts.
- **Content integrity tracking**: `.aipack-integrity.json` records SHA256 hashes at install time; `pack update` shows a file-level diff of changes.
- **`pack update` archive support**: Packs installed via archive are re-fetched using the same two-phase method with content-change detection.

### Changed

- `CopyDir` rejects symlinks in pack content (previously followed them silently).
- Tar extraction validates entries: rejects symlinks, hard links, path traversal, and enforces per-file (1MB) and total (50MB) size limits.
- Remote installs no longer auto-seed bundled registries and profiles. Use `--seed` to apply, or review the preview output. Local path installs retain auto-seed behavior.

## [0.5.0]

### Added

- **SSH URL support**: `registry fetch`, `registry add`, and `pack install` now detect `git@host:path` SCP-style and `ssh://` URLs as git sources, avoiding HTTPS credential prompts.
- **`registry add <url>`**: Configure a registry source without fetching — useful offline or in setup scripts.
- **`registry sources`**: List configured registry sources with cache status. Supports `--json` output.
- **`pack install --ref`**: Override the git ref when installing from URL or registry name.
- **`[installed]` markers**: `registry list` and `registry search` now indicate which packs are already installed.
- **`aipack init` auto-fetches registry**: Initialization now fetches the default registry so packs are discoverable immediately.
- **`aipack doctor` git check**: New `git_available` warning check detects missing git or Xcode Command Line Tools on macOS.
- **Actionable git error hints**: Common failures (HTTPS auth, SSH timeout on port 22, Xcode CLT missing) now include specific remediation steps.
- **Installer git warning**: `install.sh` warns if git is not available and suggests `xcode-select --install` on macOS.

### Changed

- `registry fetch` help and docs updated with SSH examples and `ssh://` scheme documentation.
- `registry remove` help now references `registry sources` for listing.
- README "First Use" section updated: `aipack init` auto-fetches, added registry-name install example.

## [0.4.0]

### Added

- **Multi-source registries**: `registry fetch <url>` saves each source independently under `~/.config/aipack/registries/` and records it in `sync-config.yaml` for future fetches.
- **Bare fetch iterates all sources**: `registry fetch` (no URL) fetches every source in `registry_sources`, falling back to the compiled-in default.
- **Git auto-detection**: URLs ending in `.git` or used with `--ref` are fetched via git clone. New `--ref`, `--path`, and `--name` flags for explicit git coordinates.
- **`registry remove <name>`**: Remove a registry source from sync-config and delete its cache.
- **Merged registry view**: `registry list` and `registry search` now merge local entries (highest priority) with cached sources in list order.
- **Public catalog seeded**: `aipack-core` and `essentials` packs added to the default registry.

### Changed

- `registry fetch` no longer merges into a single `registry.yaml` — each source is cached as a separate file. Existing local `registry.yaml` entries are still honored at highest priority.
- `--prune` is deprecated (emits a notice). Cached registries are overwritten on each fetch.
- Profile docs updated to schema v2.

### Removed

- `--registry` flag on `registry fetch` (single-file merge target). `--registry` on `list`/`search` is retained for single-file override mode.

## [0.3.0]

- Initial release
