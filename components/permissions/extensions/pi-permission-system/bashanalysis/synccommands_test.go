package bashanalysis

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

const sc = "access-intent/bash/sync-commands"

func syncCommands(t *testing.T, command string) []BashCommand {
	t.Helper()
	got, ok := ParseBashCommandsSync(command)
	if !ok {
		t.Fatalf("parser not warm for %q", command)
	}
	if got == nil {
		got = []BashCommand{}
	}
	return got
}

func homedir(t *testing.T) string {
	h, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestParseBashCommandsSync(t *testing.T) {
	bashparse.ResetWarmBashParser()
	t.Cleanup(bashparse.ResetWarmBashParser)

	tw(t, sc, "returns null when the parser is not warm", func(t *testing.T) {
		bashparse.ResetWarmBashParser()
		_, ok := ParseBashCommandsSync("echo hi")
		eq(t, ok, false)
	})

	bashparse.WarmBashParser()
	cmds := func(c ...BashCommand) []BashCommand { return append([]BashCommand{}, c...) }

	tw(t, sc, "withholds a wrapped reader's exemption once its argument's HOME is reassigned", func(t *testing.T) {
		eq(t, syncCommands(t, `xargs find "$HOME"`), cmds(BashCommand{
			Text: `xargs find "$HOME"`, WrapperKind: WrapperIndirection, ExecutedUnit: `find "$HOME"`, FloorExemption: FloorExemptionCoreReader,
		}))
		eq(t, syncCommands(t, `HOME=-delete; xargs find "$HOME"`), cmds(
			BashCommand{Text: "HOME=-delete"},
			BashCommand{Text: `xargs find "$HOME"`, WrapperKind: WrapperIndirection, ExecutedUnit: `find "$HOME"`},
		))
	})
	tw(t, sc, "returns a single unit for a lone command", func(t *testing.T) {
		eq(t, syncCommands(t, "echo hi"), cmds(BashCommand{Text: "echo hi"}))
	})
	tw(t, sc, "decomposes a chained command into its units", func(t *testing.T) {
		eq(t, syncCommands(t, "cd /repo && npm install x"), cmds(BashCommand{Text: "cd /repo"}, BashCommand{Text: "npm install x"}))
	})
	tw(t, sc, "descends into a command substitution, tagging its context", func(t *testing.T) {
		eq(t, syncCommands(t, "echo $(rm -rf /)"), cmds(
			BashCommand{Text: "echo $(rm -rf /)"},
			BashCommand{Text: "rm -rf /", Context: bashparse.ContextCommandSubstitution},
		))
	})
	tw(t, sc, "flags an opaque wrapper", func(t *testing.T) {
		eq(t, syncCommands(t, `bash -c "rm -rf /"`), cmds(BashCommand{Text: `bash -c "rm -rf /"`, WrapperKind: WrapperOpaquePayload, ExecutedUnit: "rm -rf /"}))
	})
	tw(t, sc, "returns an empty array for a comment-only command", func(t *testing.T) { eq(t, syncCommands(t, "# just a comment"), cmds()) })
	tw(t, sc, "returns an empty array for an empty command", func(t *testing.T) { eq(t, syncCommands(t, ""), cmds()) })
	tw(t, sc, "enumerates a command a partial parse dropped (#875)", func(t *testing.T) {
		u := func(text string, salvaged bool) BashCommand {
			return BashCommand{Text: text, ParseUnresolved: true, Salvaged: salvaged}
		}
		eq(t, syncCommands(t, "git add -A . && git commit -F - <<'MSG' 2>&1 | rm -rf /tmp/x\nmsg\nMSG"), cmds(
			u("git add -A .", false), u("git commit -F", false), u("rm -rf /tmp/x", true),
			u("git add -A .", true), u("git commit -F", true), u("rm -rf /tmp/x", true),
		))
	})
	tw(t, sc, "enumerates a command after a heredoc the grammar cannot parse", func(t *testing.T) {
		eq(t, syncCommands(t, "cat <<EOF ; rm -rf /tmp/x\nb\nEOF"), cmds(
			BashCommand{Text: "cat", ParseUnresolved: true},
			BashCommand{Text: "cat", ParseUnresolved: true, Salvaged: true},
			BashCommand{Text: "rm -rf /tmp/x", ParseUnresolved: true, Salvaged: true},
		))
	})

	tw(t, sc, "spells a command named through ~", func(t *testing.T) {
		eq(t, syncCommands(t, "~/bin/x --y"), cmds(BashCommand{Text: "~/bin/x --y", Spellings: []string{homedir(t) + "/bin/x --y"}}))
	})
	tw(t, sc, "spells a command named through $HOME", func(t *testing.T) {
		eq(t, syncCommands(t, "$HOME/bin/x"), cmds(BashCommand{Text: "$HOME/bin/x", Spellings: []string{homedir(t) + "/bin/x"}}))
	})
	tw(t, sc, "withholds the spelling once the program rebinds HOME", func(t *testing.T) {
		eq(t, syncCommands(t, "HOME=/tmp/evil; ~/bin/x"), cmds(BashCommand{Text: "HOME=/tmp/evil"}, BashCommand{Text: "~/bin/x"}))
	})
	tw(t, sc, "withholds the spelling under a prefix assignment of HOME", func(t *testing.T) {
		eq(t, syncCommands(t, "HOME=/tmp/evil ~/bin/x"), cmds(BashCommand{Text: "~/bin/x"}))
	})
	for _, command := range []string{"echo ~/x", `"~/bin/x"`} {
		t.Run("does not spell a unit whose text does not open with the prefix: "+command, func(t *testing.T) {
			eq(t, syncCommands(t, command), cmds(BashCommand{Text: command}))
		})
	}
	tw(t, sc, "does not spell a wrapper whose wrapped command opens with the prefix", func(t *testing.T) {
		units := syncCommands(t, "sudo ~/bin/x")
		eq(t, len(units), 1)
		eq(t, units[0].Spellings, []string(nil))
	})
	tw(t, sc, "spells a nested command on its own, not its enclosing one", func(t *testing.T) {
		eq(t, syncCommands(t, "echo $(~/bin/x)"), cmds(
			BashCommand{Text: "echo $(~/bin/x)"},
			BashCommand{Text: "~/bin/x", Context: bashparse.ContextCommandSubstitution, Spellings: []string{homedir(t) + "/bin/x"}},
		))
	})
}
