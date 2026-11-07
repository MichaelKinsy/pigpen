#!/usr/bin/env bash
# run-lane.sh: run the LangSmith scenarios on the original under Pi, the original under PiG's Node runtime and the Go
# port under PiG, each against its own mockls, then compare the recorded wire requests with lsdiff and the host
# traces with `pigeq diff`.
#
#   run-lane.sh [-s SCENARIO_DIR] [-o OUT_DIR] [-n name,name] [-l pi,pig-node,pig-go] [-p PORT] [-k]
#
# Needs (as inside `ls run`): go, node, pigeq on PATH; PIGEQ_PI and PIGEQ_PIG (hosts). Optional: LS_ORACLE (the
# original's dist/index.js next to a node_modules with langsmith), LS_GOEXT (the Go port's directory).
# Scenario options: lane/lsdiff-flags.txt (generated: `name flags` lines, e.g. -unordered-drain for two replica destinations);
# <name>.expect next to a scenario = it must fail to load in every lane (see below).
# Output (OUT_DIR, default ./lane-out): <lane>/<scenario>.requests.jsonl (mockls log), <lane>/traces/ (pigeq traces),
# <lane>/<scenario>.pigeq.log, report.txt. Exit 0 only when every compared pair is identical.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
port_dir=$(cd "$here/.." && pwd)
scn_dir=$port_dir/scenarios
out=$PWD/lane-out
only=""
lanes=pi,pig-node,pig-go
mock_port=39631   # the first scenario; scenario N gets mock_port+N (one port for all lanes of a scenario)
keep=""
while getopts "s:o:n:l:p:k" opt; do
  case $opt in
    s) scn_dir=$(cd "$OPTARG" && pwd) ;;
    o) out=$OPTARG ;;
    n) only=$OPTARG ;;
    l) lanes=$OPTARG ;;
    p) mock_port=$OPTARG ;;
    k) keep=--keep ;;
    *) sed -n '2,13p' "$0"; exit 2 ;;
  esac
done
oracle=${LS_ORACLE:-/home/kinsy/ls-work/oracle-run/dist/index.js}
goext=${LS_GOEXT:-$port_dir/../extensions/langsmith}
mkdir -p "$out/bin" "$out/scn"
out=$(cd "$out" && pwd)
base_port=$mock_port

for need in go node pigeq; do command -v $need >/dev/null || { echo "run-lane: $need not on PATH" >&2; exit 2; }; done
[ -n "${PIGEQ_PIG:-}" ] || { echo "run-lane: PIGEQ_PIG is not set" >&2; exit 2; }
case ",$lanes," in *,pi,*) [ -n "${PIGEQ_PI:-}" ] || { echo "run-lane: PIGEQ_PI is not set" >&2; exit 2; } ;; esac

(cd "$here" && GOWORK=off GOFLAGS=-mod=mod go build -o "$out/bin/" ./cmd/mockls ./cmd/lsdiff) || exit 2

lane_host()   { case $1 in pi) echo pi ;; *) echo pig ;; esac; }
lane_bin()    { case $1 in pi) echo "$PIGEQ_PI" ;; *) echo "$PIGEQ_PIG" ;; esac; }
lane_ext()    { case $1 in pig-go) echo "$goext" ;; *) echo "$oracle" ;; esac; }

report=$out/report.txt
: >"$report"
say() { echo "$@" | tee -a "$report"; }
fail=0
mock_pid=""
trap '[ -n "$mock_pid" ] && kill "$mock_pid" 2>/dev/null' EXIT

stop_mock() {
  if [ -n "$mock_pid" ]; then
    kill -TERM "$mock_pid" 2>/dev/null
    wait "$mock_pid" 2>/dev/null
    mock_pid=""
  fi
}

