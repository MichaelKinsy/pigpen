#!/bin/sh
# Extension binary size and cold start of two builds of the extension (for example the released tag
# and a branch): a main that builds the extension (all flags, commands and handlers registered) and
# exits, so the time is process start, package initialisation and registration. 21 interleaved runs,
# median. Usage: coldstart.sh <old extension directory> <new extension directory>
set -eu
unset GOWORK GOFLAGS
old=$1; new=$2
work=$(mktemp -d)
build() { # name dir
  mkdir -p "$work/$1" && cat > "$work/$1/main.go" <<'GO'
package main

import ahp "github.com/MichaelKinsy/pigpen/ahp"

func main() { _ = ahp.Extension() }
GO
  printf 'module perfmain\n\ngo 1.26\n' > "$work/$1/go.mod"
  ( cd "$work/$1" && printf 'go 1.26\n\nuse (\n\t.\n\t%s\n\t%s/third_party/agent-host-protocol-go\n)\n' "$2" "$2" > go.work \
      && go build -o "$work/$1.bin" . && go build -ldflags '-s -w' -o "$work/$1.stripped" . )
}
build old "$old"; build new "$new"
printf 'size old %s bytes (stripped %s)\nsize new %s bytes (stripped %s)\n' \
  "$(wc -c < "$work/old.bin")" "$(wc -c < "$work/old.stripped")" "$(wc -c < "$work/new.bin")" "$(wc -c < "$work/new.stripped")"
for which in old new; do : > "$work/$which.times"; done
i=0
while [ $i -lt 21 ]; do
  for which in old new; do
    s=$(date +%s%N); "$work/$which.bin"; e=$(date +%s%N)
    echo $(( (e - s) / 1000 )) >> "$work/$which.times"
  done
  i=$((i + 1))
done
for which in old new; do
  echo "cold start $which: median $(sort -n "$work/$which.times" | sed -n 11p) us (min $(sort -n "$work/$which.times" | sed -n 1p), max $(sort -n "$work/$which.times" | sed -n 21p))"
done
rm -rf "$work"
