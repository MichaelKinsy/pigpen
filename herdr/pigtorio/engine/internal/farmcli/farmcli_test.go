package farmcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"pigtorio/internal/hc"
)

type fakeHC struct{ terms []hc.Term }

func (f fakeHC) List() ([]hc.Term, error)             { return f.terms, nil }
func (f fakeHC) Get(string) (hc.Term, error)          { return hc.Term{}, nil }
func (f fakeHC) Send(_, _, _ string) error            { return nil }
func (f fakeHC) Read(string, int) (string, error)     { return "", nil }
func (f fakeHC) Follow(context.Context, func()) error { return nil }

func testDeps() Deps {
	return Deps{
		HC: func() (hc.Client, error) {
			return fakeHC{[]hc.Term{
				{Address: "t3", Agent: "pig", Status: "idle"},
				{Address: "t4", Agent: "pig", Status: "working"},
			}}, nil
		},
		Spaces: func(string) ([]Space, error) {
			return []Space{{ID: "sp1", Name: "toy", Folder: "/r/toy", Terminals: []string{"t3", "t4"}}}, nil
		},
	}
}

const farmJSON = `{
 "pens": {"sp1": {"test": "go test ./..."}},
 "pigs": {"t3": {"name": "Sandy", "role": "builder", "skin": "sandy"}},
 "entities": {
  "pA1": {"id":"pA1","kind":"pen","x":2,"y":6,"w":14,"h":8,"space":"sp1"},
  "o9": {"id":"o9","kind":"oven","x":4,"y":11,"w":2,"h":2,"pen":"sp1"},
  "barn": {"id":"barn","kind":"barn","x":34,"y":1,"w":3,"h":3,"pen":null},
  "jobboard": {"id":"jobboard","kind":"jobboard","x":2,"y":1,"w":4,"h":3}
 },
 "belts": [{"id":"b1","from":"pig:t3","to":"ent:o9"}]
}`

