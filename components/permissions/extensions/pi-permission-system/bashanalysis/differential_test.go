package bashanalysis

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// The differential: testdata/oracle.jsonl.gz is what the original's own functions answered for a
// corpus of bash commands, recorded by port/bashanalysis-diff/run.sh (probe.test.ts). Each line is
// compared field by field with this package's answer for the same input. testdata/differences.json
// lists inputs whose difference is explained; every other difference fails the test.

// recordedHome is the home directory of the machine the original was recorded on.
const recordedHome = "/home/kinsy"

type diffRecord map[string]any

// jsSplitWhitespace is s.split(/\s+/).
func jsSplitWhitespace(s string) []string {
	var out []string
	var cur strings.Builder
	inSpace := false
	for _, r := range s {
		if isJSSpace(r) {
			if !inSpace {
				out = append(out, cur.String())
				cur.Reset()
				inSpace = true
			}
			continue
		}
		inSpace = false
		cur.WriteRune(r)
	}
	return append(out, cur.String())
}

func jsonNormalize(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}

func commandJSON(c BashCommand) map[string]any {
	m := map[string]any{"text": c.Text}
	if c.Context != "" {
		m["context"] = string(c.Context)
	}
	if c.WrapperKind != "" {
		m["wrapperKind"] = string(c.WrapperKind)
	}
	if c.ExecutedUnit != "" {
		m["executedUnit"] = c.ExecutedUnit
	}
	if c.FloorExemption != "" {
		m["floorExemption"] = string(c.FloorExemption)
	}
	if c.ParseUnresolved {
		m["parseUnresolved"] = true
	}
	if c.Salvaged {
		m["salvaged"] = true
	}
	if c.Spellings != nil {
		m["spellings"] = c.Spellings
	}
	return m
}

func effectJSON(e bashparse.TokenEffect) map[string]any {
	return map[string]any{"effect": string(e.Effect), "source": string(e.Source)}
}

func argWordJSON(w bashparse.ArgWord) map[string]any {
	return map[string]any{"value": w.Value, "computed": w.Computed, "mayLeadWithDash": w.MayLeadWithDash}
}

func wrapperFacts(words []CommandWord, unit string) map[string]any {
	kind := any(nil)
	if k := ClassifyWrapperWords(words); k != "" {
		kind = string(k)
	}
	executed := any(nil)
	if e, ok := ExecutedUnitOf(unit, words); ok {
		executed = e
	}
	return map[string]any{
		"kind":              kind,
		"index":             InlineShellPayloadIndex(words),
		"executed":          executed,
		"transparent":       IsTransparentWrapper(words, false),
		"transparentWrites": IsTransparentWrapper(words, true),
	}
}

func commandNodesOf(node *bashparse.Node, out *[]*bashparse.Node) {
	if node.Type() == "command" {
		*out = append(*out, node)
	}
	for _, c := range node.Children() {
		commandNodesOf(c, out)
	}
}

func argWords2(words []CommandWord) []bashparse.ArgWord {
	out := make([]bashparse.ArgWord, len(words))
	for i, w := range words {
		out[i] = w.ArgWord
	}
	return out
}