idx=0
for f in "$scn_dir"/*.json; do
  name=$(basename "$f" .json)
  idx=$((idx + 1))
  # A distinct port per scenario: a host that outlives its lane (a slow flush after a failed lane) cannot post into the next scenario's log.
  mock_port=$((base_port + idx)); base="http://127.0.0.1:$mock_port"
  if [ -n "$only" ]; then case ",$only," in *,"$name",*) ;; *) continue ;; esac; fi
  expect=${f%.json}.expect
  node "$here/lane/materialise.mjs" "$f" "$out/scn/$name.json" "$out/scn/$name.mockls" "$base" || { say "SKIP $name: materialise failed"; fail=1; continue; }
  # A scenario with a .expect file (an extended regexp) must FAIL TO LOAD in every lane, with a message matching it (the original's
  # config schema rejecting an option, the port refusing it): no wire to compare, only that all lanes refuse the same way.
  if [ -f "$expect" ]; then
    re=$(cat "$expect")
    for lane in ${lanes//,/ }; do
      mkdir -p "$out/$lane/traces"
      pigeq lane --scenarios "$out/scn/$name.json" --host "$(lane_host "$lane")" --bin "$(lane_bin "$lane")" \
        --ext "$(lane_ext "$lane")" --out "$out/$lane/traces" $keep >"$out/$lane/$name.pigeq.log" 2>&1
      rc=$?
      msg=$(cat "$out/$lane/$name.pigeq.log" "$out/$lane/traces/$name"/*.jsonl 2>/dev/null | grep -E -i -m1 -o ".{0,60}($re).{0,120}")
      if [ $rc -ne 0 ] && [ -n "$msg" ]; then
        say "REFUSED-AS-EXPECTED $name [$lane]: ...$(echo "$msg" | tr '\n' ' ')"
      else
        say "NOT-REFUSED $name [$lane]: pigeq lane exit $rc, no message matching /$re/ (see $lane/$name.pigeq.log)"
        fail=1
      fi
    done
    continue
  fi
  read -r -a flagarr <<<"$(awk -v n="$name" '$1 == n { $1 = ""; print }' "$here/lane/lsdiff-flags.txt" 2>/dev/null)"
  for lane in ${lanes//,/ }; do
    mkdir -p "$out/$lane/traces"
    rm -f "$out/$lane/port"
    "$out/bin/mockls" -addr "127.0.0.1:$mock_port" -config "$out/scn/$name.mockls" -log "$out/$lane/$name.requests.jsonl" \
      -port-file "$out/$lane/port" >"$out/$lane/$name.mockls.log" 2>&1 &
    mock_pid=$!
    for _ in $(seq 1 100); do [ -s "$out/$lane/port" ] && break; kill -0 "$mock_pid" 2>/dev/null || break; sleep 0.05; done
    if ! [ -s "$out/$lane/port" ]; then
      say "FAIL $name [$lane]: mockls did not start: $(cat "$out/$lane/$name.mockls.log")"
      fail=1; stop_mock; continue
    fi
    pigeq lane --scenarios "$out/scn/$name.json" --host "$(lane_host "$lane")" --bin "$(lane_bin "$lane")" \
      --ext "$(lane_ext "$lane")" --out "$out/$lane/traces" $keep >"$out/$lane/$name.pigeq.log" 2>&1
    rc=$?
    stop_mock
    if [ $rc -ne 0 ]; then
      say "LANE-FAIL $name [$lane]: pigeq lane exit $rc (see $lane/$name.pigeq.log)"
      fail=1
    fi
  done
  for pair in pi:pig-node pi:pig-go pig-node:pig-go; do
    a=${pair%%:*}; b=${pair##*:}
    case ",$lanes," in *,"$a",*) ;; *) continue ;; esac
    case ",$lanes," in *,"$b",*) ;; *) continue ;; esac
    [ -f "$out/$a/$name.requests.jsonl" ] && [ -f "$out/$b/$name.requests.jsonl" ] || continue
    hp=""; [ "$a" = pi ] && hp=-host-prompt
    if "$out/bin/lsdiff" $hp ${LSDIFF_FLAGS:-} "${flagarr[@]}" "$out/$a/$name.requests.jsonl" "$out/$b/$name.requests.jsonl" >"$out/$a-vs-$b.$name.lsdiff.txt" 2>&1; then
      say "WIRE-SAME $name [$a = $b] ($(sed -n 1p "$out/$a-vs-$b.$name.lsdiff.txt" | cut -c1-80))"
    else
      say "WIRE-DIFF $name [$a vs $b]: $(sed -n 2,4p "$out/$a-vs-$b.$name.lsdiff.txt" | tr '\n' ' ' | cut -c1-300)  -> $a-vs-$b.$name.lsdiff.txt"
      fail=1
    fi
    if [ "$a" = pi ] && grep -q '"servers"' "$out/scn/$name.json"; then
      # A custom model server: its recorded request body holds the host's system prompt, which differs between Pi and PiG.
      say "TRACE-SKIP $name [$a vs $b]: the scenario has a model server (host prompt in its recorded requests); the wire is what is compared"
      continue
    fi
    ta=$(ls "$out/$a/traces/$name"/*.jsonl 2>/dev/null | head -1); tb=$(ls "$out/$b/traces/$name"/*.jsonl 2>/dev/null | head -1)
    if [ -n "$ta" ] && [ -n "$tb" ]; then
      if pigeq diff "$ta" "$tb" >"$out/$a-vs-$b.$name.pigeqdiff.txt" 2>&1; then
        say "TRACE-SAME $name [$a = $b]"
      else
        say "TRACE-DIFF $name [$a vs $b]: $(head -c 300 "$out/$a-vs-$b.$name.pigeqdiff.txt" | tr '\n' ' ')"
        fail=1
      fi
    fi
  done
done
exit $fail
