#!/bin/bash
# CPU and allocation profile of the Go parse, one input at a time (tables load excluded from the allocation view).
#   PP_DIR=<dir> ~/pp-perm-run.sh "bash /work/components/permissions/port/bashparse-diff/prof3.sh [input ...]" > log
set -u
cd /work/components/permissions/extensions/pi-permission-system
printf 'go 1.26\nuse /work/components/permissions/extensions/pi-permission-system\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n' "${PIG_SDK_DIR:-/pigsrc/extensions/sdk}" > /tmp/go.work.bp
export GOWORK=/tmp/go.work.bp
cd bashparse
go test -c -o /tmp/bp5.test . 2>&1 | tail -3
for name in ${@:-pipeline-heredoc-500 script-5k}; do
  echo "######## $name"
  /tmp/bp5.test -test.run XXX -test.bench "BenchmarkParse/$name\$" -test.benchtime 2s -test.cpuprofile /tmp/cpu5.out 2>&1 | grep -E "^Bench"
  go tool pprof -top -nodecount 28 /tmp/bp5.test /tmp/cpu5.out 2>&1 | sed -n 5,36p
  if test -n "${LISTFN:-}"; then go tool pprof -list "$LISTFN" /tmp/bp5.test /tmp/cpu5.out 2>&1 | grep -vE "^\s+\.\s+\.\s" | head -80; fi
  /tmp/bp5.test -test.run XXX -test.bench "BenchmarkParse/$name\$" -test.benchtime 100x -test.memprofile /tmp/mem5.out -test.memprofilerate 1 >/dev/null 2>&1
  echo "-- alloc bytes (all, incl. the one table load)"
  go tool pprof -sample_index=alloc_space -ignore=loadLanguage -top -nodecount 14 /tmp/bp5.test /tmp/mem5.out 2>&1 | sed -n 5,22p
  echo "-- alloc objects"
  go tool pprof -sample_index=alloc_objects -ignore=loadLanguage -top -nodecount 14 /tmp/bp5.test /tmp/mem5.out 2>&1 | sed -n 5,22p
done
echo "== done"
