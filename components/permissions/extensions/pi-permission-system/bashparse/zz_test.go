package bashparse

import (
	"strings"
	"testing"
)

func TestZZ(t *testing.T) {
	g, _ := dumpBoth("-rf\nread && <<\\EOF\t<<- 😀;local | |\t${!a} <(ls)let | $# | $HOME&case")
	lines := strings.Split(g, "\n")
	t.Log("\n" + strings.Join(lines[10:22], "\n"))
}
