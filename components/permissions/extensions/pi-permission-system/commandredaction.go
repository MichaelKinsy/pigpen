package pi_permission_system

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashanalysis"
	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Port of src/logging/command-redaction.ts: grammar-anchored masking of a secret bound to a sensitive name inside a bash command
// string. Every rule matches a parse node, never a substring of the command text (docs/decisions/0010 in the original). The
// original's offsets are UTF-16 indexes; here they are byte offsets of the same text, which order and splice identically.

// CommandBearingLogKeys are the log keys whose value is a bash command string.
var CommandBearingLogKeys = map[string]bool{"command": true, "executedUnit": true}

// maskSpan is a range of the command to replace, and what to put in its place.
type maskSpan struct {
	start, end  int
	replacement string
}

// words reads a word at the startup values of HOME and PWD: what is masked is decided by the name a value is bound to, and
// neither spelling of either variable is a sensitive name.
var words = bashparse.NewWordReader(bashparse.UnreboundVariables)

// maxPayloadDepth is how many payload layers to descend.
const maxPayloadDepth = 4

// RedactCommandSecrets masks every sensitively-named value in a bash command string. Best effort: a cold parser, a parse that
// panics, and a parse that recovered from a syntax error all yield what the walk did resolve; it never panics.
func RedactCommandSecrets(command string) (result string) {
	if command == "" {
		return command
	}
	defer func() {
		if recover() != nil {
			result = command
		}
	}()
	parser := bashparse.GetWarmBashParser()
	if parser == nil {
		return command
	}
	return applyMaskSpans(command, collectSpansIn(parser, command, 0, 0))
}

// collectSpansIn is every mask span in source, shifted by offset to its place in the command being masked, including the spans
// of any inline-shell payload source carries. A payload is re-parsed from its verbatim inner slice, so the masked payload stays
// quoted as it was written.
func collectSpansIn(parser bashparse.Parser, source string, offset, depth int) []maskSpan {
	tree := parser.Parse(source)
	if tree == nil {
		return nil
	}
	var own []maskSpan
	collectMaskSpans(tree.Root, &own)
	spans := make([]maskSpan, 0, len(own))
	for _, s := range own {
		spans = append(spans, maskSpan{s.start + offset, s.end + offset, s.replacement})
	}
	if depth >= maxPayloadDepth {
		return spans
	}
	for _, payload := range inlineShellPayloads(tree.Root) {
		sliced := payload.kind == "slice"
		shift := 0
		if sliced {
			shift = offset + payload.start
		}
		inner := collectSpansIn(parser, payload.text, shift, depth+1)
		if sliced {
			spans = append(spans, inner...)
		} else if len(inner) > 0 {
			spans = append(spans, maskSpan{payload.start + offset, payload.end + offset, RedactedPlaceholder})
		}
	}
	return spans
}

// payloadSource is an inline-shell payload's program, and how a span found in it maps back onto the command. "slice": the
// program is a verbatim span of the command. "stitched": assembled across quote boundaries, so the whole argument is masked when
// a secret is bound inside.
type payloadSource struct {
	kind       string
	text       string
	start, end int
}

func inlineShellPayloads(root *bashparse.Node) []payloadSource {
	var out []payloadSource
	var walk func(n *bashparse.Node)
	walk = func(n *bashparse.Node) {
		if n.Type() == "command" {
			if p := bashanalysis.InlineShellPayloadNode(n, words); p != nil {
				out = append(out, payloadSourceOf(p))
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			if c := n.Child(i); c != nil {
				walk(c)
			}
		}
	}
	walk(root)
	return out
}

var singlyQuotedPayloadTypes = map[string]bool{"string": true, "raw_string": true, "ansi_c_string": true}

func payloadSourceOf(node *bashparse.Node) payloadSource {
	start, end := node.StartByte(), node.EndByte()
	text := node.Text()
	if node.Type() == "word" {
		return payloadSource{"slice", text, start, end}
	}
	quoteAt := 0
	if strings.HasPrefix(text, "$") {
		quoteAt = 1
	}
	if singlyQuotedPayloadTypes[node.Type()] && len(text) >= quoteAt+2 && (text[quoteAt] == '\'' || text[quoteAt] == '"') && text[len(text)-1] == text[quoteAt] {
		return payloadSource{"slice", text[quoteAt+1 : len(text)-1], start + quoteAt + 1, end - 1}
	}
	return payloadSource{"stitched", words.Text(node), start, end}
}

// MaskCommandFields applies RedactCommandSecrets to every command-bearing key in a log record, recursing through plain objects
// and arrays. The result is a copy.
func MaskCommandFields(details *jsObject) *jsObject {
	return maskValue(details, false, 0).(*jsObject)
}

func maskValue(value any, bindsCommand bool, depth int) any {
	checkLogDepth(depth)
	switch v := value.(type) {
	case string:
		if bindsCommand {
			return RedactCommandSecrets(v)
		}
		return v
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = maskValue(e, bindsCommand, depth+1)
		}
		return out
	case map[string]any:
		if v != nil {
			return maskValue(orderedRecord(v), bindsCommand, depth)
		}
	case *jsObject:
		if v != nil {
			out := newObject()
			for _, k := range v.order() {
				out.set(k, maskValue(v.vals[k], CommandBearingLogKeys[k], depth+1))
			}
			return out
		}
	}
	return value
}

