package bashparse

import (
	"os"
	"reflect"
	"testing"
)

// Twins of test/access-intent/bash/shell-variable-expansion.test.ts.

const fileShellVars = "access-intent/bash/shell-variable-expansion"

// osHome is node's homedir(): what the tests expect `$HOME` to resolve to.
func osHome(t testing.TB) string {
	t.Helper()
	h, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// simpleExpansion is `$NAME` as tree-sitter-bash builds it: a `$` delimiter plus the name.
func simpleExpansion(name string) *Node {
	return NewFakeNode("simple_expansion", "$"+name, NewFakeNode("$", "$"), NewFakeNode("variable_name", name))
}

// bracedExpansion is `${NAME}` as tree-sitter-bash builds it: brace delimiters plus the name.
func bracedExpansion(name string) *Node {
	return NewFakeNode("expansion", "${"+name+"}", NewFakeNode("${", "${"), NewFakeNode("variable_name", name), NewFakeNode("}", "}"))
}

func findNodeOfType(node *Node, typ string) *Node {
	if node.typ == typ {
		return node
	}
	for _, c := range node.children {
		if f := findNodeOfType(c, typ); f != nil {
			return f
		}
	}
	return nil
}

func resolved(v *ShellVariables, n *Node) (string, bool) { return v.ResolveReference(n) }

func expectResolves(t *testing.T, v *ShellVariables, n *Node, want string) {
	t.Helper()
	if got, ok := v.ResolveReference(n); !ok || got != want {
		t.Errorf("got %q, %v; want %q", got, ok, want)
	}
}

func expectUnresolved(t *testing.T, v *ShellVariables, n *Node) {
	t.Helper()
	if got, ok := v.ResolveReference(n); ok {
		t.Errorf("resolved to %q, want unresolved", got)
	}
}

func TestShellVariablesResolveReference(t *testing.T) {
	v := UnreboundVariables

	tw(t, fileShellVars, "resolves $HOME to the OS home directory", func(t *testing.T) {
		expectResolves(t, v, simpleExpansion("HOME"), osHome(t))
	})
	tw(t, fileShellVars, "resolves ${HOME} to the OS home directory", func(t *testing.T) {
		expectResolves(t, v, bracedExpansion("HOME"), osHome(t))
	})
	tw(t, fileShellVars, "resolves $PWD to the base-relative marker", func(t *testing.T) {
		// The shell's working directory is the projection's effective base, so the base-relative
		// form resolves correctly after any `cd` folding without threading a base into this pure
		// function.
		expectResolves(t, v, simpleExpansion("PWD"), ".")
	})
	tw(t, fileShellVars, "resolves ${PWD} to the base-relative marker", func(t *testing.T) {
		expectResolves(t, v, bracedExpansion("PWD"), ".")
	})

	t.Run("variables outside the resolvable set", func(t *testing.T) {
		for _, name := range []string{"HOMEDIR", "CURRENT", "PATH", "PWDX", "TMPDIR"} {
			t.Run("leaves $"+name+" unresolved", func(t *testing.T) {
				expectUnresolved(t, v, simpleExpansion(name))
				expectUnresolved(t, v, bracedExpansion(name))
			})
		}
	})

	tw(t, fileShellVars, "leaves ${HOME:-/tmp} unresolved", func(t *testing.T) {
		node := NewFakeNode("expansion", "${HOME:-/tmp}",
			NewFakeNode("${", "${"), NewFakeNode("variable_name", "HOME"), NewFakeNode(":-", ":-"),
			NewFakeNode("word", "/tmp"), NewFakeNode("}", "}"))
		expectUnresolved(t, v, node)
	})
	tw(t, fileShellVars, "leaves ${#HOME} unresolved", func(t *testing.T) {
		node := NewFakeNode("expansion", "${#HOME}",
			NewFakeNode("${", "${"), NewFakeNode("#", "#"), NewFakeNode("variable_name", "HOME"), NewFakeNode("}", "}"))
		expectUnresolved(t, v, node)
	})

	tw(t, fileShellVars, "returns null for a node with no children", func(t *testing.T) {
		expectUnresolved(t, v, NewFakeNode("simple_expansion", "$HOME"))
	})
	tw(t, fileShellVars, "returns null for a node with no variable_name child", func(t *testing.T) {
		expectUnresolved(t, v, NewFakeNode("expansion", "${}", NewFakeNode("${", "${"), NewFakeNode("}", "}")))
	})
	tw(t, fileShellVars, "returns null for a variable_assignment naming a resolvable variable", func(t *testing.T) {
		// `HOME=/tmp` binds the name; it is not a reference to its value.
		node := NewFakeNode("variable_assignment", "HOME=/tmp",
			NewFakeNode("variable_name", "HOME"), NewFakeNode("=", "="), NewFakeNode("word", "/tmp"))
		expectUnresolved(t, v, node)
	})

	t.Run("fidelity to the shapes tree-sitter-bash actually produces", func(t *testing.T) {
		for _, c := range []struct {
			command, nodeType string
			want              *string
		}{
			{"ls $HOME", "simple_expansion", ptr(osHome(t))},
			{"ls ${HOME}", "expansion", ptr(osHome(t))},
			{"ls $PWD", "simple_expansion", ptr(".")},
			{"ls ${PWD}", "expansion", ptr(".")},
			{"ls ${HOME:-/tmp}", "expansion", nil},
			{"ls ${#HOME}", "expansion", nil},
			{"ls $HOMEDIR", "simple_expansion", nil},
		} {
			t.Run("resolves "+c.command, func(t *testing.T) {
				node := findNodeOfType(parse(t, c.command), c.nodeType)
				if node == nil {
					t.Fatalf("no %s node", c.nodeType)
				}
				got, ok := v.ResolveReference(node)
				if (c.want == nil) == ok || (ok && got != *c.want) {
					t.Errorf("got %q, %v; want %v", got, ok, c.want)
				}
			})
		}
	})
}

func ptr(s string) *string { return &s }

func TestShellVariablesScan(t *testing.T) {
	// reboundIn is which of HOME and PWD command rebinds, read through a plain reference.
	reboundIn := func(t *testing.T, command string) []string {
		t.Helper()
		variables := ScanShellVariables([]*Node{parse(t, command)})
		out := []string{}
		for _, name := range []string{"HOME", "PWD"} {
			if _, ok := variables.ResolveReference(simpleExpansion(name)); !ok {
				out = append(out, name)
			}
		}
		return out
	}
	expectRebound := func(t *testing.T, command string, want ...string) {
		t.Helper()
		if want == nil {
			want = []string{}
		}
		if got := reboundIn(t, command); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: rebinds %v, want %v", command, got, want)
		}
	}

	t.Run("a variable_name outside a plain reference rebinds it", func(t *testing.T) {
		for _, c := range []struct {
			command string
			want    []string
		}{
			{"HOME=/etc", []string{"HOME"}},
			{"HOME+=/x", []string{"HOME"}},
			{"HOME=/etc cat x", []string{"HOME"}},
			{"export HOME=/etc", []string{"HOME"}},
			{"local HOME", []string{"HOME"}},
			{"readonly PWD=/etc", []string{"PWD"}},
			{"for HOME in /etc; do :; done", []string{"HOME"}},
			{"unset HOME", []string{"HOME"}},
			{"(( HOME = 1 ))", []string{"HOME"}},
			{"echo ${HOME:=/etc}", []string{"HOME"}},
			{"f() { PWD=/; }; HOME=/etc", []string{"HOME", "PWD"}},
		} {
			t.Run(c.command, func(t *testing.T) { expectRebound(t, c.command, c.want...) })
		}
	})
	t.Run("a builtin that binds a name it is given as a word rebinds it", func(t *testing.T) {
		for _, c := range []struct {
			command string
			want    []string
		}{
			{"read HOME", []string{"HOME"}},
			{"printf -v PWD x", []string{"PWD"}},
			{"declare -n r=HOME; r=/etc", []string{"HOME"}},
			{`read "HOME"`, []string{"HOME"}},
			{"read -r HOME", []string{"HOME"}},
			{"mapfile HOME", []string{"HOME"}},
			{"getopts ab HOME", []string{"HOME"}},
			{`export "HOME=/etc"`, []string{"HOME"}},
			{"declare 'HOME=/etc'", []string{"HOME"}},
			{"let HOME=1", []string{"HOME"}},
			{"let 'PWD=1'", []string{"PWD"}},
			{"let HOME++", []string{"HOME"}},
		} {
			t.Run(c.command, func(t *testing.T) { expectRebound(t, c.command, c.want...) })
		}
	})
	t.Run("a command that runs code it cannot see rebinds both", func(t *testing.T) {
		for _, command := range []string{"eval x", "source f", ". f", `"eval" x`, `e\val x`, "trap 'HOME=/etc' DEBUG"} {
			t.Run(command, func(t *testing.T) { expectRebound(t, command, "HOME", "PWD") })
		}
	})
	t.Run("a program that only reads them rebinds nothing", func(t *testing.T) {
		for _, command := range []string{
			`cat "$HOME/x" $PWD`, "echo ${HOME} ${PWD}", `env -i HOME="$HOME" cmd`, "HOMEDIR=/etc MY_PWD=/x cmd",
			"echo HOMEDIR", "echo HOME", "grep HOME ~/.bashrc", "printf HOME", "git log -- PWD", "find . -name x",
			"echo eval source",
		} {
			t.Run(command, func(t *testing.T) { expectRebound(t, command) })
		}
	})

	t.Run("a path token spelled from a rebound HOME", func(t *testing.T) {
		spells := func(t *testing.T, command, token string) bool {
			return ScanShellVariables([]*Node{parse(t, command)}).SpellsReboundHome(token)
		}
		for _, token := range []string{"$HOME", "$HOME/x", "${HOME}", "${HOME}/x", "~", "~/x"} {
			t.Run(token+" is, once HOME is rebound", func(t *testing.T) {
				if !spells(t, "HOME=/etc", token) || spells(t, "PWD=/etc", token) {
					t.Error("wrong answer")
				}
			})
		}
		for _, token := range []string{"$HOMEDIR/x", "~user/x", "/etc/x", "x/$HOME"} {
			t.Run(token+" is not, even once HOME is rebound", func(t *testing.T) {
				if spells(t, "HOME=/etc", token) {
					t.Error("spelled from a rebound HOME")
				}
			})
		}
	})

	tw(t, fileShellVars, "reads every root it is given", func(t *testing.T) {
		first, second := parse(t, "cat x"), parse(t, "HOME=/etc")
		variables := ScanShellVariables([]*Node{first, second})
		expectUnresolved(t, variables, simpleExpansion("HOME"))
	})
}

