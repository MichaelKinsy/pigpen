#!/bin/sh
# Creates the representative repository of the changeset measurements: 400 tracked files in 20
# directories; the latest commit changes 100 of them; the work tree has 80 modified, 10 deleted and
# 40 untracked files. Deterministic. Usage: mkrepo.sh <empty directory>
set -eu
dir=$1
mkdir -p "$dir" && cd "$dir"
git init -q
git config user.email perf@example.com
git config user.name perf
i=0
while [ $i -lt 400 ]; do
  d=dir$((i % 20)); mkdir -p "$d"
  n=0; : > "$d/file$i.txt"
  while [ $n -lt 30 ]; do echo "line $n of file $i" >> "$d/file$i.txt"; n=$((n + 1)); done
  i=$((i + 1))
done
git add . && git commit -qm base
i=0
while [ $i -lt 100 ]; do echo "committed $i" >> "dir$((i % 20))/file$i.txt"; i=$((i + 1)); done
git commit -qam latest
i=100
while [ $i -lt 180 ]; do echo "dirty $i" >> "dir$((i % 20))/file$i.txt"; i=$((i + 1)); done
i=180
while [ $i -lt 190 ]; do rm "dir$((i % 20))/file$i.txt"; i=$((i + 1)); done
i=0
while [ $i -lt 40 ]; do echo "new $i" > "dir$((i % 20))/new$i.txt"; i=$((i + 1)); done
