#!/bin/sh
# The private Herdr sandbox Pigtorio is developed and tested in. It never
# touches ~/.config/herdr-app or the default herdr-core.
#   scripts/sandbox.sh env      print the exports (eval "$(scripts/sandbox.sh env)")
#   scripts/sandbox.sh install  copy the mod, layout and engine into the sandbox home
#   scripts/sandbox.sh check    herdr-app check on the sandbox copy
#   scripts/sandbox.sh shot F   save the sandbox window's last frame to F
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
root=${PIGTORIO_SANDBOX:-/tmp/hfarm}
export HERDR_APP_HOME="$root/home" HC_SESSION=hfarm HC_SOCKET="$root/rt/hc.sock" XDG_RUNTIME_DIR="$root/rt" XDG_STATE_HOME="$root/state" XDG_CONFIG_HOME="$root/cfg" HERDR_TELEMETRY=0
case "${1:-env}" in
env)
	printf 'export HERDR_APP_HOME=%s HC_SESSION=hfarm HC_SOCKET=%s XDG_RUNTIME_DIR=%s XDG_STATE_HOME=%s XDG_CONFIG_HOME=%s HERDR_TELEMETRY=0\n' \
		"$HERDR_APP_HOME" "$HC_SOCKET" "$XDG_RUNTIME_DIR" "$XDG_STATE_HOME" "$XDG_CONFIG_HOME"
	;;
install)
	(cd "$here/engine" && go build -o bin/pigtorio ./cmd/pigtorio)
	mkdir -p "$HERDR_APP_HOME/mods" "$HERDR_APP_HOME/layouts" "$HERDR_APP_HOME/pigtorio/bin"
	rsync -a --delete "$here/mods/pigtorio/" "$HERDR_APP_HOME/mods/pigtorio/"
	cp "$here/layouts/pigtorio.json" "$HERDR_APP_HOME/layouts/pigtorio.json"
	cp "$here/engine/bin/pigtorio" "$HERDR_APP_HOME/pigtorio/bin/pigtorio.new" && mv "$HERDR_APP_HOME/pigtorio/bin/pigtorio.new" "$HERDR_APP_HOME/pigtorio/bin/pigtorio"
	echo "installed into $HERDR_APP_HOME"
	;;
check)
	herdr-app check "$HERDR_APP_HOME/mods/pigtorio"
	;;
shot)
	herdr-app shot "${2:-$root/shot.png}"
	;;
*)
	echo "usage: $0 env|install|check|shot [file]" >&2
	exit 2
	;;
esac
