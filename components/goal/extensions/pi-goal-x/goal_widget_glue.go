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
