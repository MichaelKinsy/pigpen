package pi_goal_x

// The goal-widget.ts glue that needs the core: the terminal input listener (Escape / chords) bound to this core's state.

// coreWidgetCore is the core as goalWidgetCore (the interface the keybinding handler reads at event time).
type coreWidgetCore struct{ c *core }

func (a coreWidgetCore) removeDebugFile(_ goalDialogContext, path string) {
	a.c.goalService.removeDebugFile(a.c.st, path)
}
func (a coreWidgetCore) writeDebugFile(_ goalDialogContext, path, content string) {
	_ = a.c.goalService.writeDebugFile(a.c.st, path, content)
}
func (a coreWidgetCore) setGoal(_ goalDialogContext, goal *jsObject, persist bool, reason string) {
	a.c.setGoal(goal, persist, reason)
}
func (a coreWidgetCore) currentGoal() *jsObject { return a.c.state() }
func (a coreWidgetCore) clearStateGoal()        { a.c.setStateGoal(nil) }
func (a coreWidgetCore) focusedGoalID() *string { return a.c.focused }
func (a coreWidgetCore) deleteGoalByID(id string) {
	a.c.goals.delete(id)
}
func (a coreWidgetCore) assignFocusedGoalID(id *string) { a.c.assignFocused(id) }
func (a coreWidgetCore) clearStoppedRuntimeState()      { a.c.clearStoppedRuntimeState() }
func (a coreWidgetCore) updateUI(goalDialogContext)     { a.c.updateUI() }
func (a coreWidgetCore) setAuditProgress(p *auditorWidgetProgress) {
	a.c.auditProgress = p
}
func (a coreWidgetCore) auditProgressNow() *auditorWidgetProgress { return a.c.auditProgress }
func (a coreWidgetCore) goalModalDepth() int                      { return a.c.goalModalDepth }
func (a coreWidgetCore) auditProgress() *auditorWidgetProgress    { return a.c.auditProgress }
func (a coreWidgetCore) pauseActiveGoal(goalDialogContext)        { a.c.pauseActiveGoal() }
func (a coreWidgetCore) abortAudit(goalDialogContext)             { a.c.abortAudit() }
func (a coreWidgetCore) isDashboardExpanded() bool                { return a.c.isDashboardExpanded() }
func (a coreWidgetCore) toggleDashboardExpanded()                 { a.c.toggleDashboardExpanded() }
func (a coreWidgetCore) toggleGoalAuditor(goalDialogContext)      { a.c.toggleGoalAuditor() }
func (a coreWidgetCore) widgetComponent() goalWidgetHandle {
	if w := a.c.widgetRef.current; w != nil {
		return w
	}
	return nil
}
func (a coreWidgetCore) terminalInputUnsubscribe() func()     { return a.c.terminalInputUnsubscribe }
func (a coreWidgetCore) setTerminalInputUnsubscribe(f func()) { a.c.terminalInputUnsubscribe = f }
func (a coreWidgetCore) debugMode() bool                      { return a.c.debugMode }
func (a coreWidgetCore) setDebugMode(on bool)                 { a.c.debugMode = on }

// syncTerminalInputPause is syncTerminalInputPause(core, ctx) of goal-widget.ts with the current host as ctx.
func (c *core) syncTerminalInputPause() {
	syncTerminalInputPause(coreWidgetCore{c}, hostDialogCtx{c.h})
}

// toggleTaskViaService is toggleTaskViaService (goal-widget.ts): F3, toggle one task through the goal-service mutation boundary with
// the same gates as update_goal_task. Completing a pending task requires its subtasks complete first and (unless contracts are
// disabled) evidence when it carries a verification contract, collected through the UI input dialog; completing a complete task
// reopens it to pending (a deliberate TUI-only relaxation, for explicit human corrections).
func (c *core) toggleTaskViaService(goalID, taskID string) (ok bool, message string) {
	goal := c.goals.get(goalID)
	if goal == nil {
		return false, "Goal " + goalID + " not found in this session."
	}
	if gstr(goal, "status") != "active" {
		return false, "Task updates apply only to an active goal (current status: " + gstr(goal, "status") + ")."
	}
	taskList := goal.obj("taskList")
	if taskList == nil {
		return false, "The goal has no task list."
	}
	settings := loadGoalSettings(c.h.Cwd())
	tasks, _ := taskList.vals["tasks"].([]any)
	task := findTaskInTree(tasks, taskID)
	if task == nil {
		return false, "Task \"" + taskID + "\" not found."
	}
	now := nowIso()
	switch gstr(task, "status") {
	case "pending":
		var evidence any = undef{}
		if _, has := task.str("verificationContract"); !settings.disableContracts && has && gstr(task, "verificationContract") != "" {
			var input string
			var got bool
			c.awaiting(func() { input, got = c.h.Input("Evidence for task "+taskID, "") })
			if !got || jsTrim(input) == "" {
				return false, "Task \"" + taskID + "\" has a verification contract; provide evidence to complete it."
			}
			evidence = utf16Prefix(jsTrim(input), 200)
		}
		if gate := checkSubtasksComplete(task); gate != "" {
			return false, gate
		}
		token := c.focusedOperationToken(goalID)
		result, err := c.goalService.updateTask(c.st, goalTaskUpdateSpec{
			focusToken: &token,
			taskId:     taskID,
			validate: func(t *jsObject) goalGate {
				if gstr(t, "status") == "complete" {
					return goalGate{ok: false, message: "Task \"" + taskID + "\" is already complete."}
				}
				return goalGate{ok: true}
			},
			update: func(t *jsObject) (*jsObject, goalGate) {
				return goalWith(t, "status", "complete", "completedAt", now, "evidence", evidence), goalGate{ok: true}
			},
			ledger: func(written, _ *jsObject) ([]*jsObject, error) {
				return []*jsObject{ledgerEvent("task_complete", "goalId", gstr(written, "id"), "taskId", taskID, "evidence", evidence, "at", gstr(written, "updatedAt"))}, nil
			},
		})
		if err != nil {
			return false, err.Error()
		}
		if !result.ok {
			return false, result.message
		}
		return true, ""
	case "complete":
		token := c.focusedOperationToken(goalID)
		result, err := c.goalService.updateTask(c.st, goalTaskUpdateSpec{
			focusToken: &token,
			taskId:     taskID,
			validate:   func(*jsObject) goalGate { return goalGate{ok: true} },
			update: func(t *jsObject) (*jsObject, goalGate) {
				return goalWith(t, "status", "pending", "completedAt", undef{}, "evidence", undef{}), goalGate{ok: true}
			},
			ledger: func(written, _ *jsObject) ([]*jsObject, error) {
				return []*jsObject{ledgerEvent("task_reopened", "goalId", gstr(written, "id"), "taskId", taskID, "at", gstr(written, "updatedAt"))}, nil
			},
		})
		if err != nil {
			return false, err.Error()
		}
		if !result.ok {
			return false, result.message
		}
		return true, ""
	}
	return false, "Task \"" + taskID + "\" is skipped; change it with /goal-tweak or update_goal_task."
}
