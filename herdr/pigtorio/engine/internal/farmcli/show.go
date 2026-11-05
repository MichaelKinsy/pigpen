package farmcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"pigtorio/internal/engine"
)

// PigInfo is one pig in the summary.
type PigInfo struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
	Role    string `json:"role,omitempty"`
	Skin    string `json:"skin,omitempty"`
	Status  string `json:"status"` // hc status, "none" when it reports none, "gone" when not live
	Crate   string `json:"crate,omitempty"`
}

// PenInfo is one pen in the summary.
type PenInfo struct {
	Space    string    `json:"space"`
	Name     string    `json:"name,omitempty"`
	Folder   string    `json:"folder,omitempty"`
	Entity   *Entity   `json:"entity,omitempty"`
	Test     string    `json:"test,omitempty"`
	Entities []Entity  `json:"entities"`
	Pigs     []PigInfo `json:"pigs"`
}

// CrateInfo is one crate: titles and states only.
type CrateInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
	Pen   string `json:"pen,omitempty"`
	At    string `json:"at,omitempty"`
}

// Summary is what `farm show` prints.
type Summary struct {
	Pens     []PenInfo   `json:"pens"`
	Loose    []Entity    `json:"loose"`  // entities on open ground (Job Board, Barn, Runt…)
	Strays   []PigInfo   `json:"strays"` // pigs in no known pen
	Belts    []Belt      `json:"belts"`
	Lines    []Line      `json:"lines"`
	Board    []CrateInfo `json:"board"`       // crates on the Job Board
	Moving   []CrateInfo `json:"moving"`      // working, in the Oven or at the Barn
	Needs    []CrateInfo `json:"needsPerson"` // blocked, burnt, holding, failed
	Shipped  int         `json:"shipped"`
	Ghosts   *Draft      `json:"ghosts"`
	Warnings []string    `json:"warnings,omitempty"`
}

