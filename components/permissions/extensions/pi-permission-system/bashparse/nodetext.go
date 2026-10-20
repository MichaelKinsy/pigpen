package bashparse

import (
	"regexp"
	"strings"
	"unicode/utf16"
)

// SkipSubtreeTypes are the node types whose text content is never a command argument, so no path
// candidate is ever read from it (node-text.ts SKIP_SUBTREE_TYPES).
//
// This governs the subtree's text, not whether it is visited at all: an interpolating
// heredoc_body is also an execution host, so it is still descended for the commands it runs while
// its prose stays out of the path surface (#741).
var SkipSubtreeTypes = setOf("heredoc_body", "heredoc_end", "comment")

// ArgNodeTypes are the node types that represent argument values in the AST (word, concatenation,
// single-quoted string, double-quoted string).
var ArgNodeTypes = setOf("word", "concatenation", "string", "raw_string")

// ArgWord is one argument as the program receives it, and whether that is knowable. A capability
// proof must read the value after quote removal, since `'-o'` and `-o` reach `sort` identically,
// and must know when the value is computed, since a word only the shell decides can spell any
// option at all.
type ArgWord struct {
	// Value is the string the shell passes after quote removal (WordReader.Text).
	Value string
	// Computed reports whether Value may differ from what the program receives: a part only
	// running the command decides, or a spelling the shell rewrites before the program sees it (an
	// escape, a glob, a brace expansion, an ANSI-C string), which resolveText passes through as
	// written.
	Computed bool
	// MayLeadWithDash reports whether the program may receive this argument, or a word split from
	// it, beginning with `-`, the shape every option has. Exact for a word that is not computed;
	// for a computed one, false only when a literal leading character survives every rewrite the
	// shell applies.
	MayLeadWithDash bool
}

// WordReader reads argument nodes as one program's shell expands them: the node-text reads bound to
// the ShellVariables that program rebinds.
type WordReader struct {
	variables *ShellVariables
}

// NewWordReader is a reader for a program whose variables are v.
func NewWordReader(v *ShellVariables) *WordReader { return &WordReader{variables: v} }

// ArgWord reads an argument node into the word the program receives.
func (r *WordReader) ArgWord(node *Node) ArgWord {
	value := r.Text(node)
	tilde, hasTilde := r.variables.ReadTilde(leadingUnquotedText(node))
	if hasTilde && !tilde.Known {
		return ArgWord{Value: value, Computed: true, MayLeadWithDash: true}
	}
	computed := r.IsComputed(node) || !isSpelledExactly(node, r.variables)
	if hasTilde {
		// The spelling `~` is not what the program receives; it is exact enough to prove with only
		// while the home it stands for cannot be an option.
		return ArgWord{
			Value:           value,
			Computed:        computed || tilde.LeadsWithDash,
			MayLeadWithDash: tilde.LeadsWithDash || (computed && maySplitIntoWords(node, false)),
		}
	}
	mayLead := strings.HasPrefix(value, "-")
	if computed {
		mayLead = mayExpandToDashWord(node)
	}
	return ArgWord{Value: value, Computed: computed, MayLeadWithDash: mayLead}
}

// Text is the string the shell passes after quote removal (resolveText).
func (r *WordReader) Text(node *Node) string { return resolveText(node, r.variables) }

// IsComputed reports whether the node's value is decided at run time (computedPart).
func (r *WordReader) IsComputed(node *Node) bool { return computedPart(node, r.variables) }

// SpellsReboundHome reports whether a collected token is spelled from a rebound HOME.
func (r *WordReader) SpellsReboundHome(token string) bool {
	return r.variables.SpellsReboundHome(token)
}

// SpellHomeAtStart is a command unit's text with its leading home prefix spelled out.
func (r *WordReader) SpellHomeAtStart(text string) (string, bool) {
	return r.variables.SpellHomeAtStart(text)
}

// leadingUnquotedText is the unquoted literal an argument opens with, where bash expands a tilde
// prefix: the word itself, or a concatenation's first part when that is a word. A tilde after any
// other part, or inside quotes, stays literal.
func leadingUnquotedText(node *Node) string {
	if node.typ == "word" {
		return node.Text()
	}
	if node.typ != "concatenation" {
		return ""
	}
	first := node.Child(0)
	if first != nil && first.typ == "word" {
		return first.Text()
	}
	return ""
}

