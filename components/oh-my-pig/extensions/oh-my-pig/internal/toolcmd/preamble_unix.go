//go:build !windows

package toolcmd

import "fmt"

// renderDispatcherFunction emits the bash shell function `__omp_tc_call`
// that forwards one mounted tool command invocation to the session's loopback
// bridge server and writes stdout, stderr, and exit status back to the caller.
//
// Caller (`renderPreamble`) wraps the entire preamble in a `set +a` / `export -n`
// / restore guard so a persisted `allexport` never exports any `__omp_tc_*`
// variables or functions to child processes.
//
// Security and shell-hygiene properties:
//   - The per-call token is stored in an unexported shell variable
//     (`__omp_tc_token`) so child processes spawned by the model's command do
//     NOT inherit it in their environment; the preamble unlinks its own temp
//     file immediately on source via `BASH_SOURCE[0]` so programs started by
//     the command cannot read the preamble file from disk.
//   - Helper paths (`base64`, `tr`, `openssl`) and the `base64` decode flag
//     (`-d` vs `-D`) are resolved ONCE when the preamble is sourced (`command -p`
//     first so a hostile project-local binary on `PATH` cannot hijack them, then
//     falling back to `PATH` for environments like NixOS without coreutils on
//     the default POSIX path), with zero per-argument probing.
//   - `__omp_tc_call` uses a subshell function body `(...)`:
//   - `{ set +x +v +e +u +a; set +o pipefail; } 2>/dev/null` silences
//     xtrace/verbose tracing, prevents `allexport` from exporting request or
//     stdin buffers to helper processes, and protects against caller
//     `set -e`/`set -u`/`pipefail` without modifying caller shell options.
//   - Opening/closing FD 8 and FD 9 inside the subshell never clobbers or
//     closes FD 8/9 held open by the caller (e.g. `flock 9`).
//   - If the bridge closes the connection before sending an `RC` line, the
//     dispatcher writes `<name>: tool-command bridge closed the connection` to
//     stderr and exits 1 (never failing silently).
//
// Stdin is only read when argv contains a bare `-` (matching omp's
// `crates/shell/src/commands.rs:312`).
func renderDispatcherFunction(port int, token string) string {
	return fmt.Sprintf(`__omp_tc_port=%d
__omp_tc_token=%s
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
  command -p rm -f "${BASH_SOURCE[0]}" 2>/dev/null || rm -f "${BASH_SOURCE[0]}" 2>/dev/null || true
fi
__omp_tc_b64_cmd=$(command -p -v base64 2>/dev/null || command -v base64 2>/dev/null || true)
__omp_tc_tr_cmd=$(command -p -v tr 2>/dev/null || command -v tr 2>/dev/null || true)
__omp_tc_b64_dec_flag=""
__omp_tc_openssl_cmd=""
if [ -n "$__omp_tc_b64_cmd" ] && [ -n "$__omp_tc_tr_cmd" ]; then
  if printf 'YQ==' | "$__omp_tc_b64_cmd" -d >/dev/null 2>&1; then
    __omp_tc_b64_dec_flag="-d"
  else
    __omp_tc_b64_dec_flag="-D"
  fi
else
  __omp_tc_b64_cmd=""
  __omp_tc_openssl_cmd=$(command -p -v openssl 2>/dev/null || command -v openssl 2>/dev/null || true)
fi
__omp_tc_b64_enc() {
  if [ -n "$__omp_tc_b64_cmd" ]; then
    "$__omp_tc_b64_cmd" | "$__omp_tc_tr_cmd" -d '\r\n'
  elif [ -n "$__omp_tc_openssl_cmd" ]; then
    "$__omp_tc_openssl_cmd" base64 -A
  else
    return 1
  fi
}
__omp_tc_b64_dec() {
  if [ -z "$1" ]; then
    return 0
  fi
  if [ -n "$__omp_tc_b64_cmd" ]; then
    printf '%%s' "$1" | "$__omp_tc_b64_cmd" "$__omp_tc_b64_dec_flag"
  elif [ -n "$__omp_tc_openssl_cmd" ]; then
    printf '%%s' "$1" | "$__omp_tc_openssl_cmd" base64 -d -A
  else
    return 1
  fi
}
__omp_tc_call() (
  { set +x +v +e +u +a; set +o pipefail; } 2>/dev/null
  export -n __omp_tc_port __omp_tc_token __omp_tc_b64_cmd __omp_tc_b64_dec_flag __omp_tc_tr_cmd __omp_tc_openssl_cmd 2>/dev/null || true
  __omp_tc_name="$1"
  shift
  __omp_tc_cwd_b64=$(printf '%%s' "$PWD" | __omp_tc_b64_enc) || {
    printf '%%s: tool-command transport requires base64\n' "$__omp_tc_name" >&2
    exit 127
  }
  __omp_tc_need_stdin=0
  __omp_tc_req=$(printf 'TOKEN %%s\nNAME %%s\nCWD %%s\nARGC %%d\n' "$__omp_tc_token" "$__omp_tc_name" "$__omp_tc_cwd_b64" "$#")
  for __omp_tc_a in "$@"; do
    if [ "$__omp_tc_a" = "-" ]; then
      __omp_tc_need_stdin=1
    fi
    __omp_tc_arg_b64=$(printf '%%s' "$__omp_tc_a" | __omp_tc_b64_enc)
    __omp_tc_req="${__omp_tc_req}
ARG ${__omp_tc_arg_b64}"
  done
  if [ "$__omp_tc_need_stdin" = "1" ]; then
    __omp_tc_stdin_b64=$(__omp_tc_b64_enc)
    __omp_tc_req="${__omp_tc_req}
STDIN_B64
${__omp_tc_stdin_b64}
END"
  else
    __omp_tc_req="${__omp_tc_req}
END"
  fi

  __omp_tc_rc=1
  __omp_tc_got_rc=0
  __omp_tc_out_b64=""
  __omp_tc_err_b64=""

  if [ -n "${BASH_VERSION:-}" ]; then
    exec 8>&2 2>/dev/null
    if ! exec 9<>"/dev/tcp/127.0.0.1/${__omp_tc_port}"; then
      exec 2>&8 8>&-
      printf '%%s: tool-command bridge unavailable (bash /dev/tcp failed)\n' "$__omp_tc_name" >&2
      exit 127
    fi
    exec 2>&8 8>&-
    printf '%%s\n' "$__omp_tc_req" >&9
    while IFS= read -r __omp_tc_line <&9; do
      case "$__omp_tc_line" in
        RC\ *)  __omp_tc_rc="${__omp_tc_line#RC }"; __omp_tc_got_rc=1 ;;
        OUT\ *) __omp_tc_out_b64="${__omp_tc_line#OUT }" ;;
        ERR\ *) __omp_tc_err_b64="${__omp_tc_line#ERR }" ;;
        DONE)   break ;;
      esac
    done
    exec 9>&- 9<&-
  else
    printf '%%s: tool-command transport requires bash with /dev/tcp support\n' "$__omp_tc_name" >&2
    exit 127
  fi

  if [ "$__omp_tc_got_rc" != "1" ]; then
    printf '%%s: tool-command bridge closed the connection\n' "$__omp_tc_name" >&2
    exit 1
  fi
  if [ -n "$__omp_tc_out_b64" ]; then
    __omp_tc_b64_dec "$__omp_tc_out_b64"
  fi
  if [ -n "$__omp_tc_err_b64" ]; then
    __omp_tc_b64_dec "$__omp_tc_err_b64" >&2
  fi
  exit "$__omp_tc_rc"
)
`, port, singleQuoteShell(token))
}
