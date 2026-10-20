#!/bin/bash
# Runs the bp-* mutations (or those named in $1, a name prefix) with pigeq mutate --unit-only.
set -u
cd /work/components/permissions
python3 - "${1:-bp-}" <<'PY'
import json, sys
m = json.load(open("port/mutations.json"))
json.dump([x for x in m if x["name"].startswith(sys.argv[1])], open("/tmp/bpmut.json", "w"))
PY
/work/pigeq-bin mutate --unit-only --go extensions/pi-permission-system --mutations /tmp/bpmut.json --sdk-dir "${PIG_SDK_DIR:-/pigsrc}" --jobs 4 2>&1 | tail -25
echo "== done"
