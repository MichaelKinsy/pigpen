#!/usr/bin/env bash
# sdkdiff-regen.sh: regenerate the golden request logs of port/sdkdiff by running every op script through the real
# JavaScript SDK (sdkdrive.mjs) against a fresh mockls. Needs go and node (inside `ls run`) and the vendored SDK
# (default /home/kinsy/ls-work/oracle-run/node_modules/langsmith, override with LS_SDK).
#   ls run "cd components/langsmith/port/tools && node gen/sdkdiff-ops.mjs && ./sdkdiff-regen.sh [name ...]"
# GOLDEN_OUT (default port/sdkdiff/golden) is where the logs go: `ls run` syncs the worktree over the remote tree with --delete, so
# point it at a scratch directory and copy it back (rsync xd6702:<dir>/ port/sdkdiff/golden/).
set -euo pipefail
bad=0
here=$(cd "$(dirname "$0")" && pwd)
dir=$here/../sdkdiff
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
(cd "$here" && GOWORK=off GOFLAGS=-mod=mod go build -o "$bin/" ./cmd/mockls ./cmd/lsdiff)
# VERIFY=1: run the scripts again into a temp directory and lsdiff each log against the committed golden (the JavaScript SDK against
# itself: proves the normalisations cover everything that varies between two runs). Nothing is overwritten.
if [ "${VERIFY:-}" = 1 ]; then
  out=$bin/verify
else
  out=${GOLDEN_OUT:-$dir/golden}
fi
mkdir -p "$out"
names=("$@")
if [ ${#names[@]} -eq 0 ]; then
  for f in "$dir"/ops/*.json; do names+=("$(basename "$f" .json)"); done
fi
for n in "${names[@]}"; do
  node "$here/sdkdrive.mjs" --ops "$dir/ops/$n.json" --mockls "$bin/mockls" --log "$out/$n.jsonl" \
    --console "$out/$n.result.json" >/dev/null 2>"$bin/$n.stderr" || { echo "FAIL $n"; cat "$bin/$n.stderr"; exit 1; }
  if [ "${VERIFY:-}" = 1 ]; then
    if "$bin/lsdiff" -path-prefix "$(sed -n 's/.*"endpoint": "\(.*\)",/\1/p' "$dir/golden/$n.result.json")" -path-prefix "$(sed -n 's/.*"endpoint": "\(.*\)",/\1/p' "$out/$n.result.json")" "$dir/golden/$n.jsonl" "$out/$n.jsonl" >"$bin/$n.diff" 2>&1; then
      echo "same $n"
    else
      echo "DIFFERENT $n"; cat "$bin/$n.diff"; bad=1
    fi
    continue
  fi
  echo "ok $n ($(grep -c . "$out/$n.jsonl") requests)"
done
exit $bad
