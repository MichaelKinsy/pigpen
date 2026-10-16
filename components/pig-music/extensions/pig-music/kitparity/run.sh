#!/bin/bash
# kitparity/run.sh <PiG checkout>: pig-music's kit views drawn by PiG's own host renderer must be the screens' own rows
# (byte for byte; the Player screen's columns and the rows of tabs cell for cell, reported per width), and PiG must measure
# every character pig-music puts in a text node or a stage column as pig-music does. It uses the checkout's HEAD (a scratch
# clone) and leaves nothing behind.
#   1. pig-music's TestKitViewsReproduceTheScreens writes each screen's view, as the SDK of that PiG sends it, and its rows;
#   2. TestPigMusicKitParity, copied into the clone's coding/extension/host/subprocess, renders them with the host's renderer;
#   3. pig-music's TestKitParityHostRendered compares.
set -euo pipefail
pig=$(cd "${1:?usage: kitparity/run.sh <PiG checkout>}" && pwd -P)
here=$(cd "$(dirname "$0")" && pwd -P)
ext=$(dirname "$here")
# Normalized: a TMPDIR ending in a slash (macOS's default) would put a `//` in go.work, which reads it as a comment.
work=$(cd "$(mktemp -d "${TMPDIR:-/tmp}/kitparity.XXXXXX")" && pwd -P)
trap 'chmod -R u+w "$work" 2>/dev/null; rm -rf "$work"' EXIT
git clone -q --shared --no-checkout "$pig" "$work/pig"
git -C "$work/pig" checkout -q --detach "$(git -C "$pig" rev-parse HEAD)"
printf 'go 1.26.0\n\nuse (\n\t%s\n\t%s\n)\n' "$work/pig/extensions/sdk" "$ext" > "$work/go.work"
echo "PiG $(git -C "$work/pig" log --oneline -1)"
(cd "$ext" && GOWORK="$work/go.work" PIG_MUSIC_KITPARITY_DIR="$work" go test -count=1 -run '^TestKitViewsReproduceTheScreens$' .)
cp "$here/host_test.go.txt" "$work/pig/coding/extension/host/subprocess/zz_pigmusic_kitparity_test.go"
(cd "$work/pig" && GOWORK="$work/pig/go.work" PIG_MUSIC_KITPARITY_DIR="$work" go test -count=1 -v -run '^TestPigMusicKitParity$' ./coding/extension/host/subprocess/ | grep -E '^(---|ok|FAIL)|rendered')
(cd "$ext" && GOWORK="$work/go.work" PIG_MUSIC_KITPARITY_RENDERED="$work/rendered.json" go test -count=1 -v -run '^TestKitParityHostRendered$' . | grep -E '^(---|ok|FAIL)|screens:|PiG measures|row ')
