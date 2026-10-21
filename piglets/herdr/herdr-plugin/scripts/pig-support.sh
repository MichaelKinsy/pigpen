#!/bin/sh
# Installs, removes, or reports on Pigpen's herdr reporter Package, which makes
# PiG report its state to herdr. Works on its own and as the command behind the
# plugin's build step and actions.
#
# Ported from herdr-pig (https://github.com/Hai-Fai-Solutions/herdr-pig),
# copyright 2026 Hai-Fai Solutions, Apache-2.0; see ../NOTICE. Modified to
# install the Go Package with `pig install` instead of copying a file.
#
# usage: pig-support.sh <install|uninstall|status|refresh>
set -eu

# The Package version this plugin was released with. `refresh` keeps an installed
# copy in step with it, as herdr-pig's refresh keeps the extension file in step
# with its own copy. A test keeps it equal to components/herdr/package.json.
PACKAGE_NAME="npm:@pi-in-go/pigpen-herdr"
PACKAGE_VERSION="0.2.0"
# PIGPEN_HERDR_PACKAGE installs another source, for example a local checkout
# or a packed tarball of the Package; it is never refreshed.
PACKAGE="${PIGPEN_HERDR_PACKAGE:-$PACKAGE_NAME@$PACKAGE_VERSION}"
# The TypeScript extension the herdr-pig plugin copied, and the marker that
# says herdr-pig manages it. This Package replaces it.
LEGACY_NAME="herdr-pig-agent-state.ts"
LEGACY_MARKER="HERDR_PIG_EXTENSION_VERSION="

# Mirrors PiG's own lookup: PIG_CODING_AGENT_DIR, then PIG_HOME/agent, then ~/.pig/agent.
agent_dir() {
    if [ -n "${PIG_CODING_AGENT_DIR:-}" ]; then
        printf '%s\n' "$PIG_CODING_AGENT_DIR"
    elif [ -n "${PIG_HOME:-}" ]; then
        printf '%s\n' "$PIG_HOME/agent"
    else
        printf '%s\n' "$HOME/.pig/agent"
    fi
}

# Show the result in herdr when run as a plugin action; stay quiet otherwise.
notify() {
    if [ "${HERDR_ENV:-}" = "1" ] && [ -n "${HERDR_BIN_PATH:-}" ]; then
        "$HERDR_BIN_PATH" notification show "$1" --body "$2" >/dev/null 2>&1 || true
    fi
}

fail() {
    echo "pigpen-herdr: $1" >&2
    notify "PiG support" "$1"
    exit 1
}

installed() {
    pig list 2>/dev/null | grep -qF "${PIGPEN_HERDR_PACKAGE:-$PACKAGE_NAME}"
}

# The pinned entry of this Package that `pig list` shows, if any, for example
# npm:@pi-in-go/pigpen-herdr@0.1.1. An entry without a version (installed by an
# earlier plugin, or by hand) follows `pig update`, not this plugin.
pinned_entry() {
    pig list 2>/dev/null | grep -o "$PACKAGE_NAME@[^[:space:]]*" | head -n 1
}

# 0 when nothing needs refreshing: no pinned entry of this Package, or the entry is the pin.
current() {
    [ -n "${PIGPEN_HERDR_PACKAGE:-}" ] && return 0
    entry=$(pinned_entry)
    [ -z "$entry" ] || [ "$entry" = "$PACKAGE" ]
}

# Replace an outdated pinned entry by the pin.
refresh() {
    entry=$(pinned_entry)
    pig remove "$entry" >/dev/null 2>&1 || return 1
    pig install "$PACKAGE" >/dev/null 2>&1
}

command=${1:-}
legacy="$(agent_dir)/extensions/$LEGACY_NAME"

case "$command" in
install)
    # With PIG_USE_PI_DIRS=1, PiG shares Pi's settings, so Pi would be asked to
    # load the Package too.
    if [ "${PIG_USE_PI_DIRS:-}" = "1" ]; then
        fail "PIG_USE_PI_DIRS=1 shares Pi's settings; unset it before installing"
    fi
    command -v pig >/dev/null 2>&1 || fail "pig is not on PATH; install PiG first"
    if installed && current; then
        echo "PiG support is already installed ($PACKAGE)"
    elif installed; then
        refresh || fail "could not replace $(pinned_entry) by $PACKAGE"
        echo "updated PiG support to $PACKAGE"
    else
        pig install "$PACKAGE" || fail "pig install $PACKAGE failed"
        echo "installed PiG support ($PACKAGE)"
    fi
    # Two reporters in one pane would both claim it, so the herdr-pig copy goes.
    if [ -f "$legacy" ] && grep -q "$LEGACY_MARKER" "$legacy"; then
        rm "$legacy"
        echo "removed the herdr-pig extension it replaces ($legacy)"
    fi
    echo "restart pig, or run /reload in a running session"
    notify "PiG support installed" "Restart pig or run /reload in a running session."
    ;;
uninstall)
    command -v pig >/dev/null 2>&1 || fail "pig is not on PATH"
    if installed; then
        target=$PACKAGE
        [ -n "${PIGPEN_HERDR_PACKAGE:-}" ] || target=$(pinned_entry)
        [ -n "$target" ] || target=${PACKAGE_NAME}
        pig remove "$target" || fail "pig remove $target failed"
        echo "removed PiG support ($target)"
        notify "PiG support removed" "Restart pig or run /reload in a running session."
    else
        echo "PiG support is not installed ($PACKAGE)"
        notify "PiG support" "Not installed."
    fi
    ;;
status)
    command -v pig >/dev/null 2>&1 || fail "pig is not on PATH"
    if installed && ! current; then
        echo "installed ($(pinned_entry)) but outdated; run install to update it to $PACKAGE"
        notify "PiG support" "Installed but outdated. Run Install to update it."
    elif installed; then
        echo "installed ($PACKAGE); pig update $PACKAGE updates it"
        notify "PiG support" "Installed."
    else
        echo "not installed ($PACKAGE)"
        notify "PiG support" "Not installed."
    fi
    if [ -f "$legacy" ]; then
        echo "the herdr-pig extension is also installed ($legacy); run install to replace it"
    fi
    ;;
refresh)
    # Startup hook: keep an existing install in step with this plugin. It never
    # installs on its own, so nothing is added without the user asking, and it
    # never fails herdr's startup.
    if [ "${PIG_USE_PI_DIRS:-}" != "1" ] && command -v pig >/dev/null 2>&1 && installed && ! current; then
        old=$(pinned_entry)
        if refresh; then
            echo "refreshed PiG support: $old -> $PACKAGE"
        else
            echo "pigpen-herdr: could not refresh $old to $PACKAGE" >&2
        fi
    fi
    ;;
*)
    echo "usage: $0 <install|uninstall|status|refresh>" >&2
    exit 2
    ;;
esac
