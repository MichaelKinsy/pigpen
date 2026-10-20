package bashparse

// What a redirect node in the parse tree proves (redirect-analysis.ts).
//
// command-effects.ts owns the operator table (which spelling means read, which means write) and this
// module owns reading a file_redirect node well enough to consult it: finding the operator among the
// node's children, telling a destination that names a file from one that names a descriptor, and
// naming which destination is the redirect's own target.
//
// The split exists because two callers need different answers from the same read, and they need
// them under different burdens of proof. The token collector asks what effect to attribute to a
// destination it is about to emit, so it answers with a proof. The command enumerator asks whether
// it is safe to remove the wrapper floor, so it answers with a refusal: anything it cannot resolve
// counts against the exemption (#803). One reader of the node keeps the two from drifting on what a
// redirect is.
//
// The two burdens meet at one fact: whether the parse resolved at all. Both answers ask
// [ParseUnresolvedAt], so a syntax form the grammar could not handle cannot be a proof to one caller
// and a resolvable read to the other (#814).

// RedirectNodeTypes are the redirect node types a command or a statement can host. A redirect is not
// a word of the command it sits in, wherever it sits: bash accepts one before, between, or after the
// words (`2>/dev/null git push`), and none of them changes which command runs (#977).
var RedirectNodeTypes = setOf("file_redirect", "herestring_redirect", "heredoc_redirect")

// RedirectEffectForDestination is the effect redirect proves for destination, or ok=false when the
// redirect names no file and no token should be collected.
//
// `>&` and `<&` are the two operators that may name either a file descriptor (`2>&1`) or a real
// file (`cmd >& out`); the destination node's type is the parse-tree fact that tells them apart.
//
// A redirect the parse could not resolve proves nothing: ADR 0013 section 10's base case, which
// consults both directional surfaces. Reading a proof off whichever operator survived error recovery
// made `cat <> rw.txt` a read and `cat <> ~/rw.txt` a write, so one command's answer was a function
// of its filename, and the read half was a fail-open on a destination the shell may truncate (#814).
// The demotion applies to a proof, never to the ok=false: a descriptor duplication names no file
// whatever the operator around it did, so demoting first would emit a descriptor number as a path
// candidate.
func RedirectEffectForDestination(redirect, destination *Node) (TokenEffect, bool) {
	proven, ok := RedirectDestinationEffect(redirectOperatorOf(redirect), descriptorNodeTypes[destination.typ])
	if !ok {
		return TokenEffect{}, false
	}
	if ParseUnresolvedAt(redirect) {
		return UnprovenEffect, true
	}
	return proven, true
}

// RedirectMayWriteFile is true unless redirect provably only reads: the fail-closed question the
// floor exemption asks.
//
// Deliberately not the negation of a write proof. A destination this module cannot resolve counts
// as a write here, because the caller is deciding whether to remove a guard: `> $OUT`, `>${OUT}`,
// and `> $(mktemp)` name a file chosen at run time, and the parse can say nothing about which. The
// path projection does not collect them either (#609).
//
// An unresolved parse is refused up front rather than left to the loop. The loop would usually
// reach the same answer, but `cat <>&1` parses to a redirect whose only children are the operator
// and a descriptor, so the loop finds nothing to refuse on and clears the exemption for a form
// nobody understood (#814). Past that, only two things clear it: a descriptor duplication (`2>&1`),
// which names no file, and an operator that proves a read: reading a file alongside a pure reader
// leaves it a pure reader.
func RedirectMayWriteFile(redirect *Node) bool {
	if ParseUnresolvedAt(redirect) {
		return true
	}
	for _, child := range redirect.children {
		// The operator itself is the redirect's only unnamed child.
		if !child.named {
			continue
		}
		// A source or duplicated descriptor (`2`, `&1`) names no file.
		if descriptorNodeTypes[child.typ] {
			continue
		}
		if effect, ok := RedirectEffectForDestination(redirect, child); !ok || effect.Effect != EffectRead {
			return true
		}
	}
	return false
}

