#!/bin/sh
# Builds the tree the original extension runs in for the equivalence proof.
#
# Usage: port/oracle-layout.sh <out-dir>
#
# The original (port/oracle/.pi/extensions/superpowers.ts, byte for byte) finds its Skills at
# <package root>/skills and reads skills/using-superpowers/SKILL.md for the bootstrap. This Package ships the
# Skills under the pigpen-superpowers- names (port/adapt.py), so the same original code, run over the shipped
# tree, would find no using-superpowers directory. The layout gives it one: the shipped Skills, plus a
# using-superpowers symlink to the shipped using-superpowers Skill (Pi loads a Skill once per real path).
# The original and the port then see the same Skills, and the only thing compared is the extension's code.
# The unmodified Skills tree stays in port/oracle/skills; port/adapt.py is the proof it differs from the
# shipped tree by the recorded renames only.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: oracle-layout.sh <out-dir>}
rm -rf "$out"
mkdir -p "$out/.pi/extensions"
cp "$here/oracle/.pi/extensions/superpowers.ts" "$out/.pi/extensions/superpowers.ts"
cp "$here/oracle/package.json" "$out/package.json"
cp -R "$here/../skills" "$out/skills"
ln -s pigpen-superpowers-using-superpowers "$out/skills/using-superpowers"
cmp "$here/oracle/.pi/extensions/superpowers.ts" "$out/.pi/extensions/superpowers.ts"
echo "$out/.pi/extensions/superpowers.ts"
