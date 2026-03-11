#!/bin/sh

set -eu

REPO=${AIPACK_REPO:-shrug-labs/aipack}
VERSION=${VERSION:-latest}
BIN_DIR=${BIN_DIR:-}
PREFIX=${PREFIX:-}

usage() {
	cat <<'EOF'
Install aipack from GitHub Releases.

Usage:
  install.sh [--version <tag>] [--bin-dir <dir>] [--prefix <dir>] [--repo <owner/repo>]

Environment:
  VERSION       Release tag to install (default: latest stable release)
  BIN_DIR       Install directory for the aipack binary
  PREFIX        Install prefix; installs to <prefix>/bin
  AIPACK_REPO   GitHub repo in owner/name form (default: shrug-labs/aipack)

Examples:
  curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | sh
  curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | VERSION=v0.3.0 sh
  curl -fsSL https://raw.githubusercontent.com/shrug-labs/aipack/main/install.sh | BIN_DIR=$HOME/.local/bin sh
EOF
}

log() {
	printf '%s\n' "$*" >&2
}

fail() {
	log "error: $*"
	exit 1
}

need_cmd() {
	command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

normalize_version() {
	case "$1" in
		latest) printf '%s\n' "$1" ;;
		v*) printf '%s\n' "$1" ;;
		*) printf 'v%s\n' "$1" ;;
	esac
}

detect_os() {
	case "$(uname -s)" in
		Darwin) printf 'darwin\n' ;;
		Linux) printf 'linux\n' ;;
		*) fail "unsupported operating system: $(uname -s)" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
		x86_64|amd64) printf 'amd64\n' ;;
		arm64|aarch64)
			if [ "$(detect_os)" = "darwin" ]; then
				printf 'arm64\n'
			else
				fail "unsupported architecture for published releases: $(uname -m)"
			fi
			;;
		*) fail "unsupported architecture: $(uname -m)" ;;
	esac
}

resolve_bin_dir() {
	if [ -n "$BIN_DIR" ]; then
		printf '%s\n' "$BIN_DIR"
		return
	fi

	if [ -n "$PREFIX" ]; then
		printf '%s/bin\n' "$PREFIX"
		return
	fi

	if [ -d "/usr/local/bin" ] && [ -w "/usr/local/bin" ]; then
		printf '/usr/local/bin\n'
		return
	fi

	if [ ! -e "/usr/local/bin" ] && [ -d "/usr/local" ] && [ -w "/usr/local" ]; then
		printf '/usr/local/bin\n'
		return
	fi

	printf '%s/.local/bin\n' "$HOME"
}

download() {
	url=$1
	dest=$2
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 --output "$dest" "$url"
		return
	fi
	if command -v wget >/dev/null 2>&1; then
		wget -qO "$dest" "$url"
		return
	fi
	fail "install requires curl or wget"
}

sha256_file() {
	file=$1
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$file" | awk '{print $1}'
		return
	fi
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$file" | awk '{print $1}'
		return
	fi
	fail "install requires sha256sum or shasum"
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--version)
			[ "$#" -ge 2 ] || fail "--version requires a value"
			VERSION=$2
			shift 2
			;;
		--bin-dir)
			[ "$#" -ge 2 ] || fail "--bin-dir requires a value"
			BIN_DIR=$2
			shift 2
			;;
		--prefix)
			[ "$#" -ge 2 ] || fail "--prefix requires a value"
			PREFIX=$2
			shift 2
			;;
		--repo)
			[ "$#" -ge 2 ] || fail "--repo requires a value"
			REPO=$2
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			fail "unknown argument: $1"
			;;
	esac
done

need_cmd uname
need_cmd awk
need_cmd mktemp
need_cmd mkdir
need_cmd install

OS=$(detect_os)
ARCH=$(detect_arch)
ARTIFACT="aipack-${OS}-${ARCH}"
TAG=$(normalize_version "$VERSION")
TARGET_BIN_DIR=$(resolve_bin_dir)

TMPDIR=$(mktemp -d)
cleanup() {
	rm -rf "$TMPDIR"
}
trap cleanup EXIT INT TERM

if [ "$TAG" = "latest" ]; then
	BASE_URL="https://github.com/${REPO}/releases/latest/download"
	DISPLAY_VERSION="latest"
else
	BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"
	DISPLAY_VERSION="$TAG"
fi

ARTIFACT_PATH="$TMPDIR/$ARTIFACT"
CHECKSUMS_PATH="$TMPDIR/SHA256SUMS"

log "Downloading ${ARTIFACT} (${DISPLAY_VERSION}) from ${REPO}..."
download "${BASE_URL}/${ARTIFACT}" "$ARTIFACT_PATH"
download "${BASE_URL}/SHA256SUMS" "$CHECKSUMS_PATH"

EXPECTED_SUM=$(awk -v file="$ARTIFACT" '$2 == file { print $1 }' "$CHECKSUMS_PATH")
[ -n "$EXPECTED_SUM" ] || fail "could not find checksum for ${ARTIFACT}"

ACTUAL_SUM=$(sha256_file "$ARTIFACT_PATH")
[ "$EXPECTED_SUM" = "$ACTUAL_SUM" ] || fail "checksum mismatch for ${ARTIFACT}"

mkdir -p "$TARGET_BIN_DIR"
[ -w "$TARGET_BIN_DIR" ] || fail "install directory is not writable: $TARGET_BIN_DIR"

install -m 0755 "$ARTIFACT_PATH" "$TARGET_BIN_DIR/aipack"

log "Installed aipack to ${TARGET_BIN_DIR}/aipack"
"$TARGET_BIN_DIR/aipack" version

case ":$PATH:" in
	*":$TARGET_BIN_DIR:"*) ;;
	*) log "Add ${TARGET_BIN_DIR} to your PATH if it is not already present." ;;
esac
