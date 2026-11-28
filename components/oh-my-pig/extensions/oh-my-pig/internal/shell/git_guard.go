package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: crates/shell/src/git/guard.rs, crates/shell/src/git/mod.rs,
// packages/coding-agent/src/exec/settings.ts:155-166

const (
	// gitGuardDiscardedRef is the ref whose reflog records uncommitted changes
	// before a discarding command in a private checkout runs.
	// upstream: crates/shell/src/git/guard.rs:53
	gitGuardDiscardedRef = "refs/omp/discarded"

	// gitGuardOwnWorktreeHint is the default separate-checkout hint.
	// upstream: crates/shell/src/git/guard.rs:56-57
	gitGuardOwnWorktreeHint = "In a worktree of your own (git worktree add <path> [<branch>]) it runs, and uncommitted changes it would discard are saved first."

	// gitGuardProbeTimeout bounds every helper `git` subprocess so the guard
	// never hangs a tool call.
	gitGuardProbeTimeout = 5 * time.Second
)

// gitRepoLocationEnvVars mirrors GIT_REPO_LOCATION_ENV_VARS in
// crates/shell/src/git/mod.rs:53-60.
var gitRepoLocationEnvVars = []string{
	"GIT_DIR",
	"GIT_COMMON_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
}

func isGitRepoLocationVar(key string) bool {
	if runtime.GOOS == "windows" {
		for _, name := range gitRepoLocationEnvVars {
			if strings.EqualFold(key, name) {
				return true
			}
		}
		return false
	}
	for _, name := range gitRepoLocationEnvVars {
		if key == name {
			return true
		}
	}
	return false
}

// gitGuardDecision is the pre-execution result of evaluating a shell command
// under `bash.gitGuard`.
type gitGuardDecision struct {
	Refusal string   // non-empty => refuse command before spawning bash
	Notes   []string // non-empty => notices about snapshots saved on refs/omp/discarded
}

// EvaluateShellGitGuard is the exported hook wired by wiring.go into
// `eval.ShellGuard` so `sh()` in eval cells enforces `bash.gitGuard` when
// enabled. Disabled sessions return immediately with zero overhead.
func EvaluateShellGitGuard(goCtx context.Context, sdkCtx sdk.Context, command, cwd string) (refusal string, notes []string) {
	if !readSettingBool(sdkCtx, "bash.gitGuard", false) {
		return "", nil
	}
	var sessState *sessionState
	if defaultRuntime != nil {
		if sid := strictSessionID(sdkCtx); sid != "" {
			sessState = defaultRuntime.sessionState(sid)
		}
	}
	dec := checkBashGitGuardWithState(goCtx, command, cwd, sessState)
	return dec.Refusal, dec.Notes
}

// checkBashGitGuard inspects every parsed command segment in `command` under
// `baseCwd` when `bash.gitGuard` is enabled.
func checkBashGitGuard(ctx context.Context, command string, baseCwd string) gitGuardDecision {
	return checkBashGitGuardWithState(ctx, command, baseCwd, nil)
}

func checkBashGitGuardWithState(ctx context.Context, command string, baseCwd string, sessState *sessionState) gitGuardDecision {
	return checkBashGitGuardDepth(ctx, command, baseCwd, sessState, 0)
}

const maxGitGuardRecurseDepth = 4

type guardShellSegment struct {
	words         []string
	prevConnector string // "", "&&", "||", ";", "|", "(", ")", "subst"
}

// stripHeredocBodies removes heredoc body lines (`<<EOF ... EOF`, `<<-'EOF' ... EOF`)
// so content inside heredocs never triggers false git-guard refusals or corrupts
// quote state in the tokenizer (M4).
func stripHeredocBodies(command string) string {
	if !strings.Contains(command, "<<") {
		return command
	}
	lines := strings.Split(command, "\n")
	var out []string
	var pending []struct {
		delim     string
		stripTabs bool
	}
	heredocOpRE := regexp.MustCompile(`<<(-?)\s*(['"]?)([A-Za-z_][A-Za-z0-9_.-]*|[^\s'"<>|;&()]+)['"]?`)
	for _, line := range lines {
		if len(pending) > 0 {
			cur := pending[0]
			check := strings.TrimSuffix(line, "\r")
			if cur.stripTabs {
				check = strings.TrimLeft(check, "\t")
			}
			if check == cur.delim {
				pending = pending[1:]
			}
			continue
		}
		out = append(out, line)
		// Scan for << (excluding <<< here-strings) outside single/double quotes.
		inSingle, inDouble, escaped := false, false, false
		for i := 0; i < len(line); i++ {
			ch := line[i]
			if escaped {
				escaped = false
				continue
			}
			if inSingle {
				if ch == '\'' {
					inSingle = false
				}
				continue
			}
			if inDouble {
				if ch == '\\' {
					escaped = true
				} else if ch == '"' {
					inDouble = false
				}
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '\'' {
				inSingle = true
				continue
			}
			if ch == '"' {
				inDouble = true
				continue
			}
			if ch == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
				break
			}
			if ch == '<' && i+1 < len(line) && line[i+1] == '<' {
				if i+2 < len(line) && line[i+2] == '<' {
					i += 2
					continue
				}
				if m := heredocOpRE.FindStringSubmatchIndex(line[i:]); m != nil && m[0] == 0 {
					stripTabs := line[i+m[2]:i+m[3]] == "-"
					delim := line[i+m[6] : i+m[7]]
					if delim != "" {
						pending = append(pending, struct {
							delim     string
							stripTabs bool
						}{delim: delim, stripTabs: stripTabs})
					}
					i += m[1] - 1
				}
			}
		}
	}
	return strings.Join(out, "\n")
}

// tokenizeGitGuardSegments splits a shell command into segments while preserving
// the connector before each segment (`&&`, `||`, `;`, `|`, `(`, `subst`),
// handling `\`+newline line continuations, and extracting `$(...)` and “ `...` “
// command substitutions (including inside double quotes) as their own segments (H1, H3).
func tokenizeGitGuardSegments(command string) []guardShellSegment {
	// Strip heredoc bodies first (M4).
	command = stripHeredocBodies(command)
	// Join backslash-newline line continuations outside single quotes (H3).
	var norm strings.Builder
	norm.Grow(len(command))
	inSingle := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			norm.WriteByte(ch)
			continue
		}
		if ch == '\'' {
			inSingle = true
			norm.WriteByte(ch)
			continue
		}
		if ch == '\\' && i+1 < len(command) {
			if command[i+1] == '\n' {
				i++
				continue
			}
			if command[i+1] == '\r' && i+2 < len(command) && command[i+2] == '\n' {
				i += 2
				continue
			}
		}
		norm.WriteByte(ch)
	}
	src := norm.String()

	var segments []guardShellSegment
	var substs []string
	var current []string
	var tok strings.Builder
	hasTok := false
	nextConn := ""

	flushTok := func() {
		if hasTok {
			current = append(current, tok.String())
			tok.Reset()
			hasTok = false
		}
	}
	flushSeg := func(conn string) {
		flushTok()
		if len(current) > 0 {
			segments = append(segments, guardShellSegment{words: current, prevConnector: nextConn})
			current = nil
		}
		nextConn = conn
	}

	inS, inD, esc := false, false, false
	for i := 0; i < len(src); i++ {
		ch := src[i]
		if esc {
			tok.WriteByte(ch)
			hasTok = true
			esc = false
			continue
		}
		if inS {
			if ch == '\'' {
				inS = false
			} else {
				tok.WriteByte(ch)
				hasTok = true
			}
			continue
		}
		if inD {
			if ch == '\\' {
				esc = true
				continue
			}
			if ch == '"' {
				inD = false
				continue
			}
			if ch == '$' && i+1 < len(src) && src[i+1] == '(' {
				inner, end := extractParenSubst(src, i+2)
				if inner != "" {
					substs = append(substs, inner)
				}
				tok.WriteString(src[i : end+1])
				hasTok = true
				i = end
				continue
			}
			if ch == '`' {
				inner, end := extractBacktickSubst(src, i+1)
				if inner != "" {
					substs = append(substs, inner)
				}
				tok.WriteString(src[i : end+1])
				hasTok = true
				i = end
				continue
			}
			tok.WriteByte(ch)
			hasTok = true
			continue
		}
		switch ch {
		case '\\':
			esc = true
		case '\'':
			inS = true
			hasTok = true
		case '"':
			inD = true
			hasTok = true
		case ' ', '\t', '\r':
			flushTok()
		case '\n', ';':
			flushSeg(";")
		case '&':
			if i+1 < len(src) && src[i+1] == '&' {
				flushSeg("&&")
				i++
			} else if i+1 < len(src) && src[i+1] == '>' {
				tok.WriteString("&>")
				hasTok = true
				i++
			} else {
				flushSeg(";")
			}
		case '|':
			if i+1 < len(src) && src[i+1] == '|' {
				flushSeg("||")
				i++
			} else {
				flushSeg("|")
			}
		case '(':
			flushSeg("(")
		case ')':
			flushSeg(")")
		case '$':
			if i+1 < len(src) && src[i+1] == '(' {
				inner, end := extractParenSubst(src, i+2)
				if inner != "" {
					substs = append(substs, inner)
				}
				tok.WriteString(src[i : end+1])
				hasTok = true
				i = end
			} else {
				tok.WriteByte(ch)
				hasTok = true
			}
		case '`':
			inner, end := extractBacktickSubst(src, i+1)
			if inner != "" {
				substs = append(substs, inner)
			}
			tok.WriteString(src[i : end+1])
			hasTok = true
			i = end
		case '#':
			if !hasTok {
				for i < len(src) && src[i] != '\n' {
					i++
				}
				flushSeg(";")
			} else {
				tok.WriteByte(ch)
			}
		default:
			tok.WriteByte(ch)
			hasTok = true
		}
	}
	flushSeg("")

	for _, sub := range substs {
		subSegs := tokenizeGitGuardSegments(sub)
		for j := range subSegs {
			if j == 0 {
				subSegs[j].prevConnector = "subst"
			}
			segments = append(segments, subSegs[j])
		}
	}
	return segments
}

