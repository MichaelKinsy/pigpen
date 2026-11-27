#!/usr/bin/env bash
# record.sh [NAME]: record demo.sh in a headless pty with asciinema v3, render GIF with agg, encode H.264 mp4 with ffmpeg.
# Tools: the asciinema 3.x static Linux binary placed at $OUT/bin/asciinema (download it from the asciinema releases page; nothing is
# installed), and the docker images ghcr.io/asciinema/agg and jrottenberg/ffmpeg:7-alpine (pull them, delete them afterwards).
#   A_PIG=pig-basket A_PIG_APPROVE=pig-approve ./record.sh final       (or the baseline agents, to rehearse)
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
OUT=${OUT:-$HERE/demo}; NAME=${1:-clip}
COLS=${COLS:-110}; ROWS=${ROWS:-26}; FONT=${FONT:-19}
mkdir -p "$OUT"
export TERM=xterm-256color
"$OUT/bin/asciinema" rec --headless --window-size "${COLS}x${ROWS}" --overwrite --return -q \
  -t "kagent harness demo" -c "$HERE/demo.sh" "$OUT/$NAME.cast"
ids=(-u "$(id -u):$(id -g)")
docker run --rm "${ids[@]}" -v "$OUT:/data" ghcr.io/asciinema/agg --font-size "$FONT" --theme dracula --fps-cap 20 --idle-time-limit 15 \
  --last-frame-duration 3 "/data/$NAME.cast" "/data/$NAME.gif" >/dev/null 2>&1
docker run --rm "${ids[@]}" -v "$OUT:/data" jrottenberg/ffmpeg:7-alpine -y -loglevel error -i "/data/$NAME.gif" \
  -vf "scale=1280:720:flags=lanczos:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2:color=black,format=yuv420p" \
  -c:v libx264 -preset slow -crf 20 -r 20 -movflags +faststart "/data/$NAME.mp4"
ls -la "$OUT/$NAME".*
