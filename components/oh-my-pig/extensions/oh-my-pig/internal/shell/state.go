package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// sessionState persists shell state (variables, functions, aliases, shell options)
// across calls for one session using a snapshot file written atomically after
// each completed command. A killed/timed-out command never updates the snapshot,
// so a failed or timed-out run cannot corrupt the session's state.
//
// Working directory is intentionally NOT persisted: each call starts in the
// explicit `cwd` parameter if given, else the session's working directory
// (matching omp's bash tool contract: "cwd will reset after each turn, don't
// rely on cd").
type sessionState struct {
	mu      sync.Mutex
	inUse   bool
	dir     string // directory holding snapshot.sh
	file    string // path to snapshot.sh
	created bool
}

// acquire claims exclusive access to the session's persistent shell state.
// Returns (snapshotPath, release, busy). If another command is already running
// in this session (parallel or backgrounded), busy is true and the caller runs
// in an isolated temporary shell (matching omp's SESSION_SHELL_BUSY_NOTICE).
func (s *sessionState) acquire() (snapshotPath string, release func(newSnapshot string), busy bool) {
	s.mu.Lock()
	if s.inUse {
		s.mu.Unlock()
		return "", nil, true
	}
	s.inUse = true
	if s.file == "" {
		dir, err := os.MkdirTemp("", "omp-shell-state-*")
		if err == nil {
			s.dir = dir
			s.file = filepath.Join(dir, "state.sh")
		}
	}
	path := ""
	if s.created {
		path = s.file
	}
	targetFile := s.file
	s.mu.Unlock()

	return path, func(newSnapshot string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if newSnapshot != "" && targetFile != "" {
			if err := os.Rename(newSnapshot, targetFile); err != nil {
				// Fallback across filesystems if Rename fails.
				if data, rErr := os.ReadFile(newSnapshot); rErr == nil {
					if wErr := os.WriteFile(targetFile, data, 0o600); wErr == nil {
						s.created = true
					}
				}
				_ = os.Remove(newSnapshot)
			} else {
				s.created = true
			}
		} else if newSnapshot != "" {
			_ = os.Remove(newSnapshot)
		}
		s.inUse = false
	}, false
}

// dispose removes the session's snapshot directory.
func (s *sessionState) dispose() {
	s.mu.Lock()
	dir := s.dir
	s.dir = ""
	s.file = ""
	s.created = false
	s.mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// wrapCommandWithState wraps a bash command so it restores prior session state
// from loadFile (if non-empty), runs the user's command in the requested cwd,
// and saves updated variables, functions, aliases, and shell options into
// saveFile (if non-empty) while preserving the command's exit status.
//
// In bash, aliases are expanded at parse time unless `shopt -s expand_aliases`
// is set before parsing the command, and commands in the same compound block as
// `source` are parsed before `source` runs. We therefore `eval` the user's
// command after sourcing the state file so restored aliases expand cleanly.
func wrapCommandWithState(command, loadFile, saveFile string) string {
	var b strings.Builder
	b.WriteString("shopt -s expand_aliases 2>/dev/null || true\n")
	if loadFile != "" {
		// Restore prior shell state without leaking diagnostics to stdout/stderr.
		fmt.Fprintf(&b, "if [ -r %s ]; then . %s 2>/dev/null || true; fi\n", singleQuotePosix(loadFile), singleQuotePosix(loadFile))
		// `set +o posix` in bash 3.2 resets `expand_aliases` to off; re-enable
		// it before evaluating the user's command so restored aliases expand.
		b.WriteString("shopt -s expand_aliases 2>/dev/null || true\n")
	}
	// Save shell state via an EXIT trap so even commands that invoke `exit N`
	// or fail under `set -e` still persist variables, functions, aliases, and
	// shell options (matching omp's bash-session-state.test.ts).
	if saveFile != "" {
		qSave := singleQuotePosix(saveFile)
		b.WriteString("__omp_save_state() {\n")
		b.WriteString("  local __omp_rc=$?\n")
		b.WriteString("  local __omp_opts\n")
		b.WriteString("  __omp_opts=$(set +o 2>/dev/null || true)\n")
		b.WriteString("  set +e +u 2>/dev/null || true\n")
		b.WriteString("  {\n")
		b.WriteString("    echo 'shopt -s expand_aliases 2>/dev/null || true'\n")
		// Use `compgen -v` + `declare -p <name>` so both bash 3.2 (macOS /bin/bash,
		// where bare `declare -p` omits the `declare` prefix) and bash 4/5 emit
		// uniform `declare -<flags> NAME=VALUE` lines.
		b.WriteString("    local __omp_v __omp_decl\n")
		b.WriteString("    for __omp_v in $(compgen -v 2>/dev/null); do\n")
		b.WriteString("      case \"$__omp_v\" in\n")
		b.WriteString("        BASH|BASH_*|COMP_*|COPROC|DIRSTACK|EUID|FUNCNAME|GROUPS|HISTCMD|HOSTNAME|HOSTTYPE|IFS|LINENO|MACHTYPE|OLDPWD|OPTARG|OPTIND|OSTYPE|PIPESTATUS|PPID|PS1|PS2|PS3|PS4|PWD|RANDOM|READLINE_*|REPLY|SECONDS|SHELLOPTS|SHLVL|SRANDOM|UID|_|__omp_*) continue ;;\n")
		b.WriteString("      esac\n")
		b.WriteString("      __omp_decl=$(declare -p \"$__omp_v\" 2>/dev/null) || continue\n")
		b.WriteString("      case \"$__omp_decl\" in\n")
		b.WriteString("        'declare -'*r*' '*) continue ;;\n")
		b.WriteString("      esac\n")
		b.WriteString("      printf '%s\\n' \"$__omp_decl\"\n")
		b.WriteString("    done\n")
		b.WriteString("    declare -f 2>/dev/null | awk '/^__omp_save_state \\(\\)/{skip=1} !skip{print} skip && /^}$/{skip=0}' || true\n")
		b.WriteString("    alias -p 2>/dev/null || true\n")
		b.WriteString("    shopt -p 2>/dev/null | grep -vE '(login_shell|restricted_shell|expand_aliases)$' || true\n")
		b.WriteString("    printf '%s\\n' \"$__omp_opts\" | grep -vE ' (monitor|interactive|stdin|hashall|history|histexpand)$' || true\n")
		fmt.Fprintf(&b, "  } > %s 2>/dev/null || true\n", qSave)
		b.WriteString("  exit $__omp_rc\n")
		b.WriteString("}\n")
		b.WriteString("trap '__omp_save_state' EXIT\n")
	}
	// Run the user command via eval so aliases restored from loadFile take effect
	// during parsing of `command`.
	fmt.Fprintf(&b, "eval %s\n", singleQuotePosix(command))
	return b.String()
}