func extractParenSubst(src string, start int) (string, int) {
	depth := 1
	inS, inD, esc := false, false, false
	for i := start; i < len(src); i++ {
		ch := src[i]
		if esc {
			esc = false
			continue
		}
		if inS {
			if ch == '\'' {
				inS = false
			}
			continue
		}
		if inD {
			if ch == '\\' {
				esc = true
			} else if ch == '"' {
				inD = false
			}
			continue
		}
		switch ch {
		case '\\':
			esc = true
		case '\'':
			inS = true
		case '"':
			inD = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return src[start:i], i
			}
		}
	}
	return src[start:], len(src) - 1
}

func extractBacktickSubst(src string, start int) (string, int) {
	esc := false
	for i := start; i < len(src); i++ {
		ch := src[i]
		if esc {
			esc = false
			continue
		}
		if ch == '\\' {
			esc = true
			continue
		}
		if ch == '`' {
			return src[start:i], i
		}
	}
	return src[start:], len(src) - 1
}

// shellRedirRE matches shell redirection tokens like `2>&1`, `>/dev/null`, `2>`, `>>`, `<<<`, `&>`.
var shellRedirRE = regexp.MustCompile(`^\d*(>>?|<<?<?|>&|<&|&>)(.*)$`)

// stripShellRedirections removes shell redirection tokens and their separate
// target filename arguments from a command's argument list (H2).
func stripShellRedirections(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		w := args[i]
		if m := shellRedirRE.FindStringSubmatch(w); m != nil {
			// If there is no attached target (e.g., `>` or `2>` followed by `/dev/null`),
			// consume the next word as the redirection target too.
			if m[2] == "" && i+1 < len(args) {
				i++
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

// seedSessionExportsAndAliases extracts exported env var names, aliases, and
// functions from os.Environ() and persisted shell session state (M1).
func seedSessionExportsAndAliases(sessState *sessionState) (exported map[string]bool, aliases map[string]string, funcs map[string]string) {
	exported = make(map[string]bool)
	for _, kv := range os.Environ() {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			name := kv[:eq]
			if isGitRepoLocationVar(name) || name == "GIT_CONFIG_COUNT" || (runtime.GOOS == "windows" && strings.EqualFold(name, "GIT_CONFIG_COUNT")) {
				exported[name] = true
			}
		}
	}
	if sessState == nil {
		return exported, nil, nil
	}
	sessState.mu.Lock()
	defer sessState.mu.Unlock()
	if sessState.snap == nil {
		return exported, nil, nil
	}
	for name, decl := range sessState.snap.vars {
		// `declare -x ...` or `declare -...x...` marks an exported variable.
		if isExportedDeclare(decl) {
			exported[name] = true
		}
	}
	for name := range sessState.snap.unsetVars {
		delete(exported, name)
	}
	if len(sessState.snap.aliases) > 0 {
		aliases = make(map[string]string, len(sessState.snap.aliases))
		for k, v := range sessState.snap.aliases {
			aliases[k] = parseAliasValue(v)
		}
	}
	if len(sessState.snap.funcs) > 0 {
		funcs = make(map[string]string, len(sessState.snap.funcs))
		for k, v := range sessState.snap.funcs {
			funcs[k] = v
		}
	}
	return exported, aliases, funcs
}

func isExportedDeclare(decl string) bool {
	// Format: `declare -x NAME=...` or `declare -rx NAME=...`
	fields := strings.Fields(decl)
	if len(fields) >= 2 && fields[0] == "declare" && strings.HasPrefix(fields[1], "-") {
		return strings.ContainsRune(fields[1], 'x')
	}
	return false
}

func parseAliasValue(def string) string {
	// Format: `alias name='value'` or `alias name="value"` or `alias name=value`
	s := strings.TrimSpace(def)
	s = strings.TrimPrefix(s, "alias ")
	if eq := strings.IndexByte(s, '='); eq >= 0 {
		val := strings.TrimSpace(s[eq+1:])
		if len(val) >= 2 && ((val[0] == '\'' && val[len(val)-1] == '\'') || (val[0] == '"' && val[len(val)-1] == '"')) {
			return val[1 : len(val)-1]
		}
		return val
	}
	return ""
}

func checkBashGitGuardDepth(ctx context.Context, command string, baseCwd string, sessState *sessionState, depth int) gitGuardDecision {
	if strings.TrimSpace(command) == "" || depth > maxGitGuardRecurseDepth {
		return gitGuardDecision{}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	segments := tokenizeGitGuardSegments(command)
	if len(segments) == 0 {
		return gitGuardDecision{}
	}

	var notes []string
	// H1: Track working directory ONLY across top-level `&&`-chained segments.
	// Reset curCwd = baseCwd across `;`, `||`, `|`, `(`, `)`, or `subst`.
	curCwd := baseCwd
	inSubshell := false
	sessionExported, sessionAliases, sessionFuncs := seedSessionExportsAndAliases(sessState)

	for segIdx, seg := range segments {
		switch seg.prevConnector {
		case "(":
			inSubshell = true
			curCwd = baseCwd
		case ")":
			inSubshell = false
			curCwd = baseCwd
		case ";", "||", "|", "subst":
			curCwd = baseCwd
		}
		words := seg.words
		if len(words) == 0 {
			continue
		}

		// Handle `export VAR=val ...` so subsequent `git` invocations see them.
		if words[0] == "export" {
			for _, w := range words[1:] {
				if strings.HasPrefix(w, "-") {
					continue
				}
				name := w
				if eq := strings.IndexByte(w, '='); eq >= 0 {
					name = w[:eq]
				}
				if name != "" {
					sessionExported[name] = true
				}
			}
			continue
		}
		if words[0] == "unset" {
			for _, w := range words[1:] {
				if !strings.HasPrefix(w, "-") {
					delete(sessionExported, w)
				}
			}
			continue
		}

		// Strip leading inline env assignments and command wrappers (H3).
		var inlineEnv []string
		idx := skipCommandWrappers(words, &inlineEnv)

		if idx >= len(words) {
			for _, k := range inlineEnv {
				sessionExported[k] = true
			}
			continue
		}

		cmdWord := words[idx]
		cmdArgs := words[idx+1:]

		// M1: Expand persisted shell alias or function for cmdWord.
		if aliasVal, ok := sessionAliases[cmdWord]; ok && aliasVal != "" {
			expanded := aliasVal
			if len(cmdArgs) > 0 {
				expanded += " " + strings.Join(cmdArgs, " ")
			}
			dec := checkBashGitGuardDepth(ctx, expanded, curCwd, sessState, depth+1)
			if dec.Refusal != "" {
				return gitGuardDecision{Refusal: dec.Refusal, Notes: notes}
			}
			notes = append(notes, dec.Notes...)
			continue
		}
		if fnBody, ok := sessionFuncs[cmdWord]; ok && fnBody != "" {
			dec := checkBashGitGuardDepth(ctx, fnBody, curCwd, sessState, depth+1)
			if dec.Refusal != "" {
				return gitGuardDecision{Refusal: dec.Refusal, Notes: notes}
			}
			notes = append(notes, dec.Notes...)
			continue
		}

		// H3: Recursively inspect `eval <args>` and `sh/bash/zsh/dash -c <script>`.
		if innerScript, ok := extractEvalOrShellScript(cmdWord, cmdArgs); ok {
			dec := checkBashGitGuardDepth(ctx, innerScript, curCwd, sessState, depth+1)
			if dec.Refusal != "" {
				return gitGuardDecision{Refusal: dec.Refusal, Notes: notes}
			}
			notes = append(notes, dec.Notes...)
			continue
		}

		// H1: Track `cd <target>` ONLY outside subshells (and only propagates across `&&`).
		if cmdWord == "cd" && len(cmdArgs) > 0 {
			if !inSubshell {
				target := cmdArgs[0]
				if target == "--" && len(cmdArgs) > 1 {
					target = cmdArgs[1]
				}
				if target != "" && target != "-" && !strings.HasPrefix(target, "-") && !hasDynamicPathChars(target) {
					resolved := resolveShellDirectory(target, curCwd)
					if _, err := os.Stat(resolved); err == nil {
						curCwd = resolved
					} else {
						// Non-existent cd target: keep baseCwd so we never map to Outside.
						curCwd = baseCwd
					}
				}
			}
			continue
		}

		if !isGitCommandWord(cmdWord) {
			continue
		}

		exported := make([]string, 0, len(inlineEnv)+len(sessionExported))
		exported = append(exported, inlineEnv...)
		for k := range sessionExported {
			exported = append(exported, k)
		}

		// H2: Strip shell redirections before parsing git invocation.
		cleanArgs := stripShellRedirections(cmdArgs)

		isFirstSegment := segIdx == 0 && depth == 0
		dec := evaluateGitGuardInvocation(ctx, curCwd, baseCwd, exported, cleanArgs, isFirstSegment)
		if dec.Refusal != "" {
			return gitGuardDecision{Refusal: dec.Refusal, Notes: notes}
		}
		if len(dec.Notes) > 0 {
			notes = append(notes, dec.Notes...)
		}
	}

	return gitGuardDecision{Notes: notes}
}

func hasDynamicPathChars(s string) bool {
	return strings.ContainsAny(s, "$`*?[]")
}

// skipCommandWrappers advances past inline env assignments (`KEY=VAL`), `env`,
// and common command wrappers (`command`, `builtin`, `exec`, `nohup`, `time`,
// `timeout <dur>`, `nice`, `sudo`, `xargs`) with their flags (H3).
func skipCommandWrappers(words []string, inlineEnv *[]string) int {
	idx := 0
	for idx < len(words) {
		w := words[idx]
		if shellEnvAssignRE.MatchString(w) {
			eq := strings.IndexByte(w, '=')
			*inlineEnv = append(*inlineEnv, w[:eq])
			idx++
			continue
		}
		switch w {
		case "env":
			idx++
			for idx < len(words) {
				ew := words[idx]
				if ew == "--" {
					idx++
					break
				}
				if ew == "-u" || ew == "--unset" || ew == "-C" || ew == "--chdir" || ew == "-S" || ew == "--split-string" {
					idx += 2
					continue
				}
				if strings.HasPrefix(ew, "-") && ew != "-" {
					idx++
					continue
				}
				if shellEnvAssignRE.MatchString(ew) {
					eq := strings.IndexByte(ew, '=')
					*inlineEnv = append(*inlineEnv, ew[:eq])
					idx++
					continue
				}
				break
			}
			continue
		case "command", "builtin", "exec", "nohup", "time":
			idx++
			for idx < len(words) && strings.HasPrefix(words[idx], "-") && words[idx] != "-" {
				if words[idx] == "--" {
					idx++
					break
				}
				if w == "exec" && words[idx] == "-a" && idx+1 < len(words) {
					idx += 2
					continue
				}
				idx++
			}
			continue
		case "nice":
			idx++
			for idx < len(words) && strings.HasPrefix(words[idx], "-") && words[idx] != "-" {
				if words[idx] == "-n" && idx+1 < len(words) {
					idx += 2
					continue
				}
				idx++
			}
			continue
		case "timeout":
			idx++
			for idx < len(words) && strings.HasPrefix(words[idx], "-") && words[idx] != "-" {
				if (words[idx] == "-s" || words[idx] == "--signal" || words[idx] == "-k" || words[idx] == "--kill-after") && idx+1 < len(words) {
					idx += 2
					continue
				}
				idx++
			}
			// Skip the duration positional argument.
			if idx < len(words) {
				idx++
			}
			continue
		case "sudo":
			idx++
			for idx < len(words) && strings.HasPrefix(words[idx], "-") && words[idx] != "-" {
				if words[idx] == "--" {
					idx++
					break
				}
				if (words[idx] == "-u" || words[idx] == "-g" || words[idx] == "-C" || words[idx] == "-D" || words[idx] == "-h" || words[idx] == "-p" || words[idx] == "-r" || words[idx] == "-t" || words[idx] == "-U") && idx+1 < len(words) {
					idx += 2
					continue
				}
				idx++
			}
			continue
		case "xargs":
			idx++
			for idx < len(words) && strings.HasPrefix(words[idx], "-") && words[idx] != "-" {
				if words[idx] == "--" {
					idx++
					break
				}
				if (words[idx] == "-I" || words[idx] == "-L" || words[idx] == "-n" || words[idx] == "-P" || words[idx] == "-s" || words[idx] == "-d" || words[idx] == "-E" || words[idx] == "-a") && idx+1 < len(words) {
					idx += 2
					continue
				}
				idx++
			}
			continue
		}
		if shellCommandPrefixWords[w] {
			idx++
			continue
		}
		break
	}
	return idx
}

// extractEvalOrShellScript extracts the inner command string for `eval ...` or
// `sh/bash/zsh/dash -c <script>` (H3).
func extractEvalOrShellScript(cmdWord string, args []string) (string, bool) {
	base := filepath.Base(cmdWord)
	if base == "eval" {
		if len(args) == 0 {
			return "", false
		}
		return strings.Join(args, " "), true
	}
	switch base {
	case "sh", "bash", "zsh", "dash":
		for i := 0; i < len(args); i++ {
			a := args[i]
			if a == "--" {
				break
			}
			if a == "-c" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], 'c')) {
				if i+1 < len(args) {
					return args[i+1], true
				}
			}
		}
	}
	return "", false
}

