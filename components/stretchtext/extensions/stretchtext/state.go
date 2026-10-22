package stretchtext

import "strings"

// Everything stretchtext remembers, and every change to it. A port of src/state.ts and src/monad.ts
// of pi-stretchtext: the original keeps an immutable record and describes each change as a State
// value; here the record is mutated in place under the Store's lock and each change reports whether
// it changed anything, which is what the original's `next !== state` test reports.

const letters = "asdfghjklqwertyuiopzxcvbnm"

// letteredReplies is how many of the latest replies holding a stretch take letters, as in cc-stretch.
const letteredReplies = 3

// Mode is whether letters are shown over closed stretches, to open one, or over open ones, to close
// one.
type Mode string

const (
	// ModeOpen shows letters over closed stretches.
	ModeOpen Mode = "open"
	// ModeClose shows letters over open stretches.
	ModeClose Mode = "close"
)

// Growth is the stretch growing or shrinking now, and how far it has got, from 0 (short form) to 1
// (long form).
type Growth struct {
	Name     string
	Progress float64
}

// Lettering is, while letters show, the stretch each letter stands for, and the letter over each
// stretch by name.
type Lettering struct {
	Mode     Mode
	ByLetter map[string]Target
	ByName   map[string]string
}

// State is everything stretchtext remembers.
type State struct {
	// replies is every reply text holding a stretch, in the order the host first drew it.
	replies []string
	// parsed holds the segments of each remembered reply.
	parsed map[string][]Segment
	// open is the open stretches by name, each with the count it was opened at, which picks its colour.
	// A closing stretch stays here while it shrinks.
	open map[string]int
	// opened is how many stretches each reply has opened, by reply text.
	opened map[string]int
	// newest is the stretch opened last, drawn in its colour's full form; the others are faded.
	newest    string
	hasNewest bool
	growth    *Growth
	lettering *Lettering
	// read is the short forms opened since the model last answered, in the order they were opened.
	read    []string
	readSet map[string]struct{}
	// repliesAtPrompt is how many replies held a stretch when the model was last asked: those after it
	// are its last reply.
	repliesAtPrompt int
}

// NewState is the state before anything happened.
func NewState() *State {
	return &State{
		parsed:  map[string][]Segment{},
		open:    map[string]int{},
		opened:  map[string]int{},
		readSet: map[string]struct{}{},
	}
}

func (s *State) isOpen(reply, key string) bool {
	_, ok := s.open[nameIn(reply, key)]
	return ok
}

// letter is the letter over a stretch while letters for mode show, or "".
func (s *State) letter(name string, mode Mode) string {
	if s.lettering == nil || s.lettering.Mode != mode {
		return ""
	}
	if l, ok := s.lettering.ByName[name]; ok {
		return reverse(l)
	}
	return ""
}

// segmentsOf is the parse of a remembered reply, ok false for any other text.
func (s *State) segmentsOf(reply string) ([]Segment, bool) {
	segments, ok := s.parsed[reply]
	return segments, ok
}

// remember notes a reply holding a stretch, the first time the host draws it, and reports whether it
// was new.
func (s *State) remember(reply string, segments []Segment) bool {
	if _, ok := s.parsed[reply]; ok {
		return false
	}
	s.replies = append(s.replies, reply)
	s.parsed[reply] = segments
	return true
}

// toggle opens a closed stretch or starts closing an open one, and yields whether it opens, and
// whether the state changed. A closing stretch stays open until settle drops it, once it has shrunk.
func (s *State) toggle(target Target) (opening, changed bool) {
	name := nameOf(target)
	if _, isOpen := s.open[name]; isOpen {
		return false, false
	}
	s.openStretch(target, name)
	return true, true
}

// openStretch opens a stretch in the next colour of its reply, never the colour of the stretch holding it.
func (s *State) openStretch(target Target, name string) {
	var parent *int
	if key, ok := ParentOf(target.Stretch.Key); ok {
		if hue, ok := s.open[nameIn(target.Reply, key)]; ok {
			parent = &hue
		}
	}
	hue := hueAfter(s.opened[target.Reply], parent)
	s.open[name] = hue
	s.opened[target.Reply] = hue + 1
	s.newest, s.hasNewest = name, true
	short := trim(target.Stretch.Short)
	if _, ok := s.readSet[short]; !ok {
		s.readSet[short] = struct{}{}
		s.read = append(s.read, short)
	}
}

