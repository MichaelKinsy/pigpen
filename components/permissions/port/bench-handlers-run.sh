#!/bin/bash
# interleaved Node-vs-Go rounds for the handlers/service probe; runs on xd670, CPUs 8-11, flock held only while a run executes
cd ~/pp-handlers
D='docker run --rm --cpuset-cpus=8-11 -u $(id -u):$(id -g) -e HOME=/work/home -e GOCACHE=/work/gocache -e GOMODCACHE=/work/gomod -e GOTOOLCHAIN=local -e GOFLAGS=-buildvcs=false -v $HOME/pp-handlers:/work -v $HOME/pp-permint:/bp:ro -v $HOME/tern-ci/passwd:/etc/passwd:ro -v $HOME/tern-ci/group:/etc/group:ro -w /work/w/components/permissions tern-ci:v3'
# build the Go test binary and stage the probe
./d.sh "export PIG_BIN=/work/bin/pig PIG_OFFLINE=1; node /work/gt.mjs test -c -o /work/hb.test ." 2>&1 | tail -3
./d.sh "cd /work/w/components/permissions/port/oracle && ln -sfn /bp/bp/components/permissions/port/oracle/node_modules node_modules; ln -sfn /bp/bp/components/permissions/port/oracle/packages/pi-permission-system/node_modules packages/pi-permission-system/node_modules"
cp w/components/permissions/port/probe/handlers-bench.test.ts w/components/permissions/port/oracle/packages/pi-permission-system/test/zz-handlers-bench.test.ts
for i in 1 2 3 4 5 6 7 8 9 10; do
  flock /tmp/perf-xd670.lock bash -c "$D bash -c 'cd /work/w/components/permissions/port/oracle/packages/pi-permission-system && node_modules/.bin/vitest run --configLoader native --testTimeout 300000 test/zz-handlers-bench.test.ts > /work/node.out 2>&1'"
  grep '^BENCH' node.out | sed 's/^/NODE /'
  flock /tmp/perf-xd670.lock bash -c "$D bash -c 'cd /work/w/components/permissions/extensions/pi-permission-system && /work/hb.test -test.run xxx -test.bench Handlers -test.benchtime 3000x -test.benchmem 2>&1 | grep ^Benchmark | sed s/^/GO\ /'"
done
rm -f w/components/permissions/port/oracle/packages/pi-permission-system/test/zz-handlers-bench.test.ts
echo BENCHDONE