func isGitCommandWord(word string) bool {
	if word == "git" || word == "git.exe" {
		return true
	}
	base := filepath.Base(word)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(base, "git") || strings.EqualFold(base, "git.exe")
	}
	return base == "git"
}

// gitSub represents a guarded git subcommand.
// upstream: crates/shell/src/git/guard.rs:445-583
type gitSub int

const (
	gitSubNone gitSub = iota
	gitSubStash
	gitSubReset
	gitSubCheckout
	gitSubSwitch
	gitSubRestore
)

func parseGitSub(name string) gitSub {
	switch name {
	case "stash":
		return gitSubStash
	case "reset":
		return gitSubReset
	case "checkout":
		return gitSubCheckout
	case "switch":
		return gitSubSwitch
	case "restore":
		return gitSubRestore
	default:
		return gitSubNone
	}
}

func (s gitSub) name() string {
	switch s {
	case gitSubStash:
		return "stash"
	case gitSubReset:
		return "reset"
	case gitSubCheckout:
		return "checkout"
	case gitSubSwitch:
		return "switch"
	case gitSubRestore:
		return "restore"
	default:
		return ""
	}
}

func (s gitSub) valuedOptions() (shortValued string, longValued []string) {
	switch s {
	case gitSubStash:
		return "", nil
	case gitSubReset:
		return "U", []string{"pathspec-from-file", "unified", "inter-hunk-context"}
	case gitSubCheckout:
		return "bBU", []string{"orphan", "conflict", "pathspec-from-file", "unified", "inter-hunk-context"}
	case gitSubSwitch:
		return "cC", []string{"orphan", "conflict"}
	case gitSubRestore:
		return "sU", []string{"source", "conflict", "pathspec-from-file", "unified", "inter-hunk-context"}
	default:
		return "", nil
	}
}

