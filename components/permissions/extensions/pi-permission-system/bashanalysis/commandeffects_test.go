package bashanalysis

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

const cef = "access-intent/bash/command-effects"

var (
	coreRead    = bashparse.TokenEffect{Effect: "read", Source: "core"}
	retracted   = bashparse.TokenEffect{Effect: "unproven", Source: "retracted"}
	syntaxRead  = bashparse.TokenEffect{Effect: "read", Source: "syntax"}
	syntaxWrite = bashparse.TokenEffect{Effect: "write", Source: "syntax"}
)

// roster is the frozen v1 roster, spelled out so the test pins it rather than mirrors it.
var roster = []string{
	"cat", "head", "tail", "wc", "grep", "egrep", "fgrep", "rg", "diff", "ls", "stat", "pwd", "basename", "dirname",
	"realpath", "echo", "which", "cd", "find", "fd", "sort", "sed", "awk",
}

// minimalArguments are the least a presumed reader needs to prove a read.
var minimalArguments = map[string][]string{"sed": {"p"}, "awk": {"{print}"}}

func prove(head string, args ...string) bashparse.TokenEffect {
	return ProveCommandEffect(head, literalArgWords(args...))
}

func sortedCore() []string {
	var out []string
	for w := range PureReaderCore() {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

func TestPureReaderCore(t *testing.T) {
	tw(t, cef, "holds exactly the audited words", func(t *testing.T) {
		want := append([]string(nil), roster...)
		sort.Strings(want)
		eq(t, sortedCore(), want)
	})
	tw(t, cef, "matches the roster published in docs/configuration.md", func(t *testing.T) {
		// The vendored original's docs sit outside the module, so a copy of the module (the mutation
		// runner's) cannot reach them; testdata/configuration-roster.md is the same block, byte for byte,
		// and the test checks the two agree whenever the docs are reachable.
		doc, err := os.ReadFile("testdata/configuration-roster.md")
		if err != nil {
			t.Fatal(err)
		}
		if vendored, err := os.ReadFile("../../../port/oracle/packages/pi-permission-system/docs/configuration.md"); err == nil {
			if !strings.Contains(string(vendored), strings.TrimSpace(string(doc))) {
				t.Fatal("testdata/configuration-roster.md has drifted from the original's docs/configuration.md")
			}
		}
		block := regexp.MustCompile(`(?s)<!-- BEGIN PURE_READER_CORE -->(.*?)<!-- END PURE_READER_CORE -->`).FindSubmatch(doc)
		if block == nil {
			t.Fatal("no roster block")
		}
		var documented []string
		for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(string(block[1]), -1) {
			documented = append(documented, m[1])
		}
		eq(t, documented, sortedCore())
	})
}

func TestProveCommandEffect(t *testing.T) {
	for _, w := range roster {
		t.Run("proves a read for "+w, func(t *testing.T) { eq(t, prove(w, minimalArguments[w]...), coreRead) })
	}
	tw(t, cef, "proves a read whatever its arguments are", func(t *testing.T) { eq(t, prove("cat", "-n", "~/.ssh/id_rsa"), coreRead) })
	for _, w := range []string{"pnpm", "git", "gawk", "uniq", "tee", "dd", "less", "more", "file", "curl", "tree", "node", "rm"} {
		t.Run("proves nothing for "+w, func(t *testing.T) { eq(t, prove(w), bashparse.UnprovenEffect) })
	}
	tw(t, cef, "proves nothing for an unresolvable head word", func(t *testing.T) { eq(t, prove("", "~/outside"), bashparse.UnprovenEffect) })
	tw(t, cef, "never proves a write, so rm cannot ride a read grant", func(t *testing.T) {
		eq(t, prove("rm", "-rf", "~/outside"), bashparse.UnprovenEffect)
	})
	for _, w := range []string{"./grep", "../grep", "/usr/bin/grep", "/tmp/evil/grep", `bin\grep`, `C:\tools\grep`, "/bin/sed", "./sed", "./awk"} {
		t.Run("refuses the core for the path-qualified head word "+w, func(t *testing.T) { eq(t, prove(w), bashparse.UnprovenEffect) })
	}

	// find
	for _, o := range []string{"-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf", "-fls"} {
		t.Run("retracts the read claim on "+o, func(t *testing.T) { eq(t, prove("find", ".", o, "rm", "{}", ";"), retracted) })
	}
	tw(t, cef, "keeps the read claim for an ordinary search", func(t *testing.T) { eq(t, prove("find", ".", "-name", "*.ts"), coreRead) })
	tw(t, cef, "does not retract on a word that merely contains a guarded option", func(t *testing.T) {
		eq(t, prove("find", ".", "-name", "-delete.txt"), coreRead)
	})
	// fd
	for _, o := range []string{"-x", "-X", "--exec", "--exec-batch"} {
		t.Run("retracts the read claim on "+o, func(t *testing.T) { eq(t, prove("fd", "foo", o, "rm"), retracted) })
	}
	tw(t, cef, "retracts on a long stem carrying an attached value", func(t *testing.T) { eq(t, prove("fd", "foo", "--exec=rm"), retracted) })
	tw(t, cef, "retracts on a guarded letter inside a short cluster", func(t *testing.T) { eq(t, prove("fd", "-Hx", "rm"), retracted) })
	tw(t, cef, "keeps the read claim for an ordinary search", func(t *testing.T) { eq(t, prove("fd", "-H", "--type", "f", "foo"), coreRead) })
	// sort
	for _, o := range []string{"-o", "--output"} {
		t.Run("retracts the read claim on "+o, func(t *testing.T) { eq(t, prove("sort", o, "/tmp/out", "in"), retracted) })
	}
	tw(t, cef, "retracts on a long stem carrying an attached value", func(t *testing.T) { eq(t, prove("sort", "--output=/tmp/out", "in"), retracted) })
	for _, o := range []string{"--out", "--outp", "--o"} {
		t.Run("retracts on the GNU long-option abbreviation "+o, func(t *testing.T) { eq(t, prove("sort", o, "/tmp/out", "in"), retracted) })
	}
	tw(t, cef, "retracts on an abbreviation carrying an attached value", func(t *testing.T) { eq(t, prove("sort", "--out=/tmp/out", "in"), retracted) })
	tw(t, cef, "does not treat a bare -- end-of-options marker as an abbreviation", func(t *testing.T) { eq(t, prove("sort", "--", "in"), coreRead) })
	tw(t, cef, "retracts on the attached-value short form", func(t *testing.T) { eq(t, prove("sort", "-o/tmp/out", "in"), retracted) })
	tw(t, cef, "retracts on a guarded letter inside a short cluster", func(t *testing.T) { eq(t, prove("sort", "-uo", "/tmp/out", "in"), retracted) })
	tw(t, cef, "keeps the read claim for an ordinary sort", func(t *testing.T) { eq(t, prove("sort", "-u", "-k2", "in"), coreRead) })
	// computed
	for _, h := range []string{"find", "fd", "sort"} {
		t.Run("retracts "+h+"'s read claim when the word may lead with a dash", func(t *testing.T) {
			eq(t, ProveCommandEffect(h, append(literalArgWords("in"), computedArgWord("$A", true))), retracted)
		})
		t.Run("keeps "+h+"'s read claim when the word cannot lead with a dash", func(t *testing.T) {
			eq(t, ProveCommandEffect(h, append(literalArgWords("in"), computedArgWord("x*", false))), coreRead)
		})
	}
	// sed
	tw(t, cef, "keeps the read claim for a print-only script", func(t *testing.T) { eq(t, prove("sed", "-n", "1,80p", "f.md"), coreRead) })
	tw(t, cef, "retracts the read claim for an in-place edit", func(t *testing.T) { eq(t, prove("sed", "-i", "s/a/b/", "f.md"), retracted) })
	tw(t, cef, "retracts the read claim for a computed script", func(t *testing.T) {
		eq(t, ProveCommandEffect("sed", append([]bashparse.ArgWord{computedArgWord("$range", true)}, literalArgWords("f.md")...)), retracted)
	})
	// awk
	tw(t, cef, "keeps the read claim for a program that only prints", func(t *testing.T) { eq(t, prove("awk", "{print $1}", "data"), coreRead) })
	tw(t, cef, "retracts the read claim for a program that redirects its output", func(t *testing.T) {
		eq(t, prove("awk", `{print > "out"}`, "data"), retracted)
	})
	// a guard belongs to its own word only
	tw(t, cef, "does not apply sed's guard to cat", func(t *testing.T) { eq(t, prove("cat", "-i", "w", "out"), coreRead) })
	tw(t, cef, "does not apply find's guard to cat", func(t *testing.T) { eq(t, prove("cat", "-delete"), coreRead) })
	tw(t, cef, "does not apply sort's guard to grep", func(t *testing.T) { eq(t, prove("grep", "-o", "pattern", "file"), coreRead) })
	tw(t, cef, "does not apply fd's guard to find", func(t *testing.T) { eq(t, prove("find", ".", "-x"), coreRead) })
}

func TestRedirectDestinationEffect(t *testing.T) {
	for _, op := range []string{">", ">>", ">|", "&>", "&>>"} {
		t.Run("proves a write for "+op, func(t *testing.T) {
			got, ok := RedirectDestinationEffect(op, false)
			eq(t, []any{got, ok}, []any{syntaxWrite, true})
		})
	}
	for _, op := range []string{"<", "<<<"} {
		t.Run("proves a read for "+op, func(t *testing.T) {
			got, ok := RedirectDestinationEffect(op, false)
			eq(t, []any{got, ok}, []any{syntaxRead, true})
		})
	}
	for _, op := range []string{">&", "<&"} {
		t.Run("collects no token for "+op+" a descriptor", func(t *testing.T) {
			_, ok := RedirectDestinationEffect(op, true)
			eq(t, ok, false)
		})
	}
	tw(t, cef, "proves a write when >& names a file instead", func(t *testing.T) {
		got, ok := RedirectDestinationEffect(">&", false)
		eq(t, []any{got, ok}, []any{syntaxWrite, true})
	})
	tw(t, cef, "proves a read when <& names a file instead", func(t *testing.T) {
		got, ok := RedirectDestinationEffect("<&", false)
		eq(t, []any{got, ok}, []any{syntaxRead, true})
	})
	for _, op := range []string{"<>", "&", ""} {
		t.Run("proves nothing rather than dropping the token for "+op, func(t *testing.T) {
			got, ok := RedirectDestinationEffect(op, false)
			eq(t, []any{got, ok}, []any{bashparse.UnprovenEffect, true})
		})
	}
	tw(t, cef, "still collects the token when the destination is a descriptor", func(t *testing.T) {
		got, ok := RedirectDestinationEffect("<>", true)
		eq(t, []any{got, ok}, []any{bashparse.UnprovenEffect, true})
	})
}

var _ = strings.TrimSpace
