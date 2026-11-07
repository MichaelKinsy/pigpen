#!/bin/sh
# Regenerates the oracle data of the differential tests of extensions/posthorse/testdata from the unmodified original
# under Node 24 (Pi 1.5.0's node_modules, typebox 1.3.27). Run inside the xd6702 container, cwd = the Pigpen tree:
#   ~/work/ph-tools/ph run "sh components/posthorse/port/tools/gen-testdata.sh"
# then scp -r xd6702:ph-work/scratch/testdata-out/. components/posthorse/extensions/posthorse/testdata/
# The probe scripts stay in port/review/*-probes; they keep their scratch paths, this script prepares them.
set -eu
P=components/posthorse
D=/home/kinsy/ph-work/scratch/testdata-out   # copy to extensions/posthorse/testdata (ph run syncs one way and would delete it)
R=$P/port/review
SC=/home/kinsy/ph-work/scratch
NM=/home/kinsy/ph-work/pi-oracle/node_modules
ORACLE=$(pwd)/$P/port/oracle
mkdir -p "$D" "$SC/reviewcore" "$SC/historyrev" "$SC/notesstore" "$SC/uireview"
gz() { gzip -9 -n -c "$1" > "$D/$2"; }

# uireview: 656 rendered cases of the original's cards and message renderers.
sh $R/uireview-probes/run-ts.sh
gz "$SC/uireview/ui-cases.json" ui_cases.json.gz

# corereview: StringDecoder chunks, jsdiff-style text helpers, gitdir parsing.
T=$SC/reviewcore/ts
rm -rf "$T"; mkdir -p "$T"
cp -r "$ORACLE/src" "$T/src"; cp $R/corereview-probes/*.ts "$T/"
ln -s "$NM" "$T/node_modules"; echo '{"type":"module"}' > "$T/package.json"
(cd "$T" && node decoder.ts > "$SC/reviewcore/decoder.json" && node jsdiff.ts > "$SC/reviewcore/jsdiff.json" && node gitdir.ts > "$SC/reviewcore/gitdir.json")
gz "$SC/reviewcore/decoder.json" core_decoder.json.gz
gz "$SC/reviewcore/jsdiff.json" core_jsdiff.json.gz
gz "$SC/reviewcore/gitdir.json" core_gitdir.json.gz

# historyrev: folds, matchers, hits, cursors, lines, base64 of the history search.
rm -f "$SC"/historyrev/*.json
(cd $R/historyrev-probes && node probe.mts)
for f in lower prefilter matchers base64 cursors lines hits folds; do gz "$SC/historyrev/$f.json" historyrev_$f.json.gz; done

# notesstore: TypeBox schema cases, path helpers, a fork-of-fork binding for the benchmark.
rm -rf "$SC/notesstore"; mkdir -p "$SC/notesstore"
cp -r "$ORACLE" "$SC/notesstore/oracle"; rm -rf "$SC/notesstore/oracle/node_modules"; ln -s "$NM" "$SC/notesstore/oracle/node_modules"
(cd $R/notesstore-probes && node gen.mjs && node gen2.mjs && node perf.mjs 100)
gz "$SC/notesstore/tb-cases.json" notes_tb_cases.json.gz
gz "$SC/notesstore/paths-cases.json" notes_paths_cases.json.gz
gz "$SC/notesstore/grand-100.json" notes_grand_100.json.gz
ls -l "$D"/ui_cases.json.gz "$D"/core_*.json.gz "$D"/historyrev_*.json.gz "$D"/notes_*cases.json.gz "$D"/notes_grand_100.json.gz