func (s gitSub) longOptions() []string {
	switch s {
	case gitSubStash:
		return nil
	case gitSubReset:
		return []string{
			"auto-advance", "hard", "intent-to-add", "inter-hunk-context",
			"keep", "merge", "mixed", "patch", "pathspec-file-nul",
			"pathspec-from-file", "quiet", "recurse-submodules",
			"refresh", "soft", "unified",
		}
	case gitSubCheckout:
		return []string{
			"auto-advance", "conflict", "detach", "force", "guess",
			"ignore-other-worktrees", "ignore-skip-worktree-bits",
			"inter-hunk-context", "merge", "orphan", "ours", "overlay",
			"overwrite-ignore", "patch", "pathspec-file-nul",
			"pathspec-from-file", "progress", "quiet",
			"recurse-submodules", "theirs", "track", "unified",
		}
	case gitSubSwitch:
		return []string{
			"conflict", "create", "detach", "discard-changes", "force",
			"force-create", "guess", "ignore-other-worktrees", "merge",
			"orphan", "overwrite-ignore", "progress", "quiet",
			"recurse-submodules", "track",
		}
	case gitSubRestore:
		return []string{
			"conflict", "ignore-skip-worktree-bits", "ignore-unmerged",
			"inter-hunk-context", "merge", "ours", "overlay", "patch",
			"pathspec-file-nul", "pathspec-from-file", "progress",
			"quiet", "recurse-submodules", "source", "staged",
			"theirs", "unified", "worktree",
		}
	default:
		return nil
	}
}

// gitInvocation is a parsed `git [<global options>] <sub> <args>...`.
// upstream: crates/shell/src/git/guard.rs:586-655
type gitInvocation struct {
	dirs       []string
	redirected bool
	recurse    *bool
	sub        gitSub
	args       []string
}

func parseGitInvocation(args []string) (gitInvocation, bool) {
	var inv gitInvocation
	at := 0
	for at < len(args) {
		arg := args[at]
		at++
		switch {
		case arg == "-C":
			if at >= len(args) {
				return gitInvocation{}, false
			}
			inv.dirs = append(inv.dirs, args[at])
			at++
		case strings.HasPrefix(arg, "-C") && len(arg) > 2:
			inv.dirs = append(inv.dirs, arg[2:])
		case arg == "--git-dir" || arg == "--work-tree":
			if at >= len(args) {
				return gitInvocation{}, false
			}
			inv.redirected = true
			at++
		case strings.HasPrefix(arg, "--git-dir=") || strings.HasPrefix(arg, "--work-tree="):
			inv.redirected = true
		case arg == "-c":
			if at >= len(args) {
				return gitInvocation{}, false
			}
			setting := strings.ToLower(args[at])
			if value, ok := strings.CutPrefix(setting, "submodule.recurse"); ok {
				b := !(value == "=false" || value == "=no" || value == "=off" || value == "=0")
				inv.recurse = &b
			}
			at++
		case arg == "--config-env":
			if at >= len(args) {
				return gitInvocation{}, false
			}
			setting := strings.ToLower(args[at])
			if strings.HasPrefix(setting, "submodule.recurse=") {
				b := true
				inv.recurse = &b
			}
			at++
		case strings.HasPrefix(strings.ToLower(arg), "--config-env=submodule.recurse="):
			b := true
			inv.recurse = &b
		case arg == "--namespace" || arg == "--super-prefix" || arg == "--attr-source":
			if at >= len(args) {
				return gitInvocation{}, false
			}
			at++
		case strings.HasPrefix(arg, "-"):
			// Other global flags take no separate argument.
		default:
			sub := parseGitSub(arg)
			if sub == gitSubNone {
				return gitInvocation{}, false
			}
			inv.sub = sub
			inv.args = args[at:]
			return inv, true
		}
	}
	return gitInvocation{}, false
}

// gitArgKind mirrors Arg in crates/shell/src/git/guard.rs:659-671.
type gitArgKind int

const (
	gitArgLong gitArgKind = iota
	gitArgNegated
	gitArgShort
	gitArgOperand
	gitArgPathspec
)

type gitArg struct {
	kind  gitArgKind
	text  string
	short rune
}

// resolveGitLongOption resolves an exact or uniquely-abbreviated long option
// (including `--no-` negation). upstream: crates/shell/src/git/mod.rs:428-448
func resolveGitLongOption(options []string, spelled string) (name string, negated bool, ok bool) {
	if spelled == "" {
		return "", false, false
	}
	type candidate struct {
		full    string
		option  string
		negated bool
	}
	for _, opt := range options {
		if spelled == opt {
			return opt, false, true
		}
		if spelled == "no-"+opt {
			return opt, true, true
		}
	}
	var match candidate
	matches := 0
	for _, opt := range options {
		if strings.HasPrefix(opt, spelled) {
			match = candidate{full: opt, option: opt, negated: false}
			matches++
		}
		noOpt := "no-" + opt
		if strings.HasPrefix(noOpt, spelled) {
			match = candidate{full: noOpt, option: opt, negated: true}
			matches++
		}
	}
	if matches == 1 {
		return match.option, match.negated, true
	}
	return "", false, false
}

func scanGitArgs(sub gitSub, args []string) []gitArg {
	shortValued, longValued := sub.valuedOptions()
	longOpts := sub.longOptions()
	var scanned []gitArg
	options := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			for _, p := range args[i+1:] {
				scanned = append(scanned, gitArg{kind: gitArgPathspec, text: p})
			}
			break
		}
		if !options {
			scanned = append(scanned, gitArg{kind: gitArgOperand, text: arg})
			continue
		}
		if arg == "--end-of-options" {
			options = false
			continue
		}
		if long, ok := strings.CutPrefix(arg, "--"); ok {
			spelled := long
			inline := false
			if eq := strings.IndexByte(long, '='); eq >= 0 {
				spelled = long[:eq]
				inline = true
			}
			if name, neg, found := resolveGitLongOption(longOpts, spelled); found {
				if neg {
					scanned = append(scanned, gitArg{kind: gitArgNegated, text: name})
				} else {
					if !inline && containsString(longValued, name) && i+1 < len(args) {
						i++
					}
					scanned = append(scanned, gitArg{kind: gitArgLong, text: name})
				}
			} else {
				scanned = append(scanned, gitArg{kind: gitArgLong, text: spelled})
			}
			continue
		}
		if bundle, ok := strings.CutPrefix(arg, "-"); ok && bundle != "" {
			runes := []rune(bundle)
			for idx, flag := range runes {
				scanned = append(scanned, gitArg{kind: gitArgShort, short: flag})
				if strings.ContainsRune(shortValued, flag) {
					if idx == len(runes)-1 && i+1 < len(args) {
						i++
					}
					break
				}
			}
			continue
		}
		scanned = append(scanned, gitArg{kind: gitArgOperand, text: arg})
	}
	return scanned
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

type gitFlagEntry struct {
	name string
	on   bool
}

type gitFlags struct {
	entries []gitFlagEntry
}

