package bashanalysis

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

const ce = "access-intent/bash/command-enumeration"

type payload struct {
	Text       string
	StartIndex int
}

// payloadOf parses a bash snippet, finds its first `command` node, and asks what inline-shell
// payload it carries.
func payloadOf(t *testing.T, command string) *payload {
	t.Helper()
	node := findFirst(parseRoot(t, command), "command")
	if node == nil {
		t.Fatalf("no command node in %s", command)
	}
	p := InlineShellPayloadNode(node, bashparse.NewWordReader(bashparse.UnreboundVariables))
	if p == nil {
		return nil
	}
	return &payload{p.Text(), p.StartIndex()}
}

func TestInlineShellPayloadNode(t *testing.T) {
	for _, c := range []struct {
		command, text string
		start         int
	}{
		{`bash -c 'rm -rf /'`, `'rm -rf /'`, 8}, {`sh -c "ls -la"`, `"ls -la"`, 6}, {`eval "rm x"`, `"rm x"`, 5},
		{`bash -ec 'make build'`, `'make build'`, 9}, {`/bin/bash -c 'ls'`, `'ls'`, 13}, {`bash -c TOKEN=abc`, `TOKEN=abc`, 8},
	} {
		t.Run("names the payload node of "+c.command, func(t *testing.T) { eq(t, payloadOf(t, c.command), &payload{c.text, c.start}) })
	}
	tw(t, ce, "skips a leading assignment prefix, so the index is not shifted by it", func(t *testing.T) {
		eq(t, payloadOf(t, `TOKEN=outer bash -c 'API_KEY=inner x'`), &payload{`'API_KEY=inner x'`, 20})
	})
	for _, c := range []string{"ls -la", "sudo ls", "python3 -c 'print(1)'", "node -e 'x'", "bash script.sh", "bash --help", "bash -c", "eval"} {
		t.Run("answers null for "+c, func(t *testing.T) { eq(t, payloadOf(t, c), (*payload)(nil)) })
	}
}
