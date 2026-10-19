#!/bin/bash
# youtube-canary.sh [stable yt-dlp]: the daily live canary of R4's test plan (research/frontier/R4-youtube-stream.md). It is not
# wired into any build, test run or schedule; run it by hand, or from cron, daily and before each release.
#
# For the stable yt-dlp (the argument, else yt-dlp on PATH) and the latest yt-dlp nightly (downloaded into a scratch directory),
# it plays three public YouTube tracks to their end through pig-music's own Player, mpv arguments and yt-dlp shim
# (mpv/canary_live_test.go): a short one, one longer than 12 MB (googlevideo throttles a request for more than about 10 MiB)
# and one Made for Kids (which visionos does not serve). It records per track the client that served the stream, the wait
# YouTube imposed (available_at), how far it played, the resolves, and the HTTP errors and reconnects in mpv's log.
# It alerts (exit 1) when a track does not reach its end, or when stable and nightly are served by different clients: the
# next stable yt-dlp will bring the nightly's change to every user (pig-music's self-managed yt-dlp follows releases/latest).
#
# About 6 resolves (3 to 4 YouTube requests each), no account, no cookie. Needs mpv, go, curl, and the go.work the extension
# builds with (GOWORK). PIG_MUSIC_CANARY_TRACKS and PIG_MUSIC_CANARY_JS (see the test) pass through.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd -P)
ext=$here/../extensions/pig-music
stable=${1:-$(command -v yt-dlp || true)}
[ -x "$stable" ] || { echo "no stable yt-dlp: pass its path" >&2; exit 2; }
work=$(mktemp -d "${TMPDIR:-/tmp}/pm-canary.XXXXXX")
trap 'rm -rf "$work"' EXIT

case "$(uname -s)/$(uname -m)" in
  Darwin/*) asset=yt-dlp_macos ;;
  Linux/x86_64) asset=yt-dlp_linux ;;
  Linux/aarch64) asset=yt-dlp_linux_aarch64 ;;
  *) asset=yt-dlp ;; # the zipimport build: needs python3
esac
# The binary and its checksum come from one release (latest is resolved to its tag first, so that a release published between
# the two downloads cannot pair them wrongly), and the binary is run only once it matches the release's SHA2-256SUMS.
releases=https://github.com/yt-dlp/yt-dlp-nightly-builds/releases
tag=$(curl -fsSLI --retry 2 -o /dev/null -w '%{url_effective}' "$releases/latest") && tag=${tag##*/tag/}
case "$tag" in ''|*[!0-9.]*) echo "could not find the latest yt-dlp nightly" >&2; exit 2 ;; esac
nightly=$work/yt-dlp-nightly
if ! curl -fsSL --retry 2 -o "$nightly" "$releases/download/$tag/$asset" ||
  ! curl -fsSL --retry 2 -o "$work/SHA2-256SUMS" "$releases/download/$tag/SHA2-256SUMS"; then
  echo "could not download the yt-dlp nightly $tag ($asset)" >&2
  exit 2
fi
want=$(awk -v f="$asset" '$2 == f { print $1 }' "$work/SHA2-256SUMS")
got=$( (command -v sha256sum >/dev/null && sha256sum "$nightly" || shasum -a 256 "$nightly") | awk '{ print $1 }')
if [ -z "$want" ] || [ "$got" != "$want" ]; then
  echo "the yt-dlp nightly $tag ($asset) does not match its SHA2-256SUMS (want ${want:-none}, got $got): not run" >&2
  exit 2
fi
chmod +x "$nightly"

alert=0
for build in stable nightly; do
  bin=$stable
  [ "$build" = nightly ] && bin=$nightly
  echo "== $build: yt-dlp $("$bin" --version 2>/dev/null || echo '?') at $bin"
  (cd "$ext" && PIG_MUSIC_CANARY_YTDLP=$bin go test -count=1 -timeout 15m -run '^TestLiveYouTubeCanary$' -v ./mpv/) > "$work/$build.log" 2>&1
  status=$?
  grep '^CANARY ' "$work/$build.log" | sed "s/^CANARY /$build /" | tee "$work/$build.lines"
  if [ "$status" -ne 0 ]; then
    alert=1
    echo "ALERT: $build: a track did not play to its end" >&2
    grep -E '^\s+(canary_live_test|.*_test)\.go:[0-9]+:' "$work/$build.log" >&2 || tail -20 "$work/$build.log" >&2
  fi
done

# The client per track, stable against nightly.
client() { grep " id=$2 " "$work/$1.lines" | grep -o 'client=[^ ]*' | head -1; }
for id in $(grep -o ' id=[^ ]*' "$work/stable.lines" "$work/nightly.lines" 2>/dev/null | sed 's/.* id=//' | sort -u); do
  s=$(client stable "$id"); n=$(client nightly "$id")
  if [ "$s" != "$n" ]; then
    alert=1
    echo "ALERT: $id: stable $s, nightly $n: the next stable yt-dlp changes the client that serves pig-music" >&2
  fi
done
[ "$alert" -eq 0 ] && echo "canary: ok"
exit "$alert"
