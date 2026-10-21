package pi_permission_system

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashanalysis"
)

// The parts of the bash surface's decision that need no parser: resolveBashCommandCheck (src/handlers/gates/bash-command.ts) for a
// chain of simple commands. The wrapper classification of src/access-intent/bash/wrapper-analysis.ts lives in the bashanalysis
// package; classifyWrapperWords is the gate's string-word view of it.

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

// classifyWrapperWords reports why a command unit's allow is floored to ask: "opaque-payload" (eval, or a shell with a -c flag
// cluster), "indirection" (a wrapper that always runs a following command, or find/fd with an exec flag), or "" for an ordinary
// command. words[0] is the command name, matched on its basename. The classification is bashanalysis.ClassifyWrapperWords.
func classifyWrapperWords(words []string) string {
	commandWords := make([]bashanalysis.CommandWord, len(words))
	for i, word := range words {
		commandWords[i] = bashanalysis.CommandWord{Text: word}
	}
	return string(bashanalysis.ClassifyWrapperWords(commandWords))
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

// decideBash is the bash surface's decision for one command. Each unit is matched on its own and the most restrictive result
// wins (deny, then the port's refusal, then ask); a unit that runs another command has its allow floored to ask, which yoloMode
// grants, as in the original.
func (p *Policy) decideBash(command string) Verdict {
	cmd := jsTrim(command)
	patterns := p.valuePatterns("bash")
	units, ok := bashUnits(cmd)
	if !ok {
		if !patterns {
			// Every unit of any command gets the bash surface's one rule: only an allow can be floored, and yoloMode grants that.
			if c := p.constant("bash"); c.Action != "allow" || p.yolo || cmd == "" {
				return p.outcome("bash", c)
			}
			return unsupported("analysing this bash command (only simple commands joined by &&, ||, ;, | or newlines can be " +
				"checked for a wrapper the original asks about)")
		}
		return unsupported("matching this bash command against bash patterns (only simple commands joined by &&, ||, ;, | or " +
			"newlines are supported)")
	}
	best, rank := Verdict{}, 0
	for _, u := range units {
		r := p.constant("bash")
		if patterns {
			r = Evaluate("bash", u, p.rules, PosixPathFlavor, "")
		}
		v, k := p.outcome("bash", r), 0
		switch r.Action {
		case "deny":
			k = 3
		case "ask":
			k = 1
		default:
			words := strings.Fields(u)
			switch {
			case classifyWrapperWords(words) != "":
				if p.yolo {
					continue
				}
				v, k = asked("bash"), 1
			case portGuardWords[commandBasename(words[0])]:
				v, k = unsupported("checking the command that '"+commandBasename(words[0])+"' runs"), 2
			}
		}
		if k > rank {
			best, rank = v, k
		}
	}
	return best
}
