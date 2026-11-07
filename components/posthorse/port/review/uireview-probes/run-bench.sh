#!/bin/sh
# uireview: run the review benchmarks inside the container.
cd /home/kinsy/ph-work/pigpen/components/posthorse/extensions/posthorse || exit 1
go test -count=1 -run 'TestUiSafeCutScripts' -v -bench 'BenchmarkUiLeadingRows' -benchtime=200x . < /dev/null 2>&1 | grep -v '^=== RUN'
