# Changelog

All notable user-facing changes to `aipack` will be documented in this file.

The format is based on Keep a Changelog, and releases use semantic versioning tags.

## Unreleased

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
