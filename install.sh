#!/usr/bin/env bash
set -Eeuo pipefail

PREFIX="${PREFIX:-/usr/local}"
BINDIR="$PREFIX/bin"
SOURCE_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

install -d "$BINDIR"
install -m 0755 "$SOURCE_DIR/bin/localrouter" "$BINDIR/localrouter"

if [[ "${INSTALL_WIN_AI_COMPAT:-1}" == "1" ]]; then
    if [[ -e "$BINDIR/win-ai" && ! -L "$BINDIR/win-ai" ]]; then
        backup="$BINDIR/win-ai.backup.$(date +%Y%m%d%H%M%S)"
        mv "$BINDIR/win-ai" "$backup"
        printf 'Backed up existing win-ai to %s\n' "$backup"
    fi
    ln -sf "$BINDIR/localrouter" "$BINDIR/win-ai"
fi

printf 'Installed localrouter to %s/localrouter\n' "$BINDIR"
printf 'Run: localrouter --init-config\n'
