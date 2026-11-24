#!/bin/sh
# Run tern against the isolated headless proof instance only.
# Unsets every TERN_* / TERM_PROGRAM / multiplexer variable from the calling pane
# so the user's real Tern windows and daemon are never touched.
set -eu
ROOT="${OMP_PROOF_ROOT:-/tmp/omp-proof}"
exec env \
  -u TERN_ACP \
  -u TERN_BLOB_DIR \
  -u TERN_COMPLETE \
  -u TERN_IDENTITY \
  -u TERN_LENSES \
  -u TERN_PANE \
  -u TERN_PANE_SOCKET \
  -u TERN_WINDOW_KEY \
  -u TERN_WINDOW_SOCKET \
  -u TERM_PROGRAM \
  -u TERM_PROGRAM_VERSION \
  -u TMUX \
  -u TMUX_PANE \
  -u STY \
  -u ZELLIJ \
  TERN_CONFIG_DIR="$ROOT/tern-cfg" \
  TERN_DAEMON_SOCKET="$ROOT/tern-d.sock" \
  tern "$@"
