package pi_permission_system

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashanalysis"
	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Twins of test/handlers/gates/bash-command-metamorphic.test.ts: the totality properties of the bash command gate over the real parse.

var bmStrength = map[string]int{"allow": 0, "ask": 1, "deny": 2}

type bmRule struct{ match, state string }

// bmKeyedResolver decides on a command substring -> state map; a command matching no entry resolves to allow.
func bmKeyedResolver(rules ...bmRule) *gfResolver {
	return resolverFn(func(command string) PermissionCheckResult {
		state := "allow"
		for _, r := range rules {
			if strings.Contains(command, r.match) {
				state = r.state
				break
			}
		}
		return makeCheckResult(func(c *PermissionCheckResult) { c.State, c.Source, c.Command = state, "bash", sp(command) })
	})
}

func bmParse(t *testing.T, command string) *BashProgram {
	t.Helper()
	program, err := ParseBashProgram(command, bpNormalizer(t, "/cwd"), nil)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func bmDecide(t *testing.T, command string, resolver ScopedPermissionResolver) string {
	t.Helper()
	return ResolveBashCommandCheck(command, bmParse(t, command).Commands(), nil, resolver).State
}

// bmIsWordSubsequence is whether unit's whitespace-separated words appear in command, in order.
func bmIsWordSubsequence(unit, command string) bool {
	next := 0
	for _, word := range jsSplitWhitespace(unit) {
		found := strings.Index(command[next:], word)
		if found == -1 {
			return false
		}
		next += found + len(word)
	}
	return true
}

func TestBashCommandMetamorphicTwins(t *testing.T) {
	const f = "handlers/gates/bash-command-metamorphic"
	head := func(bare string) string { return strings.SplitN(bare, " ", 2)[0] }

	tw(t, f, `wrapping "${bare}" in a cd prefix does not weaken its ${state} decision`, func(t *testing.T) {
		for _, c := range []bmRule{{"git push", "ask"}, {"git commit -m wip", "ask"}, {"rm -rf build", "deny"}, {"npm install pkg", "deny"}, {"gh pr create", "ask"}} {
			each(t, c.match+"/"+c.state, func(t *testing.T) {
				r := bmKeyedResolver(bmRule{head(c.match), c.state})
				bare, wrapped := bmDecide(t, c.match, r), bmDecide(t, "cd /repo && "+c.match, r)
				eq(t, bmStrength[wrapped] >= bmStrength[bare], true, "not weaker")
				eq(t, wrapped, c.state, "decision")
			})
		}
	})

	hosts := []struct {
		label string
		wrap  func(string) string
	}{
		{"a stdout redirect", func(c string) string { return "echo hi > $(" + c + ")" }},
		{"an appending redirect", func(c string) string { return "echo hi >> $(" + c + ")" }},
		{"a stderr redirect", func(c string) string { return "echo hi 2> `" + c + "`" }},
		{"an input process substitution", func(c string) string { return "cat < <(" + c + ")" }},
		{"an interpolating heredoc", func(c string) string { return "cat <<EOF\n$(" + c + ")\nEOF" }},
	}
	tw(t, f, `hosting "${bare}" in ${label} does not weaken its ${state} decision`, func(t *testing.T) {
		for _, h := range hosts {
			for _, c := range []bmRule{{"rm -rf build", "deny"}, {"git push", "ask"}} {
				each(t, h.label+"/"+c.match, func(t *testing.T) {
					r := bmKeyedResolver(bmRule{head(c.match), c.state})
					bare, hosted := bmDecide(t, c.match, r), bmDecide(t, h.wrap(c.match), r)
					eq(t, bmStrength[hosted] >= bmStrength[bare], true, "not weaker")
					eq(t, hosted, c.state, "decision")
				})
			}
		}
	})
	tw(t, f, "denies the reported repro when the enclosing command is allowed", func(t *testing.T) {
		eq(t, bmDecide(t, `echo "hello world" > $(rm *.txt)`, bmKeyedResolver(bmRule{"rm", "deny"})), "deny", "decision")
	})
	tw(t, f, "leaves a quoted heredoc body literal, so it does not gate", func(t *testing.T) {
		eq(t, bmDecide(t, "cat <<'EOF'\n$(rm x)\nEOF", bmKeyedResolver(bmRule{"rm", "deny"})), "allow", "decision")
	})

	placements := []struct {
		label string
		place func(h, r string) string
	}{
		{"before the command", func(h, r string) string { return "2>/dev/null " + h + " " + r }},
		{"between a prefix assignment and the command", func(h, r string) string { return "FOO=1 >/dev/null " + h + " " + r }},
		{"as a herestring after the head word", func(h, r string) string { return h + " <<< x " + r }},
		{"after the head word", func(h, r string) string { return h + " 2>/dev/null " + r }},
		{"between the arguments", func(h, r string) string {
			parts := strings.Split(r, " ")
			return h + " " + parts[0] + " 2>&1 " + strings.Join(parts[1:], " ")
		}},
		{"after the head word of a list's last command", func(h, r string) string { return "cd a && " + h + " 2>/dev/null " + r }},
		{"as a heredoc after the head word", func(h, r string) string { return h + " <<EOF " + r + "\nbody\nEOF" }},
		{"as a heredoc after the head word of a list's last command", func(h, r string) string { return "cd a && " + h + " <<EOF " + r + "\nbody\nEOF" }},
		{"as a heredoc piped into the command", func(h, r string) string { return "cat <<EOF | " + h + " " + r + "\nbody\nEOF" }},
		{"as a heredoc joined to the command by &&", func(h, r string) string { return "cat <<EOF && " + h + " " + r + "\nbody\nEOF" }},
		{"as a heredoc writing a file before the command", func(h, r string) string { return "cat <<EOF > /tmp/o && " + h + " " + r + "\nbody\nEOF" }},
	}
	tw(t, f, `a redirect ${label} leaves "${head} ${rest}" at ${state}`, func(t *testing.T) {
		for _, p := range placements {
			for _, c := range []struct{ head, rest, prefix, state string }{
				{"git", "push --force", "git push", "deny"}, {"rm", "-rf build", "rm -rf", "deny"}, {"gh", "pr create", "gh pr", "ask"},
			} {
				each(t, p.label+"/"+c.head, func(t *testing.T) {
					// A `<prefix> *` rule: it matches a unit that starts with the prefix, which a redirect's text or a dropped word defeats.
					r := resolverFn(func(command string) PermissionCheckResult {
						state := "allow"
						if strings.HasPrefix(command, c.prefix) {
							state = c.state
						}
						return makeCheckResult(func(x *PermissionCheckResult) { x.State, x.Source, x.Command = state, "bash", sp(command) })
					})
					eq(t, bmDecide(t, c.head+" "+c.rest, r), c.state, "bare")
					eq(t, bmDecide(t, p.place(c.head, c.rest), r), c.state, "placed")
				})
			}
		}
	})

	wrappers := []func(string) string{
		func(c string) string { return "xargs " + c },
		func(c string) string { return "time " + c },
		func(c string) string { return "sudo timeout 5 xargs " + c },
	}
	tw(t, f, `wrapping "${bare}" in "${wrap("…")}" does not weaken its ${state} decision`, func(t *testing.T) {
		for _, wrap := range wrappers {
			for _, c := range []bmRule{{"grep -l foo", "allow"}, {"grep -l foo", "ask"}, {"grep -l foo", "deny"}, {"cat notes.txt", "deny"}, {"wc -l", "ask"}} {
				each(t, wrap("…")+"/"+c.match+"/"+c.state, func(t *testing.T) {
					r := bmKeyedResolver(bmRule{head(c.match), c.state})
					bare, wrapped := bmDecide(t, c.match, r), bmDecide(t, wrap(c.match), r)
					eq(t, bmStrength[wrapped] >= bmStrength[bare], true, "not weaker")
				})
			}
		}
	})
	tw(t, f, "holds the decision rather than flooring it, for an allowed pure reader", func(t *testing.T) {
		eq(t, bmDecide(t, "xargs grep -l foo", bmKeyedResolver()), "allow", "decision")
	})
	tw(t, f, "still floors a wrapper whose inner command is not a pure reader", func(t *testing.T) {
		eq(t, bmDecide(t, "xargs pnpm test", bmKeyedResolver()), "ask", "decision")
	})
	for _, command := range []string{"xargs grep -l foo > out.txt", "xargs grep -l foo >> out.txt", "xargs grep -l foo > $OUT", "xargs grep -l foo >${OUT}", "xargs grep -l foo > $(mktemp)"} {
		each(t, "floors "+command+" despite the pure-reader inner command", func(t *testing.T) {
			eq(t, bmDecide(t, command, bmKeyedResolver()), "ask", "decision")
		})
	}
	tw(t, f, "keeps the exemption when the redirect only reads", func(t *testing.T) {
		eq(t, bmDecide(t, "xargs grep -l foo < in.txt", bmKeyedResolver()), "allow", "read redirect")
		eq(t, bmDecide(t, "xargs grep -l foo 2>&1", bmKeyedResolver()), "allow", "dup redirect")
	})

	// A parse it could not resolve fails closed (#840).
	unresolved := []struct{ label, command string }{
		{"a heredoc redirect with 2>&1 and a pipe", "git commit -F - <<'MSG' 2>&1 | tail -4\nmsg\nMSG"},
		{"the same shape piping into a mutator", "git add -A . && git commit -F - <<'MSG' 2>&1 | rm -rf /tmp/x\nmsg\nMSG"},
		{"a heredoc followed by `;`", "cat <<EOF ; rm -rf /tmp/x\nb\nEOF"},
		{"a heredoc followed by `&`", "cat <<EOF & rm -rf /tmp/x\nb\nEOF"},
		{"a heredoc followed by words and a redirect", "cat <<EOF arg > /tmp/o\nb\nEOF"},
		{"a descriptor the grammar lexed into the delimiter", "cat 0<<EOF | rm -rf /tmp/x\nb\nEOF"},
		{"a heredoc tail inside a compound statement", "if true; then cat <<EOF ; rm -rf /tmp/x\nb\nEOF\nfi"},
		{"an unbalanced quote", "echo 'unbalanced"},
		{"an unterminated if", "if true; then echo hi"},
		{"an unterminated for", "for f in a b; do echo $f"},
		{"an unterminated brace group", "{ echo hi"},
		{"an unterminated heredoc", "cat <<'EOF'\nsee `rm -rf x` here"},
		{"a read-write open", "cat <> rw.txt"},
		{"an unclosed arithmetic expansion", "cat $(( > out.txt"},
		{"the gap inside a control-flow body", "if true; then cat <<'EOF' 2>&1 | rm -rf /x\nbody\nEOF\nfi"},
		{"the gap inside a subshell", "(cat <<'E' 2>&1 | rm -rf /x\nb\nE\n)"},
		{"the gap inside a command substitution", "x=$(cat <<'E' 2>&1 | rm -rf /x\nb\nE\n)"},
		{"the gap with a duplicating redirect to stderr", "cat <<'E' 1>&2 | rm -rf /x\nb\nE"},
		{"the gap piping stderr too", "cat <<'E' 2>&1 |& rm -rf /x\nb\nE"},
	}
	resolved := []string{
		"cd /repo && git push", "echo hi | tail -2", "cat a > out.txt", "for f in a b; do rm $f; done",
		"cat <<'EOF'\nplain body\nEOF", "git commit -F - <<'MSG' 2>&1\nmsg\nMSG", "git commit -F - <<'MSG' | tail -4\nmsg\nMSG",
	}
	primaryUnits := func(t *testing.T, command string) []BashCommand {
		t.Helper()
		parser, err := bashparse.GetParser()
		if err != nil {
			t.Fatal(err)
		}
		tree := parser.Parse(command)
		if tree == nil {
			t.Fatal("parse returned null")
		}
		return bashanalysis.CollectCommands(tree.Root, bashparse.NewWordReader(bashparse.UnreboundVariables))
	}
	for _, u := range unresolved {
		each(t, "leaves no unmarked-only unit list for "+u.label, func(t *testing.T) {
			units := bmParse(t, u.command).Commands()
			marked := 0
			for _, unit := range units {
				if unit.ParseUnresolved {
					marked++
				}
			}
			eq(t, len(units) == 0 || marked > 0, true, "marked")
		})
	}
	for _, command := range resolved {
		each(t, "marks nothing in "+command, func(t *testing.T) {
			for _, unit := range bmParse(t, command).Commands() {
				if unit.ParseUnresolved {
					t.Errorf("unit %q is marked", unit.Text)
				}
			}
		})
	}
	all := append([]string{}, resolved...)
	for _, u := range unresolved {
		all = append(all, u.command)
	}
	for _, command := range all {
		each(t, "keeps every primary unit, in order, for "+command, func(t *testing.T) {
			units, primary := bmParse(t, command).Commands(), primaryUnits(t, command)
			if len(units) < len(primary) || (len(primary) > 0 && !reflect.DeepEqual(units[:len(primary)], primary)) {
				t.Errorf("units %#v do not start with the primary %#v", units, primary)
			}
		})
		each(t, "emits no unit whose words are not the command's words, in order, for "+command, func(t *testing.T) {
			for _, unit := range bmParse(t, command).Commands() {
				if !bmIsWordSubsequence(unit.Text, command) {
					t.Errorf("unit %q is not a word subsequence of %q", unit.Text, command)
				}
			}
		})
	}
	for _, command := range resolved {
		each(t, "adds no unit at all to "+command, func(t *testing.T) {
			units, primary := bmParse(t, command).Commands(), primaryUnits(t, command)
			if len(units) != len(primary) || (len(units) > 0 && !reflect.DeepEqual(units, primary)) {
				t.Errorf("units %#v, primary %#v", units, primary)
			}
		})
	}
	for _, u := range unresolved {
		each(t, "asks for "+u.label+" under a permissive catch-all", func(t *testing.T) {
			eq(t, bmDecide(t, u.command, bmKeyedResolver()), "ask", "decision")
		})
	}
	for _, command := range resolved {
		each(t, "allows "+command+" under the same catch-all", func(t *testing.T) {
			eq(t, bmDecide(t, command, bmKeyedResolver()), "allow", "decision")
		})
	}
	tw(t, f, "still lets an explicit deny decide a command that did not parse", func(t *testing.T) {
		r := bmKeyedResolver(bmRule{"git add", "deny"})
		eq(t, bmDecide(t, "git add -A . && git commit -F - <<'MSG' 2>&1 | tail -4\nmsg\nMSG", r), "deny", "decision")
	})

	const dropped = "git add -A . && git commit -F - <<'MSG' 2>&1 | rm -rf /tmp/x\nmsg\nMSG"
	tw(t, f, "denies on a rule covering only the dropped command", func(t *testing.T) {
		eq(t, bmDecide(t, dropped, bmKeyedResolver(bmRule{"rm -rf", "deny"})), "deny", "decision")
	})
	tw(t, f, "names the dropped command as the offender", func(t *testing.T) {
		r := bmKeyedResolver(bmRule{"rm -rf", "deny"})
		got := ResolveBashCommandCheck(dropped, bmParse(t, dropped).Commands(), nil, r)
		eq(t, got.Command, sp("rm -rf /tmp/x"), "command")
	})
	tw(t, f, "still only asks when no rule covers the dropped command", func(t *testing.T) {
		eq(t, bmDecide(t, dropped, bmKeyedResolver()), "ask", "decision")
	})
	tw(t, f, "still resolves the whole command when the primary parse found nothing", func(t *testing.T) {
		eq(t, bmDecide(t, "> f <<'M' 2>&1 | rm -rf /tmp/x\nmsg\nM", bmKeyedResolver(bmRule{" rm -rf ", "deny"})), "deny", "decision")
	})
	tw(t, f, "consults no rule for a region whose own re-parse fails", func(t *testing.T) {
		eq(t, bmDecide(t, "cat <> rw.txt", bmKeyedResolver(bmRule{">", "deny"})), "ask", "decision")
	})
}
