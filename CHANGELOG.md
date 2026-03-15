# Changelog

All notable user-facing changes to `aipack` will be documented in this file.

The format is based on Keep a Changelog, and releases use semantic versioning tags.

## [Unreleased]

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
