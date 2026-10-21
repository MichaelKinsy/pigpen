package tintinweb_tasks

import (
	"strconv"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// The /tasks → Settings panel. upstream: ui/settings-menu.ts. The original opens a pi-tui SettingsList through
// ui.custom(); the port opens the same list as a kit view (the host renders and drives it, as Pi does, and reports
// each change and the cancel). Picking a row's value saves it as a project override at once.

// settingsListID names the list; the view's focus and the host's events carry it.
const settingsListID = "task-settings"

// settingsMaxVisible is the SettingsList's visible rows. upstream: settings-menu.ts (maxVisible 10).
const settingsMaxVisible = 10

// settingRow is one row of the settings panel. upstream: ui/settings-menu.ts items.
type settingRow struct {
	id, label, description string
	current                func(cfg tasksConfig) string
	values                 []string
	apply                  func(cfg tasksConfig, v string)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func settingRows() []settingRow {
	return []settingRow{
		{"taskScope", "Task storage",
			"memory: tasks live only in memory, lost when session ends. " +
				"session: persisted per session in the workspace (.pi/tasks/tasks-<sessionId>.json), survives resume. " +
				"session-global: same, but kept outside the workspace under pi's agent directory. " +
				"project: shared across all sessions (tasks.json). " +
				"Takes effect on next session start.",
			func(c tasksConfig) string { return c.str("taskScope", "session") },
			[]string{"memory", "session", "session-global", "project"}, func(c tasksConfig, v string) { c["taskScope"] = v }},
		{"autoCascade", "Auto-cascade agent tasks",
			"When ON: pending agent tasks start automatically once their dependencies complete. " +
				"When OFF: use TaskExecute to launch them manually.",
			func(c tasksConfig) string { return onOff(c.flag("autoCascade")) },
			[]string{"on", "off"}, func(c tasksConfig, v string) { c["autoCascade"] = v == "on" }},
		{"collapseCompleted", "Collapse completed tasks",
			"When ON, completed tasks are replaced by a single 'N completed' line and the " +
				"visible limit applies only to the tasks left. When OFF, they are listed individually.",
			func(c tasksConfig) string { return onOff(c.flag("collapseCompleted")) },
			[]string{"on", "off"}, func(c tasksConfig, v string) { c["collapseCompleted"] = v == "on" }},
		{"showAll", "Show all tasks in widget",
			"When ON, every listed task is shown regardless of the visible limit. " +
				"When OFF, the list is capped by 'Max visible tasks'.",
			func(c tasksConfig) string { return onOff(c.flag("showAll")) },
			[]string{"on", "off"}, func(c tasksConfig, v string) { c["showAll"] = v == "on" }},
		{"maxVisible", "Max visible tasks in widget",
			"Only applies when 'Show all tasks' is OFF. " +
				"Caps how many task lines the widget shows.",
			func(c tasksConfig) string {
				switch n := c["maxVisible"].(type) {
				case float64:
					return strconv.FormatFloat(n, 'f', -1, 64)
				case int:
					return strconv.Itoa(n)
				}
				return "10"
			}, []string{"5", "10", "15", "20", "30", "50", "100"}, func(c tasksConfig, v string) { n, _ := strconv.Atoi(v); c["maxVisible"] = float64(n) }},
		{"sortOrder", "Widget sort order",
			`"active" groups by in-progress → pending → completed; "status" is the reverse. ` +
				`"id" sorts by creation order. A custom sort spec in tasks-config.json shows as ` +
				`"custom" and can only be changed there.`,
			func(c tasksConfig) string {
				// A sort spec can't be reconstructed by cycling a menu value, so it is shown read-only; picking a
				// preset is the deliberate way to drop it. "custom" is left out of the cycle.
				if _, custom := c["sortOrder"].([]any); custom {
					return "custom"
				}
				return c.str("sortOrder", "id")
			}, builtInSortOrders, func(c tasksConfig, v string) { c["sortOrder"] = v }},
		{"hiddenAt", "Hidden tasks position",
			`"bottom" hides tasks from the end of the list. ` +
				`"top" hides tasks from the start (useful with status sort to collapse completed tasks).`,
			func(c tasksConfig) string { return c.str("hiddenAt", "bottom") },
			[]string{"bottom", "top"}, func(c tasksConfig, v string) { c["hiddenAt"] = v }},
		{"autoClearCompleted", "Auto-clear completed tasks",
			"never: completed tasks stay visible until manually cleared. " +
				"on_list_complete: cleared automatically after all tasks are done. " +
				"on_task_complete: each task cleared shortly after it completes. " +
				"Clearing lags ~" + strconv.Itoa(autoClearDelay) + " turns, or happens right away when a later batch of work starts.",
			func(c tasksConfig) string { return c.str("autoClearCompleted", "on_list_complete") },
			[]string{"never", "on_list_complete", "on_task_complete"}, func(c tasksConfig, v string) { c["autoClearCompleted"] = v }},
	}
}

// settingsPanel is the component /tasks → Settings opens: a bold accent title, a spacer and the SettingsList,
// built once as the original builds its items once.
type settingsPanel struct {
	a        *app
	cwd      string
	agentDir string
	rows     []settingRow
	view     kit.View
}

func (a *app) newSettingsPanel(cwd, agentDir string, theme sdk.UITheme) *settingsPanel {
	p := &settingsPanel{a: a, cwd: cwd, agentDir: agentDir, rows: settingRows()}
	a.mu.Lock()
	items := make([]kit.SettingItem, len(p.rows))
	for i, r := range p.rows {
		items[i] = kit.SettingItem{ID: r.id, Label: r.label, Description: r.description, CurrentValue: r.current(a.cfg), Values: r.values}
	}
	a.mu.Unlock()
	list := kit.NewSettingsList(settingsListID, items, settingsMaxVisible)
	p.view = kit.View{
		Root: kit.NewContainer(
			kit.NewText(theme.Bold(theme.Fg("accent", "⚙  Task Settings")), 0, 0),
			kit.NewSpacer(1),
			list,
		),
		Focus: settingsListID,
	}
	return p
}

func (p *settingsPanel) View(int) kit.View { return p.view }

// HandleInput ignores the keys the list does not bind: the original's panel forwards its input to the list alone.
func (p *settingsPanel) HandleInput(string) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{}, nil
}

// HandleViewEvent is the SettingsList's onChange (apply and save the project override) and onCancel (close).
func (p *settingsPanel) HandleViewEvent(event kit.Event) (sdk.RemoteComponentResult, error) {
	switch event.Type {
	case kit.EventCancel:
		return sdk.RemoteComponentResult{Done: true}, nil
	case kit.EventChange:
		for _, r := range p.rows {
			if r.id == event.ID {
				p.a.mu.Lock()
				r.apply(p.a.cfg, event.Value)
				_ = saveTasksConfig(p.a.cfg, p.cwd, p.agentDir)
				p.a.mu.Unlock()
				break
			}
		}
	}
	return sdk.RemoteComponentResult{}, nil
}

// settingsMenu is /tasks → Settings: it opens the panel and returns when it is closed. upstream: openSettingsMenu.
func (a *app) settingsMenu(ctx sdk.Context) error {
	_, err := ctx.Custom(a.newSettingsPanel(ctx.Cwd(), agentDir(), ctx.UITheme()), nil)
	return err
}
