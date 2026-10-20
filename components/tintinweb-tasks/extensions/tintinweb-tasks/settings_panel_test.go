package tintinweb_tasks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	tintinweb_tasks "github.com/MichaelKinsy/pigpen/tintinweb-tasks"
)

// panelParts returns the three children of the panel's root: the title, the spacer and the SettingsList.
func panelParts(t *testing.T, cfg obj) (title *kit.Text, spacer *kit.Spacer, list *kit.SettingsList, view kit.View, cwd string) {
	t.Helper()
	cwd = t.TempDir()
	view = tintinweb_tasks.NewSettingsPanel(cfg, cwd, t.TempDir()).View(80)
	root, ok := view.Root.(*kit.Container)
	if !ok || len(root.Children) != 3 {
		t.Fatalf("the panel's root is not a Container of three children: %#v", view.Root)
	}
	title, _ = root.Children[0].(*kit.Text)
	spacer, _ = root.Children[1].(*kit.Spacer)
	list, _ = root.Children[2].(*kit.SettingsList)
	if title == nil || spacer == nil || list == nil {
		t.Fatalf("the panel's children are %T %T %T, want Text, Spacer, SettingsList", root.Children[0], root.Children[1], root.Children[2])
	}
	return
}

func TestSettingsPanelLayout(t *testing.T) {
	// upstream: ui/settings-menu.ts: a Text bold accent "⚙  Task Settings" (padding 0 0), Spacer(1), the SettingsList
	// with maxVisible 10, all inside a Container.
	title, spacer, list, view, _ := panelParts(t, obj{})
	eqv(t, strings.Contains(title.Text, "⚙  Task Settings"), true)
	eqv(t, strings.HasPrefix(title.Text, "\x1b[1m"), true) // theme.bold
	eqv(t, [2]int{title.PaddingX, title.PaddingY}, [2]int{0, 0})
	eqv(t, spacer.Lines, 1)
	eqv(t, list.MaxVisible, 10)
	eqv(t, list.EnableSearch, false)
	eqv(t, view.Focus, list.ID) // the list receives the keys it binds
	if list.ID == "" {
		t.Fatal("the SettingsList needs an ID")
	}
}

func TestSettingsPanelItems(t *testing.T) {
	_, _, list, _, _ := panelParts(t, obj{})
	type row struct{ id, label, current string }
	var got []row
	for _, it := range list.Items {
		got = append(got, row{it.ID, it.Label, it.CurrentValue})
	}
	eqv(t, got, []row{
		{"taskScope", "Task storage", "session"},
		{"autoCascade", "Auto-cascade agent tasks", "off"},
		{"collapseCompleted", "Collapse completed tasks", "off"},
		{"showAll", "Show all tasks in widget", "off"},
		{"maxVisible", "Max visible tasks in widget", "10"},
		{"sortOrder", "Widget sort order", "id"},
		{"hiddenAt", "Hidden tasks position", "bottom"},
		{"autoClearCompleted", "Auto-clear completed tasks", "on_list_complete"},
	})
	values := map[string][]string{}
	for _, it := range list.Items {
		values[it.ID] = it.Values
	}
	eqv(t, values["taskScope"], []string{"memory", "session", "session-global", "project"})
	eqv(t, values["autoCascade"], []string{"on", "off"})
	eqv(t, values["collapseCompleted"], []string{"on", "off"})
	eqv(t, values["showAll"], []string{"on", "off"})
	// The original offers 5 to 100 here; the port used to stop at 10 because the host widget showed ten rows.
	eqv(t, values["maxVisible"], []string{"5", "10", "15", "20", "30", "50", "100"})
	eqv(t, values["sortOrder"], []string{"id", "status", "active", "recent", "oldest"})
	eqv(t, values["hiddenAt"], []string{"bottom", "top"})
	eqv(t, values["autoClearCompleted"], []string{"never", "on_list_complete", "on_task_complete"})
}

func TestSettingsPanelDescriptions(t *testing.T) {
	_, _, list, _, _ := panelParts(t, obj{})
	want := map[string]string{
		"taskScope": "memory: tasks live only in memory, lost when session ends. " +
			"session: persisted per session in the workspace (.pi/tasks/tasks-<sessionId>.json), survives resume. " +
			"session-global: same, but kept outside the workspace under pi's agent directory. " +
			"project: shared across all sessions (tasks.json). " +
			"Takes effect on next session start.",
		"autoCascade": "When ON: pending agent tasks start automatically once their dependencies complete. " +
			"When OFF: use TaskExecute to launch them manually.",
		"collapseCompleted": "When ON, completed tasks are replaced by a single 'N completed' line and the " +
			"visible limit applies only to the tasks left. When OFF, they are listed individually.",
		"showAll": "When ON, every listed task is shown regardless of the visible limit. " +
			"When OFF, the list is capped by 'Max visible tasks'.",
		"maxVisible": "Only applies when 'Show all tasks' is OFF. " +
			"Caps how many task lines the widget shows.",
		"sortOrder": `"active" groups by in-progress → pending → completed; "status" is the reverse. ` +
			`"id" sorts by creation order. A custom sort spec in tasks-config.json shows as ` +
			`"custom" and can only be changed there.`,
		"hiddenAt": `"bottom" hides tasks from the end of the list. ` +
			`"top" hides tasks from the start (useful with status sort to collapse completed tasks).`,
		"autoClearCompleted": "never: completed tasks stay visible until manually cleared. " +
			"on_list_complete: cleared automatically after all tasks are done. " +
			"on_task_complete: each task cleared shortly after it completes. " +
			"Clearing lags ~4 turns, or happens right away when a later batch of work starts.",
	}
	for _, it := range list.Items {
		eqv(t, it.Description, want[it.ID])
	}
}