func (s *State) setGrowth(name string, progress float64) {
	s.growth = &Growth{Name: name, Progress: progress}
}

// settle ends a stretch's growth: an opening one stays open, a closing one is closed.
func (s *State) settle(target Target, isOpening bool) {
	name := nameOf(target)
	if !isOpening {
		delete(s.open, name)
	}
	if s.growth != nil && s.growth.Name == name {
		s.growth = nil
	}
}

// startLettering puts letters over the stretches mode acts on in the latest replies, and yields
// whether there were any.
func (s *State) startLettering(mode Mode) bool {
	lettering := s.letteringFor(mode)
	if len(lettering.ByLetter) == 0 {
		return false
	}
	s.lettering = lettering
	return true
}

// pickLetter takes the letters away, and yields the stretch the typed key stands for, if any.
func (s *State) pickLetter(key string) (Target, bool) {
	var target Target
	var ok bool
	if s.lettering != nil {
		target, ok = s.lettering.ByLetter[key]
	}
	s.lettering = nil
	return target, ok
}

// takeReadNote is the note telling the model which stretches of its last reply the reader opened, if
// it wrote any, and starts counting again for the next reply.
func (s *State) takeReadNote() (string, bool) {
	note, ok := s.readNote()
	s.read, s.readSet = nil, map[string]struct{}{}
	s.repliesAtPrompt = len(s.replies)
	return note, ok
}

func (s *State) readNote() (string, bool) {
	var written []string
	for _, reply := range s.replies[s.repliesAtPrompt:] {
		for _, stretch := range Stretches(s.parsed[reply]) {
			if stretch.IsLinkable() {
				written = append(written, trim(stretch.Short))
			}
		}
	}
	if len(s.read) == 0 && len(written) == 0 {
		return "", false
	}
	var unopened []string
	for _, short := range written {
		if _, ok := s.readSet[short]; !ok {
			unopened = append(unopened, short)
		}
	}
	return ReadNote(s.read, unopened), true
}

func (s *State) letteringFor(mode Mode) *Lettering {
	lettering := &Lettering{Mode: mode, ByLetter: map[string]Target{}, ByName: map[string]string{}}
	replies := s.replies
	if len(replies) > letteredReplies {
		replies = replies[len(replies)-letteredReplies:]
	}
	n := 0
	for _, reply := range replies {
		for _, stretch := range s.reachable(reply, s.parsed[reply]) {
			if n == len(letters) {
				return lettering
			}
			if !stretch.IsLinkable() || s.isOpen(reply, stretch.Key) != (mode == ModeClose) {
				continue
			}
			target := Target{Reply: reply, Stretch: stretch}
			l := letters[n : n+1]
			lettering.ByLetter[l] = target
			lettering.ByName[nameOf(target)] = l
			n++
		}
	}
	return lettering
}

// reachable is the stretches a reader can reach in segments: those not inside a closed one,
// outermost first.
func (s *State) reachable(reply string, segments []Segment) []*Stretch {
	var out []*Stretch
	for _, segment := range segments {
		if segment.Stretch == nil {
			continue
		}
		out = append(out, segment.Stretch)
		if segment.Stretch.DrawnAs() == DrawnOpen || s.isOpen(reply, segment.Stretch.Key) {
			out = append(out, s.reachable(reply, segment.Stretch.Long)...)
		}
	}
	return out
}

// ReadNote words what the reader opened since the model's last reply, as cc-stretch's prompt.submit
// note.
func ReadNote(opened, unopened []string) string {
	quote := func(list []string) string {
		quoted := make([]string, len(list))
		for i, s := range list {
			quoted[i] = `"` + s + `"`
		}
		return strings.Join(quoted, ", ")
	}
	if len(opened) == 0 {
		return "Stretchtext: no click on a stretch of your last reply was recorded. The reader may have read only the short forms."
	}
	note := "Stretchtext: since your last reply the reader opened " + quote(opened) + "."
	if len(unopened) > 0 {
		note += " They did not open " + quote(unopened) + "."
	}
	return note
}
