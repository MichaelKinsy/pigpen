package pi_goal_x

// The commands (goal-commands.ts) this port runs: /goal-direct, /sisyphus-direct, /goal-list, /goal-pause, /goal-clear and
// /goal-unfocus. Every other command of the original answers with the refusal (refusal.go). registerGoalCommands is the seam the
// GoalCommands slice fills with the rest of goal-commands.ts and goal-drafting.ts.

// registerGoalCommands registers the commands on the core's extension.
func registerGoalCommands(c *core) {
	for _, d := range commandDefs {
		c.pi.command(d.name, d.desc, d.fn)
	}
	for _, d := range unportedCommands {
		feature := d.feature
		c.pi.command(d.name, d.desc, func(c *core, _ string) error {
			c.h.Notify(refusal(feature), "error")
			return nil
		})
	}
}

// commandDefs are the commands this port registers; the tests drive the same table.
var commandDefs = []struct {
	name, desc string
	fn         func(c *core, args string) error
}{
	{"goal-direct", "Create and start a regular goal immediately, without drafting.", func(c *core, args string) error { return c.direct(args, false) }},
	{"sisyphus-direct", "Create and start a Sisyphus goal immediately, without drafting.", func(c *core, args string) error { return c.direct(args, true) }},
	{"goal-list", "List all open goals and the current focus.", func(c *core, _ string) error { c.list(); return nil }},
	{"goal-pause", "Pause the currently running goal. Esc also pauses while running.", func(c *core, _ string) error { c.pause(); return nil }},
	{"goal-clear", "Archive the current goal after confirmation (user-owned abandonment).", func(c *core, _ string) error { c.clear(); return nil }},
	{"goal-unfocus", "Stop focusing the current goal (session only; goal stays open).", func(c *core, _ string) error { c.unfocus(); return nil }},
}

func (c *core) chooseOpenGoal(title string) *jsObject {
	c.reconcileFocusedGoalFromDisk(false)
	if g := c.state(); g != nil && gstr(g, "status") != "complete" {
		return g
	}
	open := c.openGoals()
	if len(open) == 0 {
		return nil
	}
	if len(open) == 1 {
		c.setFocusedGoalId(new(gstr(open[0], "id")), "selected", true)
		return c.state()
	}
	if !c.h.HasUI() {
		c.h.Notify(buildUnfocusedOpenGoalsSummary(len(open)), "warning")
		return nil
	}
	labels := make([]string, len(open))
	for i, g := range open {
		labels[i] = goalSelectorLabel(g, c.focused)
	}
	c.flush()
	picked, ok := c.h.Select(title, labels)
	var selected *string
	if ok {
		for i, l := range labels {
			if l == picked {
				selected = new(gstr(open[i], "id"))
				break
			}
		}
	}
	if selected == nil {
		c.h.Notify("Goal focus unchanged.", "info")
		return nil
	}
	c.setFocusedGoalId(selected, "selected", true)
	return c.state()
}

func (c *core) direct(raw string, sisyphus bool) error {
	raw = jsTrim(raw)
	if raw == "" {
		cmd := "/goal <objective>"
		if sisyphus {
			cmd = "/sisyphus <objective>"
		}
		c.h.Notify("No objective provided. Use "+cmd+".", "warning")
		return nil
	}
	if sisyphus && !sisyphusObjectiveSufficient(raw) {
		c.h.Notify("A Sisyphus objective needs ordered steps with per-step done criteria. Use /sisyphus for guided drafting, or provide numbered steps (1) ..., 2) ...) in the objective.", "warning")
		return nil
	}
	if keys := unportedSettings(c.h.Cwd()); len(keys) > 0 {
		c.h.Notify(refusal(unportedSettingsFeature(keys)), "error")
		return nil
	}
	if limit := loadGoalSettings(c.h.Cwd()).maxAutonomousRuns; limit == nil || *limit != 0 {
		c.h.Notify(refusal(continuationFeature), "error")
		return nil
	}
	objective, contract := raw, ""
	if !loadGoalSettings(c.h.Cwd()).disableContracts {
		objective, contract = extractVerificationContract(raw)
	}
	c.replaceGoal(goalCreationConfig{objective: objective, autoContinue: true, sisyphus: sisyphus}, true, contract, -1)
	return nil
}

func (c *core) list() {
	c.reconcileFocusedGoalFromDisk(false)
	c.h.Notify(buildGoalListText(c.goals, c.focused), "info")
	c.updateUI()
}

func (c *core) pause() {
	c.reconcileFocusedGoalFromDisk(false)
	if c.state() == nil {
		if otherOpenGoalCount(c.goals, nil) > 0 {
			picked := c.chooseOpenGoal("Pause which open goal?")
			c.flush() // the original awaits here, which lets the queued UI update run
			if picked == nil {
				return
			}
		} else {
			c.h.Notify("No goal is set.", "warning")
			return
		}
	}
	g := c.state()
	if g == nil {
		return
	}
	switch gstr(g, "status") {
	case "complete":
		c.h.Notify("Goal is complete.", "warning")
		return
	case "paused":
		c.h.Notify("Goal is already paused. Use /goal-resume to continue.", "info")
		return
	}
	c.pauseActiveGoal()
}

func (c *core) clear() {
	c.reconcileFocusedGoalFromDisk(false)
	if c.state() == nil && otherOpenGoalCount(c.goals, nil) > 0 {
		picked := c.chooseOpenGoal("Clear which open goal?")
		c.flush()
		if picked == nil {
			return
		}
	}
	target := c.state()
	if target == nil {
		c.h.Notify(clearGoalCommandMessage(false), "warning")
		return
	}
	id, rev := gstr(target, "id"), c.focusRev
	if !c.h.HasUI() {
		c.h.Notify("Run /goal-clear in an interactive session to confirm clearing: "+oneLineSummary(target), "warning")
		return
	}
	c.flush() // awaiting the dialog lets a queued UI update run first
	if !c.h.Confirm("Clear goal?", oneLineSummary(target)) {
		c.h.Notify("Goal clear cancelled.", "info")
		return
	}
	c.reconcileFocusedGoalFromDisk(false)
	cur := c.state()
	if c.focused == nil || *c.focused != id || c.focusRev != rev || cur == nil || gstr(cur, "id") != id {
		c.h.Notify("Goal changed while confirming; nothing was cleared.", "warning")
		return
	}
	archived := c.archiveCurrentGoal("user")
	c.setGoal(nil, true, "cleared")
	msg := clearGoalCommandMessage(archived != nil)
	level := "info"
	if archived == nil {
		level = "warning"
	}
	c.h.Notify(msg, level)
}

func (c *core) unfocus() {
	current := c.state()
	var runtimeID string
	if current != nil {
		runtimeID = gstr(current, "id")
	}
	c.reconcileFocusedGoalFromDisk(false)
	current = c.state()
	detached := runtimeID
	if current != nil {
		detached = gstr(current, "id")
	}
	busy := !c.h.IsIdle()
	c.setFocusedGoalId(nil, "unfocused", false)
	if detached != "" && busy {
		c.h.Abort()
	}
	if current == nil {
		if n := otherOpenGoalCount(c.goals, nil); n > 0 {
			c.h.Notify(buildUnfocusedOpenGoalsSummary(n), "info")
		} else {
			c.h.Notify(detailedSummary(nil), "info")
		}
		return
	}
	c.h.Notify("Goal unfocused for this session. It remains open in .pi/goals: "+gstr(current, "id"), "info")
}
