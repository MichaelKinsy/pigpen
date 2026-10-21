#!/bin/bash
# Records the goldens (see record.ts). Usage: PI_SRC=/path/to/pi-checkout ./record.sh [scenario-name ...]
# PI_SRC is a built checkout of Pi at commit 1402973a9d (packages/tui and packages/coding-agent with dist/, and its
# node_modules); it is the oracle's pi-tui (not on npm). The oracle's own dependencies (croner, nanoid) are installed
# into ../../oracle/node_modules the way the harness does (npm install --omit=dev --ignore-scripts).
set -euo pipefail
cd "$(dirname "$0")"
: "${PI_SRC:?set PI_SRC to a built Pi checkout at 1402973a9d}"
PI_COMMIT="$(git -C "$PI_SRC" rev-parse --short=10 HEAD)"
if [ "$PI_COMMIT" != "1402973a9d" ]; then echo "PI_SRC is at $PI_COMMIT, not 1402973a9d" >&2; exit 1; fi
npm ci --no-audit --no-fund >/dev/null
ORACLE="$(cd ../../oracle && pwd)"
if [ ! -d "$ORACLE/node_modules/croner" ]; then
	(cd "$ORACLE" && npm install --omit=dev --ignore-scripts --no-audit --no-fund --legacy-peer-deps >/dev/null)
fi
mkdir -p "$ORACLE/node_modules/@earendil-works"
for p in ai:pi-ai coding-agent:pi-coding-agent tui:pi-tui; do
	ln -sfn "$PI_SRC/packages/${p%%:*}" "$ORACLE/node_modules/@earendil-works/${p##*:}"
done
# The oracle's package is CommonJS to Node; Pi's packages only export an "import" condition, so the sources are loaded as ESM
# through a temporary package.json that is removed on exit (the oracle stays unmodified).
echo '{"type":"module"}' >"$ORACLE/src/package.json"
SCRATCH="$(mktemp -d -t sp-frames-XXXXXX)"
trap 'rm -rf "$SCRATCH" "$ORACLE/src/package.json"' EXIT
env -u LANG -u LC_ALL -u LC_MESSAGES -u LANGUAGE -u EDITOR -u VISUAL \
	TZ=UTC HOME="$SCRATCH" PI_COMMIT="$PI_COMMIT" SCRATCH="$SCRATCH" \
	node_modules/.bin/tsx record.ts "$@"