func collectGitFlags(sub gitSub, args []gitArg) gitFlags {
	var entries []gitFlagEntry
	for _, arg := range args {
		switch arg.kind {
		case gitArgLong:
			entries = append(entries, gitFlagEntry{name: arg.text, on: true})
		case gitArgNegated:
			entries = append(entries, gitFlagEntry{name: arg.text, on: false})
		case gitArgShort:
			switch {
			case (sub == gitSubReset || sub == gitSubCheckout || sub == gitSubRestore) && arg.short == 'p':
				entries = append(entries, gitFlagEntry{name: "patch", on: true})
			case (sub == gitSubCheckout || sub == gitSubSwitch) && arg.short == 'f':
				entries = append(entries, gitFlagEntry{name: "force", on: true})
			case sub == gitSubCheckout && arg.short == '2':
				entries = append(entries, gitFlagEntry{name: "ours", on: true})
			case sub == gitSubCheckout && arg.short == '3':
				entries = append(entries, gitFlagEntry{name: "theirs", on: true})
			case (sub == gitSubCheckout || sub == gitSubSwitch) && arg.short == 'd':
				entries = append(entries, gitFlagEntry{name: "detach", on: true})
			case (sub == gitSubCheckout || sub == gitSubSwitch) && arg.short == 't':
				entries = append(entries, gitFlagEntry{name: "track", on: true})
			case sub == gitSubRestore && arg.short == 'S':
				entries = append(entries, gitFlagEntry{name: "staged", on: true})
			case sub == gitSubRestore && arg.short == 'W':
				entries = append(entries, gitFlagEntry{name: "worktree", on: true})
			}
		}
	}
	return gitFlags{entries: entries}
}

func (f gitFlags) on(name string) bool {
	for i := len(f.entries) - 1; i >= 0; i-- {
		if f.entries[i].name == name {
			return f.entries[i].on
		}
	}
	return false
}

func (f gitFlags) any(names ...string) bool {
	for _, n := range names {
		if f.on(n) {
			return true
		}
	}
	return false
}

func (f gitFlags) last(names ...string) string {
	for i := len(f.entries) - 1; i >= 0; i-- {
		if f.entries[i].on && containsString(names, f.entries[i].name) {
			return f.entries[i].name
		}
	}
	return ""
}

// gitVerdictKind mirrors Verdict in crates/shell/src/git/guard.rs:724-752.
type gitVerdictKind int

const (
	gitVerdictAllow gitVerdictKind = iota
	gitVerdictStashSaves
	gitVerdictStashTakes
	gitVerdictStashClear
	gitVerdictDestructive
	gitVerdictResetTo
	gitVerdictConflictOnly
	gitVerdictBranchMove
	gitVerdictCheckoutOne
)

type gitVerdict struct {
	kind       gitVerdictKind
	stashOp    string
	stashEntry string
	hasEntry   bool
	rev        string
	soft       bool
	operand    string
}

func classifyGitCommand(sub gitSub, args []string) gitVerdict {
	switch sub {
	case gitSubStash:
		return classifyGitStash(args)
	case gitSubReset:
		return classifyGitReset(scanGitArgs(sub, args))
	case gitSubCheckout, gitSubSwitch, gitSubRestore:
		return classifyGitTreeWrite(sub, scanGitArgs(sub, args))
	default:
		return gitVerdict{kind: gitVerdictAllow}
	}
}

func gitRecurseOption(sub gitSub, args []string) *bool {
	scanned := scanGitArgs(sub, args)
	for i := len(scanned) - 1; i >= 0; i-- {
		switch scanned[i].kind {
		case gitArgLong:
			if scanned[i].text == "recurse-submodules" {
				b := true
				return &b
			}
		case gitArgNegated:
			if scanned[i].text == "recurse-submodules" {
				b := false
				return &b
			}
		}
	}
	return nil
}

func classifyGitStash(args []string) gitVerdict {
	if len(args) == 0 {
		return gitVerdict{kind: gitVerdictStashSaves}
	}
	var op string
	entryAt := 0
	switch args[0] {
	case "list", "show", "create":
		return gitVerdict{kind: gitVerdictAllow}
	case "clear":
		return gitVerdict{kind: gitVerdictStashClear}
	case "pop":
		op, entryAt = "pop", 0
	case "apply":
		op, entryAt = "apply", 0
	case "drop":
		op, entryAt = "drop", 0
	case "branch":
		op, entryAt = "branch", 1
	default:
		return gitVerdict{kind: gitVerdictStashSaves}
	}
	scanned := scanGitArgs(gitSubStash, args[1:])
	pos := 0
	for _, arg := range scanned {
		if arg.kind == gitArgOperand || arg.kind == gitArgPathspec {
			if pos == entryAt {
				return gitVerdict{
					kind:       gitVerdictStashTakes,
					stashOp:    op,
					stashEntry: arg.text,
					hasEntry:   true,
				}
			}
			pos++
		}
	}
	return gitVerdict{kind: gitVerdictStashTakes, stashOp: op}
}

func classifyGitReset(args []gitArg) gitVerdict {
	flags := collectGitFlags(gitSubReset, args)
	var operands []string
	pathspec := false
	for _, arg := range args {
		switch arg.kind {
		case gitArgPathspec:
			pathspec = true
		case gitArgOperand:
			operands = append(operands, arg.text)
		}
	}
	indexOnly := pathspec || flags.any("patch", "pathspec-from-file")
	mode := flags.last("hard", "soft", "mixed", "merge", "keep")
	if mode == "hard" {
		return gitVerdict{kind: gitVerdictDestructive}
	}
	if len(operands) == 1 && !indexOnly && operands[0] != "HEAD" && operands[0] != "@" {
		return gitVerdict{
			kind: gitVerdictResetTo,
			rev:  operands[0],
			soft: mode == "soft",
		}
	}
	return gitVerdict{kind: gitVerdictAllow}
}

func classifyGitTreeWrite(sub gitSub, args []gitArg) gitVerdict {
	if sub == gitSubCheckout && len(args) == 0 {
		return gitVerdict{kind: gitVerdictAllow}
	}
	flags := collectGitFlags(sub, args)
	var operands []string
	newBranch := false
	pathspec := false
	wholeTree := false
	for _, arg := range args {
		switch {
		case sub == gitSubCheckout && arg.kind == gitArgShort && (arg.short == 'b' || arg.short == 'B'):
			newBranch = true
		case sub == gitSubCheckout && arg.kind == gitArgOperand:
			if arg.text == "-" {
				newBranch = true
			}
			if isWholeTreePathspec(arg.text) {
				wholeTree = true
			}
			operands = append(operands, arg.text)
		case (sub == gitSubCheckout || sub == gitSubRestore) && arg.kind == gitArgPathspec,
			sub == gitSubRestore && arg.kind == gitArgOperand:
			if isWholeTreePathspec(arg.text) {
				wholeTree = true
			}
			pathspec = true
		}
	}
	forced := flags.on("force") || (sub == gitSubSwitch && flags.on("discard-changes"))
	branch := sub == gitSubSwitch || newBranch || flags.any("orphan", "detach", "track")
	paths := sub == gitSubRestore || pathspec || flags.any("ours", "theirs", "patch", "pathspec-from-file")
	if flags.on("staged") && !flags.on("worktree") {
		return gitVerdict{kind: gitVerdictAllow}
	}
	if forced || wholeTree {
		return gitVerdict{kind: gitVerdictDestructive}
	}
	if branch {
		return gitVerdict{kind: gitVerdictBranchMove}
	}
	if len(operands) == 1 && !paths {
		return gitVerdict{kind: gitVerdictCheckoutOne, operand: operands[0]}
	}
	return gitVerdict{kind: gitVerdictConflictOnly}
}

// isWholeTreePathspec mirrors is_whole_tree in crates/shell/src/git/guard.rs:921-927.
func isWholeTreePathspec(spec string) bool {
	rest := spec
	if r, ok := strings.CutPrefix(spec, ":/"); ok {
		rest = r
	} else if r, ok := strings.CutPrefix(spec, ":(top)"); ok {
		rest = r
	}
	if rest == "*" {
		return true
	}
	for _, part := range strings.Split(rest, "/") {
		if part != "" && part != "." && part != ".." {
			return false
		}
	}
	return true
}

type gitGuardSite struct {
	dir        string
	redirected bool
	recurse    *bool
	command    string
}

type gitRepoInfo struct {
	gitDir    string
	commonDir string
	topLevel  string
	inTree    bool
}

type gitPlaceKind int

