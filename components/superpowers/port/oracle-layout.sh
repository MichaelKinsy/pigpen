#!/bin/sh
# Builds the tree the original extension runs in for the equivalence proof.
#
# Usage: port/oracle-layout.sh <out-dir>
#
# The original (port/oracle/.pi/extensions/superpowers.ts, byte for byte) finds its Skills at
# <package root>/skills and reads skills/using-superpowers/SKILL.md for the bootstrap. This Package ships that Skill
# as pigpen-superpowers-using-superpowers (port/adapt.py), so the layout gives the original a
# skills/using-superpowers/SKILL.md that is the shipped Skill with ONE line removed: its `description:` line.
# The extension reads only the Skill's body (it strips the frontmatter), so the injected text is the shipped text;
# and a Skill without a description is not loaded by Pi, so the original's resources_discover contributes no Skill
# to the system prompt. The port cannot know its own directory (see PORT.md, finding F1) and contributes none under
# a bare `-e` either, so the two lanes differ in nothing but the extension's code. What resources_discover returns is
# proven by the layer-1 twin and by installing the Package under a real pig (PORT.md).
# The unmodified Skills tree stays in port/oracle/skills; port/adapt.py is the proof it differs from the shipped tree
# by the recorded renames only.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: oracle-layout.sh <out-dir>}
rm -rf "$out"
mkdir -p "$out/.pi/extensions" "$out/skills/using-superpowers"
cp "$here/oracle/.pi/extensions/superpowers.ts" "$out/.pi/extensions/superpowers.ts"
# the original is an ES module (its package.json says "type": "module"); the manifest is not vendored, it differs between releases
printf '{\n  "type": "module"\n}\n' > "$out/package.json"
shipped="$here/../skills/pigpen-superpowers-using-superpowers/SKILL.md"
awk 'BEGIN { dropped = 0 } !dropped && /^description:/ { dropped = 1; next } { print }' "$shipped" > "$out/skills/using-superpowers/SKILL.md"
[ "$(wc -l < "$shipped")" -eq "$(( $(wc -l < "$out/skills/using-superpowers/SKILL.md") + 1 ))" ] || { echo "oracle-layout: expected to drop exactly one line" >&2; exit 1; }
cmp "$here/oracle/.pi/extensions/superpowers.ts" "$out/.pi/extensions/superpowers.ts"
echo "$out/.pi/extensions/superpowers.ts"
