package bashparse

import (
	"reflect"
	"testing"
)

// Twins of test/access-intent/bash/nested-execution.test.ts.

const fileNestedExecution = "access-intent/bash/nested-execution"

// visited is a visited execution context, as the assertions read it.
type visited struct {
	Text    string
	Context BashCommandContext
}

// visitContexts parses a bash snippet and collects every nested execution context found.
func visitContexts(t *testing.T, command string) []visited {
	t.Helper()
	found := []visited{}
	ForEachNestedExecution(parse(t, command), func(node *Node, context BashCommandContext) {
		found = append(found, visited{node.Text(), context})
	})
	return found
}

// visitExecutionsIn parses a bash snippet, finds the first node of nodeType, and collects every
// execution ForEachExecutionIn reports for it, the node itself included.
func visitExecutionsIn(t *testing.T, command, nodeType string) []visited {
	t.Helper()
	node := findFirst(parse(t, command), nodeType)
	if node == nil {
		t.Fatalf("no %s node found in: %s", nodeType, command)
	}
	found := []visited{}
	ForEachExecutionIn(node, func(contextNode *Node, context BashCommandContext) {
		found = append(found, visited{contextNode.Text(), context})
	})
	return found
}

// findFirst is a depth-first search for the first node of the given type.
func findFirst(node *Node, typ string) *Node {
	if node.typ == typ {
		return node
	}
	for _, c := range node.children {
		if f := findFirst(c, typ); f != nil {
			return f
		}
	}
	return nil
}

func expectVisited(t *testing.T, got []visited, want ...visited) {
	t.Helper()
	if want == nil {
		want = []visited{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestNestedExecutionContexts(t *testing.T) {
	tw(t, fileNestedExecution, "maps the substitution node types to their execution context", func(t *testing.T) {
		want := map[string]BashCommandContext{
			"command_substitution": "command_substitution",
			"process_substitution": "process_substitution",
		}
		if !reflect.DeepEqual(NestedExecutionContexts, want) {
			t.Errorf("got %v", NestedExecutionContexts)
		}
	})
	tw(t, fileNestedExecution, "omits subshell, which the command enumerator emits whole", func(t *testing.T) {
		if _, ok := NestedExecutionContexts["subshell"]; ok {
			t.Error("subshell is a nested execution context")
		}
	})
}

func TestForEachNestedExecution(t *testing.T) {
	sub := func(text string) visited { return visited{text, ContextCommandSubstitution} }

	tw(t, fileNestedExecution, "finds a substitution in argument position", func(t *testing.T) {
		expectVisited(t, visitContexts(t, "echo $(rm x)"), sub("$(rm x)"))
	})
	tw(t, fileNestedExecution, "finds a backtick substitution", func(t *testing.T) {
		expectVisited(t, visitContexts(t, "echo `rm x`"), sub("`rm x`"))
	})
	tw(t, fileNestedExecution, "finds a process substitution", func(t *testing.T) {
		expectVisited(t, visitContexts(t, "diff <(cat /etc/shadow)"), visited{"<(cat /etc/shadow)", ContextProcessSubstitution})
	})
	tw(t, fileNestedExecution, "finds a substitution hosted in a redirect destination", func(t *testing.T) {
		expectVisited(t, visitContexts(t, "echo hi > $(rm x)"), sub("$(rm x)"))
	})
	tw(t, fileNestedExecution, "finds a substitution hosted in an interpolating heredoc body", func(t *testing.T) {
		expectVisited(t, visitContexts(t, "cat <<EOF\n$(rm e)\nEOF"), sub("$(rm e)"))
	})
	tw(t, fileNestedExecution, "finds nothing in a quoted heredoc body, which does not interpolate", func(t *testing.T) {
		expectVisited(t, visitContexts(t, "cat <<'EOF'\n$(rm e)\nEOF"))
	})
	tw(t, fileNestedExecution, "does not descend past a context it finds", func(t *testing.T) {
		// The outer substitution is visited; the inner one is left to the visitor.
		expectVisited(t, visitContexts(t, "echo $(echo $(rm x))"), sub("$(echo $(rm x))"))
	})
	tw(t, fileNestedExecution, "finds each substitution of a chain in source order", func(t *testing.T) {
		expectVisited(t, visitContexts(t, "echo $(rm a) && echo `rm b`"), sub("$(rm a)"), sub("`rm b`"))
	})
	tw(t, fileNestedExecution, "finds nothing in a command with no nested execution", func(t *testing.T) {
		expectVisited(t, visitContexts(t, "npm install pkg > out.txt"))
	})
}

func TestForEachExecutionIn(t *testing.T) {
	sub := func(text string) visited { return visited{text, ContextCommandSubstitution} }

	tw(t, fileNestedExecution, "visits a context node handed in directly", func(t *testing.T) {
		expectVisited(t, visitExecutionsIn(t, "echo $(rm x)", "command_substitution"), sub("$(rm x)"))
	})
	tw(t, fileNestedExecution, "visits a process substitution handed in directly", func(t *testing.T) {
		expectVisited(t, visitExecutionsIn(t, "diff <(rm x)", "process_substitution"), visited{"<(rm x)", ContextProcessSubstitution})
	})
	tw(t, fileNestedExecution, "searches within a node that merely contains a context", func(t *testing.T) {
		expectVisited(t, visitExecutionsIn(t, "echo $(rm x)", "command"), sub("$(rm x)"))
	})
	tw(t, fileNestedExecution, "does not descend past a context it was handed", func(t *testing.T) {
		expectVisited(t, visitExecutionsIn(t, "echo $(echo $(rm x))", "command_substitution"), sub("$(echo $(rm x))"))
	})
	tw(t, fileNestedExecution, "does not descend past a context it finds by searching", func(t *testing.T) {
		expectVisited(t, visitExecutionsIn(t, "echo $(echo $(rm x))", "command"), sub("$(echo $(rm x))"))
	})
	tw(t, fileNestedExecution, "finds nothing in a node hosting no execution", func(t *testing.T) {
		expectVisited(t, visitExecutionsIn(t, "npm install pkg", "command"))
	})
}
