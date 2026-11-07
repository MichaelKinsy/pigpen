#!/bin/sh
# usage: lanes.sh  (runs pi + pig lanes for all scenarios, diffs)
cd components/posthorse
S=/home/kinsy/ph-work/scratch/HarnessExt
rm -rf $S/pi $S/pig
pigeq lane --scenarios port/scenarios --host pi --bin $PIGEQ_PI --ext port/oracle/index.ts --out $S/pi 2>&1 | grep -v "events:"
pigeq lane --scenarios port/scenarios --host pig --bin $PIGEQ_PIG --ext port/oracle/index.ts --out $S/pig 2>&1 | grep -v "events:"
for d in $S/pi/*/; do n=$(basename $d); echo "--- diff $n"; pigeq diff $S/pi/$n/pi-lane.jsonl $S/pig/$n/pig-lane.jsonl 2>&1 | cut -c1-1500 | head -20; done