// BuildSummary reads the farm from home.
func BuildSummary(home string, deps Deps) (*Summary, error) {
	d := filepath.Join(home, "pigtorio")
	farm, err := readFarm(d)
	if err != nil {
		return nil, err
	}
	s := &Summary{Pens: []PenInfo{}, Loose: []Entity{}, Strays: []PigInfo{}, Belts: farm.Belts, Lines: farm.Lines}
	if s.Belts == nil {
		s.Belts = []Belt{}
	}
	if s.Lines == nil {
		s.Lines = []Line{}
	}
	s.Board, s.Moving, s.Needs = []CrateInfo{}, []CrateInfo{}, []CrateInfo{}

	var st engine.State
	if b, err := os.ReadFile(filepath.Join(d, "state", "state.json")); err == nil {
		if err := json.Unmarshal(b, &st); err != nil {
			s.Warnings = append(s.Warnings, "state.json unreadable: "+err.Error())
		}
	}
	live := map[string]string{} // address -> status
	if c, err := deps.HC(); err != nil {
		s.Warnings = append(s.Warnings, "hc: "+err.Error())
	} else if ts, err := c.List(); err != nil {
		s.Warnings = append(s.Warnings, "hc ls: "+err.Error())
	} else {
		for _, t := range ts {
			if t.Agent == "pig" || farm.Pigs[t.Address] != (FarmPig{}) {
				live[t.Address] = t.Status
			}
		}
	}
	var spaces []Space
	if deps.Spaces != nil {
		spaces, err = deps.Spaces(home)
		if err != nil {
			spaces = nil
		}
	}
	spaceOf := map[string]Space{}
	penOfPig := map[string]string{}
	for _, sp := range spaces {
		spaceOf[sp.ID] = sp
		for _, t := range sp.Terminals {
			penOfPig[t] = sp.ID
		}
	}
	for _, l := range farm.Lines {
		for _, stop := range l.Stops {
			if stop.Kind == "pig" && penOfPig[stop.Pig] == "" {
				penOfPig[stop.Pig] = l.Pen
			}
		}
	}

	// Pens: farm.json pens, pen entities, and spaces seen by herdr-app.
	ids := map[string]bool{}
	for id := range farm.Pens {
		ids[id] = true
	}
	for _, e := range farm.Entities {
		if e.Kind == "pen" {
			ids[e.Space] = true
		}
	}
	for id := range spaceOf {
		ids[id] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	pigInfo := func(addr string) PigInfo {
		p := PigInfo{Address: addr, Status: "gone"}
		fp := farm.Pigs[addr]
		p.Name, p.Role, p.Skin = fp.Name, fp.Role, fp.Skin
		if stt, ok := live[addr]; ok {
			p.Status = stt
			if stt == "" {
				p.Status = "none"
			}
		}
		p.Crate = st.Pigs[addr].Crate
		return p
	}
	allPigs := map[string]bool{}
	for a := range farm.Pigs {
		allPigs[a] = true
	}
	for a := range live {
		allPigs[a] = true
	}
	pigAddrs := make([]string, 0, len(allPigs))
	for a := range allPigs {
		pigAddrs = append(pigAddrs, a)
	}
	sort.Slice(pigAddrs, func(i, j int) bool { return addrLess(pigAddrs[i], pigAddrs[j]) })

	for _, id := range sorted {
		pi := PenInfo{Space: id, Test: farm.Pens[id].Test, Folder: farm.Pens[id].Folder, Entities: []Entity{}, Pigs: []PigInfo{}}
		if sp, ok := spaceOf[id]; ok {
			pi.Name = sp.Name
			if sp.Folder != "" {
				pi.Folder = sp.Folder
			}
		}
		if pe, ok := farm.penEntity(id); ok {
			pe := pe
			pi.Entity = &pe
		}
		for _, e := range farm.Entities {
			if e.Kind != "pen" && e.Pen == id {
				pi.Entities = append(pi.Entities, e)
			}
		}
		sortEntities(pi.Entities)
		for _, a := range pigAddrs {
			if penOfPig[a] == id {
				pi.Pigs = append(pi.Pigs, pigInfo(a))
			}
		}
		s.Pens = append(s.Pens, pi)
	}
	for _, e := range farm.Entities {
		if e.Kind != "pen" && (e.Pen == "" || !ids[e.Pen]) {
			s.Loose = append(s.Loose, e)
		}
	}
	sortEntities(s.Loose)
	for _, a := range pigAddrs {
		if p := penOfPig[a]; p == "" || !ids[p] {
			s.Strays = append(s.Strays, pigInfo(a))
		}
	}

	for _, c := range st.Crates {
		ci := CrateInfo{ID: c.ID, Title: c.Title, State: c.State, Pen: c.Pen}
		if c.At != nil {
			ci.At = c.At.Kind
			if c.At.Pig != "" {
				ci.At += ":" + c.At.Pig
			}
		}
		switch c.State {
		case "board":
			s.Board = append(s.Board, ci)
		case "working", "oven", "barn":
			s.Moving = append(s.Moving, ci)
		case "blocked", "burnt", "holding", "failed":
			s.Needs = append(s.Needs, ci)
		case "shipped":
			s.Shipped++
		}
	}
	if s.Ghosts, err = readDraft(d); err != nil {
		return nil, err
	}
	return s, nil
}

func addrLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

func sortEntities(es []Entity) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].Kind != es[j].Kind {
			return es[i].Kind < es[j].Kind
		}
		return es[i].ID < es[j].ID
	})
}

// RunFarm runs `pigtorio farm …`; args follow "farm".
func RunFarm(args []string, out io.Writer, deps Deps) error {
	if len(args) == 0 || args[0] != "show" {
		return errors.New("usage: pigtorio farm show [--json] [--home H]")
	}
	fs := newFlags("farm show", out)
	home := fs.String("home", DefaultHome(), "Herdr app home")
	asJSON := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("farm show: %w", err)
	}
	s, err := BuildSummary(*home, deps)
	if err != nil {
		return err
	}
	if *asJSON {
		b, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(b))
		return nil
	}
	s.WriteText(out)
	return nil
}

