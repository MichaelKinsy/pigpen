package pi_goal_x

// coreWidgetCore is the core as the terminal-input keybindings and the debug helpers of goal-widget.ts see it (goalWidgetCore).
// The core's own methods take no context (it works on its host), so the interface the widget slice ported against is adapted here:
// syncTerminalInputPause(newCoreWidgetCore(c), hostDialogCtx{c.h}).
type coreWidgetCore struct{ c *core }

func newCoreWidgetCore(c *core) goalWidgetCore { return coreWidgetCore{c} }

func (w coreWidgetCore) removeDebugFile(_ goalDialogContext, path string) {
	w.c.goalService.removeDebugFile(w.c.st, path)
}
func (w coreWidgetCore) writeDebugFile(_ goalDialogContext, path, content string) {
	_ = w.c.goalService.writeDebugFile(w.c.st, path, content)
}
func (w coreWidgetCore) setGoal(_ goalDialogContext, goal *jsObject, persist bool, reason string) {
	w.c.setGoal(goal, persist, reason)
}
func (w coreWidgetCore) currentGoal() *jsObject                    { return w.c.state() }
func (w coreWidgetCore) clearStateGoal()                           { w.c.setStateGoal(nil) }
func (w coreWidgetCore) focusedGoalID() *string                    { return w.c.focused }
func (w coreWidgetCore) deleteGoalByID(id string)                  { w.c.goals.delete(id) }
func (w coreWidgetCore) assignFocusedGoalID(id *string)            { w.c.assignFocused(id) }
func (w coreWidgetCore) clearStoppedRuntimeState()                 { w.c.clearStoppedRuntimeState() }
func (w coreWidgetCore) updateUI(goalDialogContext)                { w.c.updateUI() }
func (w coreWidgetCore) setAuditProgress(p *auditorWidgetProgress) { w.c.auditProgress = p }
func (w coreWidgetCore) auditProgressNow() *auditorWidgetProgress  { return w.c.auditProgress }
func (w coreWidgetCore) goalModalDepth() int                       { return w.c.goalModalDepth }
func (w coreWidgetCore) auditProgress() *auditorWidgetProgress     { return w.c.auditProgress }
func (w coreWidgetCore) pauseActiveGoal(goalDialogContext)         { w.c.pauseActiveGoal() }
func (w coreWidgetCore) abortAudit(goalDialogContext)              { w.c.abortAudit() }
func (w coreWidgetCore) isDashboardExpanded() bool                 { return w.c.isDashboardExpanded() }
func (w coreWidgetCore) toggleDashboardExpanded()                  { w.c.toggleDashboardExpanded() }
func (w coreWidgetCore) toggleGoalAuditor(goalDialogContext)       { w.c.toggleGoalAuditor() }
func (w coreWidgetCore) widgetComponent() goalWidgetHandle {
	if w.c.widgetRef.current == nil {
		return nil
	}
	return w.c.widgetRef.current
}
func (w coreWidgetCore) terminalInputUnsubscribe() func()     { return w.c.terminalInputUnsubscribe }
func (w coreWidgetCore) setTerminalInputUnsubscribe(f func()) { w.c.terminalInputUnsubscribe = f }
func (w coreWidgetCore) debugMode() bool                      { return w.c.debugMode }
func (w coreWidgetCore) setDebugMode(on bool)                 { w.c.debugMode = on }

var _ goalWidgetCore = coreWidgetCore{}
