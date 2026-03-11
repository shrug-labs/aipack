# Releasing aipack

## Release contract

- `VERSION` is the source of truth for the release line.
- Stable tags use `vMAJOR.MINOR.PATCH`.
- Prerelease tags use `vMAJOR.MINOR.PATCH-<suffix>`.
- A valid release tag must match the current `VERSION` file, with an optional prerelease suffix.

Examples:

- `VERSION=0.3.0` → valid stable tag: `v0.3.0`
- `VERSION=0.3.0` → valid prerelease tag: `v0.3.0-rc.1`

## Before tagging

1. Update `VERSION` if the release line changed.
2. Update `CHANGELOG.md`:
   - move shipped entries out of `Unreleased`
   - add a new section for the release tag
   - start a fresh `Unreleased` section for future work
3. Run verification:

   ```bash
   make fmt-check
   make test
   go vet ./...
   make dist
   ```

4. Verify release artifacts locally:

   ```bash
   cd dist
   sha256sum aipack-* > SHA256SUMS
   ```

   Then run the binary matching your current platform and confirm `aipack version`
   reports the expected release line.

5. Validate the intended tag against `VERSION`:

   ```bash
   make release-tag-check TAG=vX.Y.Z
   ```

   For a prerelease:

   ```bash
   make release-tag-check TAG=vX.Y.Z-rc.1
   ```

## Tag and publish

1. Create the release commit.
2. Create and push the git tag.
3. GitHub Actions will:
    - run formatting, test, and vet checks
    - verify the pushed tag matches `VERSION`
    - build `darwin/arm64`, `darwin/amd64`, and `linux/amd64` binaries
    - generate `SHA256SUMS`
    - publish GitHub Release assets
    - update `dfoster-oracle/homebrew-tap` for stable tags (requires `HOMEBREW_TAP_GITHUB_TOKEN`)

## After publish

1. Confirm the release page contains:
   - all three binaries
   - `SHA256SUMS`
   - generated release notes
2. Confirm the release is marked as a prerelease when the tag contains a prerelease suffix.
3. Download one asset and verify:

   ```bash
   shasum -a 256 -c SHA256SUMS --ignore-missing
   ```

   Then run the downloaded binary and confirm `aipack version` reports the
   published release line.

4. Run the installer against the published release:

   ```bash
   VERSION=vX.Y.Z BIN_DIR="$PWD/bin" ./install.sh
   ./bin/aipack version
   ```

5. For stable tags, confirm the Homebrew tap updated to the published version:

   ```bash
   brew install dfoster-oracle/tap/aipack
   brew info dfoster-oracle/tap/aipack
   ```

6. If install instructions changed, update `README.md` in the same release-prep change.
