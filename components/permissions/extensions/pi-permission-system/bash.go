package pi_permission_system

import "strings"

// Port of the parts of the bash surface's decision that need no parser: classifyWrapperWords from
// src/access-intent/bash/wrapper-analysis.ts, and resolveBashCommandCheck (src/handlers/gates/bash-command.ts) for a chain of
// simple commands.

// shellWrapperNames run an inline program after a -c flag cluster (wrapper-analysis.ts SHELL_WRAPPER_NAMES).
var shellWrapperNames = map[string]bool{"bash": true, "sh": true, "dash": true, "zsh": true, "ksh": true}

// indirectionWrapperNames always run a following command (wrapper-analysis.ts INDIRECTION_WRAPPER_NAMES).
var indirectionWrapperNames = map[string]bool{
	"sudo": true, "env": true, "xargs": true, "time": true, "nohup": true, "timeout": true, "nice": true, "parallel": true,
	"rust-parallel": true, "rush": true, "doas": true, "setsid": true, "stdbuf": true, "watch": true, "flock": true,
}

// execConditionalWrappers run a command per result only with one of these flags (wrapper-analysis.ts EXEC_CONDITIONAL_WRAPPERS).
var execConditionalWrappers = map[string]map[string]bool{
	"find": {"-exec": true, "-execdir": true, "-ok": true, "-okdir": true},
	"fd":   {"-x": true, "--exec": true, "-X": true, "--exec-batch": true},
}

// pureReaderCommands are the pure-reader core the floor exemption admits (command-effects.ts coreAdmissions, minus the
// commands whose read claim needs an option-and-script proof the port does not read: sed, awk). No arguments make a
// core command write a file, so an indirection wrapper around one may stand aside and let the inner command's rule
// decide (isTransparentWrapper, ADR 0013 §11).
var pureReaderCommands = map[string]bool{
	"cat": true, "head": true, "tail": true, "wc": true, "grep": true, "egrep": true, "fgrep": true, "rg": true,
	"diff": true, "ls": true, "stat": true, "pwd": true, "basename": true, "dirname": true, "realpath": true,
	"echo": true, "which": true, "cd": true, "find": true, "fd": true, "sort": true,
}

// retractionFlags are the arguments that withdraw a guarded core command's read claim (command-effects.ts
// RETRACTION_GUARDS, the subset the port reads): a writer option on sort, a write or exec predicate on find or fd.
var retractionFlags = map[string]map[string]bool{
	"sort": {"-o": true, "--output": true},
	"find": {"-delete": true, "-exec": true, "-execdir": true, "-ok": true, "-okdir": true, "-fprint": true, "-fprint0": true, "-fprintf": true, "-fls": true},
	"fd":   {"-x": true, "--exec": true, "-X": true, "--exec-batch": true},
}

// valueTakingFlags are each wrapper's options that consume the following word, so skipping a wrapper's own arguments
// does not mistake an option's value for the inner command (wrapper-analysis.ts VALUE_TAKING_FLAGS).
var valueTakingFlags = map[string]map[string]bool{
	"sudo":    {"-u": true, "-g": true, "-p": true, "-C": true, "-h": true, "-U": true, "-r": true, "-t": true},
	"doas":    {"-u": true, "-C": true},
	"env":     {"-u": true, "-C": true, "--unset": true, "--chdir": true},
	"xargs":   {"-n": true, "-P": true, "-I": true, "-i": true, "-d": true, "-E": true, "-L": true, "-l": true, "-s": true, "-a": true},
	"timeout": {"-s": true, "-k": true, "--signal": true, "--kill-after": true},
	"nice":    {"-n": true, "--adjustment": true},
	"time":    {"-o": true, "-f": true, "--output": true, "--format": true},
	"stdbuf":  {"-i": true, "-o": true, "-e": true, "--input": true, "--output": true, "--error": true},
	"watch":   {"-n": true, "--interval": true},
	"flock":   {"-w": true, "-E": true, "--timeout": true, "--conflict-exit-code": true},
}

