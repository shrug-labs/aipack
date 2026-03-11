# Changelog

All notable user-facing changes to `aipack` will be documented in this file.

The format is based on Keep a Changelog, and releases use semantic versioning tags.

## [Unreleased]

### Added
- GitHub Actions CI for test, vet, and tagged release assets
- Release binaries as the primary public install path
- `install.sh` for checksum-verified macOS and Linux installs from GitHub Releases
- Homebrew tap automation for stable releases via `dfoster-oracle/homebrew-tap`
- Public registry bootstrap with a stable default `registry.yaml`
- `make fmt`, `make fmt-check`, and `make release-tag-check`

### Changed
- Release policy now treats `VERSION` as the source of truth for release tags
- Public docs now describe checksum verification and release process references