func collectMaskSpans(node *bashparse.Node, spans *[]maskSpan) {
	if s := maskSpanOf(node); s != nil {
		*spans = append(*spans, *s)
	}
	for i := 0; i < node.ChildCount(); i++ {
		if c := node.Child(i); c != nil {
			collectMaskSpans(c, spans)
		}
	}
}

func maskSpanOf(node *bashparse.Node) *maskSpan {
	if s := assignmentValueSpan(node); s != nil {
		return s
	}
	if s := wordAssignmentSpan(node); s != nil {
		return s
	}
	return headerValueSpan(node)
}

func newMaskSpan(start, end int, replacement string) *maskSpan {
	if start < end {
		return &maskSpan{start, end, replacement}
	}
	return nil
}

// assignmentValueSpan: `KEY="sk-..." curl ...`, `KEY=sk-...`, `export OPENROUTER_KEY="sk-..."`. The span runs to the assignment
// node's own end, so a value the grammar splits across several children is covered whole.
func assignmentValueSpan(node *bashparse.Node) *maskSpan {
	if node.Type() != "variable_assignment" {
		return nil
	}
	name := node.Child(0)
	if name == nil || !IsSensitiveName(name.Text()) {
		return nil
	}
	value := node.Child(2)
	if value == nil {
		return nil
	}
	return newMaskSpan(value.StartByte(), node.EndByte(), RedactedPlaceholder)
}

// wordAssignment: `env MY_KEY=abc deploy`, which tree-sitter classifies as a plain word. A long option (`--my-key=abc`) cannot
// match: the flag forms are deliberately out of scope.
var wordAssignment = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=`)

func wordAssignmentSpan(node *bashparse.Node) *maskSpan {
	if node.Type() != "word" {
		return nil
	}
	m := wordAssignment.FindStringSubmatch(node.Text())
	if m == nil || !IsSensitiveName(m[1]) {
		return nil
	}
	return newMaskSpan(node.StartByte()+len(m[0]), node.EndByte(), RedactedPlaceholder)
}

// headerField reads `^([A-Za-z][A-Za-z0-9_-]*)[ \t]*:[ \t]*\S` and returns the field name. \S is JavaScript's: any character
// that is not Unicode white space.
func headerField(s string) (string, bool) {
	i := 0
	if i >= len(s) || !(s[i] >= 'A' && s[i] <= 'Z' || s[i] >= 'a' && s[i] <= 'z') {
		return "", false
	}
	for i < len(s) && (s[i] >= 'A' && s[i] <= 'Z' || s[i] >= 'a' && s[i] <= 'z' || s[i] >= '0' && s[i] <= '9' || s[i] == '_' || s[i] == '-') {
		i++
	}
	field := s[:i]
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) || s[i] != ':' {
		return "", false
	}
	i++
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) {
		return "", false
	}
	if r, _ := utf8.DecodeRuneInString(s[i:]); isJSSpace(r) {
		return "", false
	}
	return field, true
}

var camelCased = regexp.MustCompile(`[a-z][A-Z]`)

// headerValueSpan: `curl -H "Authorization: Bearer sk-..."`, in any of its quoting forms. An HTTP field name is hyphenated, never
// camel-cased.
func headerValueSpan(node *bashparse.Node) *maskSpan {
	if !bashparse.ArgNodeTypes[node.Type()] {
		return nil
	}
	field, ok := headerField(words.Text(node))
	if !ok || !IsSensitiveName(field) || camelCased.MatchString(field) {
		return nil
	}
	text := node.Text()
	colon := strings.IndexByte(text, ':')
	if colon < 0 {
		return nil
	}
	// The span swallows a closing quote, so the replacement puts one back and the masked argument stays quoted as written.
	return newMaskSpan(node.StartByte()+colon+1, node.EndByte(), RedactedPlaceholder+openQuoteAt(text, colon))
}

// openQuoteAt is the quote character still open at index, or "", read at the mask's own position.
func openQuoteAt(text string, index int) string {
	quote := byte(0)
	for i := 0; i < index; i++ {
		c := text[i]
		switch {
		case quote == 0:
			if c == '"' || c == '\'' {
				quote = c
			}
		case quote == '"' && c == '\\':
			i++
		case c == quote:
			quote = 0
		}
	}
	if quote == 0 {
		return ""
	}
	return string(quote)
}

// applyMaskSpans replaces each span, outermost-wins and right to left.
func applyMaskSpans(command string, spans []maskSpan) string {
	if len(spans) == 0 {
		return command
	}
	ordered := append([]maskSpan(nil), spans...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].start != ordered[j].start {
			return ordered[i].start < ordered[j].start
		}
		return ordered[i].end > ordered[j].end
	})
	var disjoint []maskSpan
	for _, s := range ordered {
		if n := len(disjoint); n > 0 && s.start < disjoint[n-1].end {
			continue
		}
		disjoint = append(disjoint, s)
	}
	masked := command
	for i := len(disjoint) - 1; i >= 0; i-- {
		s := disjoint[i]
		masked = masked[:s.start] + s.replacement + masked[s.end:]
	}
	return masked
}