// leadingOperandWrappers take a bare operand (a duration, a lock file) before the inner command
// (wrapper-analysis.ts LEADING_OPERAND_WRAPPERS).
var leadingOperandWrappers = map[string]bool{"timeout": true, "flock": true}

// isEnvAssignment reports a NAME=value environment prefix.
func isEnvAssignment(word string) bool {
	i := strings.IndexByte(word, '=')
	if i <= 0 {
		return false
	}
	for j := 0; j < i; j++ {
		c := word[j]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
			if j == 0 && c >= '0' && c <= '9' {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// innerCommandIndex is the index of the word beginning a wrapper's inner command (wrapper-analysis.ts
// innerCommandIndex): the wrapper name, its environment assignments, its options (consuming a following value where
// the wrapper takes one), a leading operand where the wrapper takes one, and an exec flag's command; -1 when the
// wrapper's own options run out first.
func innerCommandIndex(words []string) int {
	if len(words) == 0 {
		return -1
	}
	name := commandBasename(words[0])
	if flags := execConditionalWrappers[name]; flags != nil {
		for i, a := range words[1:] {
			if flags[a] {
				return i + 3 // words[i+1] is the flag; the command begins at words[i+2], whose index is i+3
			}
		}
	}
	valueTaking := valueTakingFlags[name]
	operandPending := leadingOperandWrappers[name]
	index := 1
	for index < len(words) {
		word := words[index]
		switch {
		case word == "--":
			return index + 1
		case isEnvAssignment(word):
			index++
		case strings.HasPrefix(word, "-"):
			if valueTaking != nil && valueTaking[word] {
				index += 2
			} else {
				index++
			}
		case operandPending:
			operandPending = false
			index++
		default:
			return index
		}
	}
	return -1
}

// execTerminatorIndex is the index of a find/fd exec clause's `;` or `+` terminator, which belongs to the search tool
// and not to the command it runs, or len(words) when there is none.
func execTerminatorIndex(words []string, start int) int {
	for i := start; i < len(words); i++ {
		if t := strings.TrimPrefix(words[i], `\`); t == ";" || t == "+" {
			return i
		}
	}
	return len(words)
}

// unwrapIndirection peels a wrapper unit's indirection layers to the inner command (wrapper-analysis.ts
// unwrapIndirection): ok is false for an inline-shell payload (an inner *program* the port never parsed), for a
// wrapper whose options never yield a command, and for a unit that does not unwrap to something new.
func unwrapIndirection(words []string) ([]string, bool) {
	current := words
	for depth := 0; depth < 4; depth++ {
		kind := classifyWrapperWords(current)
		if kind == "" {
			return current, len(current) < len(words)
		}
		if kind == "opaque-payload" {
			return nil, false
		}
		start := innerCommandIndex(current)
		if start == -1 || start >= len(current) {
			return nil, false
		}
		current = current[start:execTerminatorIndex(current, start)]
	}
	return nil, false
}

// isTransparentWrapper reports whether a wrapper unit's floor has no reason left to hold: it is an indirection, it
// unwraps to an inner command without passing an inline-shell payload, that command proves a read (bare-basename core
// word, retraction guards applied), and the unit writes no file through a redirect (isTransparentWrapper).
func isTransparentWrapper(words []string, hasWriteRedirect bool) bool {
	if hasWriteRedirect || classifyWrapperWords(words) != "indirection" {
		return false
	}
	inner, ok := unwrapIndirection(words)
	if !ok || len(inner) == 0 {
		return false
	}
	name := commandBasename(inner[0])
	if !pureReaderCommands[name] {
		return false
	}
	for _, a := range inner[1:] {
		if retractionFlags[name][a] {
			return false
		}
	}
	return true
}

// hasWriteRedirect reports whether a unit writes a file through a redirect operator (>, >>, &>, N>, N>>).
func hasWriteRedirect(words []string) bool {
	for _, w := range words {
		if w == ">" || w == ">>" || w == "&>" {
			return true
		}
		if i := strings.IndexByte(w, '>'); i > 0 {
			digits := true
			for c := range w[:i] {
				if c < '0' || c > '9' {
					digits = false
					break
				}
			}
			if digits && (w[i:] == ">" || w[i:] == ">>") {
				return true
			}
		}
	}
	return false
}

// splitChainUnits splits a shell program into its top-level command units on &&, ||, ;, |, & and newlines outside
// quotes. Unlike bashUnits it keeps units the port cannot read as words: the plain-value path needs each unit to
// classify its wrapper even when quoting or expansion makes the words unreadable.
func splitChainUnits(cmd string) []string {
	var units []string
	start, inSingle, inDouble := 0, false, false
	flush := func(end int) {
		if u := strings.TrimSpace(cmd[start:end]); u != "" {
			units = append(units, u)
		}
	}
	for i := 0; i < len(cmd); i++ {
		switch c := cmd[i]; {
		case c == '\\' && !inSingle:
			i++
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case inSingle || inDouble:
		case c == '&' || c == '|':
			if i+1 < len(cmd) && cmd[i+1] == c {
				i++
			}
			flush(i)
			if i+1 < len(cmd) && (cmd[i+1] == ' ' || cmd[i+1] == '\t') {
				i++
			}
			start = i + 1
		case c == ';' || c == '\n':
			flush(i)
			start = i + 1
		}
	}
	flush(len(cmd))
	return units
}

// fieldWords splits a unit into its words on whitespace outside quotes, dropping the quotes: `echo "a b"` is two
// words, and `"~/x"` is the path word ~/x. Backslashes are kept, so a Windows path survives as one word, the way
// isSimpleCommand reads them. What quoting joins, the port cannot see into: a word this gives is the word the
// original's parse reads only while the quoting is one balanced pair.
func fieldWords(unit string) []string {
	var words []string
	var b strings.Builder
	inSingle, inDouble := false, false
	flush := func() {
		if b.Len() > 0 {
			words = append(words, b.String())
			b.Reset()
		}
	}
	for i := 0; i < len(unit); i++ {
		switch c := unit[i]; {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case (c == ' ' || c == '\t') && !inSingle && !inDouble:
			flush()
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return words
}

// commandBasename is the final path segment of a command name (/bin/bash is bash).
func commandBasename(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// shortFlagCIndex is the index in args of a short-flag cluster holding c (-c, -ec, -xc) before any --, or -1.
func shortFlagCIndex(args []string) int {
	for i, a := range args {
		if a == "--" {
			return -1
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
			return i
		}
	}
	return -1
}

// classifyWrapperWords reports why a command unit's allow is floored to ask: "opaque-payload" (eval, or a shell with a -c flag
// cluster), "indirection" (a wrapper that always runs a following command, or find/fd with an exec flag), or "" for an ordinary
// command. words[0] is the command name, matched on its basename.
func classifyWrapperWords(words []string) string {
	if len(words) == 0 {
		return ""
	}
	name, args := commandBasename(words[0]), words[1:]
	switch {
	case name == "eval":
		return "opaque-payload"
	case shellWrapperNames[name] && shortFlagCIndex(args) != -1:
		return "opaque-payload"
	case indirectionWrapperNames[name]:
		return "indirection"
	}
	if flags := execConditionalWrappers[name]; flags != nil {
		for _, a := range args {
			if flags[a] {
				return "indirection"
			}
		}
	}
	return ""
}

// bashUnits splits a command into its units when it is simple commands joined by &&, ||, ;, | or a newline: the units the
// original's parse gives such a command. ok is false for anything else, which the port cannot read without a parser.
func bashUnits(cmd string) (units []string, ok bool) {
	start := 0
	cut := func(end int) bool {
		u := strings.Trim(cmd[start:end], " ")
		if !isSimpleCommand(u) {
			return false
		}
		units = append(units, u)
		return true
	}
	for i := 0; i < len(cmd); i++ {
		switch c := cmd[i]; {
		case (c == '&' || c == '|') && i+1 < len(cmd) && cmd[i+1] == c:
			if !cut(i) {
				return nil, false
			}
			i++
			start = i + 1
		case c == '|' || c == ';' || c == '\n':
			if !cut(i) {
				return nil, false
			}
			start = i + 1
		}
	}
	if !cut(len(cmd)) {
		return nil, false
	}
	return units, true
}

// decideBash is the bash surface's decision for one command (resolveBashCommandCheck): each unit is resolved on its
// own — its patterns when bash has value patterns, else the surface's constant — and the most restrictive result wins
// (deny, then ask). A wrapper unit's allow is floored to ask (the original's <opaque-bash-wrapper> and
// <indirection-bash-wrapper>), unless the floor has no reason left to hold: an indirection whose payload proves a
// read is resolved by the inner command's rule instead (isTransparentWrapper, ADR 0013 §11). Ordinary commands and
// commands that run another command without a wrapper flag (ssh, source, `bash script.sh`) are decided by the rules
// alone, exactly as the original decides them. A command the port cannot read is refused while bash has value
// patterns, which the raw text cannot be matched against; without patterns the value decides it — the parse only
// ever added wrapper floors on top. When the surface allows, the words of each unit are scanned for paths outside
// the working directory, the word-level subset of the original's bash external-directory gate.
func (p *Policy) decideBash(command string, session Ruleset, cwd string) Verdict {
	cmd := jsTrim(command)
	patterns := p.valuePatterns("bash")
	units, simple := bashUnits(cmd)
	if !simple {
		if patterns {
			return unsupported("matching this bash command against bash patterns (only simple commands joined by &&, ||, ;, | or " +
				"newlines are supported)")
		}
		units = splitChainUnits(cmd)
	}
	if len(units) == 0 {
		// A trivially-empty command (empty, whitespace-only, comments only) has nothing to decompose, so
		// the surface's rule resolves it as a whole — its ask and its deny stand (the original's
		// isTriviallyEmptyCommand path in resolveBashCommandCheck).
		r := p.constantIn("bash", session)
		if patterns {
			r = Evaluate("bash", cmd, p.sessionWith(session), PosixPathFlavor, "")
		}
		return p.outcome(r, bashAsk(cmd, r))
	}
	best, rank := Verdict{}, 0
	for _, u := range units {
		if u == "" {
			continue
		}
		r := p.constantIn("bash", session)
		if patterns {
			r = Evaluate("bash", u, p.sessionWith(session), PosixPathFlavor, "")
		}
		v, k := p.outcome(r, bashAsk(u, r)), 0
		switch r.Action {
		case "deny":
			k = 3
		case "ask":
			k = 1
		default:
			words := fieldWords(u)
			if classifyWrapperWords(words) != "" && !(p.yolo && !patterns) {
				floored := true
				if isTransparentWrapper(words, hasWriteRedirect(words)) {
					// The floor has no reason left to hold: the inner command's rule decides, and the
					// unit is still what runs — the prompt and the session grant name it (the original's
					// resolveWrapperUnit). Its decision stands whatever it is; only an inner allow
					// reaches the caller without a block or an ask.
					if inner, ok := unwrapIndirection(words); ok {
						if patterns {
							r = Evaluate("bash", strings.Join(inner, " "), p.sessionWith(session), PosixPathFlavor, "")
						}
						v, k = p.outcome(r, bashAsk(u, r)), 0
						switch r.Action {
						case "deny":
							k = 3
						case "ask":
							k = 1
						}
						floored = false
					}
				}
				if floored {
					// A wrapper's allow is floored to ask; yoloMode grants the floored ask.
					v, k = p.floorAsk(bashAsk(u, r)), 1
				}
			}
		}
		if k > rank {
			best, rank = v, k
		}
	}
	if best.Block || best.Ask != nil {
		return best
	}
	return worstVerdict(best, p.bashExternalGate(cmd, cwd, session))
}
