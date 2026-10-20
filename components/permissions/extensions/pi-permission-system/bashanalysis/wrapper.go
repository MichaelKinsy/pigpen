package bashanalysis

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Wrapper interpretation for a bash command unit: what kind of wrapper it is, what it actually
// runs, and whether its floor still has a reason to hold (wrapper-analysis.ts). Pure and
// word-based; the AST walk that produces the words lives in command enumeration. The three
// questions live here together deliberately: the shape that floors a unit to `ask`, the shape that
// names its inner command, and the shape that exempts it must agree.

// CommandWord is one word of a command unit: its source text and its UTF-16 offset into the unit's
// text, beside the value the program receives once the shell removes quotes. The source text is
// what unwrapping and slicing read; the ArgWord is what a capability proof reads, since `'-o'`
// reaches `sort` as `-o`.
type CommandWord struct {
	bashparse.ArgWord
	Text   string
	Offset int
}

// WrapperKind is why a command unit's decision is floored to at least `ask`. The empty string is
// the original's `undefined`: an ordinary command.
//
// WrapperOpaquePayload is an inline-shell payload (`bash -c`/`eval`) whose inner program is not
// re-parsed (#481). WrapperIndirection is a prefix/exec wrapper (`sudo`/`env`/`xargs`/`find
// -exec`/...) whose inner command is a visible argument but is not gated on its own (#490). The
// kind selects the audit sentinel; both floor identically.
type WrapperKind string

const (
	WrapperOpaquePayload WrapperKind = "opaque-payload"
	WrapperIndirection   WrapperKind = "indirection"
)

// ClassifyWrapperWords classifies a command unit's words as a floored wrapper, or "" for an
// ordinary command. words[0] is the command name; a leading `variable_assignment` prefix is already
// stripped by the caller. The command name is matched on its basename, so `/bin/bash -c ...`
// counts.
//
// WrapperOpaquePayload: `eval`, or a shell (bash/sh/dash/zsh/ksh) with a `-c` short-flag cluster
// (`-c`, `-ec`, `-xc`). WrapperIndirection: an always-invoking prefix/exec wrapper, or a search tool
// (`find`/`fd`) carrying a per-result exec flag (#490). A bare `find`/`fd` runs no subcommand and
// is not flagged.
func ClassifyWrapperWords(words []CommandWord) WrapperKind {
	name, ok := wrapperName(words)
	if !ok {
		return ""
	}
	args := wordTexts(words[1:])
	if name == "eval" {
		return WrapperOpaquePayload
	}
	if shellWrapperNames[name] && shortFlagCIndex(args) != -1 {
		return WrapperOpaquePayload
	}
	if indirectionWrapperNames[name] {
		return WrapperIndirection
	}
	if execFlagIndex(name, args) != -1 {
		return WrapperIndirection
	}
	return ""
}