// mayExpandToDashWord reports whether a computed word may reach the program, whole or as one of the
// words the shell splits it into, beginning with `-`.
//
// Globbing, brace expansion, and escape removal each keep a literal prefix, so a word whose leading
// character is a literal other than `-` cannot become an option; an expansion that can split into
// several words decides nothing, since only the first of them carries the prefix.
func mayExpandToDashWord(node *Node) bool {
	if maySplitIntoWords(node, false) {
		return true
	}
	lead, defined := leadingCharacterMayBeDash(node)
	return !defined || lead
}

// wordSplittingTypes are the expansions whose unquoted result the shell splits into words.
var wordSplittingTypes = setOf("simple_expansion", "expansion", "command_substitution", "arithmetic_expansion")

// unsplitTypes are the nodes whose inside is never split into the argument's words: a
// single-quoted or ANSI-C string, and a process substitution, whose body belongs to its own command.
var unsplitTypes = setOf("raw_string", "ansi_c_string", "process_substitution")

// parameterExpansionTypes are the parameter expansions, which may yield several words even when
// quoted.
var parameterExpansionTypes = setOf("simple_expansion", "expansion")

// maySplitIntoWords reports whether any expansion in the word may produce more than one word.
//
// Unquoted, every expansion splits. Inside double quotes a command substitution or arithmetic
// expansion is one word, but a parameter expansion may not be: `$@` and `${arr[@]}` expand to one
// word per element, an indirect `${!name}` may name either, and any variable may be a nameref
// (`declare -n s='arr[@]'`), with nothing in its own spelling to show it. So every quoted parameter
// expansion counts, over-counting a joined `$*`.
func maySplitIntoWords(node *Node, quoted bool) bool {
	if wordSplittingTypes[node.typ] {
		return !quoted || parameterExpansionTypes[node.typ]
	}
	if unsplitTypes[node.typ] {
		return false
	}
	childrenQuoted := quoted || node.typ == "string"
	for _, child := range node.children {
		if maySplitIntoWords(child, childrenQuoted) {
			return true
		}
	}
	return false
}

// leadingCharacterMayBeDash reports whether the first character the word produces may be `-`;
// defined is false while every part read so far produced nothing (`""`, `”`). A part this does
// not know is non-literal, and so may lead with anything.
func leadingCharacterMayBeDash(node *Node) (mayBeDash, defined bool) {
	switch node.typ {
	case "word", "number":
		return unquotedLiteralMayLeadWithDash(node.Text())
	case "raw_string":
		return quotedLiteralMayLeadWithDash(sliceInner(node.Text()))
	case "string_content":
		return quotedLiteralMayLeadWithDash(node.Text())
	case `"`:
		return false, false
	case "process_substitution":
		// The program receives a `/dev/fd/N` path.
		return false, true
	case "string", "concatenation":
		for _, child := range node.children {
			if lead, ok := leadingCharacterMayBeDash(child); ok {
				return lead, true
			}
		}
		return false, false
	}
	return true, true
}

// sliceInner is text.slice(1, -1) over UTF-16 units.
func sliceInner(text string) string {
	units := utf16.Encode([]rune(text))
	if len(units) <= 2 {
		return ""
	}
	return string(utf16.Decode(units[1 : len(units)-1]))
}

// undeterminedLead matches a leading character that may become, or already is, `-`.
var undeterminedLead = regexp.MustCompile(`^[-*?[{]`)

// unquotedLiteralMayLeadWithDash handles an unquoted literal: a leading glob or brace can produce
// any character, and an escape yields the character after it.
func unquotedLiteralMayLeadWithDash(text string) (bool, bool) {
	if text == "" {
		return false, false
	}
	if strings.HasPrefix(text, "\\") {
		units := utf16.Encode([]rune(text))
		return len(units) < 2 || units[1] == '-', true
	}
	return undeterminedLead.MatchString(text), true
}

// quotedLiteralMayLeadWithDash: a quoted literal reaches the program as written.
func quotedLiteralMayLeadWithDash(text string) (bool, bool) {
	if text == "" {
		return false, false
	}
	return strings.HasPrefix(text, "-"), true
}

