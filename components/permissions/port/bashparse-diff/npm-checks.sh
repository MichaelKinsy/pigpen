#!/bin/bash
# npm run generate, npm run check in a full clone (needs the real git history), and what generate changed.
#   PP_DIR=<dir> ~/pp-perm-run.sh "bash /work/components/permissions/port/bashparse-diff/npm-checks.sh" > log
set -u
cd /work
export npm_config_cache=/home/kinsy/.npm-pp-bashperf
git checkout -- components/subagents 2>&1 | tail -2
echo "== npm ci"; npm ci --ignore-scripts --no-audit --no-fund 2>&1 | tail -5
echo "== git"; git log --oneline -1; git status --short | grep -v "^??" | head -20
echo "== npm run generate"; npm run generate 2>&1 | tail -15
echo "== what generate changed"; git status --short | head -30; git diff --stat | tail -10
echo "== npm run check"; npm run check 2>&1 | tail -40
echo "== done"
