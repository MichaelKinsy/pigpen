#!/bin/bash
# Records the goldens (see record.ts). Usage: PI_SRC=/path/to/pi-checkout ./record.sh [scenario-name ...]
# PI_SRC is a built checkout of Pi at commit 1402973a9d (packages/tui and packages/coding-agent with dist/, and its
# node_modules); it is the oracle's pi-tui and pi-coding-agent (not on npm).
set -euo pipefail
cd "$(dirname "$0")"
: "${PI_SRC:?set PI_SRC to a built Pi checkout at 1402973a9d}"
PI_COMMIT="$(git -C "$PI_SRC" rev-parse --short=10 HEAD)"
if [ "$PI_COMMIT" != "1402973a9d" ]; then echo "PI_SRC is at $PI_COMMIT, not 1402973a9d" >&2; exit 1; fi
npm ci --no-audit --no-fund >/dev/null
rm -rf node_modules/@earendil-works
mkdir -p node_modules/@earendil-works
ln -sfn "$PI_SRC/packages/tui" node_modules/@earendil-works/pi-tui
ln -sfn "$PI_SRC/packages/coding-agent" node_modules/@earendil-works/pi-coding-agent
ORACLE="$(cd ../../oracle && pwd)"
LINKED=0
if [ ! -e "$ORACLE/node_modules" ]; then
	ln -s "$(pwd)/node_modules" "$ORACLE/node_modules"
	LINKED=1
fi
SCRATCH="$(mktemp -d -t auq-frames-XXXXXX)"
cleanup() {
	if [ "$LINKED" = 1 ]; then rm -f "$ORACLE/node_modules"; fi
	rm -rf "$SCRATCH"
}
trap cleanup EXIT
env -u LANG -u LC_ALL -u LC_MESSAGES -u LANGUAGE -u EDITOR -u VISUAL \
	TZ=UTC HOME="$SCRATCH" PI_CODING_AGENT_DIR="$SCRATCH/agent" PI_COMMIT="$PI_COMMIT" \
	node_modules/.bin/tsx record.ts "$@"
