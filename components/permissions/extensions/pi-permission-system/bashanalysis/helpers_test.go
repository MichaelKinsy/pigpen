package bashanalysis

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// eq fails unless got and want are deeply equal.
func eq(t *testing.T, got, want any, msg ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s got %#v, want %#v", strings.Join(msg, " "), got, want)
	}
}

// parseRoot is the corrected parser's tree for command (getParser().parse).
func parseRoot(t testing.TB, command string) *bashparse.Node {
	t.Helper()
	p, err := bashparse.GetParser()
	if err != nil {
		t.Fatal(err)
	}
	tree := p.Parse(command)
	if tree == nil {
		t.Fatalf("parse returned nil for %q", command)
	}
	return tree.Root
}

func findFirst(node *bashparse.Node, typ string) *bashparse.Node {
	if node.Type() == typ {
		return node
	}
	for _, child := range node.Children() {
		if found := findFirst(child, typ); found != nil {
			return found
		}
	}
	return nil
}

// words splits a command unit into words the way the AST walk does (wrapper-analysis.test.ts
// words()): whitespace separated, but a quoted span is one word carrying its quotes. It is the
// pattern /"[^"]*"|'[^']*'|\S+/g with JavaScript's \s, offsets in UTF-16 units.
func words(unitText string) []CommandWord {
	rs := []rune(unitText)
	var out []CommandWord
	unit := 0 // UTF-16 index of rs[i]
	width := func(r rune) int {
		if r >= 0x10000 {
			return 2
		}
		return 1
	}
	for i := 0; i < len(rs); {
		end := -1
		if q := rs[i]; q == '"' || q == '\'' {
			for j := i + 1; j < len(rs); j++ {
				if rs[j] == q {
					end = j + 1
					break
				}
			}
		}
		if end == -1 && !isJSSpace(rs[i]) {
			end = i
			for end < len(rs) && !isJSSpace(rs[end]) {
				end++
			}
		}
		if end == -1 {
			unit += width(rs[i])
			i++
			continue
		}
		text := string(rs[i:end])
		out = append(out, CommandWord{ArgWord: argWordOf(text), Text: text, Offset: unit})
		for _, r := range rs[i:end] {
			unit += width(r)
		}
		i = end
	}
	return out
}

var quotedPattern = regexp.MustCompile(`(?s)^(['"])(.*)(['"])$`)

// argWordOf is the value a stand-in word reaches its program with: the surrounding quotes removed,
// and computed when an unquoted or double-quoted `$` decides it. A computed stand-in may always
// lead with a dash.
func argWordOf(text string) bashparse.ArgWord {
	value, quote := text, ""
	if m := quotedPattern.FindStringSubmatch(text); m != nil && m[1] == m[3] {
		value, quote = m[2], m[1]
	}
	computed := quote != "'" && strings.Contains(value, "$")
	return bashparse.ArgWord{Value: value, Computed: computed, MayLeadWithDash: computed || strings.HasPrefix(value, "-")}
}

// literalArgWords are argument words the source spells exactly (test/helpers/arg-words.ts).
func literalArgWords(values ...string) []bashparse.ArgWord {
	out := make([]bashparse.ArgWord, len(values))
	for i, v := range values {
		out[i] = bashparse.ArgWord{Value: v, MayLeadWithDash: strings.HasPrefix(v, "-")}
	}
	return out
}

// computedArgWord is an argument word whose value only the shell decides.
func computedArgWord(value string, mayLeadWithDash bool) bashparse.ArgWord {
	return bashparse.ArgWord{Value: value, Computed: true, MayLeadWithDash: mayLeadWithDash}
}
