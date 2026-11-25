//go:build windows

package toolcmd

import "fmt"

// renderDispatcherFunction emits the Git Bash / MSYS2 dispatcher on Windows.
//
// Security and shell-hygiene properties match preamble_unix.go:
//   - Caller (`renderPreamble`) clears/restores `set -a` and unexports all
//     `__omp_tc_*` vars/functions so child processes never inherit the token.
//   - Helper paths (`base64`, `tr`) are resolved once at source time (`command -p`
//     first, then `PATH`), with zero per-argument probing.
//   - Subshell function body isolates `set +x +v +e +u +a`, `pipefail`, and FD 8/9.
//   - Uses `pwd -W` (falling back to `$PWD`) so Go on Windows receives a native
//     Windows path (`C:/...`) for relative `@file` resolution rather than MSYS `/c/...`.
//   - Reports `<name>: tool-command bridge closed the connection` on EOF without RC.
func renderDispatcherFunction(port int, token string) string {
	return fmt.Sprintf(`__omp_tc_port=%d
__omp_tc_token=%s
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
  command -p rm -f "${BASH_SOURCE[0]}" 2>/dev/null || rm -f "${BASH_SOURCE[0]}" 2>/dev/null || true
fi
__omp_tc_b64_cmd=$(command -p -v base64 2>/dev/null || command -v base64 2>/dev/null || true)
__omp_tc_tr_cmd=$(command -p -v tr 2>/dev/null || command -v tr 2>/dev/null || true)
__omp_tc_b64_dec_flag="-d"
__omp_tc_openssl_cmd=""
if [ -z "$__omp_tc_b64_cmd" ] || [ -z "$__omp_tc_tr_cmd" ]; then
  __omp_tc_b64_cmd=""
fi
__omp_tc_b64_enc() {
  if [ -n "$__omp_tc_b64_cmd" ]; then
    "$__omp_tc_b64_cmd" | "$__omp_tc_tr_cmd" -d '\r\n'
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
  else
    return 1
  fi
}
__omp_tc_call() (
  { set +x +v +e +u +a; set +o pipefail; } 2>/dev/null
  export -n __omp_tc_port __omp_tc_token __omp_tc_b64_cmd __omp_tc_b64_dec_flag __omp_tc_tr_cmd __omp_tc_openssl_cmd 2>/dev/null || true
  __omp_tc_name="$1"
  shift
  if [ -z "${BASH_VERSION:-}" ]; then
    printf '%%s: tool-command transport on Windows requires Git Bash with /dev/tcp support\n' "$__omp_tc_name" >&2
    exit 127
  fi
  __omp_tc_cwd=$(pwd -W 2>/dev/null || printf '%%s' "$PWD")
  __omp_tc_cwd_b64=$(printf '%%s' "$__omp_tc_cwd" | __omp_tc_b64_enc) || {
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
  exec 8>&2 2>/dev/null
  if ! exec 9<>"/dev/tcp/127.0.0.1/${__omp_tc_port}"; then
    exec 2>&8 8>&-
    printf '%%s: tool-command transport unsupported in this Windows shell (/dev/tcp unavailable)\n' "$__omp_tc_name" >&2
    exit 127
  fi
  exec 2>&8 8>&-
  printf '%%s\n' "$__omp_tc_req" >&9
  __omp_tc_rc=1
  __omp_tc_got_rc=0
  __omp_tc_out_b64=""
  __omp_tc_err_b64=""
  while IFS= read -r __omp_tc_line <&9; do
    __omp_tc_line="${__omp_tc_line%%$'\r'}"
    case "$__omp_tc_line" in
      RC\ *)  __omp_tc_rc="${__omp_tc_line#RC }"; __omp_tc_got_rc=1 ;;
      OUT\ *) __omp_tc_out_b64="${__omp_tc_line#OUT }" ;;
      ERR\ *) __omp_tc_err_b64="${__omp_tc_line#ERR }" ;;
      DONE)   break ;;
    esac
  done
  exec 9>&- 9<&-
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
