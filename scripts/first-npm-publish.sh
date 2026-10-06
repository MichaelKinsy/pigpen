#!/usr/bin/env bash
# The owner's one command for the FIRST npm publish: every Package, then every Piglet source, from a clean checkout, with the
# owner's own `npm login`. Skips what is already on npm, so it is safe to run again after an interruption. Then it prints the
# Trusted Publisher settings to add on npmjs.com so later releases run from CI. Works with macOS's bash 3.2.
#
#   scripts/first-npm-publish.sh            check, then publish
#   scripts/first-npm-publish.sh --dry-run  check and `npm publish --dry-run` everything; publishes nothing
set -eu

DRY=""
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY="--dry-run" ;;
    -h|--help) sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

say() { printf '\n==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

cd "$(git rev-parse --show-toplevel 2>/dev/null)" || die "run this inside the pigpen checkout"
[ -f scripts/npm-publish.mjs ] || die "this is not the pigpen checkout (scripts/npm-publish.mjs is missing)"

say "Checkout"
[ -z "$(git status --porcelain)" ] || die "the working tree is not clean; commit or stash first"
git fetch --quiet origin main || die "cannot fetch origin/main"
git merge-base --is-ancestor HEAD origin/main || die "HEAD is not on origin/main; switch to main and pull"
echo "publishing commit $(git rev-parse --short HEAD)"

say "Tools"
command -v node >/dev/null || die "node is not installed"
command -v npm >/dev/null || die "npm is not installed"
node -e 'const [a,b]=process.versions.node.split(".").map(Number);if(a<22||(a===22&&b<18))process.exit(1)' || die "node $(node --version) is older than 22.18"
echo "node $(node --version), npm $(npm --version)"

say "npm login"
NPM_USER="$(npm whoami 2>/dev/null)" || die "not logged in: run 'npm login' (an account in the @pi-in-go scope), then run this again"
echo "logged in to $(npm config get registry) as $NPM_USER"

say "Install and check (manifest contract, quality gates, index)"
npm ci --ignore-scripts
npm run check

say "Publish ${DRY:-for real}: Packages first, then Piglets; versions already on npm are skipped"
# With 2FA on writes npm asks for a one-time password at each publish; with 2FA on login only it does not.
node scripts/npm-publish.mjs $DRY

say "Trusted Publisher settings, to add once per package on npmjs.com"
node scripts/npm-publish.mjs --trusted-publishers

if [ -n "$DRY" ]; then
  printf '\nDry run only: nothing was published.\n'
else
  printf '\nDone. Next: add the Trusted Publisher above to each package, then push the tag npm/v0.1.0 (or run the workflow) to confirm CI can publish.\n'
fi
