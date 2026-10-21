#!/bin/bash
# eqcopy.sh <out>: copy the Go extension with the original's names (pt- commands, pi-tweaks-settings.json),
# the only way the same scenario can drive the original and the port (see port/PORT.md, "Equivalence runs").
set -euo pipefail
src="$(cd "$(dirname "$0")/../extensions/pig-model-tweaks" && pwd)"
out="$1"
rm -rf "$out"; mkdir -p "$out"
cp -R "$src"/. "$out"/
sed -i 's|^const rememberModelName = "pmt-remember-model"|const rememberModelName = "pt-remember-model"|' "$out/remember_model.go"
sed -i 's|^const modelGuardName = "pmt-model-guard-pref"|const modelGuardName = "pt-model-guard-pref"|' "$out/model_guard.go"
sed -i 's|^const lockProviderName = "pmt-openrouter-lock-provider"|const lockProviderName = "pt-openrouter-lock-provider"|' "$out/openrouter_lock.go"
sed -i 's|^const settingsFileName = "pmt-settings.json"|const settingsFileName = "pi-tweaks-settings.json"|' "$out/store.go"
grep -c '"pt-remember-model"\|"pt-model-guard-pref"\|"pt-openrouter-lock-provider"\|"pi-tweaks-settings.json"' "$out"/*.go | grep -v ':0' | wc -l | grep -qx 4