// InlineShellPayloadIndex is the index within words of the inline-shell payload (the inner program
// a shell or `eval` runs), or -1 when the unit carries none.
//
// Indirection layers are peeled first, so `sudo bash -c '...'` answers the payload's index rather
// than -1: ExecutedUnitOf peels them too, so a consumer that did not would mask a secret under
// `executedUnit` and write it verbatim under `command` (#923). The index names the payload's
// position, which a vacant one still has (`bash -c`).
func InlineShellPayloadIndex(words []CommandWord) int {
	base := 0
	current := words
	for depth := 0; depth < maxUnwrapDepth; depth++ {
		if direct := directPayloadIndex(current); direct != -1 {
			return base + direct
		}
		if ClassifyWrapperWords(current) != WrapperIndirection {
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

// directPayloadIndex is the payload index of a unit that is already the shell or `eval` running
// it. `eval` takes its program as the first argument (no `-c`, so the flag scan answers -1 and the
// index falls out as 1); a shell takes it after the `-c` cluster. Any other command name carries no
// inline program, which keeps an interpreter (`python3 -c`, `node -e`) out.
func directPayloadIndex(words []CommandWord) int {
	name, ok := wrapperName(words)
	if !ok {
		return -1
	}
	isShell := shellWrapperNames[name]
	if name != "eval" && !isShell {
		return -1
	}
	flagIndex := shortFlagCIndex(wordTexts(words[1:]))
	if isShell && flagIndex == -1 {
		return -1
	}
	return flagIndex + 2
}

// ExecutedUnitOf is the command a wrapper unit actually runs, or ok=false when it cannot be
// established or adds nothing over the unit itself.
//
// Display-only (ADR 0011 section 3.5, #713): the result names what runs, including the payload of
// an inline shell, so it deliberately looks past a `sh -c` layer that the gate must not look past.
// It fails to nothing rather than to a guess. Nested wrappers unwrap to the innermost command
// (`sudo timeout 5 xargs grep foo` gives `grep foo`), bounded by maxUnwrapDepth.
func ExecutedUnitOf(unitText string, words []CommandWord) (string, bool) {
	unwrapped := unwrapIndirection(words, unitText)
	if unwrapped.opaque {
		return nothingNew(unwrapped.payload, unwrapped.hasPayload, unitText)
	}
	return nothingNew(unwrapped.text, true, unitText)
}

// IsTransparentWrapper is true when a wrapper unit's floor has no reason left to hold: the command
// it runs is in the pure-reader core, so its direction is provable however unknown its argument
// feed is (ADR 0013 section 11, #803).
//
// Four things must hold: the unit is an indirection wrapper; unwrapping reached the inner command
// without passing through an opaque payload (`sh -c '...'` carries an unparsed program whose first
// word says nothing about the rest of it); the inner command proves a read (bare-basename core
// word, retraction guards applied, so `xargs sort -o /tmp/x` and `xargs find . -delete` are not
// transparent); and the unit writes no file through a redirect, which the caller reads off the
// parse tree (writesViaRedirect).
func IsTransparentWrapper(words []CommandWord, writesViaRedirect bool) bool {
	if writesViaRedirect {
		return false
	}
	if ClassifyWrapperWords(words) != WrapperIndirection {
		return false
	}
	// Only the peeled words matter here, so the walk is handed no source span to cut from.
	unwrapped := unwrapIndirection(words, "")
	if unwrapped.opaque || unwrapped.layers == 0 {
		return false
	}
	head := ""
	if len(unwrapped.words) > 0 {
		head = unwrapped.words[0].Text
	}
	var args []bashparse.ArgWord
	if len(unwrapped.words) > 1 {
		args = argWordsOf(unwrapped.words[1:])
	}
	return ProveCommandEffect(head, args).Effect == bashparse.EffectRead
}

// unwrapResult is how an unwrap ended: at the innermost command reachable by peeling indirection
// layers (opaque false), or at an inline-shell payload (opaque true). They are different kinds of
// answer: a peeled result is a slice of this command line with words the caller can inspect; a
// payload is an inner program the enumerator never parsed, so it has text and nothing else
// (#803).
type unwrapResult struct {
	opaque     bool
	payload    string
	hasPayload bool
	text       string
	words      []CommandWord
	layers     int
}

// maxUnwrapDepth is how many wrapper layers to unwrap before giving up.
const maxUnwrapDepth = 4

// unwrapIndirection peels indirection layers off a command unit until an ordinary command, an
// opaque payload, or an unrecognized option shape stops the walk. Stopping early is not an error:
// the words peeled so far are returned. unitText is the span the peeled text is cut from; a caller
// that wants only the words passes "".
func unwrapIndirection(words []CommandWord, unitText string) unwrapResult {
	text := unitText
	current := words
	layers := 0
	for depth := 0; depth < maxUnwrapDepth; depth++ {
		kind := ClassifyWrapperWords(current)
		if kind == "" {
			break
		}
		if kind == WrapperOpaquePayload {
			// The payload is an inner program, not a slice of this command line, so it is unquoted
			// and terminal: unwrapping it further would need a parse.
			payload, ok := opaquePayload(current)
			return unwrapResult{opaque: true, payload: payload, hasPayload: ok}
		}
		start := innerCommandIndex(current)
		if start == -1 || start >= len(current) {
			break
		}
		end := execTerminatorIndex(current, start)
		text = jsTrimEnd(sliceWords(text, current, start, end))
		current = rebase(current, start, end)
		layers++
	}
	return unwrapResult{text: text, words: current, layers: layers}
}

// nothingNew is the extracted text, or ok=false when it establishes nothing new: it is absent or
// empty, it still begins with an option (so the inner command was never reached), or it simply
// repeats the unit.
func nothingNew(text string, present bool, unitText string) (string, bool) {
	if !present || text == "" || text == unitText {
		return "", false
	}
	if strings.HasPrefix(text, "-") {
		return "", false
	}
	return text, true
}

// opaquePayload is the inline-shell payload argument, unquoted; ok=false when absent. It reads the
// direct index: unwrapIndirection has already peeled every wrapper layer by the time it reaches its
// opaque branch.
func opaquePayload(words []CommandWord) (string, bool) {
	index := directPayloadIndex(words)
	if index == -1 || index >= len(words) {
		return "", false
	}
	return unquote(words[index].Text), true
}

// unquote strips one matching pair of surrounding quotes.
func unquote(text string) string {
	if text == "" || (text[0] != '\'' && text[0] != '"') {
		return text
	}
	if utf16Len(text) >= 2 && strings.HasSuffix(text, text[:1]) {
		return slice16(text, 1, -1)
	}
	return text
}

// innerCommandIndex is the index of the word beginning the inner command, or -1 when the
// wrapper's own options run out first. It skips the wrapper name, environment assignments, options
// (consuming a following value for the options in valueTakingFlags), and a leading operand for the
// wrappers that take one. An exec-conditional wrapper instead starts immediately after its exec
// flag.
func innerCommandIndex(words []CommandWord) int {
	name, ok := wrapperName(words)
	if !ok {
		return -1
	}
	if execFlag := execFlagIndex(name, wordTexts(words[1:])); execFlag != -1 {
		return execFlag + 2
	}
	valueTaking := valueTakingFlags[name]
	operandPending := leadingOperandWrappers[name]
	index := 1
	for index < len(words) {
		word := words[index].Text
		if word == "--" {
			return index + 1
		}
		if isEnvironmentAssignment(word) {
			index++
			continue
		}
		if strings.HasPrefix(word, "-") {
			if valueTaking[word] {
				index += 2
			} else {
				index++
			}
			continue
		}
		if operandPending {
			operandPending = false
			index++
			continue
		}
		return index
	}
	return -1
}

// execTerminatorIndex is the index of an exec wrapper's `;`/`+` terminator, or len(words): the
// terminator belongs to `find`, not to the command it runs.
func execTerminatorIndex(words []CommandWord, start int) int {
	for index := start; index < len(words); index++ {
		if execTerminators[strings.TrimPrefix(words[index].Text, `\`)] {
			return index
		}
	}
	return len(words)
}

// sliceWords is the unit text spanned by words[start..end).
func sliceWords(unitText string, words []CommandWord, start, end int) string {
	from := words[start].Offset
	if end < len(words) {
		return slice16(unitText, from, words[end].Offset)
	}
	return slice16From(unitText, from)
}

// rebase is words[start..end) with offsets rebased onto the sliced text.
func rebase(words []CommandWord, start, end int) []CommandWord {
	origin := words[start].Offset
	out := make([]CommandWord, 0, end-start)
	for _, word := range words[start:end] {
		word.Offset -= origin
		out = append(out, word)
	}
	return out
}

// isEnvironmentAssignment is true for a `NAME=value` environment prefix (/^[A-Za-z_][A-Za-z0-9_]*=/).
func isEnvironmentAssignment(word string) bool {
	if word == "" || !(word[0] == '_' || isASCIILetter(word[0])) {
		return false
	}
	for i := 1; i < len(word); i++ {
		c := word[i]
		if c == '=' {
			return true
		}
		if !(c == '_' || isASCIILetter(c) || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return false
}

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// shellWrapperNames are the shell command names whose `-c` flag introduces an opaque inline program.
var shellWrapperNames = setOf("bash", "sh", "dash", "zsh", "ksh")

// indirectionWrapperNames are the wrappers that always invoke a following command, so the wrapper
// (not the inner command) is what a bash rule matches. Floored by command-name basename alone.
// Beyond the first seven, the survey of #575: parallelizers (parallel/rust-parallel/rush), a sudo
// rewrite (doas), and prefix wrappers (setsid/stdbuf/watch/flock) that all always invoke a
// following command.
var indirectionWrapperNames = setOf(
	"sudo", "env", "xargs", "time", "nohup", "timeout", "nice",
	"parallel", "rust-parallel", "rush", "doas", "setsid", "stdbuf", "watch", "flock",
)

// execConditionalWrappers are the search tools that invoke a command per result only when an exec
// flag is present; a bare search runs no subcommand.
var execConditionalWrappers = map[string]map[string]bool{
	"find": setOf("-exec", "-execdir", "-ok", "-okdir"),
	"fd":   setOf("-x", "--exec", "-X", "--exec-batch"),
}

// valueTakingFlags are the curated per-wrapper options that consume the following word, so
// skipping a wrapper's own arguments does not mistake an option's value for the inner command.
// Attached forms (`-I{}`, `--user=root`) need no entry: they are one word. Only the display-side
// extraction reads this, and a missing or wrong entry yields nothing, never a weaker gate.
var valueTakingFlags = map[string]map[string]bool{
	"sudo":    setOf("-u", "-g", "-p", "-C", "-h", "-U", "-r", "-t"),
	"doas":    setOf("-u", "-C"),
	"env":     setOf("-u", "-C", "--unset", "--chdir"),
	"xargs":   setOf("-n", "-P", "-I", "-i", "-d", "-E", "-L", "-l", "-s", "-a"),
	"timeout": setOf("-s", "-k", "--signal", "--kill-after"),
	"nice":    setOf("-n", "--adjustment"),
	"time":    setOf("-o", "-f", "--output", "--format"),
	"stdbuf":  setOf("-i", "-o", "-e", "--input", "--output", "--error"),
	"watch":   setOf("-n", "--interval"),
	"flock":   setOf("-w", "-E", "--timeout", "--conflict-exit-code"),
}

// leadingOperandWrappers are the wrappers whose first bare word is an operand (a duration, a lock
// file) rather than the start of the inner command.
var leadingOperandWrappers = setOf("timeout", "flock")

// execTerminators are the words ending a `find -exec` clause; they belong to `find`.
var execTerminators = setOf(";", "+")

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// wrapperName is the wrapper's command-name basename; ok=false for an empty unit.
func wrapperName(words []CommandWord) (string, bool) {
	if len(words) == 0 {
		return "", false
	}
	name := words[0].Text
	if slash := strings.LastIndexByte(name, '/'); slash != -1 {
		name = name[slash+1:]
	}
	return name, true
}

func wordTexts(words []CommandWord) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w.Text
	}
	return out
}

func argWordsOf(words []CommandWord) []bashparse.ArgWord {
	out := make([]bashparse.ArgWord, len(words))
	for i, w := range words {
		out[i] = w.ArgWord
	}
	return out
}

// shortFlagCIndex is the index within args of the `-c` short-flag cluster (a single-dash word
// containing `c`) before any `--` end-of-options marker, or -1.
func shortFlagCIndex(args []string) int {
	for index, arg := range args {
		if arg == "--" {
			return -1
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "c") {
			return index
		}
	}
	return -1
}

// execFlagIndex is the index within args of a matched per-result exec flag, or -1.
func execFlagIndex(commandName string, args []string) int {
	flags := execConditionalWrappers[commandName]
	if flags == nil {
		return -1
	}
	for index, arg := range args {
		if flags[arg] {
			return index
		}
	}
	return -1
}
