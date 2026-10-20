package bashanalysis

import (
	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// FloorExemption names why a wrapper unit's floor has no reason left to hold (types.ts
// FloorExemption). "" is absent.
type FloorExemption string

// FloorExemptionCoreReader: the command the wrapper runs is in the pure-reader core (#803).
const FloorExemptionCoreReader FloorExemption = "core-reader"

// BashCommand is one command-pattern unit of a parsed bash program (command-enumeration.ts
// BashCommand). Text is the simple-command (or whole compound statement) string matched against
// the bash rules. The zero value of every other field is the original's absent property.
type BashCommand struct {
	Text string
	// Context is the execution context for a nested command (substitution or subshell); empty for
	// a current-shell (top-level) command.
	Context bashparse.BashCommandContext
	// WrapperKind is set when this unit is a floored wrapper; its decision is floored to at least
	// `ask` so the wrapped command cannot ride a permissive `allow`.
	WrapperKind WrapperKind
	// ExecutedUnit is the command this wrapper unit actually runs (#713); empty for an ordinary
	// command, and for a wrapper whose inner command cannot be established. Display-only, and
	// deliberately looks past an `sh -c` layer the gate must not look past: FloorExemption is the
	// gateable answer, established by its own walk (#803).
	ExecutedUnit string
	// FloorExemption is set when this wrapper unit's floor has no reason left to hold (#803). Only
	// ever present alongside WrapperIndirection and an established ExecutedUnit.
	FloorExemption FloorExemption
	// ParseUnresolved is set when this unit was emitted from, or beneath, a statement holding a
	// region tree-sitter could not resolve. Its decision is floored to at least `ask`, because the
	// recovered structure is not evidence of what runs: ADR 0013 section 10's fail-closed base case
	// (#840).
	ParseUnresolved bool
	// Salvaged is set when this unit came from a region re-parsed out of a subtree the primary parse
	// could not resolve, rather than from the primary parse itself (#875). Narrower than
	// ParseUnresolved, which a primary unit also carries when its enclosing statement failed: the
	// verdict fold needs the distinction to tell whether the primary parse found anything.
	Salvaged bool
	// Spellings are other spellings of Text the shell runs identically, matched with it as aliases
	// of one invocation; nil when there are none. Today the only spelling is the home-expanded one,
	// for a unit whose text opens with `~`, `$HOME`, or `${HOME}` in a program that leaves HOME
	// alone.
	Spellings []string
}

// unitScope is what the statement enclosing a command unit establishes about it. Both facts flow
// down the walk together because both are the statement's, not the command's: a subshell's commands
// run in a subshell however they are spelled, and a redirected statement writes a file however
// read-only the command in front of the operator is.
type unitScope struct {
	context bashparse.BashCommandContext
	// writesViaRedirect: the enclosing statement redirects output into a real file, which withholds
	// the floor exemption from any wrapper unit beneath it.
	writesViaRedirect bool
	// parseUnresolved: the enclosing statement holds a region tree-sitter could not resolve (#840).
	parseUnresolved bool
	// salvaged: the walk started from a salvaged region (#875). Relayed unchanged, including into
	// nested executions.
	salvaged bool
	// words is how the program's shell expands an argument word; relayed unchanged.
	words *bashparse.WordReader
}

// commandEnumDescend are the container node types descended into with the enclosing scope
// unchanged. `redirected_statement` is descended too, but has its own branch.
var commandEnumDescend = setOf("program", "list", "pipeline")

// compoundStatementTypes are the compound statements: emitted whole, then descended for their
// statements, so the commands found inside are additional units, never a replacement (#306).
// `select` parses as `for_statement` and `until` as `while_statement`.
var compoundStatementTypes = setOf(
	"if_statement", "while_statement", "for_statement", "c_style_for_statement", "case_statement",
	"function_definition", "compound_statement", "negated_command",
)

// statementGroupTypes are syntactic groupings inside a compound statement: descended, never
// emitted (a `do_group` is the loop body's punctuation).
var statementGroupTypes = setOf("do_group", "case_item", "elif_clause", "else_clause")

// commandEnumSkip are the named node types abandoned during command enumeration: neither commands
// nor able to host one. A redirect and a heredoc body are deliberately NOT listed: each can host a
// substitution that really executes (#741).
var commandEnumSkip = setOf("comment", "heredoc_end")

// statementTypes is every node type the enumerator recognizes as a statement: "is this a
// statement, so that descending an enclosing compound reaches it?" A compound statement's named
// children are a mix (a loop variable, a word list, a `case` subject, a function's name), and
// descending all of them would emit operand words as bash command units.
var statementTypes = func() map[string]bool {
	m := setOf("command", "redirected_statement", "subshell", "declaration_command", "variable_assignment",
		"test_command", "unset_command", "ERROR")
	for _, s := range []map[string]bool{commandEnumDescend, compoundStatementTypes, statementGroupTypes} {
		for k := range s {
			m[k] = true
		}
	}
	return m
}()

// CollectCommands enumerates the command units of a bash program, in source order.
//
// It descends container nodes (`program`, `list`, `pipeline`, `redirected_statement`) and emits
// each `command` node whole. Additionally it descends into the three nested execution contexts:
// command substitution, process substitution, and subshells, emitting each inner command as its own
// unit in addition to the enclosing command, since those inner commands really execute (#306). A
// compound statement is emitted whole and then descended for the statements it contains, while its
// operand words are not commands. An `ERROR` node's recovered structure is invented rather than
// observed, so the unparsed blob is emitted whole and never descended (#742).
//
// A unit emitted from, or beneath, a statement holding a region tree-sitter could not resolve is
// marked ParseUnresolved (#840). Each emitted command unit has any leading `variable_assignment`
// prefix stripped, and a wrapper unit is tagged with a WrapperKind so its decision is later floored.
func CollectCommands(node *bashparse.Node, words *bashparse.WordReader) []BashCommand {
	var out []BashCommand
	collectCommandsInto(node, unitScope{words: words}, &out)
	return out
}

// CollectSalvagedCommands enumerates the command units of a region the primary parse could not
// resolve, re-parsed cleanly on its own (#875). The same walk as CollectCommands, differing only in
// the scope it starts from: every unit is marked ParseUnresolved and Salvaged.
func CollectSalvagedCommands(node *bashparse.Node, words *bashparse.WordReader) []BashCommand {
	var out []BashCommand
	collectCommandsInto(node, unitScope{parseUnresolved: true, salvaged: true, words: words}, &out)
	return out
}

// InlineShellPayloadNode is the node holding a `command` node's inline-shell payload (the inner
// program of `bash -c '...'`, `sh -c "..."`, or `eval '...'`), or nil for any other command. The
// node rather than its text, because the log's command masker re-parses the payload and offsets
// the spans it recovers by the node's start index (#923). The payload set is the shell set, which
// keeps an interpreter (`python3 -c`, `node -e`) out.
func InlineShellPayloadNode(command *bashparse.Node, words *bashparse.WordReader) *bashparse.Node {
	nodes := commandWordNodes(command)
	commandWords := make([]CommandWord, len(nodes))
	for i, node := range nodes {
		commandWords[i] = CommandWord{ArgWord: words.ArgWord(node), Text: node.Text(), Offset: node.StartIndex()}
	}
	index := InlineShellPayloadIndex(commandWords)
	if index == -1 || index >= len(nodes) {
		return nil
	}
	return nodes[index]
}

func collectCommandsInto(node *bashparse.Node, inherited unitScope, out *[]BashCommand) {
	// Anonymous tokens (operators, delimiters) carry no command.
	if !node.IsNamed() {
		return
	}
	typ := node.Type()
	if commandEnumSkip[typ] {
		return
	}
	scope := unresolvedScope(node, inherited)

	switch {
	case typ == "command":
		*out = append(*out, makeCommandUnit(node, scope))
		// A command's text already contains any substitution; descend its subtree to ALSO emit the
		// inner commands of command/process substitutions.
		collectHostedCommands(node, scope, out)
	case typ == "redirected_statement":
		descendCommandChildren(node, redirectedScope(node, scope), out)
	case bashparse.ExecutionHostTypes[typ]:
		// Not a command itself, but its subtree can host one that really runs (`> $(rm x)`,
		// `< <(rm c)`). Emit only what it hosts (#741).
		collectHostedCommands(node, scope, out)
	case typ == "subshell":
		*out = append(*out, makeUnit(node.Text(), scope, unitFacts{})) // never-weaker whole emit
		sub := scope
		sub.context = bashparse.ContextSubshell
		descendCommandChildren(node, sub, out)
	case commandEnumDescend[typ]:
		descendCommandChildren(node, scope, out)
	case compoundStatementTypes[typ]:
		*out = append(*out, makeUnit(node.Text(), scope, unitFacts{})) // never-weaker whole emit
		descendStatementChildren(node, scope, out)
	case statementGroupTypes[typ]:
		descendStatementChildren(node, scope, out)
	case typ == "ERROR":
		// Tree-sitter's error recovery invents structure, so the node types inside an ERROR subtree
		// are not evidence that anything runs. Emit the unparsed blob whole and stop (#742).
		*out = append(*out, makeUnit(node.Text(), scope, unitFacts{}))
	default:
		// Any other named statement: emit whole. A declaration, assignment, test, or `unset` still
		// hosts executions that really run (`local x=$(rm y)`, `[[ $(rm x) ]]`), so those are
		// enumerated in addition to the statement (#742).
		*out = append(*out, makeUnit(node.Text(), scope, unitFacts{}))
		collectHostedCommands(node, scope, out)
	}
}

// unresolvedScope is the scope node's own subtree establishes, marking it unresolved when
// tree-sitter could not parse a region within it (#840). The three pure containers are excluded:
// `program`, `list`, and `pipeline` report an error whenever anything beneath them failed, so
// asking there would make the answer per-program rather than per-statement. Over-marking is the
// fail-closed direction.
func unresolvedScope(node *bashparse.Node, scope unitScope) unitScope {
	if scope.parseUnresolved || commandEnumDescend[node.Type()] {
		return scope
	}
	if bashparse.ParseUnresolvedWithin(node) {
		scope.parseUnresolved = true
	}
	return scope
}

// unitFacts are what a `command` node's words establish about its unit.
type unitFacts struct {
	wrapperKind    WrapperKind
	executedUnit   string
	floorExemption FloorExemption
	spellings      []string
}

func makeUnit(text string, scope unitScope, facts unitFacts) BashCommand {
	return BashCommand{
		Text:            text,
		Context:         scope.context,
		WrapperKind:     facts.wrapperKind,
		ExecutedUnit:    facts.executedUnit,
		FloorExemption:  facts.floorExemption,
		ParseUnresolved: scope.parseUnresolved,
		Salvaged:        scope.salvaged,
		Spellings:       facts.spellings,
	}
}

// makeCommandUnit builds the unit for a `command` node, reading its words once to answer all three
// wrapper questions: whether the unit is floored, what it actually runs, and whether the floor
// still has a reason to hold. The floor question also reads the command's own redirects: one
// written before or between the words (`>/tmp/o xargs grep foo`) writes a file as surely as one on
// the enclosing statement.
func makeCommandUnit(node *bashparse.Node, scope unitScope) BashCommand {
	text, words := readCommandUnit(node, scope.words)
	facts := unitFacts{wrapperKind: ClassifyWrapperWords(words)}
	if spelling, ok := scope.words.SpellHomeAtStart(text); ok {
		facts.spellings = []string{spelling}
	}
	facts.executedUnit, _ = ExecutedUnitOf(text, words)
	if IsTransparentWrapper(words, redirectedScope(node, scope).writesViaRedirect) {
		facts.floorExemption = FloorExemptionCoreReader
	}
	return makeUnit(text, scope, facts)
}

// redirectedScope is the scope a node's own children run under: the enclosing one, plus a write
// unless every `file_redirect` among its children provably only reads. Asked of a
// `redirected_statement` and of a `command`, since a redirect may hang off either. Over-attributing
// is the fail-closed direction: the flag can only withhold an exemption, never grant one.
func redirectedScope(node *bashparse.Node, scope unitScope) unitScope {
	if scope.writesViaRedirect {
		return scope
	}
	for _, child := range node.Children() {
		if child.Type() != "file_redirect" {
			continue
		}
		if bashparse.RedirectMayWriteFile(child) {
			scope.writesViaRedirect = true
			return scope
		}
	}
	return scope
}

// readCommandUnit is a `command` node's unit: the command-pattern text a bash rule is matched
// against, and its words (the `command_name` followed by its arguments), each carrying its UTF-16
// offset into that text.
//
// The text runs from the first word to the last, so it leaves out an env-var prefix
// (`AWS_PROFILE=prod aws ...`, which must not defeat a rule that gates the underlying command) and
// a redirect wherever it sits (`2>/dev/null git push`, `git <<< x push`, #977). The source between
// two consecutive words is kept verbatim; where a redirect sat between two words, one space joins
// them instead. A pure assignment (`FOO=bar`, no `command_name`) runs no command, has no words,
// and keeps its whole text.
func readCommandUnit(node *bashparse.Node, reader *bashparse.WordReader) (string, []CommandWord) {
	nodes := commandWordNodes(node)
	if len(nodes) == 0 {
		return node.Text(), nil
	}
	redirects := hostedRedirects(node)
	words := make([]CommandWord, 0, len(nodes))
	text := ""
	textLen := 0
	var previous *bashparse.Node
	for _, word := range nodes {
		if previous != nil {
			gap := gapBetween(node, previous, word, redirects)
			text += gap
			textLen += utf16Len(gap)
		}
		wordText := word.Text()
		words = append(words, CommandWord{ArgWord: reader.ArgWord(word), Text: wordText, Offset: textLen})
		text += wordText
		textLen += utf16Len(wordText)
		previous = word
	}
	return text, words
}

// gapBetween is the text that joins two consecutive words of a unit: the command's own source
// between them, or one space where a hosted redirect sat there.
func gapBetween(command, before, after *bashparse.Node, redirects []*bashparse.Node) string {
	for _, redirect := range redirects {
		if redirect.StartIndex() >= before.EndIndex() && redirect.StartIndex() < after.StartIndex() {
			return " "
		}
	}
	return slice16(command.Text(), before.EndIndex()-command.StartIndex(), after.StartIndex()-command.StartIndex())
}

// commandWordNodes are the nodes readCommandUnit reports words for, in the same order: every named
// child except a prefix assignment and a hosted redirect. Split out so a consumer that needs a node
// rather than a word (the log's command masker) walks the identical filtered list.
func commandWordNodes(node *bashparse.Node) []*bashparse.Node {
	var nodes []*bashparse.Node
	for _, child := range node.Children() {
		if !child.IsNamed() || child.Type() == "variable_assignment" || bashparse.RedirectNodeTypes[child.Type()] {
			continue
		}
		nodes = append(nodes, child)
	}
	return nodes
}

// hostedRedirects are the redirects a `command` node hosts among its own children.
func hostedRedirects(node *bashparse.Node) []*bashparse.Node {
	var redirects []*bashparse.Node
	for _, child := range node.Children() {
		if bashparse.RedirectNodeTypes[child.Type()] {
			redirects = append(redirects, child)
		}
	}
	return redirects
}

func descendCommandChildren(node *bashparse.Node, scope unitScope, out *[]BashCommand) {
	for _, child := range node.Children() {
		collectCommandsInto(child, scope, out)
	}
}

// descendStatementChildren descends a compound statement's children, enumerating only the ones that
// are themselves statements. A non-statement child is an operand word rather than something that
// runs, but is not abandoned: `for f in $(rm x)` hosts a real execution in its word list. The scope
// is relayed unchanged: a compound statement's body runs in the current shell (#803).
func descendStatementChildren(node *bashparse.Node, scope unitScope, out *[]BashCommand) {
	for _, child := range node.Children() {
		if !child.IsNamed() {
			continue
		}
		if statementTypes[child.Type()] {
			collectCommandsInto(child, scope, out)
		} else {
			collectHostedCommands(child, scope, out)
		}
	}
}

// collectHostedCommands enumerates the commands of every nested execution context in a subtree,
// each tagged with the context it was found in. node may be a context outright or merely host one,
// so the traversal is the root-inclusive ForEachExecutionIn (#741).
func collectHostedCommands(node *bashparse.Node, scope unitScope, out *[]BashCommand) {
	bashparse.ForEachExecutionIn(node, func(contextNode *bashparse.Node, context bashparse.BashCommandContext) {
		// A nested execution starts fresh: an enclosing statement's redirect is that statement's, not
		// the substitution's (#807), and the parse question starts fresh for the same reason (#840).
		descendCommandChildren(contextNode, unitScope{context: context, salvaged: scope.salvaged, words: scope.words}, out)
	})
}