func home(t *testing.T) string {
	t.Setenv("HERDR_APP_HOME", t.TempDir()) // a stray default home must never be a real one
	h := t.TempDir()
	d := filepath.Join(h, "pigtorio")
	if err := os.MkdirAll(filepath.Join(d, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "farm.json"), []byte(farmJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return h
}

func ghost(t *testing.T, h string, args ...string) (string, error) {
	var out bytes.Buffer
	err := RunGhost(append(args, "--home", h), &out, testDeps())
	return strings.TrimSpace(out.String()), err
}

func TestPlaceBeltListClear(t *testing.T) {
	h := home(t)
	id, err := ghost(t, h, "place", "--kind", "oven", "--pen", "sp1", "--near", "pig:t4")
	if err != nil || id != "g1" {
		t.Fatal(id, err)
	}
	if id, err = ghost(t, h, "place", "--kind", "review", "--pen", "sp1", "--x", "9", "--y", "11"); err != nil || id != "g2" {
		t.Fatal(id, err)
	}
	if id, err = ghost(t, h, "belt", "--from", "pig:t4", "--to", "ghost:g1"); err != nil || id != "g3" {
		t.Fatal(id, err)
	}
	if _, err = ghost(t, h, "note", "make", "an", "oven"); err != nil {
		t.Fatal(err)
	}
	if id, err = ghost(t, h, "crate", "--title", "T", "--done-when", "W", "--pen", "sp1"); err != nil || id != "g4" {
		t.Fatal(id, err)
	}
	b, _ := os.ReadFile(filepath.Join(h, "pigtorio", "ghosts.json"))
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["by"] != "runt" || got["note"] != "make an oven" || len(got["ghosts"].([]any)) != 4 {
		t.Fatalf("%s", b)
	}
	g0 := got["ghosts"].([]any)[0].(map[string]any)
	if g0["op"] != "place" || g0["near"] != "pig:t4" || g0["pen"] != "sp1" || g0["x"] != nil {
		t.Fatalf("%v", g0)
	}
	g1 := got["ghosts"].([]any)[1].(map[string]any)
	if g1["x"] != 9.0 || g1["y"] != 11.0 {
		t.Fatalf("%v", g1)
	}
	c := got["ghosts"].([]any)[3].(map[string]any)["crate"].(map[string]any)
	if c["title"] != "T" || c["doneWhen"] != "W" || c["size"] != "one-shot" || c["pen"] != "sp1" || c["text"] != "" {
		t.Fatalf("%v", c)
	}
	list, _ := ghost(t, h, "list")
	if !strings.Contains(list, "g3 belt pig:t4 -> ghost:g1") || !strings.Contains(list, "note: make an oven") {
		t.Fatal(list)
	}
	if _, err := ghost(t, h, "clear"); err != nil {
		t.Fatal(err)
	}
	if list, _ = ghost(t, h, "list"); list != "no ghosts" {
		t.Fatal(list)
	}
	if _, err := os.Stat(filepath.Join(h, "pigtorio", ".ghosts.json.tmp")); err == nil {
		t.Fatal("temp file left behind")
	}
}

func TestValidation(t *testing.T) {
	h := home(t)
	bad := [][]string{
		{"place", "--kind", "castle"},
		{"place", "--kind", "oven", "--x", "3"},
		{"place", "--kind", "oven", "--x", "3", "--y", "4", "--near", "pig:t3"},
		{"place", "--kind", "oven", "--near", "pig:t99"},
		{"place", "--kind", "oven", "--near", "ent:nope"},
		{"place", "--kind", "oven", "--near", "ghost:g9"},
		{"place", "--kind", "oven", "--pen", "spX"},
		{"place", "--kind", "pen"},
		{"place", "--kind", "pen", "--pen", "sp1"},
		{"belt", "--from", "pig:t3"},
		{"belt", "--from", "pig:t9", "--to", "ent:o9"},
		{"belt", "--from", "pig:t3", "--to", "ent:nope"},
		{"belt", "--from", "pig:t3", "--to", "ent:jobboard"},
		{"belt", "--from", "ent:barn", "--to", "ent:o9"},
		{"belt", "--from", "pig:t3", "--to", "ghost:g1"},
		{"belt", "--from", "pig:t3", "--to", "pig:t3"},
		{"belt", "--from", "pig:t3", "--to", "wat"},
		{"crate", "--title", "T"},
		{"crate", "--done-when", "W"},
		{"crate", "--title", "T", "--done-when", "W", "--size", "huge"},
		{"crate", "--title", "T", "--done-when", "W", "--pen", "spX"},
		{"note"},
		{"frob"},
		{},
	}
	for _, a := range bad {
		if id, err := ghost(t, h, a...); err == nil {
			t.Errorf("%v accepted as %s", a, id)
		}
	}
	if _, err := os.Stat(filepath.Join(h, "pigtorio", "ghosts.json")); err == nil {
		t.Error("a refused ghost wrote the draft")
	}
	if _, err := ghost(t, h, "place", "--kind", "pen", "--pen", "spNew"); err != nil {
		t.Error(err)
	}
	if _, err := ghost(t, h, "place", "--kind", "barn", "--near", "ent:o9"); err != nil {
		t.Error(err)
	}
}

func TestByAndNoHC(t *testing.T) {
	h := home(t)
	if _, err := ghost(t, h, "place", "--kind", "jobboard", "--by", "pat"); err != nil {
		t.Fatal(err)
	}
	if _, err := ghost(t, h, "note", "hi"); err != nil {
		t.Fatal(err)
	}
	dr, _ := readDraft(filepath.Join(h, "pigtorio"))
	if dr.By != "pat" {
		t.Fatal(dr.By)
	}
	var out bytes.Buffer
	err := RunGhost([]string{"place", "--kind", "oven", "--near", "pig:t3", "--home", h}, &out,
		Deps{HC: func() (hc.Client, error) { return nil, errors.New("no hc") }})
	if err == nil {
		t.Fatal("pig address accepted without hc")
	}
}

func TestConcurrentGhostsGetDistinctIDs(t *testing.T) {
	h := home(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ghost(t, h, "place", "--kind", "barn"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	dr, err := readDraft(filepath.Join(h, "pigtorio"))
	if err != nil || len(dr.Ghosts) != 12 {
		t.Fatal(err, len(dr.Ghosts))
	}
	seen := map[string]bool{}
	for _, g := range dr.Ghosts {
		seen[g.ID] = true
	}
	if len(seen) != 12 {
		t.Fatal("duplicate ids")
	}
}

func TestFarmShow(t *testing.T) {
	h := home(t)
	st := `{"crates":[{"id":"c1","title":"Board one","state":"board"},{"id":"c2","title":"Moving","state":"working","at":{"kind":"pig","pig":"t4"}},{"id":"c3","title":"Stuck","state":"burnt"},{"id":"c4","title":"Done","state":"shipped"}],"pigs":{"t4":{"crate":"c2","status":"working"}}}`
	os.WriteFile(filepath.Join(h, "pigtorio", "state", "state.json"), []byte(st), 0o644)
	ghost(t, h, "place", "--kind", "review", "--pen", "sp1")
	var out bytes.Buffer
	if err := RunFarm([]string{"show", "--home", h}, &out, testDeps()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pen sp1 toy folder /r/toy", "oven test: go test ./...", "oven o9", "pig t3 Sandy (builder) idle",
		"pig t4 working holding c2", "OPEN GROUND", "barn barn", "b1 pig:t3 -> ent:o9", "c1 \"Board one\"", "CRATES MOVING", "c2 \"Moving\" [working] at pig:t4",
		"CRATES NEEDING A PERSON", "c3", "1 shipped", "g1 place review pen=sp1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := RunFarm([]string{"show", "--json", "--home", h}, &out, testDeps()); err != nil {
		t.Fatal(err)
	}
	var s Summary
	if err := json.Unmarshal(out.Bytes(), &s); err != nil || len(s.Pens) != 1 || len(s.Pens[0].Pigs) != 2 || s.Pens[0].Name != "toy" || len(s.Board) != 1 || len(s.Ghosts.Ghosts) != 1 {
		t.Fatalf("%v %s", err, out.String())
	}
}

func TestFarmShowWithoutWindow(t *testing.T) {
	h := home(t)
	d := testDeps()
	d.Spaces = func(string) ([]Space, error) { return nil, errors.New("no window") }
	d.HC = func() (hc.Client, error) { return nil, errors.New("no hc") }
	var out bytes.Buffer
	if err := RunFarm([]string{"show", "--home", h}, &out, d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pen sp1 (unnamed)") || !strings.Contains(out.String(), "warning: hc:") || !strings.Contains(out.String(), "t3 Sandy (builder) gone") {
		t.Fatal(out.String())
	}
}

func TestParseSpaces(t *testing.T) {
	sp, err := parseSpaces([]byte(`{"spaces":[{"id":"s1","name":"n","folder":"/f","tabs":[{"terminals":[{"terminal":"t1"}]},{"terminals":[{"terminal":"t2"}]}]}]}`))
	if err != nil || len(sp) != 1 || sp[0].Name != "n" || strings.Join(sp[0].Terminals, ",") != "t1,t2" {
		t.Fatal(sp, err)
	}
}
