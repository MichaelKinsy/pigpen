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

// portGuardWords are commands that run another command which the original does not floor (it reads them by their own rule).
// The port refuses to allow them: it is stricter here than the original, on purpose.
var portGuardWords = map[string]bool{
	"command": true, "builtin": true, "exec": true, "strace": true, "bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true,
	"fish": true, "source": true, ".": true, "ionice": true, "chroot": true, "su": true, "busybox": true, "script": true,
	"unbuffer": true, "ssh": true,
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

// decideBash is the bash surface's decision for one command. Each unit is matched on its own and the most
// restrictive result wins (deny, then ask); a unit that runs another command has its allow floored to ask, which
// yoloMode grants, as in the original. A command the port cannot read decides by its catch-all when there are no
// bash patterns — an allow there is floored to ask, since it could hide a wrapper (or a pattern match) the original
// reads — and is refused while bash has value patterns, which the raw text cannot be matched against. When the
// surface allows, the words of the simple command are scanned for paths outside the working directory, the word
// subset of the original's bash external-directory gate.
func (p *Policy) decideBash(command string, session Ruleset, cwd string) Verdict {
	cmd := jsTrim(command)
	patterns := p.valuePatterns("bash")
	units, ok := bashUnits(cmd)
	if !ok {
		if patterns {
			return unsupported("matching this bash command against bash patterns (only simple commands joined by &&, ||, ;, | or " +
				"newlines are supported)")
		}
		r := p.constantIn("bash", session)
		if r.Action != "allow" {
			return p.outcome(r, bashAsk(cmd, r))
		}
		if p.yolo || cmd == "" {
			return Verdict{}
		}
		return p.floorAsk(bashAsk(cmd, r))
	}
	best, rank := Verdict{}, 0
	for _, u := range units {
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
			words := strings.Fields(u)
			switch {
			case classifyWrapperWords(words) != "", portGuardWords[commandBasename(words[0])]:
				// A wrapper's and a guarded command's allow is floored to ask: the port cannot stand in for
				// what the original does with the command they run (a wrapper exemption it reads, an inner
				// rule it matches), and yoloMode grants the ask.
				v, k = p.floorAsk(bashAsk(u, r)), 1
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