func TestShellVariablesSpellHomeAtStart(t *testing.T) {
	spellingIn := func(t *testing.T, command, text string) (string, bool) {
		return ScanShellVariables([]*Node{parse(t, command)}).SpellHomeAtStart(text)
	}
	v := UnreboundVariables

	t.Run("a leading home prefix is spelled as the startup home", func(t *testing.T) {
		for _, c := range []struct{ text, rest string }{
			{"~/bin/x", "/bin/x"}, {"~", ""}, {"$HOME/bin/x --y", "/bin/x --y"}, {"${HOME}/bin/x", "/bin/x"}, {"$HOME", ""},
		} {
			t.Run(c.text, func(t *testing.T) {
				if got, ok := v.SpellHomeAtStart(c.text); !ok || got != osHome(t)+c.rest {
					t.Errorf("got %q, %v", got, ok)
				}
			})
		}
		tw(t, fileShellVars, "keeps the rest of the text verbatim rather than normalizing it as a path", func(t *testing.T) {
			if got, ok := v.SpellHomeAtStart("~/evil /x/../../safe"); !ok || got != osHome(t)+"/evil /x/../../safe" {
				t.Errorf("got %q, %v", got, ok)
			}
		})
	})
	t.Run("text bash does not expand from HOME gets no spelling", func(t *testing.T) {
		for _, text := range []string{"~\\bin\\x", "~user/x", "$HOMEDIR/x", "${HOME:-/tmp}/x", "echo ~/x", `"~/bin/x"`, "/abs/bin/x"} {
			t.Run(text, func(t *testing.T) {
				if got, ok := v.SpellHomeAtStart(text); ok {
					t.Errorf("spelled %q", got)
				}
			})
		}
	})
	t.Run("a program that rebinds HOME gets no spelling", func(t *testing.T) {
		for _, command := range []string{"HOME=/tmp; ~/x", "export HOME=/tmp", "HOME=/tmp ~/x"} {
			t.Run(command, func(t *testing.T) {
				for _, text := range []string{"~/x", "$HOME/x"} {
					if got, ok := spellingIn(t, command, text); ok {
						t.Errorf("%s: spelled %q", text, got)
					}
				}
			})
		}
	})
	tw(t, fileShellVars, "spells it when the program rebinds only PWD", func(t *testing.T) {
		if got, ok := spellingIn(t, "PWD=/tmp; ~/x", "~/x"); !ok || got != osHome(t)+"/x" {
			t.Errorf("got %q, %v", got, ok)
		}
	})
}
