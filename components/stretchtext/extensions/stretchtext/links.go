package stretchtext

import (
	"strconv"
	"strings"
)

// Each short form and « is an OSC 8 link whose address names the stretch, so a click on the drawn
// line finds it again after Markdown has wrapped it. The address has no http scheme, which Pi's
// Markdown would turn into a link of its own. A port of src/links.ts of pi-stretchtext; the host
// reads the address under a click and hands it to Resolve, where the original read the drawn line
// itself (see PORT.md D2).

// LinkScheme is the scheme of the address a stretch's link carries.
const LinkScheme = "stretchtext"

const hrefPrefix = LinkScheme + ":"

// Target is a stretch and the reply holding it. The Markdown transformer sees only a reply's text,
// so the text names the reply.
type Target struct {
	Reply   string
	Stretch *Stretch
}

// nameIn is one string per stretch of a reply, to key maps by: its reply's text and its key.
func nameIn(reply, key string) string { return reply + "\x00" + key }

func nameOf(t Target) string { return nameIn(t.Reply, t.Stretch.Key) }

// LinkTable holds the stretches drawn as links, each under a number its link's address carries.
type LinkTable struct {
	targets []Target
	ids     map[string]int
}

// NewLinkTable returns an empty table.
func NewLinkTable() *LinkTable { return &LinkTable{ids: map[string]int{}} }

// Link wraps text as a link to target.
func (l *LinkTable) Link(target Target, text string) string {
	name := nameOf(target)
	id, ok := l.ids[name]
	if !ok {
		id = len(l.targets)
		l.targets = append(l.targets, target)
		l.ids[name] = id
	}
	return "\x1b]8;;" + hrefPrefix + strconv.Itoa(id) + "\x07" + text + "\x1b]8;;\x07"
}

// Resolve is the stretch a link address names, ok false for an address that is not one of this
// table's.
func (l *LinkTable) Resolve(address string) (Target, bool) {
	rest, ok := strings.CutPrefix(address, hrefPrefix)
	if !ok {
		return Target{}, false
	}
	id, err := strconv.Atoi(rest)
	if err != nil || id < 0 || id >= len(l.targets) || strconv.Itoa(id) != rest {
		return Target{}, false
	}
	return l.targets[id], true
}