// goRecord is the Go side of probe.test.ts record().
func goRecord(s string) diffRecord {
	rec := diffRecord{"s": s}
	parser := bashparse.GetWarmBashParser()
	if cmds, ok := ParseBashCommandsSync(s); ok {
		list := make([]any, len(cmds))
		for i, c := range cmds {
			list[i] = commandJSON(c)
		}
		rec["sync"] = list
	} else {
		rec["sync"] = nil
	}

	tree := parser.Parse(s)
	if tree != nil {
		root := tree.Root
		reader := bashparse.NewWordReader(bashparse.ScanShellVariables([]*bashparse.Node{root}))
		toks := []any{}
		for _, tok := range CollectPathCandidateTokens(root, reader) {
			toks = append(toks, map[string]any{"token": tok.Token, "effect": effectJSON(tok.Effect), "role": string(tok.Role)})
		}
		rec["tokens"] = toks
		var nodes []*bashparse.Node
		commandNodesOf(root, &nodes)
		cmds := []any{}
		for _, node := range nodes {
			args := []bashparse.ArgWord{}
			for _, child := range node.Children() {
				if !child.IsNamed() || CommandPrefixTypes[child.Type()] || bashparse.RedirectNodeTypes[child.Type()] {
					continue
				}
				args = append(args, reader.ArgWord(child))
			}
			head, haveHead := ExtractCommandWord(node, reader)
			name, haveName := ExtractCommandName(node, reader)
			m := map[string]any{"name": nil, "word": nil, "payload": nil}
			if haveName {
				m["name"] = name
			}
			if haveHead {
				m["word"] = head
			}
			if p := InlineShellPayloadNode(node, reader); p != nil {
				m["payload"] = map[string]any{"text": p.Text(), "start": p.StartIndex()}
			}
			argsJSON := make([]any, len(args))
			for i, a := range args {
				argsJSON[i] = argWordJSON(a)
			}
			m["args"] = argsJSON
			m["prove"] = effectJSON(ProveCommandEffect(head, args))
			cmds = append(cmds, m)
		}
		rec["cmds"] = cmds
	} else {
		rec["tokens"], rec["cmds"] = nil, nil
	}

	words := words(s)
	rec["words"] = wrapperFacts(words, s)
	unitWords := []any{}
	if cmds, ok := ParseBashCommandsSync(s); ok {
		for _, unit := range cmds {
			unitWords = append(unitWords, wrapperFacts(standIn(unit.Text), unit.Text))
		}
	}
	rec["unitWords"] = unitWords
	tokens := jsSplitWhitespace(s)
	rec["arity"] = map[string]any{"prefix": Prefix(tokens), "strip": StripBashCommentLines(s)}
	literal := make([]bashparse.ArgWord, len(tokens))
	for i, v := range tokens {
		literal[i] = bashparse.ArgWord{Value: v, MayLeadWithDash: strings.HasPrefix(v, "-")}
	}
	wordArgs := argWords2(words)
	rest := func(a []bashparse.ArgWord) []bashparse.ArgWord {
		if len(a) == 0 {
			return nil
		}
		return a[1:]
	}
	rec["sed"] = []any{
		SedWithdrawsReadClaim(wordArgs), SedWithdrawsReadClaim(rest(wordArgs)),
		SedWithdrawsReadClaim(literal), SedWithdrawsReadClaim(rest(literal)),
	}
	rec["awk"] = []any{
		AwkWithdrawsReadClaim(wordArgs), AwkWithdrawsReadClaim(rest(wordArgs)),
		AwkWithdrawsReadClaim(literal), AwkWithdrawsReadClaim(rest(literal)),
	}
	return rec
}

func standIn(unit string) []CommandWord { return words(unit) }

func TestDifferential(t *testing.T) {
	bashparse.WarmBashParser()
	f, err := os.Open("testdata/oracle.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	explained := map[string]string{}
	if b, err := os.ReadFile("testdata/differences.json"); err == nil {
		if err := json.Unmarshal(b, &explained); err != nil {
			t.Fatal(err)
		}
	}
	// The recording ran with HOME=/home/kinsy; the home-expanded spellings and `~` words carry it.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	jsonOf := func(v string) string {
		b, _ := json.Marshal(v)
		return string(b[1 : len(b)-1])
	}
	homeJSON, recordedHomeJSON := jsonOf(home), jsonOf(recordedHome)
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<28)
	total, bad := 0, 0
	fields := map[string]int{}
	var examples []string
	for sc.Scan() {
		var want diffRecord
		line := sc.Bytes()
		if recordedHome != home {
			line = []byte(strings.ReplaceAll(string(line), recordedHomeJSON, homeJSON))
		}
		if err := json.Unmarshal(line, &want); err != nil {
			t.Fatal(err)
		}
		total++
		input := want["s"].(string)
		got := jsonNormalize(goRecord(input)).(map[string]any)
		var differing []string
		for k, w := range want {
			if !reflect.DeepEqual(got[k], w) {
				differing = append(differing, k)
				fields[k]++
			}
		}
		if len(differing) == 0 {
			continue
		}
		if _, ok := explained[input]; ok {
			continue
		}
		bad++
		if len(examples) < 12 {
			sort.Strings(differing)
			k := differing[0]
			wb, _ := json.Marshal(want[k])
			gb, _ := json.Marshal(got[k])
			examples = append(examples, fmt.Sprintf("%q field %s\n  original: %s\n  go:       %s", input, k, wb, gb))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if total < 42038 {
		t.Fatalf("only %d recorded inputs", total)
	}
	t.Logf("%d inputs compared, %d differences (fields %v)", total, bad, fields)
	if bad > 0 {
		t.Fatalf("%d unexplained differences:\n%s", bad, strings.Join(examples, "\n"))
	}
}
