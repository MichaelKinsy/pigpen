package bashanalysis

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// ProveCommandEffect is the effect a command's head word proves for the path tokens that command
// owns, from the built-in pure-reader core (ADR 0013 section 7; command-effects.ts).
//
// A word is core only as a bare basename: `./grep` and `/tmp/evil/grep` prove nothing, because a
// path-qualified head word names a program the core's audit never saw. Rejecting on the separator
// characters directly keeps the rule fail-closed on both platforms without reading the host's path
// language.
//
// A guarded word's claim is withdrawn when an argument names one of its write-capable options, or
// is computed and may arrive as one, yielding `retracted` rather than a write: the command may
// still only read, so the fail-closed base case is the honest answer and `retracted` is the blame
// line that says why.
func ProveCommandEffect(headWord string, argWords []bashparse.ArgWord) bashparse.TokenEffect {
	if !isBareCoreWord(headWord) {
		return bashparse.UnprovenEffect
	}
	if withdraws := retractionGuards[headWord]; withdraws != nil && withdraws(argWords) {
		return retractedEffect
	}
	return coreReadEffect
}

// RedirectDestinationEffect is the effect a redirect operator proves for its destination token, or
// ok=false when the redirect names no file at all. It is bashparse's, which owns the redirect
// operator table; re-exported so the original's command-effects surface is whole here.
func RedirectDestinationEffect(operator string, destinationIsDescriptor bool) (bashparse.TokenEffect, bool) {
	return bashparse.RedirectDestinationEffect(operator, destinationIsDescriptor)
}

// coreAdmission is a group of core words admitted for one shared structural reason.
type coreAdmission struct {
	words  []string
	reason string
}

// coreAdmissions is the roster, grouped by admission reason. The bar is structural, never
// popularity: implementation-independent read-only-ness across GNU and BSD alike, no option that
// redirects output to a file, and effects stable under argument content. Deliberately excluded:
// gawk/nawk (their dialects' options are unaudited; awk is guarded instead), uniq (`uniq IN OUT`
// writes its second positional), tee/dd/split/csplit/xxd/tree/curl/wget (each has a positional or
// option that writes a file), less/more (interactive shell escape and LESSOPEN), file (`-C` writes
// a magic.mgc), and git/pnpm/npm/node/python3/gh (subcommand- and argument-dependent). Widening
// the roster only ever loosens; a wrong admission is a fail-open.
var coreAdmissions = []coreAdmission{
	{[]string{"cat", "head", "tail", "wc", "grep", "egrep", "fgrep", "rg"}, "Content readers: no output-file option in any surveyed dialect; output is stdout only"},
	{[]string{"diff"}, "Writes nothing; `-D` emits merged output to stdout"},
	{[]string{"ls", "stat", "pwd"}, "Metadata and listing: report only"},
	{[]string{"basename", "dirname", "realpath"}, "Path-string transforms"},
	{[]string{"echo", "which", "cd"}, "No filesystem write"},
	{[]string{"find", "fd", "sort"}, "Read-only until an argument says otherwise: see retractionGuards"},
	{[]string{"sed"}, "Read-only until the command line says otherwise: an allowlist proof over its options and script"},
	{[]string{"awk"}, "Read-only until the command line says otherwise: an allowlist proof over its options and program text"},
}

// PureReaderCore is the pure-reader core: the command words that are read-only for any arguments,
// in any implementation (PURE_READER_CORE). The returned set is a copy.
func PureReaderCore() map[string]bool {
	out := make(map[string]bool, len(pureReaderCore))
	for w := range pureReaderCore {
		out[w] = true
	}
	return out
}

var pureReaderCore = func() map[string]bool {
	m := map[string]bool{}
	for _, a := range coreAdmissions {
		for _, w := range a.words {
			m[w] = true
		}
	}
	return m
}()

