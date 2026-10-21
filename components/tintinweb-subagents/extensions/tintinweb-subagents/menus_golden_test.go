package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The original's drawing of the workflow card, the workflow dialog and pi-tui's SettingsList on fixed inputs
// (testdata/menus-fixtures.json), recorded by port/record-menus.spec.ts under Pi's own pi-tui into
// testdata/menus-golden.json, compared here line by line, markup included (the theme tags every colour and bold span).

type menusFixtures struct {
	Cards []struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"cards"`
	Dialogs []struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"dialogs"`
	SettingsLists []struct {
		Name       string `json:"name"`
		Items      []struct {
			ID           string   `json:"id"`
			Label        string   `json:"label"`
			Description  string   `json:"description"`
			CurrentValue string   `json:"currentValue"`
			Values       []string `json:"values"`
		} `json:"items"`
		MaxVisible int `json:"maxVisible"`
		Downs      int `json:"downs"`
		Ups        int `json:"ups"`
		Width      int `json:"width"`
	} `json:"settingsLists"`
}

type fixtureEntries []workflowEntry

func (f *fixtureEntries) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return err
	}
	for _, raw := range raws {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return err
		}
		var e workflowEntry
		switch head.Type {
		case "workflow_phase":
			e = &workflowPhaseEntry{}
		case "workflow_log":
			e = &workflowLogEntry{}
		default:
			e = &workflowAgentEntry{}
		}
		if err := json.Unmarshal(raw, e); err != nil {
			return err
		}
		*f = append(*f, e)
	}
	return nil
}

type fixtureTask struct {
	Status        string `json:"status"`
	WorkflowName  string `json:"workflowName"`
	StartTime     int64  `json:"startTime"`
	EndTime       *int64 `json:"endTime"`
	TotalPausedMs int64  `json:"totalPausedMs"`
}

func (t fixtureTask) card() workflowCardTask {
	return workflowCardTask{Status: t.Status, WorkflowName: t.WorkflowName, StartTime: t.StartTime, EndTime: t.EndTime, TotalPausedMs: t.TotalPausedMs}
}

func TestMenusMatchTheOriginalsDrawing(t *testing.T) {
	var fixtures menusFixtures
	data, err := os.ReadFile("testdata/menus-fixtures.json")
	eq(t, err, nil)
	eq(t, json.Unmarshal(data, &fixtures), nil)
	var golden struct {
		Cards         map[string][]string `json:"cards"`
		Dialogs       map[string][]string `json:"dialogs"`
		SettingsLists map[string][]string `json:"settingsLists"`
	}
	data, err = os.ReadFile("testdata/menus-golden.json")
	eq(t, err, nil)
	eq(t, json.Unmarshal(data, &golden), nil)
	same := func(t *testing.T, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%d lines, the original draws %d:\n%s\n---\n%s", len(got), len(want), strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("line %d:\n got %q\nwant %q", i, got[i], want[i])
			}
		}
	}
	for _, c := range fixtures.Cards {
		t.Run("card "+c.Name, func(t *testing.T) {
			var in struct {
				Progress      fixtureEntries `json:"progress"`
				Task          fixtureTask    `json:"task"`
				Meta          *workflowMeta  `json:"meta"`
				AgentCount    int            `json:"agentCount"`
				Width         int            `json:"width"`
				Now           int64          `json:"now"`
				ASCII         bool           `json:"ascii"`
				ShowToolTitle bool           `json:"showToolTitle"`
			}
			eq(t, json.Unmarshal(c.Input, &in), nil)
			lines := layoutWorkflowCard(workflowCardInput{Progress: in.Progress, Task: in.Task.card(), Meta: in.Meta, AgentCount: in.AgentCount,
				Width: in.Width, Now: in.Now, ASCII: in.ASCII, ShowToolTitle: in.ShowToolTitle})
			same(t, styleWorkflowCardLines(lines, tagTheme()), golden.Cards[c.Name])
		})
	}
	for _, d := range fixtures.Dialogs {
		t.Run("dialog "+d.Name, func(t *testing.T) {
			var in struct {
				Progress     fixtureEntries `json:"progress"`
				Task         fixtureTask    `json:"task"`
				Meta         *workflowMeta  `json:"meta"`
				AgentCount   int            `json:"agentCount"`
				Width        int            `json:"width"`
				Now          int64          `json:"now"`
				SpinnerFrame int            `json:"spinnerFrame"`
				ASCII        bool           `json:"ascii"`
				BodyRows     int            `json:"bodyRows"`
				State        struct {
					SelectedPhase  int    `json:"selectedPhase"`
					SelectedAgent  int    `json:"selectedAgent"`
					Level          string `json:"level"`
					Filter         string `json:"filter"`
					PromptExpanded bool   `json:"promptExpanded"`
				} `json:"state"`
			}
			eq(t, json.Unmarshal(d.Input, &in), nil)
			lines := layoutWorkflowDialog(workflowDialogInput{
				workflowDialogSource: workflowDialogSource{Progress: in.Progress, Task: in.Task.card(), Meta: in.Meta, AgentCount: in.AgentCount},
				State:                workflowDialogState{in.State.SelectedPhase, in.State.SelectedAgent, in.State.Level, in.State.Filter, in.State.PromptExpanded},
				Width:                in.Width, Now: in.Now, SpinnerFrame: in.SpinnerFrame, ASCII: in.ASCII, BodyRows: in.BodyRows})
			same(t, styleWorkflowCardLines(lines, tagTheme()), golden.Dialogs[d.Name])
		})
	}
	for _, s := range fixtures.SettingsLists {
		t.Run("settings list "+s.Name, func(t *testing.T) {
			var items []*settingItem
			for _, it := range s.Items {
				items = append(items, &settingItem{ID: it.ID, Label: it.Label, Description: it.Description, CurrentValue: it.CurrentValue, Values: it.Values})
			}
			list := newSettingsList(items, s.MaxVisible, settingsListThemeFor(tagTheme()), func(string, string) {}, func() {})
			for range s.Downs {
				list.HandleInput("\x1b[B")
			}
			for range s.Ups {
				list.HandleInput("\x1b[A")
			}
			same(t, list.Render(s.Width), golden.SettingsLists[s.Name])
		})
	}
}
