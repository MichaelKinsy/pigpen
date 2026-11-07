#!/bin/sh
# uireview: run the differential tests inside the container: sh run-go.sh 'TestUi' (needs run-ts.sh first) [extra go test args]
cd /home/kinsy/ph-work/pigpen/components/posthorse/extensions/posthorse || exit 1
pat=$1; shift
go test -count=1 -run "$pat" -v "$@" . > /home/kinsy/ph-work/scratch/uireview/go.log 2>&1 < /dev/null
echo "exit $?"
tail -c 30000 /home/kinsy/ph-work/scratch/uireview/go.log
