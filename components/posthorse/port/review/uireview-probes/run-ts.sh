#!/bin/sh
# uireview: regenerate the TS side of the differential (inside the container).
set -e
S=/home/kinsy/ph-work/scratch/uireview
P=/home/kinsy/ph-work/pigpen/components/posthorse
rm -rf $S/ts; mkdir -p $S/ts
cp -r $P/port/oracle/src $P/port/oracle/ui.ts $P/port/review/uireview-probes/*.ts $S/ts/
ln -s /home/kinsy/ph-work/pi-oracle/node_modules $S/ts/node_modules
echo '{"type":"module"}' > $S/ts/package.json
cd $S/ts && node cases.ts $S/ui-cases.json
