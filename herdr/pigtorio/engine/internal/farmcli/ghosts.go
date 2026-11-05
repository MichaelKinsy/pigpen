// Package farmcli implements `pigtorio farm show` and `pigtorio ghost …`: the commands the Runt
// uses to read the farm and draft ghosts (changes nothing builds until a person presses Build).
package farmcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Ghost is one drafted change in ghosts.json.
type Ghost struct {
	ID    string      `json:"id"`
	Op    string      `json:"op"` // place | belt | crate
	Kind  string      `json:"kind,omitempty"`
	Pen   string      `json:"pen,omitempty"`
	Near  string      `json:"near,omitempty"`
	X     *int        `json:"x,omitempty"`
	Y     *int        `json:"y,omitempty"`
	From  string      `json:"from,omitempty"`
	To    string      `json:"to,omitempty"`
	Crate *GhostCrate `json:"crate,omitempty"`
}

// GhostCrate is the crate of a crate ghost.
type GhostCrate struct {
	Title    string `json:"title"`
	DoneWhen string `json:"doneWhen"`
	Text     string `json:"text"`
	Size     string `json:"size"`
	Pen      string `json:"pen,omitempty"`
}

// Draft is ghosts.json.
type Draft struct {
	Note   string  `json:"note,omitempty"`
	By     string  `json:"by"`
	Ghosts []Ghost `json:"ghosts"`
}

// Paths of the draft and its lock inside D.
func draftPath(d string) string { return filepath.Join(d, "ghosts.json") }
func lockPath(d string) string  { return filepath.Join(d, "ghosts.lock") }

// readDraft reads D/ghosts.json; a missing file is an empty draft.
func readDraft(d string) (*Draft, error) {
	b, err := os.ReadFile(draftPath(d))
	if errors.Is(err, os.ErrNotExist) {
		return &Draft{Ghosts: []Ghost{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var dr Draft
	if err := json.Unmarshal(b, &dr); err != nil {
		return nil, fmt.Errorf("ghosts.json: %w", err)
	}
	if dr.Ghosts == nil {
		dr.Ghosts = []Ghost{}
	}
	return &dr, nil
}

// writeDraft replaces D/ghosts.json atomically (temp + rename).
func writeDraft(d string, dr *Draft) error {
	b, err := json.MarshalIndent(dr, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(d, ".ghosts.json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, draftPath(d))
}

// withDraft locks D/ghosts.lock, reads the draft, runs fn on it and, if fn returns true, writes it back.
func withDraft(d string, fn func(*Draft) (bool, error)) error {
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(lockPath(d), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	dr, err := readDraft(d)
	if err != nil {
		return err
	}
	save, err := fn(dr)
	if err != nil || !save {
		return err
	}
	return writeDraft(d, dr)
}

// nextID returns g<n> with n one past the highest g-number in the draft.
func (dr *Draft) nextID() string {
	n := 0
	for _, g := range dr.Ghosts {
		if v, err := strconv.Atoi(strings.TrimPrefix(g.ID, "g")); err == nil && v > n {
			n = v
		}
	}
	return "g" + strconv.Itoa(n+1)
}

func (dr *Draft) find(id string) *Ghost {
	for i := range dr.Ghosts {
		if dr.Ghosts[i].ID == id {
			return &dr.Ghosts[i]
		}
	}
	return nil
}

// String is a one-line description for `ghost list`.
func (g Ghost) String() string {
	switch g.Op {
	case "place":
		s := g.ID + " place " + g.Kind
		if g.Pen != "" {
			s += " pen=" + g.Pen
		}
		if g.Near != "" {
			s += " near=" + g.Near
		}
		if g.X != nil && g.Y != nil {
			s += fmt.Sprintf(" at=%d,%d", *g.X, *g.Y)
		}
		return s
	case "belt":
		return g.ID + " belt " + g.From + " -> " + g.To
	case "crate":
		if g.Crate == nil {
			return g.ID + " crate"
		}
		s := fmt.Sprintf("%s crate %q (%s) done when: %s", g.ID, g.Crate.Title, g.Crate.Size, g.Crate.DoneWhen)
		if g.Crate.Pen != "" {
			s += " pen=" + g.Crate.Pen
		}
		return s
	}
	return g.ID + " " + g.Op
}
