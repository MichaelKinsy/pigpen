#!/usr/bin/env bash
# One command to measure: port/perf/run.sh go|ts|both [extra perf.py run flags, e.g. --samples 15 --workloads a,b,e]
#   ts    pig running the TS original (dist/index.js, langsmith SDK 0.10.5) in its Node extension runtime (side "ts"), and Pi 1.5.0 (side "pi")
#   go    pig running the Go port (side "go")
#   both  go, ts and pi interleaved sample by sample: the authoritative comparison
#   collect <name>  wait for a remote run whose local run.sh was interrupted (the remote job keeps running), fetch and summarize it
# Runs on xd6702 in the tern-ci container through ~/work/ls-tools/ls (rsync of the worktree, nice 10, LS_CPUS=3), then copies
# the raw data back to port/perf/data/<name>/ and prints the summary. The no-extension baselines (nopig, nopi) run in workloads a and c.
# Needs the Mac worktree; never touches real ~/.pig or ~/.pi. LS_PERF_NAME renames the data directory.
set -euo pipefail
mode=${1:-}; shift || true
here=$(cd "$(dirname "$0")" && pwd)
remote_scratch=/home/kinsy/ls-work/scratch/LsPerf
case "$mode" in
  collect)
    name=${1:?usage: run.sh collect <name>}
    while ssh -n xd6702 "pgrep -f 'perf.py run --out [^ ]*/out/$name( |\$)' >/dev/null"; do sleep 20; done
    mkdir -p "$here/data/$name"
    rsync -a xd6702:ls-work/scratch/LsPerf/out/$name/ "$here/data/$name/"
    python3 "$here/perf.py" summarize "$here/data/$name" >/dev/null || true
    echo "data: $here/data/$name"; exit 0 ;;
  ts) sides=ts,pi; name=${LS_PERF_NAME:-ts} ;;
  go) sides=go; name=${LS_PERF_NAME:-go} ;;
  both) sides=go,ts,pi; name=${LS_PERF_NAME:-both} ;;
  *) echo "usage: run.sh go|ts|both|collect <name> [perf.py run flags]" >&2; exit 2 ;;
esac
wt=${LS_WT:-$(cd "$here/../../../.." && pwd)}
remote_out=$remote_scratch/out/$name
data="$here/data/$name"
# The remote copy lives under the scratch tree: `ls sync` rsyncs --delete into components/langsmith and would erase data kept there.
LS_WT=$wt LS_CPUS=${LS_CPUS:-3} ~/work/ls-tools/ls run "mkdir -p $remote_out && rm -f $remote_out/samples.jsonl && python3 components/langsmith/port/perf/perf.py run --out $remote_out --sides $sides --baselines $*; rc=\$?; python3 components/langsmith/port/perf/perf.py summarize $remote_out >/dev/null || true; exit \$rc"
mkdir -p "$data"
rsync -a xd6702:ls-work/scratch/LsPerf/out/$name/ "$data/"
python3 "$here/perf.py" summarize "$data" > /dev/null || true   # re-summarize locally: the remote summary ran the code as synced
echo "data: $data"
head -c 30000 "$data/summary.md"