const (
	gitPlaceRedirected gitPlaceKind = iota
	gitPlaceUnreadable
	gitPlaceOutside
	gitPlaceShared
	gitPlacePrivate
)

func evaluateGitGuardInvocation(ctx context.Context, cwd, baseCwd string, exported []string, args []string, isFirstSegment bool) gitGuardDecision {
	inv, ok := parseGitInvocation(args)
	if !ok {
		return gitGuardDecision{}
	}
	verdict := classifyGitCommand(inv.sub, inv.args)
	if verdict.kind == gitVerdictAllow {
		return gitGuardDecision{}
	}

	dir := cwd
	alsoCheckBase := false
	for _, d := range inv.dirs {
		if hasDynamicPathChars(d) {
			alsoCheckBase = true
			continue
		}
		resolved := resolveShellDirectory(d, dir)
		if _, err := os.Stat(resolved); err != nil {
			alsoCheckBase = true
		}
		dir = resolved
	}

	redirected := inv.redirected
	if !redirected {
		for _, envName := range exported {
			if isGitRepoLocationVar(envName) {
				redirected = true
				break
			}
		}
	}

	recurse := gitRecurseOption(inv.sub, inv.args)
	if recurse == nil {
		recurse = inv.recurse
	}
	if recurse == nil {
		for _, envName := range exported {
			if envName == "GIT_CONFIG_COUNT" || (runtime.GOOS == "windows" && strings.EqualFold(envName, "GIT_CONFIG_COUNT")) {
				b := true
				recurse = &b
				break
			}
		}
	}

	evalSite := func(targetDir string) gitGuardDecision {
		site := gitGuardSite{
			dir:        targetDir,
			redirected: redirected,
			recurse:    recurse,
			command:    strings.Join(args, " "),
		}
		place, repo := detectGitPlace(ctx, site)
		if place == gitPlacePrivate {
			return decideInPrivate(ctx, repo, inv.sub, verdict, site, isFirstSegment)
		}
		refusal := decideInShared(ctx, place, repo, inv.sub, verdict, site)
		if refusal != "" {
			return gitGuardDecision{Refusal: refusal}
		}
		return gitGuardDecision{}
	}

	dec := evalSite(dir)
	if dec.Refusal != "" {
		return dec
	}
	if alsoCheckBase && baseCwd != "" && baseCwd != dir {
		baseDec := evalSite(baseCwd)
		if baseDec.Refusal != "" {
			return baseDec
		}
		dec.Notes = append(dec.Notes, baseDec.Notes...)
	}
	return dec
}

// runGitProbe runs a helper `git` subprocess with a bounded timeout and WaitDelay,
// returning (stdout, exitCode, err) so callers can distinguish exit code 1
// ("not found / absent") from timeouts, cancellations, or other errors (M3).
func runGitProbe(ctx context.Context, dir string, args ...string) (string, int, error) {
	probeCtx, cancel := context.WithTimeout(ctx, gitGuardProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "git", args...)
	cmd.WaitDelay = 500 * time.Millisecond
	if dir != "" {
		cmd.Dir = dir
	}
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		key := kv
		if eq := strings.IndexByte(kv, '='); eq >= 0 {
			key = kv[:eq]
		}
		if isGitRepoLocationVar(key) {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(stdout.String()), exitCode, fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(stdout.String()), 0, nil
}

func discoverGitRepo(ctx context.Context, dir string) (*gitRepoInfo, gitPlaceKind) {
	if dir != "" {
		if _, err := os.Stat(dir); err != nil {
			return nil, gitPlaceUnreadable
		}
	}
	resolveRel := func(p string) string {
		if p == "" {
			return ""
		}
		if !filepath.IsAbs(p) && dir != "" {
			p = filepath.Join(dir, p)
		}
		p = filepath.Clean(p)
		if eval, e := filepath.EvalSymlinks(p); e == nil {
			return eval
		}
		return p
	}
	out, _, err := runGitProbe(ctx, dir, "rev-parse", "--git-dir", "--git-common-dir", "--is-inside-work-tree", "--show-toplevel")
	if err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "not a git repository") {
			return nil, gitPlaceOutside
		}
		// H1 LOW: Inside `.git` directory, `--show-toplevel` fails with
		// "must be run in a work tree". Fall back to `--git-dir --git-common-dir`
		// so `.git` is classified as shared/private, never Outside.
		if strings.Contains(lower, "must be run in a work tree") {
			out2, _, err2 := runGitProbe(ctx, dir, "rev-parse", "--git-dir", "--git-common-dir")
			if err2 == nil {
				lines2 := strings.Split(strings.ReplaceAll(out2, "\r\n", "\n"), "\n")
				if len(lines2) >= 2 {
					gitDir := resolveRel(strings.TrimSpace(lines2[0]))
					commonDir := resolveRel(strings.TrimSpace(lines2[1]))
					top := filepath.Dir(gitDir)
					return &gitRepoInfo{
						gitDir:    gitDir,
						commonDir: commonDir,
						inTree:    false,
						topLevel:  top,
					}, gitPlaceShared
				}
			}
		}
		return nil, gitPlaceUnreadable
	}
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	if len(lines) < 4 {
		return nil, gitPlaceUnreadable
	}
	info := &gitRepoInfo{
		gitDir:    resolveRel(strings.TrimSpace(lines[0])),
		commonDir: resolveRel(strings.TrimSpace(lines[1])),
		inTree:    strings.TrimSpace(lines[2]) == "true",
		topLevel:  resolveRel(strings.TrimSpace(lines[3])),
	}
	return info, gitPlaceShared
}

func detectGitPlace(ctx context.Context, site gitGuardSite) (gitPlaceKind, *gitRepoInfo) {
	if site.redirected {
		return gitPlaceRedirected, nil
	}
	repo, kind := discoverGitRepo(ctx, site.dir)
	if kind != gitPlaceShared || repo == nil {
		return kind, nil
	}
	if isPrivateCheckout(ctx, repo) {
		return gitPlacePrivate, repo
	}
	return gitPlaceShared, repo
}

// isPrivateCheckout mirrors is_private in crates/shell/src/git/guard.rs:198-214.
func isPrivateCheckout(ctx context.Context, repo *gitRepoInfo) bool {
	if repo == nil {
		return false
	}
	// 1. Repository-local ovnfs.checkout config.
	if out, _, err := runGitProbe(ctx, repo.topLevel, "config", "--local", "--bool", "--get", "ovnfs.checkout"); err == nil {
		if strings.EqualFold(strings.TrimSpace(out), "true") {
			return true
		}
	}
	// 2. Linked worktree of a repository that has a main checkout (common_dir ends with `.git`).
	if repo.gitDir != "" && repo.commonDir != "" && repo.gitDir != repo.commonDir {
		return filepath.Base(repo.commonDir) == ".git"
	}
	// 3. Submodule (its `.git` is a regular file) inside a private superproject.
	if repo.topLevel != "" {
		gitEntry := filepath.Join(repo.topLevel, ".git")
		if st, err := os.Stat(gitEntry); err == nil && !st.IsDir() {
			parent := filepath.Dir(repo.topLevel)
			if parent != "" && parent != repo.topLevel {
				if superRepo, kind := discoverGitRepo(ctx, parent); kind == gitPlaceShared && superRepo != nil {
					return isPrivateCheckout(ctx, superRepo)
				}
			}
		}
	}
	return false
}

func gitOperationInProgress(repo *gitRepoInfo) bool {
	if repo == nil || repo.gitDir == "" {
		return false
	}
	for _, marker := range []string{
		"MERGE_HEAD",
		"REBASE_HEAD",
		"CHERRY_PICK_HEAD",
		"REVERT_HEAD",
		"rebase-merge",
		"rebase-apply",
	} {
		if _, err := os.Stat(filepath.Join(repo.gitDir, marker)); err == nil {
			return true
		}
	}
	return false
}

