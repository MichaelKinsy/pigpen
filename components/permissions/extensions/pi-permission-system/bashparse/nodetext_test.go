package bashparse

import "testing"

// Twins of test/access-intent/bash/node-text.test.ts.

const fileNodeText = "access-intent/bash/node-text"

// words is a reader for a program that rebinds neither HOME nor PWD.
var words = NewWordReader(UnreboundVariables)

func fake(typ, text string, children ...*Node) *Node { return NewFakeNode(typ, text, children...) }

func TestSkipSubtreeTypes(t *testing.T) {
	tw(t, fileNodeText, "contains the three node types that must not be descended", func(t *testing.T) {
		for _, typ := range []string{"heredoc_body", "heredoc_end", "comment"} {
			if !SkipSubtreeTypes[typ] {
				t.Errorf("%s missing", typ)
			}
		}
	})
	tw(t, fileNodeText, "does not contain common argument node types", func(t *testing.T) {
		for _, typ := range []string{"word", "string", "raw_string"} {
			if SkipSubtreeTypes[typ] {
				t.Errorf("%s present", typ)
			}
		}
	})
}

func expectText(t *testing.T, node *Node, want string) {
	t.Helper()
	if got := words.Text(node); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWordReaderText(t *testing.T) {
	tw(t, fileNodeText, "returns the node text unchanged", func(t *testing.T) {
		expectText(t, fake("word", "hello"), "hello")
	})
	tw(t, fileNodeText, "strips surrounding single quotes", func(t *testing.T) {
		expectText(t, fake("raw_string", "'content'"), "content")
	})
	tw(t, fileNodeText, "strips single quotes around a path", func(t *testing.T) {
		expectText(t, fake("raw_string", "'/etc/hosts'"), "/etc/hosts")
	})
	tw(t, fileNodeText, "returns text as-is when not fully single-quoted", func(t *testing.T) {
		// A raw_string node without enclosing quotes (defensive fallback)
		expectText(t, fake("raw_string", "noquotes"), "noquotes")
	})

	tw(t, fileNodeText, "concatenates inner word children, skipping quote delimiters", func(t *testing.T) {
		node := fake("string", `"hello world"`, fake(`"`, `"`), fake("string_content", "hello world"), fake(`"`, `"`))
		expectText(t, node, "hello world")
	})
	tw(t, fileNodeText, "concatenates multiple inner children", func(t *testing.T) {
		node := fake("string", `"foo$BAR"`, fake(`"`, `"`), fake("string_content", "foo"), fake("simple_expansion", "$BAR"), fake(`"`, `"`))
		expectText(t, node, "foo$BAR")
	})
	tw(t, fileNodeText, "returns empty string for an empty double-quoted string", func(t *testing.T) {
		expectText(t, fake("string", `""`, fake(`"`, `"`), fake(`"`, `"`)), "")
	})

	tw(t, fileNodeText, "returns text as-is for string_content", func(t *testing.T) {
		expectText(t, fake("string_content", "plain text"), "plain text")
	})
	tw(t, fileNodeText, "resolves a plain $HOME reference to the home directory", func(t *testing.T) {
		// The children matter: the resolver discriminates a plain reference from an
		// operator-bearing expansion structurally, not by text prefix (#694).
		node := fake("simple_expansion", "$HOME", fake("$", "$"), fake("variable_name", "HOME"))
		expectText(t, node, osHome(t))
	})
	tw(t, fileNodeText, "resolves a plain ${HOME} reference to the home directory", func(t *testing.T) {
		node := fake("expansion", "${HOME}", fake("${", "${"), fake("variable_name", "HOME"), fake("}", "}"))
		expectText(t, node, osHome(t))
	})
	tw(t, fileNodeText, "returns text as-is for a variable outside the resolvable set", func(t *testing.T) {
		node := fake("expansion", "${VAR}", fake("${", "${"), fake("variable_name", "VAR"), fake("}", "}"))
		expectText(t, node, "${VAR}")
	})
	tw(t, fileNodeText, "returns text as-is for an expansion carrying an operator", func(t *testing.T) {
		node := fake("expansion", "${HOME:-/tmp}", fake("${", "${"), fake("variable_name", "HOME"), fake(":-", ":-"), fake("word", "/tmp"), fake("}", "}"))
		expectText(t, node, "${HOME:-/tmp}")
	})

	tw(t, fileNodeText, "concatenates resolved children", func(t *testing.T) {
		expansion := fake("simple_expansion", "$FILE", fake("$", "$"), fake("variable_name", "FILE"))
		expectText(t, fake("concatenation", "/etc/$FILE", fake("word", "/etc/"), expansion), "/etc/$FILE")
	})
	tw(t, fileNodeText, "concatenates a resolved $HOME reference with its suffix", func(t *testing.T) {
		expansion := fake("simple_expansion", "$HOME", fake("$", "$"), fake("variable_name", "HOME"))
		expectText(t, fake("concatenation", "$HOME/sub", expansion, fake("word", "/sub")), osHome(t)+"/sub")
	})
	tw(t, fileNodeText, "handles nested concatenation-of-string", func(t *testing.T) {
		// A concatenation whose child is a double-quoted string
		inner := fake("string", `"bar"`, fake(`"`, `"`), fake("string_content", "bar"), fake(`"`, `"`))
		expectText(t, fake("concatenation", `foo"bar"`, fake("word", "foo"), inner), "foobar")
	})
	tw(t, fileNodeText, "returns the raw text for unknown node types", func(t *testing.T) {
		expectText(t, fake("unknown_type", "rawtext"), "rawtext")
	})
}

// argumentNode parses `echo <argument>` and returns the command's first argument node.
func argumentNode(t *testing.T, argument string) *Node {
	t.Helper()
	node := parse(t, "echo "+argument).Child(0).Child(1)
	if node == nil {
		t.Fatalf("no argument node in: echo %s", argument)
	}
	return node
}

func TestWordReaderIsComputed(t *testing.T) {
	for _, c := range []struct {
		label, argument string
		want            bool
	}{
		{"a bare word", "out.txt", false},
		{"a single-quoted dollar sign", "'$x'", false},
		{"a plain $HOME reference the resolver expands", `"$HOME/out"`, false},
		{"an unquoted variable", "$OUT", true},
		{"a quoted variable", `"$OUT"`, true},
		{"a variable concatenated with a literal", `"${DIR}/x"`, true},
		{"a command substitution inside a word", "out-$(date).txt", true},
		{"a process substitution", "<(cmd)", true},
		{"an arithmetic expansion", "out-$((1+1)).txt", true},
	} {
		t.Run("answers "+map[bool]string{true: "true", false: "false"}[c.want]+" for "+c.label+" ("+c.argument+")", func(t *testing.T) {
			if got := words.IsComputed(argumentNode(t, c.argument)); got != c.want {
				t.Errorf("got %v", got)
			}
		})
	}
}

func TestWordReaderArgWord(t *testing.T) {
	argWordOf := func(t *testing.T, argument string) ArgWord { return words.ArgWord(argumentNode(t, argument)) }

	t.Run("a value the source spells exactly", func(t *testing.T) {
		for _, c := range []struct {
			label, argument, value string
			mayLeadWithDash        bool
		}{
			{"a bare word", "-n", "-n", true},
			{"a single-quoted word", "'-i'", "-i", true},
			{"a double-quoted word", `"1,80p"`, "1,80p", false},
			{"a single-quoted backslash", `'s/\./x/'`, `s/\./x/`, false},
			{"a tilde path", "~/notes.md", "~/notes.md", false},
			{"a concatenation of quoted parts", `-'i'""`, "-i", true},
			{"an empty brace pair, which bash leaves alone", "{}", "{}", false},
			{"a number", "2", "2", false},
			{"a negative number", "-20", "-20", true},
		} {
			t.Run("reads "+c.label+" as exact ("+c.argument+")", func(t *testing.T) {
				want := ArgWord{Value: c.value, Computed: false, MayLeadWithDash: c.mayLeadWithDash}
				if got := argWordOf(t, c.argument); got != want {
					t.Errorf("got %+v, want %+v", got, want)
				}
			})
		}
	})

	t.Run("a value only the shell decides", func(t *testing.T) {
		// The shell removes an escape, expands a glob, and decodes an ANSI-C string before the
		// program sees the word, so the source spelling is not the value a capability proof must read.
		for _, c := range []struct{ label, argument string }{
			{"an unquoted escape", `-\i`},
			{"an escape inside double quotes", `"-\i"`},
			{"an unquoted glob", "-*"},
			{"a bracket glob", "-[i]"},
			{"a comma brace expansion", "{-i,-n}"},
			{"a brace expansion glued to an option", "-n{,i}"},
			{"an ANSI-C string", "$'-i'"},
			{"a variable", "$OPT"},
			{"a command substitution", "$(echo -i)"},
			{"a based number with an expansion", "10#$x"},
		} {
			t.Run("marks "+c.label+" computed ("+c.argument+")", func(t *testing.T) {
				if !argWordOf(t, c.argument).Computed {
					t.Error("not computed")
				}
			})
		}
	})

	t.Run("a leading tilde, read in a program that may reassign HOME", func(t *testing.T) {
		// argWordAfter parses `<program>; echo <argument>` and reads that last argument.
		argWordAfter := func(t *testing.T, program, argument string) ArgWord {
			root := parse(t, program+"; echo "+argument)
			node := root.Child(len(root.children) - 1).Child(1)
			if node == nil {
				t.Fatalf("no argument node in: echo %s", argument)
			}
			return NewWordReader(ScanShellVariables([]*Node{root})).ArgWord(node)
		}

		for _, c := range []struct{ label, argument string }{
			{"a bare tilde", "~"}, {"a tilde path", "~/x"}, {"a tilde path concatenated with a quoted part", `~/x"y"`},
		} {
			t.Run("marks "+c.label+" computed and maybe an option once HOME is reassigned ("+c.argument+")", func(t *testing.T) {
				got := argWordAfter(t, "HOME=-x", c.argument)
				if !got.Computed || !got.MayLeadWithDash {
					t.Errorf("got %+v", got)
				}
			})
		}
		for _, c := range []struct{ label, argument string }{
			{"a tilde after a literal, which bash leaves alone", "a~/x"},
			{"a quoted tilde", `"~/x"`},
			{"a named user's tilde, which reads no HOME", "~root/x"},
		} {
			t.Run("leaves "+c.label+" exact once HOME is reassigned ("+c.argument+")", func(t *testing.T) {
				got := argWordAfter(t, "HOME=-x", c.argument)
				if got.Computed || got.MayLeadWithDash {
					t.Errorf("got %+v", got)
				}
			})
		}

		t.Run("with an inherited HOME that begins with a dash", func(t *testing.T) {
			for _, c := range []struct{ label, argument string }{
				{"a bare tilde", "~"}, {"a tilde path", "~/x"}, {"a glob under a tilde path", "~/*.ts"},
			} {
				t.Run("reads "+c.label+" as computed and maybe an option ("+c.argument+")", func(t *testing.T) {
					t.Setenv("HOME", "-h")
					got := argWordAfter(t, "PWD=/x", c.argument)
					if !got.Computed || !got.MayLeadWithDash {
						t.Errorf("got %+v", got)
					}
				})
			}
		})

		tw(t, fileNodeText, "leaves a tilde path exact when only PWD is reassigned", func(t *testing.T) {
			want := ArgWord{Value: "~/x", Computed: false, MayLeadWithDash: false}
			if got := argWordAfter(t, "PWD=-x", "~/x"); got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	})

	t.Run("whether a computed word may lead with a dash", func(t *testing.T) {
		// shapeOf is the two facts a guard reads, without the unresolved source spelling.
		shapeOf := func(t *testing.T, argument string) [2]bool {
			got := argWordOf(t, argument)
			return [2]bool{got.Computed, got.MayLeadWithDash}
		}

		t.Run("a word that may reach the program beginning with `-`", func(t *testing.T) {
			for _, c := range []struct{ label, argument string }{
				{"a bare variable", "$A"},
				{"a quoted variable", `"$O"`},
				{"a command substitution", "$(echo -o)"},
				{"a backtick substitution", "`echo -o`"},
				{"a quoted variable leading a path", `"$pkg/src"`},
				{"an unquoted variable after a literal, which splits", "x$y"},
				{"an unquoted variable after an empty string", `""$x`},
				{"an unquoted variable after a single-quoted literal", "'x'$y"},
				// A quoted `$@` still expands to one word per element, and only the first carries the
				// literal prefix.
				{"quoted positional parameters after a literal", `"x$@"`},
				{"a quoted array expansion after a literal", `"x${arr[@]}"`},
				{"a quoted positional slice after a literal", `"x${@:2}"`},
				// An indirect expansion names its target at run time, and `a='arr[@]'` expands per
				// element with no `@` in the source.
				{"a quoted indirect expansion after a literal", `"x${!a}"`},
				{"an indirect expansion nested in a default", `"x${y:-${!b}}"`},
				// Any variable may be a nameref (`declare -n s='arr[@]'`), which expands per element
				// with nothing in its own spelling to show it.
				{"a quoted variable after a literal", `"x$y"`},
				{"a quoted braced variable after a literal", `"x${y}"`},
				// `$*` and `${arr[*]}` join into one word inside quotes; answering true for them is
				// the conservative over-count of the rule above.
				{"quoted joined positional parameters after a literal", `"x$*"`},
				{"a quoted joined array after a literal", `"x${arr[*]}"`},
				{"a leading glob", "*"},
				{"a dash before a glob", "-*"},
				{"an escaped dash", `\-delete`},
				{"a leading brace expansion", "{-delete,}"},
				{"an ANSI-C string", "$'-o'"},
				{"an option glued to an ANSI-C string", `-t$'\t'`},
			} {
				t.Run("answers true for "+c.label+" ("+c.argument+")", func(t *testing.T) {
					if got := shapeOf(t, c.argument); got != [2]bool{true, true} {
						t.Errorf("got %v", got)
					}
				})
			}
		})

		t.Run("a word whose leading literal survives every rewrite", func(t *testing.T) {
			for _, c := range []struct{ label, argument string }{
				{"a quoted command substitution after a literal", `"x$(echo -o)"`},
				{"a quoted arithmetic expansion after a literal", `"x$((1))"`},
				{"a glob after a literal", "packages/*/docs"},
				{"an escaped parenthesis", `\(`},
				{"a brace expansion after a literal", "x{a,-b}"},
				{"a glob under a tilde path", "~/*.ts"},
				{"a process substitution", "<(cmd)"},
				{"a process substitution whose body splits", "<(cmd $a)"},
			} {
				t.Run("answers false for "+c.label+" ("+c.argument+")", func(t *testing.T) {
					if got := shapeOf(t, c.argument); got != [2]bool{true, false} {
						t.Errorf("got %v", got)
					}
				})
			}
		})
	})
}
