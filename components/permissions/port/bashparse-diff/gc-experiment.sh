#!/bin/bash
# Go parse latency on 1 CPU, 4 CPUs, with the collector on and off, to see how much of it is collector work.
set -u
cd /work/components/permissions/extensions/pi-permission-system
printf 'go 1.26\nuse /work/components/permissions/extensions/pi-permission-system\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "${PIG_SDK_DIR:-/pigsrc/extensions/sdk}" > /tmp/go.work.bp
export GOWORK=/tmp/go.work.bp
go test -c -o /tmp/bp6.test ./bashparse 2>&1 | tail -3
cd bashparse
for cpus in 130 130,131 130,131,133,134 130-137; do
  for gogc in 100 off; do
    echo "== cpus=$cpus GOGC=$gogc"
    GOGC=$gogc taskset -c $cpus /tmp/bp6.test -test.run XXX -test.bench 'BenchmarkParse' -test.benchtime 300ms 2>&1 | grep -E "^Benchmark"
  done
done
echo "== done"
