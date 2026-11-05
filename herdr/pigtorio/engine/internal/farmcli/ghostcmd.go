package farmcli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"pigtorio/internal/hc"
)

// DefaultHome is $HERDR_APP_HOME, else ~/.config/herdr-app.
func DefaultHome() string {
	if h := os.Getenv("HERDR_APP_HOME"); h != "" {
		return h
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(u, ".config", "herdr-app")
}

var placeKinds = map[string]bool{"oven": true, "review": true, "barn": true, "pen": true, "jobboard": true}

// beltKinds are the entity kinds a belt end may name.
var beltKinds = map[string]bool{"oven": true, "review": true, "barn": true}

func newFlags(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// RunGhost runs `pigtorio ghost …`; args follow "ghost". Output goes to out.
func RunGhost(args []string, out io.Writer, deps Deps) error {
	const usage = "usage: pigtorio ghost place|belt|crate|note|list|clear …"
	if len(args) == 0 {
		return errors.New(usage)
	}
	sub, rest := args[0], args[1:]
	fs := newFlags("ghost "+sub, out)
	home := fs.String("home", DefaultHome(), "Herdr app home")
	by := fs.String("by", "", "who drafts (default runt)")
	var kind, pen, near, from, to, title, doneWhen, text, size string
	var x, y int
	var asJSON bool
	switch sub {
	case "place":
		fs.StringVar(&kind, "kind", "", "oven|review|barn|pen|jobboard")
		fs.StringVar(&pen, "pen", "", "pen (space id)")
		fs.StringVar(&near, "near", "", "pig:tN | ent:ID")
		fs.IntVar(&x, "x", 0, "tile x")
		fs.IntVar(&y, "y", 0, "tile y")
	case "belt":
		fs.StringVar(&from, "from", "", "pig:<addr> | ent:<id> | ghost:<id>")
		fs.StringVar(&to, "to", "", "pig:<addr> | ent:<id> | ghost:<id>")
	case "crate":
		fs.StringVar(&title, "title", "", "crate title")
		fs.StringVar(&doneWhen, "done-when", "", "how anyone knows it is done")
		fs.StringVar(&text, "text", "", "what to do")
		fs.StringVar(&size, "size", "one-shot", "one-shot|large")
		fs.StringVar(&pen, "pen", "", "pen (space id)")
	case "list":
		fs.BoolVar(&asJSON, "json", false, "print ghosts.json")
	case "note", "clear":
	default:
		return fmt.Errorf("unknown ghost command %q; %s", sub, usage)
	}
	// Flags may follow the text: parse, take a positional word, parse the rest.
	var pos []string
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return fmt.Errorf("ghost %s: %w", sub, err)
		}
		rest = fs.Args()
		if len(rest) > 0 {
			pos = append(pos, rest[0])
			rest = rest[1:]
		}
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	d := filepath.Join(*home, "pigtorio")
	author := *by
	if author == "" {
		author = "runt"
	}
	// setBy names the draft's author: the first drafter, or whoever says --by.
	setBy := func(dr *Draft) {
		if dr.By == "" || *by != "" {
			dr.By = author
		}
	}

	switch sub {
	case "list":
		dr, err := readDraft(d)
		if err != nil {
			return err
		}
		if asJSON {
			b, _ := json.MarshalIndent(dr, "", "  ")
			fmt.Fprintln(out, string(b))
			return nil
		}
		if len(dr.Ghosts) == 0 && dr.Note == "" {
			fmt.Fprintln(out, "no ghosts")
			return nil
		}
		if dr.Note != "" {
			fmt.Fprintln(out, "note:", dr.Note)
		}
		for _, g := range dr.Ghosts {
			fmt.Fprintln(out, g)
		}
		return nil
	case "clear":
		return withDraft(d, func(*Draft) (bool, error) {
			err := os.Remove(draftPath(d))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return false, err
			}
			return false, nil
		})
	case "note":
		note := strings.TrimSpace(strings.Join(pos, " "))
		if note == "" {
			return errors.New("usage: pigtorio ghost note TEXT")
		}
		return withDraft(d, func(dr *Draft) (bool, error) {
			dr.Note = note
			setBy(dr)
			return true, nil
		})
	}

	if len(pos) > 0 {
		return fmt.Errorf("ghost %s: unexpected argument %q", sub, pos[0])
	}
	farm, err := readFarm(d)
	if err != nil {
		return err
	}
	v := &validator{farm: farm, deps: deps}
	var g Ghost
	switch sub {
	case "place":
		g = Ghost{Op: "place", Kind: kind, Pen: pen, Near: near}
		if set["x"] != set["y"] {
			return errors.New("--x and --y go together")
		}
		if set["x"] {
			g.X, g.Y = &x, &y
		}
		if err := v.place(&g); err != nil {
			return err
		}
	case "belt":
		g = Ghost{Op: "belt", From: from, To: to}
	case "crate":
		g = Ghost{Op: "crate", Crate: &GhostCrate{Title: title, DoneWhen: doneWhen, Text: text, Size: size, Pen: pen}}
		if err := v.crate(g.Crate); err != nil {
			return err
		}
	}
	var id string
	err = withDraft(d, func(dr *Draft) (bool, error) {
		if sub == "belt" {
			if err := v.belt(dr, &g); err != nil {
				return false, err
			}
		} else if g.Op == "place" && g.Near != "" {
			if err := v.ref(dr, g.Near, "--near"); err != nil {
				return false, err
			}
		}
		g.ID = dr.nextID()
		id = g.ID
		dr.Ghosts = append(dr.Ghosts, g)
		setBy(dr)
		return true, nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, id)
	return nil
}

// validator checks ghosts against farm.json and hc.
type validator struct {
	farm *Farm
	deps Deps
	pigs map[string]bool
}

func (v *validator) livePigs() (map[string]bool, error) {
	if v.pigs != nil {
		return v.pigs, nil
	}
	var c hc.Client
	c, err := v.deps.HC()
	if err != nil {
		return nil, fmt.Errorf("cannot check pig addresses: %w", err)
	}
	ts, err := c.List()
	if err != nil {
		return nil, fmt.Errorf("cannot check pig addresses: %w", err)
	}
	v.pigs = map[string]bool{}
	for _, t := range ts {
		v.pigs[t.Address] = true
	}
	return v.pigs, nil
}

// ref validates pig:<addr>, ent:<id> or ghost:<id> (ghosts in dr; nil dr rejects ghost refs).
func (v *validator) ref(dr *Draft, ref, what string) error {
	typ, id, ok := strings.Cut(ref, ":")
	if !ok || id == "" {
		return fmt.Errorf("%s %q: want pig:<address>, ent:<id> or ghost:<id>", what, ref)
	}
	switch typ {
	case "pig":
		pigs, err := v.livePigs()
		if err != nil {
			return err
		}
		if !pigs[id] {
			return fmt.Errorf("%s %q: no pig %s in hc ls", what, ref, id)
		}
	case "ent":
		if _, ok := v.farm.Entities[id]; !ok {
			return fmt.Errorf("%s %q: no entity %s in farm.json", what, ref, id)
		}
	case "ghost":
		if dr == nil || dr.find(id) == nil {
			return fmt.Errorf("%s %q: no ghost %s in the draft", what, ref, id)
		}
	default:
		return fmt.Errorf("%s %q: want pig:<address>, ent:<id> or ghost:<id>", what, ref)
	}
	return nil
}

func (v *validator) needPen(space string) error {
	if _, ok := v.farm.penEntity(space); !ok {
		return fmt.Errorf("--pen %s: no pen entity for that space in farm.json", space)
	}
	return nil
}

func (v *validator) place(g *Ghost) error {
	if !placeKinds[g.Kind] {
		return fmt.Errorf("--kind %q: want oven, review, barn, pen or jobboard", g.Kind)
	}
	if g.Near != "" && g.X != nil {
		return errors.New("--near and --x/--y are alternatives")
	}
	// ghost: refs are checked against the draft under the lock
	if g.Near != "" && !strings.HasPrefix(g.Near, "ghost:") {
		if err := v.ref(nil, g.Near, "--near"); err != nil {
			return err
		}
	}
	if g.Kind == "pen" {
		if g.Pen == "" {
			return errors.New("a pen ghost needs --pen SPACE")
		}
		if _, ok := v.farm.penEntity(g.Pen); ok {
			return fmt.Errorf("space %s already has a pen", g.Pen)
		}
		return nil
	}
	if g.Pen != "" {
		return v.needPen(g.Pen)
	}
	return nil
}

func (v *validator) crate(c *GhostCrate) error {
	if strings.TrimSpace(c.Title) == "" {
		return errors.New("--title is required")
	}
	if strings.TrimSpace(c.DoneWhen) == "" {
		return errors.New("--done-when is required: a crate without it cannot leave the board")
	}
	if c.Size != "one-shot" && c.Size != "large" {
		return fmt.Errorf("--size %q: want one-shot or large", c.Size)
	}
	if c.Pen != "" {
		return v.needPen(c.Pen)
	}
	return nil
}

// end validates one belt end and returns the entity kind it stands on ("" for a pig).
func (v *validator) end(dr *Draft, ref, what string) (string, error) {
	if err := v.ref(dr, ref, what); err != nil {
		return "", err
	}
	typ, id, _ := strings.Cut(ref, ":")
	var k string
	switch typ {
	case "ent":
		k = v.farm.Entities[id].Kind
	case "ghost":
		gh := dr.find(id)
		if gh.Op != "place" {
			return "", fmt.Errorf("%s %q: that ghost is not a building", what, ref)
		}
		k = gh.Kind
	}
	if typ != "pig" && !beltKinds[k] {
		return "", fmt.Errorf("%s %q: a belt end is a pig, an Oven, a Review or a Barn (not %s)", what, ref, k)
	}
	return k, nil
}

func (v *validator) belt(dr *Draft, g *Ghost) error {
	if g.From == "" || g.To == "" {
		return errors.New("usage: pigtorio ghost belt --from REF --to REF")
	}
	if g.From == g.To {
		return errors.New("a belt needs two different ends")
	}
	fk, err := v.end(dr, g.From, "--from")
	if err != nil {
		return err
	}
	if _, err := v.end(dr, g.To, "--to"); err != nil {
		return err
	}
	if fk == "barn" {
		return errors.New("--from: nothing leaves a Barn")
	}
	return nil
}
