package farmcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"pigtorio/internal/hc"
)

// Entity is one entry of farm.json `entities`.
type Entity struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	Space string `json:"space,omitempty"` // pens
	Pen   string `json:"pen,omitempty"`   // stations: the pen's space, "" on open ground
}

// Belt is one entry of farm.json `belts`.
type Belt struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// PenConf is farm.json `pens[space]`.
type PenConf struct {
	Test   string `json:"test"`
	Folder string `json:"folder"`
}

// FarmPig is farm.json `pigs[addr]`.
type FarmPig struct {
	Name string `json:"name"`
	Role string `json:"role"`
	Skin string `json:"skin"`
}

// Stop and Line mirror farm.json `lines`.
type Stop struct {
	Kind   string `json:"kind"`
	Pig    string `json:"pig,omitempty"`
	Entity string `json:"entity,omitempty"`
}
type Line struct {
	ID    string `json:"id"`
	Pen   string `json:"pen"`
	Stops []Stop `json:"stops"`
}

// Farm is farm.json as far as the CLI reads it.
type Farm struct {
	Pens     map[string]PenConf `json:"pens"`
	Pigs     map[string]FarmPig `json:"pigs"`
	Lines    []Line             `json:"lines"`
	Entities map[string]Entity  `json:"entities"`
	Belts    []Belt             `json:"belts"`
}

func readFarm(d string) (*Farm, error) {
	var f Farm
	b, err := os.ReadFile(filepath.Join(d, "farm.json"))
	if errors.Is(err, os.ErrNotExist) {
		return &f, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, errors.New("farm.json: " + err.Error())
	}
	return &f, nil
}

// penEntity returns the pen entity of a space.
func (f *Farm) penEntity(space string) (Entity, bool) {
	for _, e := range f.Entities {
		if e.Kind == "pen" && e.Space == space {
			return e, true
		}
	}
	return Entity{}, false
}

// Space is one Herdr space as `herdr-app ls` reports it.
type Space struct {
	ID        string
	Name      string
	Folder    string
	Terminals []string
}

// Deps are the outside world of the commands, replaceable in tests.
type Deps struct {
	HC     func() (hc.Client, error)
	Spaces func(home string) ([]Space, error)
}

// DefaultDeps talks to the real hc and herdr-app.
func DefaultDeps() Deps {
	return Deps{
		HC:     func() (hc.Client, error) { return hc.NewCLI() },
		Spaces: herdrSpaces,
	}
}

// herdrSpaces runs `herdr-app ls` against home. It fails when no window runs.
func herdrSpaces(home string) ([]Space, error) {
	bin, err := exec.LookPath("herdr-app")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "ls")
	cmd.Env = append(os.Environ(), "HERDR_APP_HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseSpaces(out)
}

func parseSpaces(out []byte) ([]Space, error) {
	var w struct {
		Spaces []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Folder string `json:"folder"`
			Tabs   []struct {
				Terminals []struct {
					Terminal string `json:"terminal"`
				} `json:"terminals"`
			} `json:"tabs"`
		} `json:"spaces"`
	}
	if err := json.Unmarshal(out, &w); err != nil {
		return nil, err
	}
	var res []Space
	for _, s := range w.Spaces {
		sp := Space{ID: s.ID, Name: s.Name, Folder: s.Folder}
		for _, t := range s.Tabs {
			for _, tm := range t.Terminals {
				sp.Terminals = append(sp.Terminals, tm.Terminal)
			}
		}
		res = append(res, sp)
	}
	return res, nil
}