// LiteralNodeTypes are the node types tree-sitter-bash 0.25.1 parses a literal word as: its
// `_literal` rule, which a file redirect's destinations and a heredoc's trailing words are both
// declared as (LITERAL_NODE_TYPES).
var LiteralNodeTypes = setOf(
	"concatenation", "word", "string", "raw_string", "translated_string", "ansi_c_string", "number",
	"expansion", "simple_expansion", "command_substitution", "process_substitution",
	"arithmetic_expansion", "brace_expression",
)

// closeOperators close a descriptor, naming no file (`>&-`, `<&-`).
var closeOperators = setOf(">&-", "<&-")

// RedirectTargetIndex is the child index of the node redirect reads or writes (its first named child
// after the operator), or ok=false when it names none: nothing follows the operator, or the operator
// closes a descriptor (`>&-`, `<&-`).
//
// The operator is the redirect's only unnamed child, and a source descriptor (`2` in `2>`) precedes
// it, so the first named child after it is the target without asking its type. Only the first:
// tree-sitter-bash 0.25.1 declares the destination repeat1, so the words after it in
// `grep pat 2>/dev/null f.txt` parse as further destinations, while bash passes them to the
// redirected command as arguments ([TrailingArgumentIndex] names where they begin, #977). A close
// operator takes an optional destination in the grammar, but it closes a descriptor and names no
// file, so a word after it is the command's too. An index rather than a node, because a caller
// iterating the children compares positions rather than wrapper identity.
func RedirectTargetIndex(redirect *Node) (int, bool) {
	operator, ok := RedirectOperatorIndex(redirect)
	if !ok {
		return 0, false
	}
	if closeOperators[redirect.children[operator].typ] {
		return 0, false
	}
	return namedChildIndexAfter(redirect, operator)
}

// TrailingArgumentIndex is the child index of the first word the grammar appended after redirect's
// own target, or ok=false when none follows.
//
// Every LiteralNodeTypes child from there on is a word bash passes to the redirected command rather
// than a destination of the redirect: `f.txt` in `grep pat 2>/dev/null f.txt`, and `arg` in
// `cmd >&- arg`, where the close operator has no target at all (#977). A heredoc carries them the
// same way: `git <<EOF push --force` hangs `push` and `--force` after its delimiter, where its own
// target would be. Its body follows them and is not a word, and neither is any other tail the
// grammar lets a heredoc carry (a redirect, a `| ...` or `&& ...` statement), so the first child
// after the target counts only when it is a literal (#979).
func TrailingArgumentIndex(redirect *Node) (int, bool) {
	operator, ok := RedirectOperatorIndex(redirect)
	if !ok {
		return 0, false
	}
	after := operator
	if target, ok := RedirectTargetIndex(redirect); ok {
		after = target
	}
	index, ok := namedChildIndexAfter(redirect, after)
	if !ok {
		return 0, false
	}
	if LiteralNodeTypes[redirect.children[index].typ] {
		return index, true
	}
	return 0, false
}

// descriptorNodeTypes are the destination node types that name a file descriptor rather than a
// file, so `>&` / `<&` duplicate a stream instead of touching the filesystem.
var descriptorNodeTypes = setOf("file_descriptor", "number")

// redirectOperatorOf is the redirect operator of a redirect node: tree-sitter-bash emits it as an
// unnamed child whose type is the operator text itself, and a redirect's only unnamed child is that
// operator, so the syntax proof is a lookup on the first one found.
func redirectOperatorOf(node *Node) string {
	if operator, ok := RedirectOperatorIndex(node); ok {
		return node.children[operator].typ
	}
	return ""
}

// RedirectOperatorIndex is the index of redirect's operator, its only unnamed child.
func RedirectOperatorIndex(redirect *Node) (int, bool) {
	for i, c := range redirect.children {
		if !c.named {
			return i, true
		}
	}
	return 0, false
}

// namedChildIndexAfter is the index of the first named child of node after index after.
func namedChildIndexAfter(node *Node, after int) (int, bool) {
	for i := after + 1; i < len(node.children); i++ {
		if node.children[i].named {
			return i, true
		}
	}
	return 0, false
}
