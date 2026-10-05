#!/bin/sh
# Re-records the original's effects for the Go replay test: python3 port/gen-cases.py first, then this, from components/goal.
set -e
cd "$(dirname "$0")/drive"
TZ=UTC node --experimental-strip-types drive.mjs cases.json ../../extensions/pi-goal-x/testdata/drive-golden.json
cp cases.json ../../extensions/pi-goal-x/testdata/drive-cases.json