func TestSettingsPanelShowsTheEffectiveConfig(t *testing.T) {
	_, _, list, _, _ := panelParts(t, obj{
		"taskScope": "project", "autoCascade": true, "collapseCompleted": true, "showAll": true,
		"maxVisible": float64(30), "sortOrder": "status", "hiddenAt": "top", "autoClearCompleted": "never",
	})
	var got []string
	for _, it := range list.Items {
		got = append(got, it.CurrentValue)
	}
	eqv(t, got, []string{"project", "on", "on", "on", "30", "status", "top", "never"})
}

func TestSettingsPanelCustomSortSpecIsShownButNotCycled(t *testing.T) {
	_, _, list, _, _ := panelParts(t, obj{"sortOrder": arr{obj{"by": "id"}}})
	for _, it := range list.Items {
		if it.ID == "sortOrder" {
			eqv(t, it.CurrentValue, "custom")
			eqv(t, it.Values, []string{"id", "status", "active", "recent", "oldest"})
		}
	}
}

func TestSettingsPanelSavesEachChange(t *testing.T) {
	changes := []struct {
		id, value string
		key       string
		want      any
	}{
		{"taskScope", "project", "taskScope", "project"},
		{"autoCascade", "on", "autoCascade", true},
		{"collapseCompleted", "on", "collapseCompleted", true},
		{"showAll", "on", "showAll", true},
		{"maxVisible", "50", "maxVisible", float64(50)},
		{"sortOrder", "status", "sortOrder", "status"},
		{"hiddenAt", "top", "hiddenAt", "top"},
		{"autoClearCompleted", "never", "autoClearCompleted", "never"},
	}
	for _, c := range changes {
		t.Run(c.id, func(t *testing.T) {
			cwd, agent := t.TempDir(), t.TempDir()
			panel := tintinweb_tasks.NewSettingsPanel(obj{}, cwd, agent)
			res, err := panel.HandleViewEvent(kit.Event{Node: "x", Type: kit.EventChange, ID: c.id, Value: c.value})
			if err != nil || res.Done {
				t.Fatalf("a change must save and keep the panel open: %+v %v", res, err)
			}
			data, err := os.ReadFile(filepath.Join(cwd, ".pi", "tasks-config.json"))
			if err != nil {
				t.Fatalf("nothing saved: %v", err)
			}
			var saved map[string]any
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			eqv(t, saved[c.key], c.want)
		})
	}
	t.Run("a value switched back off is saved too", func(t *testing.T) {
		cwd := t.TempDir()
		global := t.TempDir()
		writeConfig(t, filepath.Join(global, "tasks-config.json"), obj{"autoCascade": true})
		panel := tintinweb_tasks.NewSettingsPanel(obj{"autoCascade": true}, cwd, global)
		panel.HandleViewEvent(kit.Event{Type: kit.EventChange, ID: "autoCascade", Value: "off"})
		data, _ := os.ReadFile(filepath.Join(cwd, ".pi", "tasks-config.json"))
		eqv(t, strings.Contains(string(data), `"autoCascade": false`), true)
	})
	t.Run("an unknown setting is ignored", func(t *testing.T) {
		cwd := t.TempDir()
		panel := tintinweb_tasks.NewSettingsPanel(obj{}, cwd, t.TempDir())
		res, err := panel.HandleViewEvent(kit.Event{Type: kit.EventChange, ID: "nonsense", Value: "on"})
		eqv(t, [2]any{res.Done, err}, [2]any{false, error(nil)})
		_, statErr := os.Stat(filepath.Join(cwd, ".pi", "tasks-config.json"))
		eqv(t, os.IsNotExist(statErr), true)
	})
}

func TestSettingsPanelCancelClosesIt(t *testing.T) {
	panel := tintinweb_tasks.NewSettingsPanel(obj{}, t.TempDir(), t.TempDir())
	res, err := panel.HandleViewEvent(kit.Event{Node: "x", Type: kit.EventCancel})
	eqv(t, [2]any{res.Done, err}, [2]any{true, error(nil)})
	// Keys the list does not bind are ignored: the original's panel forwards its input to the list alone.
	res, err = panel.HandleInput("x")
	eqv(t, [2]any{res.Done, err}, [2]any{false, error(nil)})
}

func TestSettingsPanelViewDoesNotChangeUnderTheHost(t *testing.T) {
	// The host's SettingsList owns the shown values, as Pi's does; the extension must not send a changed list
	// back for the host to reset (upstream builds the items once).
	cwd := t.TempDir()
	panel := tintinweb_tasks.NewSettingsPanel(obj{}, cwd, t.TempDir())
	before := panel.View(80)
	panel.HandleViewEvent(kit.Event{Type: kit.EventChange, ID: "maxVisible", Value: "50"})
	after := panel.View(80)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	eqv(t, string(a), string(b))
}