// retractionGuard names the option forms that withdraw a guarded word's read claim. Matching is
// fail-closed over the forms ADR 0013 section 7 names: a long stem matches bare, with an attached
// `=value`, or as any prefix of itself (GNU getopt_long accepts any unambiguous abbreviation), and
// a short letter matches anywhere in a single-dash cluster, which covers the attached-value form
// (`-oFILE`) too. Over-retraction costs one ask; under-retraction misses a write.
type retractionGuard struct {
	exactWords   []string // whole argument words, for options that neither cluster nor take `=`
	longStems    []string // matched bare (`--output`) or attached (`--output=/tmp/x`)
	shortLetters []string // matched anywhere in a single-dash cluster (`-uo`)
}

// claimWithdrawal is whether a guarded word's arguments withdraw its read claim.
type claimWithdrawal func(argWords []bashparse.ArgWord) bool

// optionGuard is a withdrawal decided by option spellings: any argument naming one. A computed
// argument's value is its unresolved source spelling, so it is asked only whether it may arrive
// beginning with `-`, the shape every guarded option has.
func optionGuard(guard retractionGuard) claimWithdrawal {
	return func(argWords []bashparse.ArgWord) bool {
		for _, word := range argWords {
			if word.Computed {
				if word.MayLeadWithDash {
					return true
				}
			} else if guard.retractsClaim(word.Value) {
				return true
			}
		}
		return false
	}
}

// retractionGuards are the guarded words and what withdraws each one's claim. `find`, `fd`, and
// `sort` are guarded by option spellings, chosen because their write options spell identically in
// GNU and BSD. `sed` needs a proof over its script too, and `awk` over its program, so each owns a
// predicate. `find`'s options are single-dash long words that never cluster, so they match as exact
// words; `sort`'s only short option containing `o` is `-o` itself.
var retractionGuards = map[string]claimWithdrawal{
	"find": optionGuard(retractionGuard{exactWords: []string{
		"-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf", "-fls",
	}}),
	"fd":   optionGuard(retractionGuard{longStems: []string{"--exec", "--exec-batch"}, shortLetters: []string{"x", "X"}}),
	"sort": optionGuard(retractionGuard{longStems: []string{"--output"}, shortLetters: []string{"o"}}),
	"sed":  SedWithdrawsReadClaim,
	"awk":  AwkWithdrawsReadClaim,
}

var (
	coreReadEffect  = bashparse.TokenEffect{Effect: bashparse.EffectRead, Source: bashparse.SourceCore}
	retractedEffect = bashparse.TokenEffect{Effect: bashparse.EffectUnproven, Source: bashparse.SourceRetracted}
)

// isBareCoreWord rejects a head word holding a path separator of either flavor, then asks the roster.
func isBareCoreWord(headWord string) bool {
	if strings.ContainsAny(headWord, `/\`) {
		return false
	}
	return pureReaderCore[headWord]
}

func (g retractionGuard) retractsClaim(word string) bool {
	for _, w := range g.exactWords {
		if w == word {
			return true
		}
	}
	if matchesLongStem(word, g.longStems) {
		return true
	}
	return matchesShortCluster(word, g.shortLetters)
}

// matchesLongStem: a long option, bare, carrying its value inline, or abbreviated. The name is
// taken before the first `=`, so `--out=/tmp/x` is tested as `--out`, an abbreviation of `--output`
// that GNU getopt_long resolves to it. A bare `--` abbreviates nothing.
func matchesLongStem(word string, stems []string) bool {
	if len(stems) == 0 {
		return false
	}
	name, _, _ := strings.Cut(word, "=")
	if !strings.HasPrefix(name, "--") || utf16Len(name) <= 2 {
		return false
	}
	for _, stem := range stems {
		if strings.HasPrefix(stem, name) {
			return true
		}
	}
	return false
}

// matchesShortCluster: a guarded letter anywhere in a single-dash cluster. The scan runs past the
// letters into an attached value, which is what makes `-oFILE` retract as surely as `-o FILE`.
func matchesShortCluster(word string, letters []string) bool {
	if len(letters) == 0 {
		return false
	}
	if !strings.HasPrefix(word, "-") || strings.HasPrefix(word, "--") || utf16Len(word) < 2 {
		return false
	}
	cluster := word[1:]
	for _, letter := range letters {
		if strings.Contains(cluster, letter) {
			return true
		}
	}
	return false
}
