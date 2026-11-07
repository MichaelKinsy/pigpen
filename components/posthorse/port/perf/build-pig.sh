#!/usr/bin/env bash
# build-pig.sh <commit> [name]: inside the container (`ph run "port/perf/build-pig.sh <commit> sdkframe"`), build a second pig from a PiG
# commit into $PH_PERF_SCRATCH/bin/pig-<name> from a private shallow checkout (never touches ~/ph-work/PiG), and stage that pig's SDK
# with an isolated PIG_HOME. Prints the two variables run.sh sf needs.
set -euo pipefail
commit=${1:?usage: build-pig.sh <commit> [name]}; name=${2:-sdkframe}
S=${PH_PERF_SCRATCH:-/home/kinsy/ph-work/scratch/PerfLane}
mkdir -p "$S/bin"
src=$S/pig-$name
if [ ! -d "$src/.git" ]; then mkdir -p "$src"; git -C "$src" init -q; git -C "$src" remote add origin https://github.com/MichaelKinsy/PiG.git; fi
git -C "$src" fetch -q --depth 1 origin "$commit"
git -C "$src" checkout -q FETCH_HEAD
test "$(git -C "$src" rev-parse HEAD)" = "$commit" || { echo "checkout is not $commit" >&2; exit 1; }
# Same build as the reference pig ($PIGEQ_PIG): trimpath, default cgo, symbols kept; the checkout's own go.work.
(cd "$src" && env -u GOWORK -u GOFLAGS go build -trimpath -o "$S/bin/pig-$name" ./cmd/pig)
mkdir -p "$S/sdkhome-$name" "$S/sdkagent-$name" "$S/sdkhome-$name-home"
sdk=$(env HOME="$S/sdkhome-$name-home" PIG_HOME="$S/sdkhome-$name" PIG_CODING_AGENT_DIR="$S/sdkagent-$name" PI_CODING_AGENT_DIR="$S/sdkagent-$name" "$S/bin/pig-$name" reload --sdk-path | tail -1)
echo "PH_PERF_PIG_SF=$S/bin/pig-$name"
echo "PH_PERF_SDK_SF=$sdk"
