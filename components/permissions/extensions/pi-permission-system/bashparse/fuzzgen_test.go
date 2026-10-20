package bashparse

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// The fuzz corpus of the bash parse differential: inputs built from a vocabulary and from mutations
// of the recorded corpora, generated deterministically here so that the same strings are fed to the
// original (port/bashparse-diff/run.sh reads them from the file TestGenFuzz writes) and to the Go
// parse (TestDifferential regenerates them), and only their hashes are committed.

// splitmix is a splitmix64 generator: tiny, and stable across Go versions.
type splitmix struct{ state uint64 }

func (s *splitmix) next() uint64 {
	s.state += 0x9e3779b97f4a7c15
	z := s.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// intn is a value in [0, n).
func (s *splitmix) intn(n int) int { return int(s.next() % uint64(n)) }

var fuzzVocabulary = []string{
	"echo", "cat", "git", "rm", "ls", "xargs", "sh", "bash -c", "sudo", "env", "FOO=bar", "x=$(", "a", "b", "c", "arg", "-rf", "--force",
	"/tmp/o", "~/x", "$HOME", "${HOME}", "${x:-d}", "$x", "$((1+2))", "$(", ")", "`", "$(echo hi)", "<(ls)", ">(cat)",
	"'", "\"", "'a b'", "\"a b\"", "$'x'", "\\", "\\\n", "#", "# c", ";", ";;", "&", "&&", "||", "|", "|&", "!", "(", "((", "{", "}", "[", "[[", "]]", "]",
	">", ">>", "<", "<<<", ">&", "<&", ">&-", "<&-", "&>", "&>>", ">|", "<>", "2>", "2>&1", "10>", "{fd}>", "0<<", "<<", "<<-", "<<EOF", "<<-EOF", "<<'EOF'", "<<\"EOF\"", "<<\\EOF", "<< EOF",
	"EOF", "\nEOF", "\nEOF\n", "\nbody\nEOF", "\n$(rm x)\nEOF", "\n", "\n\n", "\t", " ", "  ", "\r\n", "\r",
	"if", "then", "else", "elif", "fi", "while", "until", "do", "done", "for", "in", "case", "esac", "select", "function", "f()", "time", "coproc",
	"[ -f x ]", "[[ -f x && -d y ]]", "test -f x", "=~", "==", "=", "+=", "x=1", "x[1]=2", "declare -a", "export", "local", "unset", "read", "let",
	"*", "?", "{a,b}", "{1..3}", "~", "~root", "$@", "$*", "$#", "$?", "$$", "$!", "$0", "${!a}", "${#a}", "${a[@]}", "${a//b/c}",
	"é", "😀", "\u00a0", "\u2028", "\ufeff", "\x00", "\x7f", "日本語", "e\u0301",
	"push", "2>/dev/null", "> out", "< in", "| tail -4", "&& rm x", "; rm x", "arg > o", "cat <<EOF | rm x\nb\nEOF",
	"git commit -F - <<'MSG' 2>&1 | tail -4\nmsg\nMSG",
}

var fuzzSeparators = []string{" ", " ", " ", " ", "", "\n", "\t", " ; ", " && ", " | ", ";", "&", " \\\n"}

var fuzzCharPool = []rune{'a', 'Z', '0', ' ', '\t', '\n', '\r', ';', '&', '|', '<', '>', '(', ')', '{', '}', '[', ']', '$', '`', '\'', '"', '\\', '#', '!', '=', '*', '?', '~', '-', '_', '.', ',', ':', '/', '@', '%', '^', '+', 'é', '😀', 0x2028, 0xa0, 0xfeff, 0x3000, 0x85, 0}

// fuzzInputs generates the committed fuzz corpus from the seeds (the recorded command strings).
func fuzzInputs(seeds []string) []string {
	return fuzzInputsFrom(seeds, 0x5eed5eed, 100000, 100000, 30000, true)
}

// fuzzInputsFrom generates soup token-soup strings, mutated mutations of the seeds, concat
// concatenations of them, and the chunk-boundary forms when chunks is set, from a generator seed.
func fuzzInputsFrom(seeds []string, seed uint64, soup, mutated, concat int, chunks bool) []string {
	rng := &splitmix{state: seed}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !utf8.ValidString(s) || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	pick := func() string { return fuzzVocabulary[rng.intn(len(fuzzVocabulary))] }
	sep := func() string { return fuzzSeparators[rng.intn(len(fuzzSeparators))] }

	// A: token soup.
	for i := 0; i < soup; i++ {
		n := 1 + rng.intn(24)
		var b strings.Builder
		for j := 0; j < n; j++ {
			if j > 0 {
				b.WriteString(sep())
			}
			b.WriteString(pick())
		}
		add(b.String())
	}

	// B: mutated seeds.
	var short []string
	for _, s := range seeds {
		if n := utf8.RuneCountInString(s); n > 0 && n <= 200 {
			short = append(short, s)
		}
	}
	for i := 0; i < mutated && len(short) > 0; i++ {
		r := []rune(short[rng.intn(len(short))])
		for ops := 1 + rng.intn(3); ops > 0; ops-- {
			if len(r) == 0 {
				r = []rune(pick())
				continue
			}
			at := rng.intn(len(r))
			switch rng.intn(7) {
			case 0: // insert a token
				tok := []rune(pick())
				r = append(r[:at], append(tok, r[at:]...)...)
			case 1: // delete a span
				end := at + 1 + rng.intn(5)
				if end > len(r) {
					end = len(r)
				}
				r = append(r[:at], r[end:]...)
			case 2: // duplicate a span
				end := at + 1 + rng.intn(8)
				if end > len(r) {
					end = len(r)
				}
				span := append([]rune(nil), r[at:end]...)
				r = append(r[:end], append(span, r[end:]...)...)
			case 3: // swap adjacent
				if at+1 < len(r) {
					r[at], r[at+1] = r[at+1], r[at]
				}
			case 4: // replace a character
				r[at] = fuzzCharPool[rng.intn(len(fuzzCharPool))]
			case 5: // insert a character
				c := fuzzCharPool[rng.intn(len(fuzzCharPool))]
				r = append(r[:at], append([]rune{c}, r[at:]...)...)
			case 6: // truncate
				r = r[:at]
			}
		}
		add(string(r))
	}

	// C: concatenations of seeds.
	for i := 0; i < concat && len(short) > 0; i++ {
		n := 2 + rng.intn(3)
		var b strings.Builder
		for j := 0; j < n; j++ {
			if j > 0 {
				b.WriteString(sep())
			}
			b.WriteString(short[rng.intn(len(short))])
		}
		add(b.String())
	}

	// D: a non-BMP character, and a heredoc, around the 5119-unit chunk the web binding reads
	// (and its multiples), where a surrogate pair is split between two chunks.
	for _, base := range chunkBases(chunks) {
		for off := -12; off <= 12; off++ {
			pad := strings.Repeat("a", base+off-5)
			for _, form := range []string{
				"echo " + pad + "😀 tail",
				"echo " + pad + "😀😀😀 > o",
				"echo '" + pad[:len(pad)-1] + "😀' x",
				"cat <<EOF\n" + pad + "😀 $(rm x)\nEOF",
				"x=" + pad + "😀 cmd",
				"echo \"" + pad[:len(pad)-1] + "😀$(ls)\"",
				"cat <<EOF | rm x\n" + pad + "\n😀\nEOF",
			} {
				add(form)
			}
		}
	}
	return out
}

// TestGenFuzz writes the fuzz inputs for the oracle run: BP_GEN_FUZZ=<out.json> go test -run
// TestGenFuzz ./bashparse (see port/bashparse-diff/run.sh).
func TestGenFuzz(t *testing.T) {
	path := os.Getenv("BP_GEN_FUZZ")
	if path == "" {
		t.Skip("BP_GEN_FUZZ not set: writes the fuzz inputs the oracle run reads")
	}
	data, err := json.Marshal(fuzzInputs(differentialSeeds(t)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func chunkBases(on bool) []int {
	if !on {
		return nil
	}
	return []int{5119, 10238}
}

// TestGenFuzzMore writes a further fuzz corpus from another generator seed for the oracle run (an
// investigation tool; the committed corpus is TestGenFuzz's): BP_GEN_FUZZ_MORE=<out.json>
// BP_FUZZ_SEED=<n> go test -run TestGenFuzzMore ./bashparse
func TestGenFuzzMore(t *testing.T) {
	path := os.Getenv("BP_GEN_FUZZ_MORE")
	if path == "" {
		t.Skip("BP_GEN_FUZZ_MORE not set")
	}
	seed, _ := strconv.ParseUint(os.Getenv("BP_FUZZ_SEED"), 10, 64)
	n := 250000
	data, err := json.Marshal(fuzzInputsFrom(differentialSeeds(t), seed*0x9e3779b97f4a7c15+1, n*2/5, n*2/5, n/5, false))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCompareFuzzMore compares the Go parse with the original's hashes for a TestGenFuzzMore corpus:
// BP_FUZZ_IN=<in.json> BP_FUZZ_HASHES=<hash.txt> go test -run TestCompareFuzzMore ./bashparse
func TestCompareFuzzMore(t *testing.T) {
	in, hashes := os.Getenv("BP_FUZZ_IN"), os.Getenv("BP_FUZZ_HASHES")
	if in == "" || hashes == "" {
		t.Skip("BP_FUZZ_IN and BP_FUZZ_HASHES not set")
	}
	inputs := readInputs(t, in)
	raw, err := os.ReadFile(hashes)
	if err != nil {
		t.Fatal(err)
	}
	var want []uint32
	for _, f := range strings.Fields(string(raw)) {
		v, err := strconv.ParseUint(f, 16, 32)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, uint32(v))
	}
	compareHashes(t, "fuzz-more", inputs, want)
}
