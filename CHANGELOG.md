# Changelog

All notable user-facing changes to `aipack` will be documented in this file.

The format is based on Keep a Changelog, and releases use semantic versioning tags.

## Unreleased

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
