#!/usr/bin/env bash
set -Eeuo pipefail

# install.sh — build localrouter from source and install the binary.
#
#   PREFIX="$HOME/.local" ./install.sh   # user-local, no sudo
#   sudo ./install.sh                    # system-wide (defaults to /usr/local)
#
# Requires Go 1.22+. Works on Linux and macOS.

PREFIX="${PREFIX:-/usr/local}"
BINDIR="$PREFIX/bin"
SOURCE_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v go >/dev/null 2>&1; then
    printf 'error: go is not on PATH. Install Go 1.22+ and retry.\n' >&2
    exit 1
fi

BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR"' EXIT

(
    cd "$SOURCE_DIR"
    go build -trimpath -o "$BUILD_DIR/localrouter" ./cmd/localrouter
)

install -d "$BINDIR"
install -m 0755 "$BUILD_DIR/localrouter" "$BINDIR/localrouter"

printf 'Installed localrouter to %s/localrouter\n' "$BINDIR"
printf 'Run: localrouter init-config\n'
