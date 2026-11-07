#!/usr/bin/env bash
# One command to measure: port/perf/run.sh go|ts|both [extra perf.py run flags, e.g. --samples 15 --workloads a,b]
#   ts    pig 0.7.0+1.5.0 running the TS original in its Node extension runtime (side "ts"), and Pi 1.5.0 as the reference (side "pi")
#   go    pig running the Go port (side "go")
#   both  go, ts and pi interleaved sample by sample: the authoritative comparison for the ratchet
#   collect <name>  wait for a remote run whose local run.sh was interrupted (the remote job keeps running), fetch and summarize it
# Runs on xd6702 in the tern-ci container through ~/work/ph-tools/ph (rsync of the worktree, nice 10, 8 CPUs), then copies the
# raw data back to port/perf/data/<name>/ and prints the summary. Needs the Mac worktree; never touches real ~/.pig or ~/.pi.
set -euo pipefail
mode=${1:-}; shift || true
case "$mode" in
  collect) # run.sh collect <name>: wait for a remote run whose local `run.sh` was interrupted, then fetch and summarize it
    name=${1:?usage: run.sh collect <name>}
    here=$(cd "$(dirname "$0")" && pwd)
    while ssh xd6702 "pgrep -f 'perf.py run --out [^ ]*/out/$name( |\$)' >/dev/null"; do sleep 20; done
    mkdir -p "$here/data/$name"
    rsync -a xd6702:ph-work/scratch/PerfLane/out/$name/ "$here/data/$name/"
    python3 "$here/perf.py" summarize "$here/data/$name" >/dev/null || true
    echo "data: $here/data/$name"; exit 0 ;;
  ts) sides=ts,pi; name=${PH_PERF_NAME:-ts-baseline} ;;
  go) sides=go; name=${PH_PERF_NAME:-go} ;;
  both) sides=go,ts,pi; name=${PH_PERF_NAME:-both} ;;
  *) echo "usage: run.sh go|ts|both [perf.py run flags]" >&2; exit 2 ;;
esac
here=$(cd "$(dirname "$0")" && pwd)
wt=${PH_WT:-$(cd "$here/../../../.." && pwd)}
remote_out=/home/kinsy/ph-work/scratch/PerfLane/out/$name
data="$here/data/$name"
# The remote copy lives under the scratch tree: `ph sync` rsyncs --delete into components/posthorse and would erase data kept there.
PH_WT=$wt ~/work/ph-tools/ph run "mkdir -p $remote_out && ${PH_PERF_KEEP:+:} ${PH_PERF_KEEP:-rm -f $remote_out/samples.jsonl} && python3 components/posthorse/port/perf/perf.py run --out $remote_out --sides $sides $*; rc=\$?; python3 components/posthorse/port/perf/perf.py summarize $remote_out >/dev/null || true; exit \$rc"
mkdir -p "$data"
rsync -a xd6702:ph-work/scratch/PerfLane/out/$name/ "$data/"
python3 "$here/perf.py" summarize "$data" > /dev/null || true   # re-summarize locally: the remote summary ran the code as synced
echo "data: $data"
head -c 30000 "$data/summary.md"
