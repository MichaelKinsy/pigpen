#!/bin/sh
# Builds the self-contained byo-ext harness tree (harnesses/pig/ in kagent-dev's byo-ext repository) from the canonical Pigpen tree,
# or checks that a tree already exported still matches it.
#
#   scripts/export-byo-ext.sh DIR           write the tree into DIR (created; refused if it is not empty)
#   scripts/export-byo-ext.sh --check DIR   export to a temporary directory and fail (exit 1) if DIR differs in any file or in the executable bit
#
# The tree is the whole `docker build` context for the Dockerfile, so it holds everything the build reads:
#   Dockerfile .dockerignore LICENSE NOTICE README.md pig-kagent.sh   from components/kagent/harness (README.md and docs/ are copied when present)
#   adapter/ chart/ docs/                                             from components/kagent/harness
#   policy/   the kagent-policy extension (MIT, with its LICENSE)     from components/kagent/extensions/kagent-policy
#   piglet/piglet.yaml  the kagent Piglet, its Package source rewritten to a Piglet-local origin (the Dockerfile copies
#                       policy/ to piglet/extensions/kagent-policy, because PiG resolves local origins inside the Piglet directory)
#
# Dropped as test-only or proof-only: *_test.go, testdata/ and proof/ directories, .DS_Store. Symlinks are refused.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
harness="$root/components/kagent/harness"
policy="$root/components/kagent/extensions/kagent-policy"
piglet="$root/piglets/kagent/piglet.yaml"
policy_license="$root/components/kagent/LICENSE"

die() { echo "export-byo-ext: $*" >&2; exit 2; }

# copy_tree SRC DST: every regular file under SRC except the dropped ones, keeping the executable bit.
copy_tree() {
  src=$1; dst=$2
  [ -d "$src" ] || die "missing directory $src"
  # Each find runs on its own, so a failing find stops the export; in a pipeline into `while` its status would be lost.
  links=$(find "$src" -type l -print -quit) || die "find failed on $src"
  if [ -n "$links" ]; then die "$src contains a symlink; the export refuses them"; fi
  list=$(cd "$src" && find . \( -name testdata -o -name proof \) -type d -prune -o -type f ! -name '*_test.go' ! -name .DS_Store -print) || die "find failed on $src"
  mkdir -p "$dst"
  printf '%s\n' "$list" | LC_ALL=C sort |
    while IFS= read -r file; do
      [ -n "$file" ] || continue
      mkdir -p "$dst/$(dirname "$file")"
      cp -p "$src/$file" "$dst/$file"
    done
}

# export_to OUT: OUT must exist and be empty.
export_to() {
  out=$1
  for f in Dockerfile .dockerignore LICENSE NOTICE pig-kagent.sh; do
    [ -f "$harness/$f" ] || die "components/kagent/harness/$f is missing"
    cp -p "$harness/$f" "$out/$f"
  done
  if [ -f "$harness/README.md" ]; then cp -p "$harness/README.md" "$out/README.md"; else echo "export-byo-ext: warning: components/kagent/harness/README.md is missing; byo-ext requires one" >&2; fi
  copy_tree "$harness/adapter" "$out/adapter"
  copy_tree "$harness/chart" "$out/chart"
  if [ -d "$harness/docs" ]; then copy_tree "$harness/docs" "$out/docs"; else echo "export-byo-ext: warning: components/kagent/harness/docs is missing" >&2; fi
  copy_tree "$policy" "$out/policy"
  [ -f "$policy_license" ] || die "components/kagent/LICENSE is missing (the policy extension's MIT licence)"
  cp -p "$policy_license" "$out/policy/LICENSE"

  [ -f "$piglet" ] || die "piglets/kagent/piglet.yaml is missing"
  mkdir -p "$out/piglet"
  # Drop the `packages:` mapping, point the extension at the Piglet-local copy of policy/, and replace the first header line.
  awk '
    NR == 1 && /^# Pigpen composition input/ {
      print "# Exported from piglets/kagent/piglet.yaml of MichaelKinsy/pigpen by scripts/export-byo-ext.sh. The Dockerfile copies policy/ to"
      print "# extensions/kagent-policy beside this file: PiG resolves local origins only inside the Piglet directory."
      next
    }
    /^packages:/ { skipping = 1; next }
    skipping && /^[ \t]/ { next }
    { skipping = 0 }
    { sub(/origins: \[package:kagent\]/, "origins: [local:./extensions/kagent-policy]"); print }
  ' "$piglet" > "$out/piglet/piglet.yaml"
  if grep -Eq 'package:|\.\./|^packages:' "$out/piglet/piglet.yaml"; then
    die "the rewritten piglet.yaml still names a Package or a path outside the Piglet directory; update the rewrite in scripts/export-byo-ext.sh"
  fi
  grep -q 'local:./extensions/kagent-policy' "$out/piglet/piglet.yaml" || die "the kagent-policy origin was not rewritten; update the rewrite in scripts/export-byo-ext.sh"
}

case "${1:-}" in
  --check)
    [ $# -eq 2 ] || die "usage: export-byo-ext.sh --check DIR"
    dir=$2
    [ -d "$dir" ] || { echo "export-byo-ext: $dir does not exist; run scripts/export-byo-ext.sh $dir" >&2; exit 1; }
    tmp=$(mktemp -d "${TMPDIR:-/tmp}/export-byo-ext.XXXXXX")
    trap 'rm -rf "$tmp"' EXIT INT TERM
    export_to "$tmp"
    status=0
    diff -r "$tmp" "$dir" || status=1
    # diff -r ignores modes: the one executable file must stay executable on both sides.
    for f in pig-kagent.sh; do
      if [ -x "$tmp/$f" ] && [ ! -x "$dir/$f" ]; then echo "export-byo-ext: $dir/$f lost its executable bit" >&2; status=1; fi
    done
    if [ "$status" -ne 0 ]; then echo "export-byo-ext: $dir has drifted from the canonical tree; re-run scripts/export-byo-ext.sh on an empty directory" >&2; exit 1; fi
    echo "export-byo-ext: $dir matches the canonical tree"
    ;;
  ''|-h|--help)
    sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
    [ -n "${1:-}" ] && exit 0
    exit 2
    ;;
  -*) die "unknown option $1" ;;
  *)
    [ $# -eq 1 ] || die "usage: export-byo-ext.sh DIR"
    dir=$1
    if [ -e "$dir" ]; then
      [ -d "$dir" ] || die "$dir exists and is not a directory"
      [ -z "$(ls -A "$dir")" ] || die "$dir is not empty; export into a new or empty directory"
    else
      mkdir -p "$dir"
    fi
    export_to "$dir"
    echo "export-byo-ext: wrote $dir"
    ;;
esac
