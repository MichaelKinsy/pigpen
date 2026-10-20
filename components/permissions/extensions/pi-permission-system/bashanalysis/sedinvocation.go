package bashanalysis

import (
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// SedWithdrawsReadClaim reports whether a `sed` invocation's arguments withdraw its read claim
// (sed-invocation.ts).
//
// The claim stands only when the whole command line is proven read-only: every option is one the
// allowlist knows, and every script parses under a grammar that admits no command able to write a
// file or run one. Anything the walk does not recognize withdraws the claim. A computed word
// withdraws it wherever it sits: GNU `sed` permutes options, so even a trailing `"$f"` can arrive
// as `-i`. Where GNU and BSD would read the same argument list differently (an `-e` after the
// first positional) it withdraws.
func SedWithdrawsReadClaim(argWords []bashparse.ArgWord) bool {
	values := make([]string, len(argWords))
	for i, w := range argWords {
		if w.Computed {
			return true
		}
		values[i] = w.Value
	}
	scripts, ok := sedSelectScripts(values)
	if !ok {
		return true
	}
	for _, script := range scripts {
		if !sedProvesReadOnlyScript(script) {
			return true
		}
	}
	return false
}

// sedFlagLetters are the short options that take no argument and cannot write: `-n -E -r -s -u -z`.
const sedFlagLetters = "nErsuz"

// sedFlagWords are the long options that take no argument and cannot write, matched exactly.
// Abbreviations are deliberately absent: GNU accepts `--qui` for `--quiet` and `--in` for
// `--in-place` alike, and matching whole words only lets every abbreviation withdraw.
var sedFlagWords = setOf(
	"--quiet", "--silent", "--regexp-extended", "--separate", "--unbuffered", "--null-data", "--posix", "--debug", "--sandbox",
)

const sedExpressionOption = "--expression"

// sedSelectScripts is the scripts an argument list hands `sed`, or ok=false when the list cannot be
// proven to hand it only those. Every `-e` value is a script; with none, the first positional is.
// Options are recognized wherever they sit until a `--`, because GNU permutes them.
func sedSelectScripts(values []string) ([]string, bool) {
	var scripts []string
	firstPositional, havePositional := "", false
	optionsEnded := false
	for i := 0; i < len(values); i++ {
		value := values[i]
		if optionsEnded || !isOptionWord(value) {
			if !havePositional {
				firstPositional, havePositional = value, true
			}
			continue
		}
		if value == "--" {
			optionsEnded = true
			continue
		}
		if sedFlagWords[value] {
			continue
		}
		next, haveNext := "", i+1 < len(values)
		if haveNext {
			next = values[i+1]
		}
		reading, ok := sedExpressionOf(value, next, haveNext)
		if !ok {
			return nil, false
		}
		// An -e after a positional is a script to GNU and a file to BSD.
		if reading.hasScript && havePositional {
			return nil, false
		}
		if reading.hasScript {
			scripts = append(scripts, reading.script)
		}
		if reading.consumesNext {
			i++
		}
	}
	if len(scripts) > 0 {
		return scripts, true
	}
	if !havePositional {
		return nil, false
	}
	return []string{firstPositional}, true
}

// sedOptionReading is what one option word contributes: a script, and whether it took the next word.
type sedOptionReading struct {
	script       string
	hasScript    bool
	consumesNext bool
}

// sedExpressionOf reads one option word, or ok=false when it is not one the allowlist proves. A
// short cluster may hold only flag letters until an `e`, which ends it: the rest of the word is the
// script, or the next word is when nothing follows.
func sedExpressionOf(value, next string, haveNext bool) (sedOptionReading, bool) {
	if strings.HasPrefix(value, "--") {
		return sedLongExpressionOf(value, next, haveNext)
	}
	// Every letter before the `e` must be an ASCII flag letter, so a byte walk finds the same
	// position the UTF-16 walk does, and the script is the remainder after it.
	for j := 1; j < len(value); j++ {
		letter := value[j]
		if letter == 'e' {
			if attached := value[j+1:]; attached != "" {
				return sedOptionReading{script: attached, hasScript: true}, true
			}
			if !haveNext {
				return sedOptionReading{}, false
			}
			return sedOptionReading{script: next, hasScript: true, consumesNext: true}, true
		}
		if !strings.ContainsRune(sedFlagLetters, rune(letter)) || letter >= 0x80 {
			return sedOptionReading{}, false
		}
	}
	return sedOptionReading{}, true
}

func sedLongExpressionOf(value, next string, haveNext bool) (sedOptionReading, bool) {
	if strings.HasPrefix(value, sedExpressionOption+"=") {
		return sedOptionReading{script: value[len(sedExpressionOption)+1:], hasScript: true}, true
	}
	if value != sedExpressionOption || !haveNext {
		return sedOptionReading{}, false
	}
	return sedOptionReading{script: next, hasScript: true, consumesNext: true}, true
}

// sedReadOnlyCommands take no argument and neither write a file nor run a command. `l`, `q`, and
// `Q` accept an optional number, read by the scanner.
const sedReadOnlyCommands = "pPdDnNgGhHx=zFlqQ"

// sedNumberedCommands are the commands among sedReadOnlyCommands that take an optional number.
const sedNumberedCommands = "lqQ"

// sedLabelCommands take a label running to the end of the command.
const sedLabelCommands = "bTt:"

// sedProvesReadOnlyScript is whether a script is made only of commands that cannot write a file or
// run a command: an allowlist grammar where `w`, `W`, `r`, `R`, `e`, `v`, `a`, `i`, `c`, an `s` flag
// outside `g p i I m M` and digits (so `s///w` and `s///e`), and any character the scanner does not
// recognize end the proof.
func sedProvesReadOnlyScript(script string) bool {
	s := &sedScriptScanner{source: utf16.Encode([]rune(script))}
	return s.provesReadOnly()
}

// sedNone is the scanner's `undefined` character.
const sedNone = -1

type sedAddressReading int

const (
	addressNone sedAddressReading = iota
	addressRead
	addressInvalid
)

// sedScriptScanner is a single left-to-right pass over one script, indexed by UTF-16 code unit like
// the original's string.
type sedScriptScanner struct {
	source     []uint16
	position   int
	openBlocks int
}

func (s *sedScriptScanner) provesReadOnly() bool {
	s.skipSeparators()
	for !s.atEnd() {
		if !s.readCommand() {
			return false
		}
		s.skipSeparators()
	}
	return s.openBlocks == 0
}

// readCommand reads one `[address[,address]][!...]command`, and what may follow it.
func (s *sedScriptScanner) readCommand() bool {
	if s.peek() == '#' {
		return s.skipComment()
	}
	if !s.readAddresses() {
		return false
	}
	s.skipBlanks()
	for s.peek() == '!' {
		s.position++
		s.skipBlanks()
	}
	command := s.peek()
	if command == sedNone {
		return false
	}
	s.position++
	if command == '{' {
		s.openBlocks++
		return true
	}
	if !s.readCommandBody(command) {
		return false
	}
	s.skipBlanks()
	return s.atEnd() || s.atSeparator() || s.closesBlock()
}

// readCommandBody reads what follows a command letter, up to where a separator must come.
func (s *sedScriptScanner) readCommandBody(command int) bool {
	if command == '}' {
		return s.closeBlock()
	}
	if command == 's' {
		return s.readSubstitution()
	}
	if command == 'y' {
		return s.readTransliteration()
	}
	if command < 0x80 && strings.ContainsRune(sedLabelCommands, rune(command)) {
		return s.readLabel(command)
	}
	if command >= 0x80 || !strings.ContainsRune(sedReadOnlyCommands, rune(command)) {
		return false
	}
	if strings.ContainsRune(sedNumberedCommands, rune(command)) {
		s.skipBlanks()
		s.skipDigits()
	}
	return true
}

// readAddresses reads an optional address, then an optional `,` and a second one.
func (s *sedScriptScanner) readAddresses() bool {
	first := s.readAddress()
	if first == addressInvalid {
		return false
	}
	if first == addressNone {
		return true
	}
	s.skipBlanks()
	if s.peek() != ',' {
		return true
	}
	s.position++
	s.skipBlanks()
	return s.readSecondAddress()
}

// readAddress reads a line number, `first~step`, `$`, `/re/`, or `\cREc`.
func (s *sedScriptScanner) readAddress() sedAddressReading {
	next := s.peek()
	if next == '$' {
		s.position++
		return addressRead
	}
	if next == '/' || next == '\\' {
		return s.readRegexAddress()
	}
	if !s.skipDigits() {
		return addressNone
	}
	if s.peek() == '~' {
		s.position++
		s.skipDigits()
	}
	return addressRead
}

func (s *sedScriptScanner) readRegexAddress() sedAddressReading {
	if s.peek() == '\\' {
		s.position++
	}
	delimiter, ok := s.takeDelimiter()
	if !ok || !s.readRegex(delimiter) {
		return addressInvalid
	}
	// GNU's case-insensitive and multiline address flags.
	for s.peek() == 'I' || s.peek() == 'M' {
		s.position++
	}
	return addressRead
}

// readSecondAddress reads the end of a range: an address, or GNU's `+N` / `~N`.
func (s *sedScriptScanner) readSecondAddress() bool {
	sign := s.peek()
	if sign == '+' || sign == '~' {
		s.position++
		return s.skipDigits()
	}
	return s.readAddress() == addressRead
}

// readSubstitution reads `s/regex/replacement/flags`, whose flags must not write or execute.
func (s *sedScriptScanner) readSubstitution() bool {
	delimiter, ok := s.takeDelimiter()
	if !ok {
		return false
	}
	if !s.readRegex(delimiter) || !s.readLiteral(delimiter) {
		return false
	}
	for isSubstitutionFlag(s.peek()) {
		s.position++
	}
	return true
}

// isSubstitutionFlag is /[gpiImM0-9]/: the flags an `s` command may carry without writing a file
// or running one.
func isSubstitutionFlag(c int) bool {
	return c != sedNone && c < 0x80 && strings.ContainsRune("gpiImM0123456789", rune(c))
}

// readTransliteration reads `y/source/target/`, whose operands are character lists, not regexes.
func (s *sedScriptScanner) readTransliteration() bool {
	delimiter, ok := s.takeDelimiter()
	if !ok {
		return false
	}
	return s.readLiteral(delimiter) && s.readLiteral(delimiter)
}

// readLabel reads a label, running to `;` or a newline. GNU ends a label there; BSD runs it to the
// newline, so it sees fewer commands than this reading does, never more. A `}` stays in the label,
// which leaves its block open and the proof unfinished.
func (s *sedScriptScanner) readLabel(command int) bool {
	s.skipBlanks()
	start := s.position
	for !s.atEnd() && !s.atSeparator() {
		s.position++
	}
	return command != ':' || s.position > start
}

func (s *sedScriptScanner) closeBlock() bool {
	s.openBlocks--
	return s.openBlocks >= 0
}

// closesBlock: a `}` directly after a command ends it, as a separator would.
func (s *sedScriptScanner) closesBlock() bool { return s.peek() == '}' }

func (s *sedScriptScanner) skipComment() bool {
	for !s.atEnd() && s.peek() != '\n' {
		s.position++
	}
	return true
}

// takeDelimiter is the delimiter a regex or `s`/`y` command opens with, or ok=false when it is one
// this grammar does not accept. A letter, digit, backslash, blank, or newline delimiter is legal
// to GNU in places and a trap everywhere: `\n` and `\d` stop meaning what they say. Accepted are
// the ASCII punctuation ranges `!-/ :-@ [-` {-~`.
func (s *sedScriptScanner) takeDelimiter() (int, bool) {
	d := s.peek()
	if d == sedNone || !isDelimiterChar(d) || d == '\\' {
		return 0, false
	}
	s.position++
	return d, true
}

func isDelimiterChar(c int) bool {
	return (c >= '!' && c <= '/') || (c >= ':' && c <= '@') || (c >= '[' && c <= '`') || (c >= '{' && c <= '~')
}

// readRegex reads a regex up to its closing delimiter, bracket expressions included. A delimiter
// inside a bracket expression ends the proof: BSD reads `[/]` as a bracket and GNU ends the regex at
// its `/`, so the two disagree about where every later section starts.
func (s *sedScriptScanner) readRegex(delimiter int) bool {
	for !s.atEnd() {
		next := s.peek()
		if next == '\n' {
			return false
		}
		if next == delimiter {
			s.position++
			return true
		}
		if next == '\\' {
			s.position += 2
			continue
		}
		if next == '[' {
			if !s.readBracket(delimiter) {
				return false
			}
			continue
		}
		s.position++
	}
	return false
}

// readBracket reads `[...]`, with `]` literal first and `[:class:]` / `[.x.]` / `[=x=]` inside.
func (s *sedScriptScanner) readBracket(delimiter int) bool {
	s.position++
	if s.peek() == '^' {
		s.position++
	}
	if s.peek() == ']' {
		s.position++
	}
	for !s.atEnd() {
		next := s.peek()
		if next == ']' {
			s.position++
			return true
		}
		if next == delimiter || next == '\n' {
			return false
		}
		if next == '[' && s.opensBracketClass() {
			if !s.skipBracketClass(delimiter) {
				return false
			}
			continue
		}
		s.position++
	}
	return false
}

func (s *sedScriptScanner) opensBracketClass() bool {
	marker := s.at(s.position + 1)
	return marker == ':' || marker == '.' || marker == '='
}

func (s *sedScriptScanner) skipBracketClass(delimiter int) bool {
	marker := uint16(s.at(s.position + 1))
	// source.indexOf(`${marker}]`, position + 2)
	close := -1
	for i := s.position + 2; i+1 < len(s.source); i++ {
		if s.source[i] == marker && s.source[i+1] == ']' {
			close = i
			break
		}
	}
	if close == -1 {
		return false
	}
	for _, u := range s.source[s.position+2 : close] {
		if int(u) == delimiter {
			return false
		}
	}
	s.position = close + 2
	return true
}

// readLiteral reads a replacement or `y` operand up to its closing delimiter. Brackets mean
// nothing here, so `s/x/[]/` is the literal `[]`.
func (s *sedScriptScanner) readLiteral(delimiter int) bool {
	for !s.atEnd() {
		next := s.peek()
		if next == '\n' {
			return false
		}
		if next == delimiter {
			s.position++
			return true
		}
		if next == '\\' {
			s.position += 2
		} else {
			s.position++
		}
	}
	return false
}

func (s *sedScriptScanner) skipSeparators() {
	for s.atSeparator() || isBlank(s.peek()) {
		s.position++
	}
}

func (s *sedScriptScanner) skipBlanks() {
	for isBlank(s.peek()) {
		s.position++
	}
}

// skipDigits advances over digits; true when there was at least one.
func (s *sedScriptScanner) skipDigits() bool {
	start := s.position
	for c := s.peek(); c >= '0' && c <= '9'; c = s.peek() {
		s.position++
	}
	return s.position > start
}

func (s *sedScriptScanner) atSeparator() bool {
	next := s.peek()
	return next == ';' || next == '\n'
}

func isBlank(c int) bool { return c == ' ' || c == '\t' }

func (s *sedScriptScanner) atEnd() bool { return s.position >= len(s.source) }

func (s *sedScriptScanner) peek() int { return s.at(s.position) }

func (s *sedScriptScanner) at(i int) int {
	if i < 0 || i >= len(s.source) {
		return sedNone
	}
	return int(s.source[i])
}
