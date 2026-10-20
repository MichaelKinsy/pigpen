package bashparse

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// dumpTree is the Go side of dump.test.ts in port/bashparse-diff: one line per node in pre-order,
// depth|type|flags|start|end|childCount|field|prev. See that file for the format.
func dumpTree(root *Node, grammar bool) string {
	var lines []string
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		flags := ""
		if n.named {
			flags += "N"
		}
		if n.hasError {
			flags += "E"
		}
		if grammar {
			if n.missing {
				flags += "M"
			}
			if n.extra {
				flags += "X"
			}
		}
		if flags == "" {
			flags = "-"
		}
		field := "-"
		if grammar && n.field != "" {
			field = escType(n.field)
		}
		prev := "-"
		if n.prev != nil {
			prev = escType(n.prev.typ) + "@" + strconv.Itoa(n.prev.StartIndex()) + "-" + strconv.Itoa(n.prev.EndIndex())
		}
		lines = append(lines, fmt.Sprintf("%d|%s|%s|%d|%d|%d|%s|%s", depth, escType(n.typ), flags, n.StartIndex(), n.EndIndex(), len(n.children), field, prev))
		for _, c := range n.children {
			walk(c, depth+1)
		}
	}
	walk(root, 0)
	return strings.Join(lines, "\n")
}

func escType(s string) string {
	var b strings.Builder
	for _, u := range utf16.Encode([]rune(s)) {
		if u >= 0x21 && u <= 0x7e && u != '\\' && u != '|' {
			b.WriteByte(byte(u))
		} else {
			fmt.Fprintf(&b, "\\u%04x", u)
		}
	}
	return b.String()
}

// fnvDump is the hash dump.test.ts writes in hash mode: FNV-1a/32 over g + "\x01" + p (ASCII).
func fnvDump(g, p string) string {
	h := uint32(0x811c9dc5)
	for _, s := range []string{g, "\x01", p} {
		for i := 0; i < len(s); i++ {
			h ^= uint32(s[i])
			h *= 0x01000193
		}
	}
	return fmt.Sprintf("%08x", h)
}

// dumpBoth parses input with the grammar and with the corrected parser and returns both dumps.
func dumpBoth(input string) (g, p string) {
	gp, _ := GrammarParser()
	cp, _ := GetParser()
	gt := gp.Parse(input)
	if gt == nil {
		g = "NULL"
	} else {
		g = dumpTree(gt.Root, true)
	}
	pt := cp.Parse(input)
	if pt == nil {
		p = "NULL"
	} else {
		p = dumpTree(pt.Root, false)
	}
	return g, p
}