func entText(e Entity) string {
	return fmt.Sprintf("%s %s at %d,%d (%dx%d)", e.Kind, e.ID, e.X, e.Y, e.W, e.H)
}

func pigText(p PigInfo) string {
	s := p.Address
	if p.Name != "" {
		s += " " + p.Name
	}
	if p.Role != "" {
		s += " (" + p.Role + ")"
	}
	s += " " + p.Status
	if p.Crate != "" {
		s += " holding " + p.Crate
	}
	return s
}

func crateText(w io.Writer, title string, cs []CrateInfo) {
	if len(cs) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s:\n", title)
	for _, c := range cs {
		line := fmt.Sprintf("  %s %q [%s]", c.ID, c.Title, c.State)
		if c.At != "" {
			line += " at " + c.At
		}
		fmt.Fprintln(w, line)
	}
}

// WriteText prints the summary as short readable text.
func (s *Summary) WriteText(w io.Writer) {
	for _, m := range s.Warnings {
		fmt.Fprintln(w, "warning:", m)
	}
	fmt.Fprintf(w, "PENS (%d)\n", len(s.Pens))
	for _, p := range s.Pens {
		name := p.Name
		if name == "" {
			name = "(unnamed)"
		}
		fmt.Fprintf(w, "pen %s %s", p.Space, name)
		if p.Folder != "" {
			fmt.Fprintf(w, " folder %s", p.Folder)
		}
		if p.Entity != nil {
			fmt.Fprintf(w, " at %d,%d (%dx%d)", p.Entity.X, p.Entity.Y, p.Entity.W, p.Entity.H)
		} else {
			fmt.Fprint(w, " (no pen entity yet)")
		}
		fmt.Fprintln(w)
		if p.Test != "" {
			fmt.Fprintf(w, "  oven test: %s\n", p.Test)
		}
		for _, e := range p.Entities {
			fmt.Fprintf(w, "  %s\n", entText(e))
		}
		for _, pg := range p.Pigs {
			fmt.Fprintf(w, "  pig %s\n", pigText(pg))
		}
	}
	if len(s.Loose) > 0 {
		fmt.Fprintln(w, "\nOPEN GROUND:")
		for _, e := range s.Loose {
			fmt.Fprintf(w, "  %s\n", entText(e))
		}
	}
	if len(s.Strays) > 0 {
		fmt.Fprintln(w, "\nPIGS IN NO PEN:")
		for _, p := range s.Strays {
			fmt.Fprintf(w, "  %s\n", pigText(p))
		}
	}
	if len(s.Belts) > 0 {
		fmt.Fprintln(w, "\nBELTS:")
		for _, b := range s.Belts {
			fmt.Fprintf(w, "  %s %s -> %s\n", b.ID, b.From, b.To)
		}
	}
	if len(s.Lines) > 0 {
		fmt.Fprintln(w, "\nLINES:")
		for _, l := range s.Lines {
			var stops []string
			for _, st := range l.Stops {
				switch {
				case st.Pig != "":
					stops = append(stops, st.Pig)
				case st.Entity != "":
					stops = append(stops, st.Kind+":"+st.Entity)
				default:
					stops = append(stops, st.Kind)
				}
			}
			fmt.Fprintf(w, "  %s (pen %s): %s\n", l.ID, l.Pen, strings.Join(stops, " > "))
		}
	}
	crateText(w, "CRATES ON THE BOARD", s.Board)
	crateText(w, "CRATES MOVING", s.Moving)
	crateText(w, "CRATES NEEDING A PERSON", s.Needs)
	if s.Shipped > 0 {
		fmt.Fprintf(w, "\n%d shipped\n", s.Shipped)
	}
	fmt.Fprintln(w, "\nGHOSTS:")
	if len(s.Ghosts.Ghosts) == 0 && s.Ghosts.Note == "" {
		fmt.Fprintln(w, "  none")
	}
	if s.Ghosts.Note != "" {
		fmt.Fprintf(w, "  note: %s\n", s.Ghosts.Note)
	}
	for _, g := range s.Ghosts.Ghosts {
		fmt.Fprintf(w, "  %s\n", g)
	}
}
