package tintinweb_subagents

// The runtime state the settings this slice owns apply to, and the setter hooks that wire it. The state is read
// live unless a comment says "at registration" (the original reads those once, so a change applies next session).
// upstream: index.ts (the `let` settings above the Agent tool) and the appliers passed to applyAndEmitLoaded.
type runtimeSettings struct {
	defaultMaxTurns     *int   // nil or 0: unlimited. upstream: agent-runner.ts setDefaultMaxTurns
	graceTurns          int    // turns after the wrap-up steer before the abort. upstream: setGraceTurns
	defaultJoinMode     string // async, group or smart: how a background agent's completion is announced
	backgroundByDefault bool   // what a top-level Agent call that does not say means
	strictAgentFiles    bool   // read at startup only: an unreadable agent file aborts the load
	toolDescriptionMode string // read at registration
}

func defaultRuntimeSettings() runtimeSettings {
	return runtimeSettings{graceTurns: defaultGraceTurns, defaultJoinMode: defaultJoinModeSetting, backgroundByDefault: true, toolDescriptionMode: "full"}
}

func (a *app) runtime() runtimeSettings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rt
}

// update changes the runtime settings under the lock.
func (a *app) update(fn func(rt *runtimeSettings)) {
	a.mu.Lock()
	fn(&a.rt)
	a.mu.Unlock()
}

// appliers wires the persisted settings into the running state. A key another slice owns gets its setter added here,
// on its own line, when that slice lands; until then its value is held in the store (settingsView).
func (a *app) appliers() settingsAppliers {
	var ap settingsAppliers
	ap.SetMaxConcurrent = a.mgr.setMaxConcurrent
	ap.SetMaxConcurrentForeground = a.mgr.setMaxConcurrentForeground
	ap.SetDefaultMaxTurns = func(n int) { a.update(func(rt *runtimeSettings) { rt.defaultMaxTurns = &n }) }
	ap.SetGraceTurns = func(n int) { a.update(func(rt *runtimeSettings) { rt.graceTurns = max(1, n) }) }
	ap.SetDefaultJoinMode = func(mode string) { a.update(func(rt *runtimeSettings) { rt.defaultJoinMode = mode }) }
	ap.SetBackgroundByDefault = func(b bool) { a.update(func(rt *runtimeSettings) { rt.backgroundByDefault = b }) }
	ap.SetScopeModels = setScopeModelsEnabled
	ap.SetStrictAgentFiles = func(b bool) { a.update(func(rt *runtimeSettings) { rt.strictAgentFiles = b }) }
	ap.SetDisableDefaultAgents = func(b bool) { setDefaultsDisabled(b); a.reregisterAgents() }
	ap.SetToolDescriptionMode = func(mode string) { a.update(func(rt *runtimeSettings) { rt.toolDescriptionMode = mode }) }
	ap.SetFallbackSubagent = func(v string) { setFallbackSubagent(&v) }
	return ap
}