func gitResolveRef(ctx context.Context, repo *gitRepoInfo, rev string) (sha string, exists bool, err error) {
	if repo == nil {
		return "", false, fmt.Errorf("no repo")
	}
	out, code, runErr := runGitProbe(ctx, repo.topLevel, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if runErr != nil {
		if ctx.Err() != nil || code != 1 {
			return "", false, runErr
		}
		// Distinguish unknown ref from unreadable repo: try plain --verify.
		out2, code2, runErr2 := runGitProbe(ctx, repo.topLevel, "rev-parse", "--verify", "--quiet", rev)
		if runErr2 != nil {
			if ctx.Err() != nil || code2 != 1 {
				return "", false, runErr2
			}
			return "", false, nil
		}
		return strings.TrimSpace(out2), true, nil
	}
	return strings.TrimSpace(out), true, nil
}

func gitMovesHead(ctx context.Context, repo *gitRepoInfo, rev string) bool {
	target, exists, err := gitResolveRef(ctx, repo, rev)
	if err != nil {
		return true
	}
	if !exists {
		return false
	}
	head, headExists, headErr := gitResolveRef(ctx, repo, "HEAD")
	if headErr != nil || !headExists {
		return true
	}
	return head != target
}

func gitNamesPath(ctx context.Context, repo *gitRepoInfo, dir string, operand string) bool {
	if strings.Contains(operand, "...") {
		return false
	}
	_, exists, err := gitResolveRef(ctx, repo, operand)
	if err != nil || exists {
		return false
	}
	if strings.ContainsAny(operand, "*?[\\") {
		return true
	}
	target := operand
	if !filepath.IsAbs(target) && dir != "" {
		target = filepath.Join(dir, target)
	}
	_, statErr := os.Lstat(target)
	return statErr == nil
}

func gitRecursesSubmodules(ctx context.Context, repo *gitRepoInfo, explicit *bool) bool {
	if explicit != nil {
		return *explicit
	}
	if repo == nil {
		return false
	}
	out, code, err := runGitProbe(ctx, repo.topLevel, "config", "--get", "submodule.recurse")
	if err != nil {
		// M3: Only exit code 1 with non-cancelled ctx means "key absent"; fail closed otherwise.
		if ctx.Err() == nil && code == 1 {
			return false
		}
		return true
	}
	v := strings.ToLower(strings.TrimSpace(out))
	return v != "" && v != "false" && v != "no" && v != "off" && v != "0"
}

func gitHasSubmodules(repo *gitRepoInfo) bool {
	if repo == nil {
		return false
	}
	if repo.topLevel != "" {
		if _, err := os.Stat(filepath.Join(repo.topLevel, ".gitmodules")); err == nil {
			return true
		}
	}
	if repo.gitDir != "" {
		if st, err := os.Stat(filepath.Join(repo.gitDir, "modules")); err == nil && st.IsDir() {
			return true
		}
	}
	if repo.commonDir != "" {
		if st, err := os.Stat(filepath.Join(repo.commonDir, "modules")); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

func decideInShared(ctx context.Context, place gitPlaceKind, repo *gitRepoInfo, sub gitSub, verdict gitVerdict, site gitGuardSite) string {
	if place == gitPlaceRedirected {
		return formatRefusalRedirected(sub)
	}
	if place == gitPlaceOutside {
		return ""
	}
	switch verdict.kind {
	case gitVerdictAllow:
		return ""
	case gitVerdictStashSaves, gitVerdictStashTakes, gitVerdictStashClear:
		return formatRefusalStash()
	case gitVerdictDestructive:
		return formatRefusalDestructive(sub)
	case gitVerdictBranchMove:
		return formatRefusalBranchMove(sub)
	case gitVerdictResetTo:
		if repo != nil {
			if gitMovesHead(ctx, repo, verdict.rev) {
				return formatRefusalCommitMove()
			}
			return ""
		}
		if place == gitPlaceUnreadable {
			return formatRefusalCommitMove()
		}
		return ""
	case gitVerdictConflictOnly:
		if repo == nil || !gitOperationInProgress(repo) {
			return formatRefusalOutsideConflict(sub)
		}
		return ""
	case gitVerdictCheckoutOne:
		if repo != nil && !gitNamesPath(ctx, repo, site.dir, verdict.operand) {
			return formatRefusalBranchMove(sub)
		}
		if repo == nil || !gitOperationInProgress(repo) {
			return formatRefusalOutsideConflict(sub)
		}
		return ""
	default:
		return ""
	}
}

func decideInPrivate(ctx context.Context, repo *gitRepoInfo, sub gitSub, verdict gitVerdict, site gitGuardSite, isFirstSegment bool) gitGuardDecision {
	switch verdict.kind {
	case gitVerdictAllow, gitVerdictStashSaves, gitVerdictBranchMove:
		return gitGuardDecision{}
	case gitVerdictStashTakes:
		var entryPtr *string
		if verdict.hasEntry {
			e := verdict.stashEntry
			entryPtr = &e
		}
		if msg := checkForeignStash(ctx, repo, verdict.stashOp, entryPtr); msg != "" {
			return gitGuardDecision{Refusal: msg}
		}
		return gitGuardDecision{}
	case gitVerdictStashClear:
		if msg := checkForeignStash(ctx, repo, "clear", nil); msg != "" {
			return gitGuardDecision{Refusal: msg}
		}
		return gitGuardDecision{}
	case gitVerdictResetTo:
		if verdict.soft {
			return gitGuardDecision{}
		}
		return saveThenRun(ctx, repo, sub, site, false, isFirstSegment)
	case gitVerdictDestructive:
		return saveThenRun(ctx, repo, sub, site, false, isFirstSegment)
	case gitVerdictConflictOnly:
		return saveThenRun(ctx, repo, sub, site, true, isFirstSegment)
	case gitVerdictCheckoutOne:
		if gitNamesPath(ctx, repo, site.dir, verdict.operand) {
			return saveThenRun(ctx, repo, sub, site, true, isFirstSegment)
		}
		return gitGuardDecision{}
	default:
		return gitGuardDecision{}
	}
}

func saveThenRun(ctx context.Context, repo *gitRepoInfo, sub gitSub, site gitGuardSite, targeted bool, isFirstSegment bool) gitGuardDecision {
	// M2: Snapshot-requiring discards in a private worktree must be the first
	// segment of a bash call so the snapshot cannot be stale or taken for an
	// unreached branch.
	if !isFirstSegment {
		return gitGuardDecision{Refusal: formatRefusalCompoundDiscard(sub)}
	}
	if gitRecursesSubmodules(ctx, repo, site.recurse) && gitHasSubmodules(repo) {
		return gitGuardDecision{Refusal: formatRefusalRecursive(sub)}
	}
	id, _, err := runGitProbe(ctx, repo.topLevel, "stash", "create")
	if err != nil {
		if targeted && gitOperationInProgress(repo) {
			return gitGuardDecision{}
		}
		return gitGuardDecision{Refusal: formatRefusalUnsaved(sub, err.Error())}
	}
	id = strings.TrimSpace(id)
	if id == "" {
		// Working tree and index have no tracked uncommitted changes.
		return gitGuardDecision{}
	}
	msg := fmt.Sprintf("git-guard: before git %s", site.command)
	if _, _, refErr := runGitProbe(ctx, repo.topLevel, "update-ref", "--create-reflog", gitGuardDiscardedRef, id, "-m", msg); refErr != nil {
		return gitGuardDecision{Refusal: formatRefusalUnsaved(sub, refErr.Error())}
	}
	shortID := id
	if len(shortID) > 10 {
		if s, _, sErr := runGitProbe(ctx, repo.topLevel, "rev-parse", "--short", id); sErr == nil && s != "" {
			shortID = s
		} else {
			shortID = id[:10]
		}
	}
	note := fmt.Sprintf("git %s: saved your uncommitted changes as %s on %s first; git stash apply --index %s brings them back.",
		sub.name(), shortID, gitGuardDiscardedRef, shortID)
	return gitGuardDecision{Notes: []string{note}}
}

type stashTargetKind int

const (
	stashTargetEntry stashTargetKind = iota
	stashTargetUnindexed
	stashTargetCommit
)

func parseStashTarget(entry *string) (stashTargetKind, int) {
	if entry == nil {
		return stashTargetEntry, 0
	}
	s := *entry
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return stashTargetEntry, n
	}
	selector := ""
	if rest, ok := strings.CutPrefix(s, "refs/stash"); ok {
		selector = rest
	} else if rest, ok := strings.CutPrefix(s, "stash"); ok {
		selector = rest
	} else {
		return stashTargetCommit, 0
	}
	if selector == "" {
		return stashTargetEntry, 0
	}
	if rest, ok := strings.CutPrefix(selector, "@{"); ok {
		if inner, ok2 := strings.CutSuffix(rest, "}"); ok2 {
			if n, err := strconv.Atoi(inner); err == nil && n >= 0 {
				return stashTargetEntry, n
			}
		}
		return stashTargetUnindexed, 0
	}
	if strings.HasPrefix(selector, "^") || strings.HasPrefix(selector, "~") || strings.HasPrefix(selector, ":") || strings.HasPrefix(selector, "@") {
		return stashTargetUnindexed, 0
	}
	return stashTargetCommit, 0
}

type stashSaverKind int

const (
	stashSaverBranch stashSaverKind = iota
	stashSaverDetached
	stashSaverUnknown
)

func parseStashSavedOn(subject string) (stashSaverKind, string) {
	head, _, ok := strings.Cut(subject, ": ")
	if !ok {
		return stashSaverUnknown, ""
	}
	branch, ok := strings.CutPrefix(head, "WIP on ")
	if !ok {
		branch, ok = strings.CutPrefix(head, "On ")
		if !ok {
			return stashSaverUnknown, ""
		}
	}
	if branch == "(no branch)" {
		return stashSaverDetached, ""
	}
	if branch != "" {
		return stashSaverBranch, branch
	}
	return stashSaverUnknown, ""
}

func checkForeignStash(ctx context.Context, repo *gitRepoInfo, op string, entry *string) string {
	targetKind, position := parseStashTarget(entry)
	if targetKind == stashTargetCommit {
		return ""
	}
	here := ""
	if br, _, err := runGitProbe(ctx, repo.topLevel, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		here = strings.TrimSpace(br)
	}
	if targetKind != stashTargetEntry {
		label := "stash@{0}"
		if entry != nil && *entry != "" {
			label = *entry
		}
		return formatRefusalForeignStash(op, label, "", here)
	}
	out, _, err := runGitProbe(ctx, repo.topLevel, "stash", "list", "--format=%gs")
	if err != nil {
		label := "stash@{0}"
		if entry != nil && *entry != "" {
			label = *entry
		}
		return formatRefusalForeignStash(op, label, "", here)
	}
	var subjects []string
	if out != "" {
		subjects = strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	}
	isForeign := func(subject string) bool {
		kind, saverBranch := parseStashSavedOn(subject)
		return !(kind == stashSaverBranch && here != "" && saverBranch == here)
	}
	if op == "clear" {
		for idx, subj := range subjects {
			if isForeign(subj) {
				return formatRefusalForeignStash(op, fmt.Sprintf("stash@{%d}", idx), subj, here)
			}
		}
		return ""
	}
	if position < 0 || position >= len(subjects) {
		return ""
	}
	if isForeign(subjects[position]) {
		return formatRefusalForeignStash(op, fmt.Sprintf("stash@{%d}", position), subjects[position], here)
	}
	return ""
}

// Refusal message formatters mirroring crates/shell/src/git/guard.rs:953-1050.
func formatRefusalStash() string {
	return "git-guard: git stash is blocked by workspace policy — do not work around it.\n" +
		"Other sessions may be using this checkout (it is not a private checkout): stash takes their uncommitted changes out of the working tree along with yours. git stash list, git stash show, and git stash create run here.\n" +
		gitGuardOwnWorktreeHint
}

func formatRefusalDestructive(sub gitSub) string {
	return fmt.Sprintf("git-guard: git %s is blocked: this form (--hard / --force / --discard-changes, or a whole-tree pathspec like `.`, `:/`, `*`) discards the working tree/index wholesale, and other sessions may be using this checkout (it is not a private checkout) — being mid-conflict does not make it safe.\n"+
		"Resolve conflicts with targeted forms instead: git checkout/restore --ours|--theirs <path>. Unstage with git reset <path> or git restore --staged <path>.\n"+
		"%s", sub.name(), gitGuardOwnWorktreeHint)
}

func formatRefusalCommitMove() string {
	return "git-guard: git reset to another commit is blocked: it moves the branch checked out here, and other sessions may be using this checkout (it is not a private checkout).\n" +
		"Unstaging is allowed — drop the commit argument: git reset [-q] [--] <path> (or bare git reset to unstage everything), or git restore --staged <path>.\n" +
		gitGuardOwnWorktreeHint
}

func formatRefusalBranchMove(sub gitSub) string {
	return fmt.Sprintf("git-guard: git %s to another branch or commit is blocked by workspace policy — do not work around it.\n"+
		"Other sessions may be using this checkout (it is not a private checkout); switching branches moves HEAD and the working tree under them. A merge or rebase in progress does not make it safe. While resolving conflicts, git checkout -- <path> and git restore <path> still run; put -- before a path that shares its name with a branch. Inspect other revisions read-only with git show <rev>:<path> / git diff / git log.\n"+
		"%s", sub.name(), gitGuardOwnWorktreeHint)
}

func formatRefusalOutsideConflict(sub gitSub) string {
	return fmt.Sprintf("git-guard: git %s is blocked by workspace policy — do not work around it.\n"+
		"Other sessions may be using this checkout (it is not a private checkout); overwriting working-tree files discards uncommitted changes they may rely on. It is exempt only while a merge/rebase conflict is being resolved, and the repository is not in one.\n"+
		"Unstaging is always allowed: git reset [<path>] / git restore --staged <path>. To undo working-tree edits here, edit forward; inspect history read-only with git diff/log/show.\n"+
		"%s", sub.name(), gitGuardOwnWorktreeHint)
}

func formatRefusalRedirected(sub gitSub) string {
	return fmt.Sprintf("git-guard: git %s is blocked: the guard judges the checkout found from the working directory, and this command points git elsewhere (--git-dir / --work-tree / GIT_DIR / GIT_WORK_TREE / GIT_INDEX_FILE), so it cannot tell whose checkout it changes.\n"+
		"Run it from inside the target checkout (cd or git -C) without overriding git's repository location.", sub.name())
}

func formatRefusalForeignStash(op, entry, subject, here string) string {
	saver := "does not say which branch saved it"
	if subject != "" {
		kind, branch := parseStashSavedOn(subject)
		switch kind {
		case stashSaverBranch:
			saver = "was saved on " + branch
		case stashSaverDetached:
			saver = "was saved on a detached HEAD"
		}
	}
	subjPart := ""
	if subject != "" {
		subjPart = fmt.Sprintf(" (%q)", subject)
	}
	herePart := "this worktree's HEAD is detached"
	if here != "" {
		herePart = "this worktree is on " + here
	}
	own := "Name your own entry from git stash list (stash@{n}), or apply a commit by id: git stash apply <commit>."
	if op == "clear" {
		own = "Drop your own entries one at a time: git stash drop stash@{n}."
	}
	return fmt.Sprintf("git-guard: git stash %s is blocked: %s%s %s, and %s. Every worktree of a repository shares one stash list, so the entry may be another session's work.\n%s",
		op, entry, subjPart, saver, herePart, own)
}

func formatRefusalUnsaved(sub gitSub, errMsg string) string {
	return fmt.Sprintf("git-guard: git %s is blocked: it would discard uncommitted changes, and saving them first failed: %s\n"+
		"Commit them or save a patch first (git diff > <file>); a merge or rebase in progress ends with git merge --abort or git rebase --abort.",
		sub.name(), strings.TrimSpace(errMsg))
}

func formatRefusalRecursive(sub gitSub) string {
	return fmt.Sprintf("git-guard: git %s is blocked: it recurses into submodules (--recurse-submodules or submodule.recurse), and uncommitted changes it discards there cannot be saved first.\n"+
		"Run it without recursing (--no-recurse-submodules), or commit or save a patch of each submodule's changes first (git -C <submodule> diff > <file>).",
		sub.name())
}

func formatRefusalCompoundDiscard(sub gitSub) string {
	return fmt.Sprintf("git-guard: git %s is blocked in a compound command: saving uncommitted changes before discarding requires running git %s as its own bash call so the snapshot reflects the exact working tree state.",
		sub.name(), sub.name())
}
