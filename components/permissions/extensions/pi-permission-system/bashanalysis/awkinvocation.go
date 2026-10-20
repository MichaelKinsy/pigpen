package bashanalysis

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// AwkWithdrawsReadClaim reports whether an `awk` invocation's arguments withdraw its read claim
// (awk-invocation.ts).
//
// The claim stands only when the command line is proven read-only: the only options are `-F` and
// `-v`, the program arrives as literal text, and that text holds none of the constructs through
// which a POSIX or GNU awk program writes a file or runs a command. The program check is a
// character scan, not a parse, so it over-retracts: `NR>=100` withdraws the claim because `>` is
// also `print >`'s redirect. A computed word withdraws the claim wherever it sits, since it could
// spell `-f` or the program itself.
func AwkWithdrawsReadClaim(argWords []bashparse.ArgWord) bool {
	values := make([]string, len(argWords))
	for i, w := range argWords {
		if w.Computed {
			return true
		}
		values[i] = w.Value
	}
	program, ok := awkSelectProgram(values)
	return !ok || !awkProvesReadOnlyProgram(program)
}

// awkValueOptions are `-F sep` and `-v name=value`: the options every awk shares that cannot write.
var awkValueOptions = setOf("-F", "-v")

// awkSelectProgram is the program text an argument list hands `awk`, or ok=false when the list
// cannot be proven to hand it only that. The program is the first positional. An option-looking
// word anywhere else withdraws the claim: whether an awk stops reading options at the program
// varies by implementation, so a trailing `-f` is not assumed to be a file.
func awkSelectProgram(values []string) (string, bool) {
	program, have := "", false
	optionsEnded := false
	for i := 0; i < len(values); i++ {
		value := values[i]
		if optionsEnded || !isOptionWord(value) {
			if !have {
				program, have = value, true
			}
			continue
		}
		if have {
			return "", false
		}
		if value == "--" {
			optionsEnded = true
			continue
		}
		if awkValueOptions[value] {
			if i+1 >= len(values) {
				return "", false
			}
			i++
			continue
		}
		if !awkIsAttachedValueOption(value) {
			return "", false
		}
	}
	return program, have
}

func isOptionWord(value string) bool { return strings.HasPrefix(value, "-") && value != "-" }

// awkIsAttachedValueOption: `-F:` or `-vn=3`, an allowed option carrying its value in the same
// word. (The first two characters are compared; a non-ASCII second character matches no option.)
func awkIsAttachedValueOption(value string) bool {
	return len(value) > 2 && awkValueOptions[value[:2]]
}

// awkWriteCapableText is the text whose presence means the program may write a file or run a
// command: `>` covers `print >` and `>>`; `|` a pipe to or from a command and gawk's `|&`
// coprocess; `system` covers `system()`; `@` gawk's `@include`, `@load`, and indirect calls.
var awkWriteCapableText = []string{">", "|", "system", "@"}

func awkProvesReadOnlyProgram(program string) bool {
	for _, text := range awkWriteCapableText {
		if strings.Contains(program, text) {
			return false
		}
	}
	return true
}
