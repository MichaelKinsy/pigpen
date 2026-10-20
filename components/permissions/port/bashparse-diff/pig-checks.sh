#!/bin/bash
# pig install --validate-only and pig package validate with the shared pig, in an isolated HOME.
#   PP_DIR=<dir> ~/pp-perm-run.sh "bash /work/components/permissions/port/bashparse-diff/pig-checks.sh" > log
set -u
export HOME=/tmp/pp-iso-home PIG_HOME=/tmp/pp-iso-home/.pig PIG_CODING_AGENT_DIR=/tmp/pp-iso-home/agent PI_CODING_AGENT_DIR=/tmp/pp-iso-home/agent
rm -rf "$HOME"; mkdir -p "$HOME" "$PIG_HOME" "$PIG_CODING_AGENT_DIR"
PIG=/opt/pp-shared-pig/bin/pig
echo "== pig --version"; $PIG --version 2>&1 | head -3
echo "== pig install <extension> --validate-only --json"
$PIG install /work/components/permissions/extensions/pi-permission-system --validate-only --json 2>&1 | tail -30
echo "== pig package validate components/permissions"
$PIG package validate /work/components/permissions 2>&1 | tail -20
echo "== done"