// isSpelledExactly reports whether resolveText returns exactly the string the shell passes. It
// answers false for any node type it does not know, which is the fail-closed direction for a caller
// proving what a word cannot be.
func isSpelledExactly(node *Node, v *ShellVariables) bool {
	switch node.typ {
	case "raw_string":
		return true
	case "word":
		return !shellRewrittenCharacters.MatchString(node.Text())
	case "string_content":
		return !strings.Contains(node.Text(), "\\")
	case "simple_expansion", "expansion":
		_, ok := v.ResolveReference(node)
		return ok
	case "string", "number":
		// A digit run is an ordinary word to the shell; the grammar's `10#$x` form carries an
		// expansion child, which answers for itself.
		return childrenSpelledExactly(node, v)
	case "concatenation":
		// The grammar splits `{-i,-n}` into plain words, so the expansion is visible only across
		// the whole concatenation's text.
		return !braceExpansion.MatchString(node.Text()) && childrenSpelledExactly(node, v)
	}
	return false
}

// childrenSpelledExactly: a `"` delimiter is spelled exactly; every named child must be too.
func childrenSpelledExactly(node *Node, v *ShellVariables) bool {
	for _, child := range node.children {
		if child.typ == `"` {
			continue
		}
		if !isSpelledExactly(child, v) {
			return false
		}
	}
	return true
}

// shellRewrittenCharacters match an escape, or a glob the shell may expand into other words.
var shellRewrittenCharacters = regexp.MustCompile(`[\\*?[]`)

// braceExpansion matches a brace the shell expands: one holding a `,` or a `..` range. An empty
// `{}` is left alone by bash, which is why `find -exec ... {} +` keeps its placeholder as written.
var braceExpansion = regexp.MustCompile(`\{[^}]*(,|\.\.)[^}]*\}`)

// computedNodeTypes are the node types whose value only running the command can produce.
var computedNodeTypes = setOf("command_substitution", "process_substitution", "arithmetic_expansion")

// variableExpansionTypes are variable references, computed unless they resolve as a plain
// reference.
var variableExpansionTypes = setOf("simple_expansion", "expansion")

// computedPart reports whether an argument node's value is decided at run time: it contains a
// command or process substitution, an arithmetic expansion, or a variable expansion
// ResolveReference cannot resolve.
//
// The complement of what resolveText can spell exactly. A plain `$HOME` / `$PWD` reference
// resolves, so `"$HOME/out"` is not computed; any other expansion falls back to its own source text
// there, which names a file that is not the one the shell will touch (ADR 0009's computed-path
// residual). A single-quoted `'$x'` is a literal.
func computedPart(node *Node, v *ShellVariables) bool {
	if computedNodeTypes[node.typ] {
		return true
	}
	if variableExpansionTypes[node.typ] {
		_, ok := v.ResolveReference(node)
		return !ok
	}
	for _, child := range node.children {
		if computedPart(child, v) {
			return true
		}
	}
	return false
}

// resolveText is the "shell value" of an argument node: the string the shell would pass to the
// command after quote removal.
//
//   - word: text (already unquoted)
//   - raw_string: strip surrounding single quotes
//   - string: strip surrounding double quotes, concatenate children text
//   - concatenation: concatenate resolved children
//   - expansions: the resolved value of a plain $HOME / $PWD reference, else text
//   - other: text as fallback
func resolveText(node *Node, v *ShellVariables) string {
	switch node.typ {
	case "word":
		return node.Text()
	case "raw_string":
		// Strip surrounding single quotes: 'content' -> content
		text := node.Text()
		units := utf16.Encode([]rune(text))
		if len(units) >= 2 && strings.HasPrefix(text, "'") && strings.HasSuffix(text, "'") {
			return string(utf16.Decode(units[1 : len(units)-1]))
		}
		return text
	case "string":
		// Double-quoted string: concatenate the resolved text of inner children, skipping the
		// quote-delimiter nodes (literal `"`).
		var b strings.Builder
		for _, child := range node.children {
			if child.typ == `"` {
				continue
			}
			b.WriteString(resolveText(child, v))
		}
		return b.String()
	case "string_content":
		return node.Text()
	case "simple_expansion", "expansion":
		if value, ok := v.ResolveReference(node); ok {
			return value
		}
		return node.Text()
	case "concatenation":
		var b strings.Builder
		for _, child := range node.children {
			b.WriteString(resolveText(child, v))
		}
		return b.String()
	}
	return node.Text()
}
