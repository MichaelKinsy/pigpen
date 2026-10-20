#!/bin/bash
# Build the pig-popular Piglet (it selects pi-permission-system) as a linux/amd64 binary with the shared pig, from the
# shallow clone of the PiG v0.5.0 tag (the build wants a git checkout of the source the pig was built from; /pigsrc holds only the SDK).
#   PP_DIR=<dir> ~/pp-perm-run.sh "bash /work/components/permissions/port/bashparse-diff/piglet-build.sh" > log
set -u
export HOME=/tmp/pp-iso-home PIG_HOME=/tmp/pp-iso-home/.pig PIG_CODING_AGENT_DIR=/tmp/pp-iso-home/agent PI_CODING_AGENT_DIR=/tmp/pp-iso-home/agent
rm -rf "$HOME"; mkdir -p "$HOME" "$PIG_HOME" "$PIG_CODING_AGENT_DIR"
SRC=/tmp/pigsrc-git
rm -rf "$SRC"; git clone -q --depth 1 --branch v0.5.0 https://github.com/MichaelKinsy/PiG.git "$SRC" && git -C "$SRC" rev-parse HEAD
export PIG_BIN=/opt/pp-shared-pig/bin/pig PIG_SOURCE_ROOT=$SRC PIGPEN_BIN_DIR=/tmp/pp-bin GOFLAGS=-buildvcs=false
mkdir -p /tmp/pp-bin
cd /work
cat scripts/pig-requirement.json; $PIG_BIN --version
echo "== npm run build:piglet -- pig-popular"
time npm run build:piglet -- pig-popular --replace -- --targets linux/amd64 2>&1 | tail -40
ls -l /tmp/pp-bin/ 2>&1
/tmp/pp-bin/pig-popular --version 2>&1 | head -3
echo "== done"
