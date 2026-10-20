package pi_permission_system

import (
	"regexp"
	"strings"
)

// The part of src/access-intent/bash/wrapper-analysis.ts that inlineShellPayloadIndex needs, over the words' source text (the
// original reads `.text` of each CommandWord and nothing else here). Lane-local: the wrapper-analysis lane owns the full module.

const maxUnwrapDepth = 4

var valueTakingFlags = map[string]map[string]bool{
	"sudo":    setOfStrings("-u", "-g", "-p", "-C", "-h", "-U", "-r", "-t"),
	"doas":    setOfStrings("-u", "-C"),
	"env":     setOfStrings("-u", "-C", "--unset", "--chdir"),
	"xargs":   setOfStrings("-n", "-P", "-I", "-i", "-d", "-E", "-L", "-l", "-s", "-a"),
	"timeout": setOfStrings("-s", "-k", "--signal", "--kill-after"),
	"nice":    setOfStrings("-n", "--adjustment"),
	"time":    setOfStrings("-o", "-f", "--output", "--format"),
	"stdbuf":  setOfStrings("-i", "-o", "-e", "--input", "--output", "--error"),
	"watch":   setOfStrings("-n", "--interval"),
	"flock":   setOfStrings("-w", "-E", "--timeout", "--conflict-exit-code"),
}

var leadingOperandWrappers = setOfStrings("timeout", "flock")

var envAssignmentWord = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func setOfStrings(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// execFlagIndex is the index within args of a matched per-result exec flag, or -1.
func execFlagIndex(commandName string, args []string) int {
	flags := execConditionalWrappers[commandName]
	if flags == nil {
		return -1
	}
	for i, a := range args {
		if flags[a] {
			return i
		}
	}
	return -1
}

// directPayloadIndex is the payload index of a unit that is already the shell or `eval` running it.
func directPayloadIndex(words []string) int {
	if len(words) == 0 {
		return -1
	}
	name := commandBasename(words[0])
	isShell := shellWrapperNames[name]
	if name != "eval" && !isShell {
		return -1
	}
	flagIndex := shortFlagCIndex(words[1:])
	if isShell && flagIndex == -1 {
		return -1
	}
	return flagIndex + 2
}

// innerCommandIndex is the index of the word beginning a wrapper's inner command, or -1 when the wrapper's own options run out.
func innerCommandIndex(words []string) int {
	if len(words) == 0 {
		return -1
	}
	name := commandBasename(words[0])
	if execFlag := execFlagIndex(name, words[1:]); execFlag != -1 {
		return execFlag + 2
	}
	valueTaking := valueTakingFlags[name]
	operandPending := leadingOperandWrappers[name]
	for index := 1; index < len(words); {
		word := words[index]
		switch {
		case word == "--":
			return index + 1
		case envAssignmentWord.MatchString(word):
			index++
		case strings.HasPrefix(word, "-"):
			if valueTaking[word] {
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

// execTerminatorIndex is the index of an exec wrapper's `;`/`+` terminator at or after start, or len(words).
func execTerminatorIndex(words []string, start int) int {
	for i := start; i < len(words); i++ {
		if w := strings.TrimPrefix(words[i], `\`); w == ";" || w == "+" {
			return i
		}
	}
	return len(words)
}

// inlineShellPayloadIndex is the index within words of the inline-shell payload (the inner program a shell or `eval` runs), or -1.
// Indirection layers are peeled first, so `sudo bash -c '...'` answers the payload's index.
func inlineShellPayloadIndex(words []string) int {
	base := 0
	current := words
	for depth := 0; depth < maxUnwrapDepth; depth++ {
		if direct := directPayloadIndex(current); direct != -1 {
			return base + direct
		}
		if classifyWrapperWords(current) != "indirection" {
			return -1
		}
		start := innerCommandIndex(current)
		if start == -1 || start >= len(current) {
			return -1
		}
		end := execTerminatorIndex(current, start)
		base += start
		current = current[start:end]
	}
	return -1
}
