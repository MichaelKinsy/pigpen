#!/bin/sh
# Regenerates every Pigtorio art asset into mods/pigtorio/art and the font.
#  1. Pigsty's sprites (APNG) -> 4x lossless animated WebP, same frame times
#     (method from pigpen/gen.sh); props -> 4x PNG; sprites/index.json.
#  2. artgen (art/artgen, Go) draws the new art as 1x frames; this script scales
#     them 4x nearest-neighbour, still -> PNG, animated -> looping WebP.
#  3. Silkscreen-Regular.ttf + OFL.txt (downloaded once, kept if present).
#  4. art/contact.png: one frame of every new asset on grass (all Runt frames, hotbar icons on a slot).
# Idempotent. Needs go, ffmpeg, ffprobe, img2webp, webpmux, curl, python3.
# PIGSTY_WEB overrides the Pigsty web dir.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
web=${PIGSTY_WEB:-$HOME/kinsy-utils-tern/pig-stuff/pigstyd/web}
out=$root/mods/pigtorio/art
fonts=$root/mods/pigtorio/fonts
scale=4
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

[ -f "$web/sprites/index.json" ] || { echo "no Pigsty sprites at $web" >&2; exit 1; }

# pack <name-for-log> <ms> <dst.webp> <frame png...>: frames are already scaled
pack() {
  dst=$1 ms=$2; shift 2
  args=""
  for f in "$@"; do args="$args -d $ms $f"; done
  # shellcheck disable=SC2086
  img2webp -loop 0 -lossless -exact $args -o "$tmp/a.webp" >/dev/null 2>&1
  webpmux -set bgcolor 0,0,0,0 "$tmp/a.webp" -o "$dst" >/dev/null 2>&1
}

scalepng() { # scalepng <in> <out>
  ffmpeg -nostdin -v error -y -i "$1" -vf "scale=iw*$scale:ih*$scale:flags=neighbor,format=rgba" -frames:v 1 "$2"
}

apng2webp() { # same frames and frame times as the APNG
  in=$1 dst=$2 args="" i=1
  rm -f "$tmp"/s*.png
  ffmpeg -nostdin -v error -i "$in" -vf "scale=iw*$scale:ih*$scale:flags=neighbor,format=rgba" -fps_mode passthrough "$tmp/s%03d.png"
  for sec in $(ffprobe -v error -select_streams v -show_entries frame=duration_time -of csv=p=0 "$in"); do
    args="$args -d $(awk "BEGIN{printf \"%d\", $sec*1000+0.5}") $(printf '%s/s%03d.png' "$tmp" $i)"
    i=$((i + 1))
  done
  # shellcheck disable=SC2086
  img2webp -loop 0 -lossless -exact $args -o "$tmp/a.webp" >/dev/null 2>&1
  webpmux -set bgcolor 0,0,0,0 "$tmp/a.webp" -o "$dst" >/dev/null 2>&1
}

rm -rf "$out"
mkdir -p "$out/sprites" "$out/props" "$out/tiles" "$out/ui" "$out/swineherd"

# 1. Pigsty sprites and props
skins=$(python3 -c "import json,sys;print(' '.join(json.load(open(sys.argv[1]))['sprites']))" "$web/sprites/index.json")
states=$(python3 -c "import json,sys;print(' '.join(json.load(open(sys.argv[1]))['states']))" "$web/sprites/index.json")
for s in $skins; do
  mkdir -p "$out/sprites/$s"
  for a in $states; do apng2webp "$web/sprites/$s/$a.png" "$out/sprites/$s/$a.webp"; done
done
python3 - "$web/sprites/index.json" "$out/sprites/index.json" "$scale" <<'PY'
import json, sys
src = json.load(open(sys.argv[1])); k = int(sys.argv[3])
w, h = src["size"]["w"], src["size"]["h"]
json.dump({"skins": src["sprites"], "states": src["states"],
           "size": {"w": w * k, "h": h * k}, "size1x": {"w": w, "h": h},
           "scale": k, "format": "webp"},
          open(sys.argv[2], "w"), indent=1)
open(sys.argv[2], "a").write("\n")
PY
for p in "$web"/props/*.png; do scalepng "$p" "$out/props/$(basename "$p")"; done

# 2. artgen
(cd "$here/artgen" && go run . -out "$tmp/frames")
for d in $(cd "$tmp/frames" && find . -mindepth 2 -maxdepth 2 -type d | sed 's|^\./||' | sort); do
  mkdir -p "$out/$(dirname "$d")"
  n=$(ls "$tmp/frames/$d"/*.png | wc -l | tr -d ' ')
  rm -rf "$tmp/sc"; mkdir "$tmp/sc"
  for f in "$tmp/frames/$d"/*.png; do scalepng "$f" "$tmp/sc/$(basename "$f")"; done
  if [ -f "$tmp/frames/$d/ms" ]; then
    # shellcheck disable=SC2046
    pack "$out/$d.webp" "$(cat "$tmp/frames/$d/ms")" $(ls "$tmp/sc"/*.png)
  else
    cp "$tmp/sc/00.png" "$out/$d.png"
  fi
done

# 3. font
mkdir -p "$fonts"
base=https://github.com/google/fonts/raw/main/ofl/silkscreen
[ -s "$fonts/Silkscreen-Regular.ttf" ] || curl -fsSL "$base/Silkscreen-Regular.ttf" -o "$fonts/Silkscreen-Regular.ttf"
[ -s "$fonts/OFL.txt" ] || curl -fsSL "$base/OFL.txt" -o "$fonts/OFL.txt"
ps=$(fc-query -f '%{postscriptname}\n' "$fonts/Silkscreen-Regular.ttf" | head -n1)
[ -n "$ps" ] || { echo "font has no PostScript name" >&2; exit 1; }

# 4. contact sheet (1x frames, shown at 4x)
(cd "$here/artgen" && go run . -contact "$here/contact.png" -frames "$tmp/frames")

echo "art: $(find "$out" -type f | wc -l | tr -d ' ') files, font $ps, contact sheet art/contact.png"
