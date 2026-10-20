#!/bin/bash
# The interleaved measurement of the supervision slice (imladris layout: ~/pp-warden/supervision/{pp,pp-sec,pp-base} are repo copies of this tree, the tree below it and
# the earlier release; each holds components/warden). Rounds alternate: this tree, security-2, the earlier release, the Go benchmarks, the original under Node.
# Output: perf-go.log, perf-bench.log, perf-node.log; the medians are taken of the 10 rounds.
# Interleaved: per round, this tree, the previous release's tree, the Go benchmarks, the original under Node. Same CPU set.
cd ~/pp-warden/supervision
run() { PW_DIR=$HOME/pp-warden/supervision/$1 taskset -c 96-151 ~/pp-warden/run.sh ". /home/kinsy/env-supervision.sh; export PATH=/opt/pp-shared-pig/bin:\$PATH; $2"; }
W="cd components/warden/extensions/warden"
for t in pp pp-sec pp-base; do
  mkdir -p $t/components/warden/extensions/warden/zzprobe
  printf 'package main\n\nimport warden "github.com/MichaelKinsy/pigpen/warden"\n\nfunc main() { println(warden.Extension() != nil) }\n' > $t/components/warden/extensions/warden/zzprobe/main.go
  echo "SIZE $t $(run $t "$W && go build -ldflags '-s -w' -o /tmp/probe ./zzprobe && stat -c %s /tmp/probe" 2>&1 | tail -1)"
  rm -rf $t/components/warden/extensions/warden/zzprobe
done
rm -f perf-*.log; : > perf-run.log
for i in $(seq 1 10); do
  run pp "$W && WARDEN_PERF=1 go test -count=1 -run TestPerf -v . 2>&1 | grep ^PERF" | sed "s/^/mine /" >> perf-go.log
  run pp-sec "$W && WARDEN_PERF=1 go test -count=1 -run TestPerf -v . 2>&1 | grep ^PERF" | sed "s/^/sec /" >> perf-go.log
  run pp-base "$W && WARDEN_PERF=1 go test -count=1 -run TestPerf -v . 2>&1 | grep ^PERF" | sed "s/^/base /" >> perf-go.log
  run pp "$W && go test -count=1 -run xxx -bench 'WasteRecord|BuildStuckEvidence|ParseFailureTAP|TriageReportOffline' -benchtime=300ms -benchmem . 2>&1 | grep ^Benchmark" >> perf-bench.log
  run pp "cd components/warden/port && node /opt/pp-shared-pig/pi-src/node_modules/jiti/lib/jiti-cli.mjs perf/bench-oracle.ts 2>&1 | grep -E '^PERF|rror'" >> perf-node.log
done
echo "== done" >> perf-node.log
