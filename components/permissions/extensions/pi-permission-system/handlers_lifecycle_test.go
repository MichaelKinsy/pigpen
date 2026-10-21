package pi_permission_system

import (
	"testing"
)

// Twins of test/handlers/lifecycle.test.ts and test/handlers/session-turn-prep.test.ts.

// recordedOrder is the call order a spy writes, shared across the collaborators a test watches.
type recordedOrder struct{ calls []string }

func (o *recordedOrder) add(s string) { o.calls = append(o.calls, s) }

// orderSession wraps a real session and records the order of the calls the handlers make.
type orderSession struct {
	*PermissionSession
	order *recordedOrder
}

func (s orderSession) ResetForNewSession(ctx SessionContext, trusted bool) error {
	s.order.add("resetForNewSession")
	return s.PermissionSession.ResetForNewSession(ctx, trusted)
}
func (s orderSession) RefreshConfig(ctx *SessionContext, trusted bool) {
	s.order.add("refreshConfig")
	s.PermissionSession.RefreshConfig(ctx, trusted)
}
func (s orderSession) Shutdown() {
	s.order.add("shutdown")
	s.PermissionSession.Shutdown()
}

// orderStore records refreshes in the shared order as well.
type orderStore struct {
	*fakeConfigStore
	order *recordedOrder
}

func (s orderStore) Refresh(cwd string, trusted bool) {
	s.order.add("store.refresh")
	s.fakeConfigStore.Refresh(cwd, trusted)
}

type fakeIssueReporter struct {
	reports int
	order   *recordedOrder
}

func (r *fakeIssueReporter) Report() {
	r.reports++
	if r.order != nil {
		r.order.add("report")
	}
}

type fakeServiceLifecycle struct {
	activated []SessionContext
	tornDown  int
	order     *recordedOrder
}

func (s *fakeServiceLifecycle) Activate(ctx SessionContext) { s.activated = append(s.activated, ctx) }
func (s *fakeServiceLifecycle) Teardown() {
	s.tornDown++
	if s.order != nil {
		s.order.add("teardown")
	}
}

type fakeAuditWriter struct{ logger AuditLogger }

func (a *fakeAuditWriter) WriteSummary(l AuditLogger) { a.logger = l }

type fakeDialogs struct {
	reasons []string
	order   *recordedOrder
}

func (d *fakeDialogs) ReleaseAll(reason string) {
	d.reasons = append(d.reasons, reason)
	if d.order != nil {
		d.order.add("release")
	}
}

type lifecycleRig struct {
	handler  *SessionLifecycleHandler
	rig      *sessionRig
	session  *PermissionSession
	logger   *gfLogger
	service  *fakeServiceLifecycle
	audit    *fakeAuditWriter
	issues   *fakeIssueReporter
	dialogs  *fakeDialogs
	order    *recordedOrder
	resolver *PermissionResolver
}

func makeLifecycle(configIssues ...string) *lifecycleRig {
	r := makeRealSession(nil)
	order := &recordedOrder{}
	if len(configIssues) > 0 {
		r.pm.issues = configIssues
	}
	r.session = NewPermissionSession(r.paths, r.fwd, r.pm, r.rules, orderStore{r.store, order}, r.auth, PathFlavorForPlatform("linux"))
	l := &lifecycleRig{rig: r, session: r.session, logger: &gfLogger{}, service: &fakeServiceLifecycle{order: order},
		audit: &fakeAuditWriter{}, issues: &fakeIssueReporter{order: order}, dialogs: &fakeDialogs{order: order}, order: order,
		resolver: NewPermissionResolver(r.pm, r.rules)}
	l.handler = NewSessionLifecycleHandler(orderSession{r.session, order}, l.resolver, l.service, l.logger, l.audit, l.issues, l.dialogs)
	return l
}

// trustedCtx is makeCtx of the fixtures: cwd /test/project, a UI, and a project the host trusts unless told otherwise.
func trustedCtx(trusted bool, mut ...func(*SessionContext)) (SessionContext, *fakeNotifier) {
	return makeCtx(append([]func(*SessionContext){func(c *SessionContext) { c.IsProjectTrusted = func() bool { return trusted } }}, mut...)...)
}

func logJSON(w gfLogWrite) string { return w.event + " " + marshalJSON(w.details, "") }

func TestLifecycleHandlerTwins(t *testing.T) {
	const f = "handlers/lifecycle"
	start := func(t *testing.T, l *lifecycleRig, reason string, ctx SessionContext) {
		t.Helper()
		if err := l.handler.HandleSessionStart(SessionStartPayload{Reason: reason}, ctx); err != nil {
			t.Fatal(err)
		}
	}

	tw(t, f, "refreshes config with ctx, trusted", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, l.rig.store.refreshed, []struct {
			cwd     string
			trusted bool
		}{{ctx.Cwd, true}}, "refreshed")
	})
	tw(t, f, "calls resetForNewSession with ctx, trusted", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, l.rig.pm.configured, []string{ctx.Cwd}, "the trusted reset configures the manager for the cwd")
	})
	tw(t, f, "withholds the project scope from refreshConfig and resetForNewSession", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(false)
		start(t, l, "startup", ctx)
		eq(t, l.rig.store.refreshed[0].trusted, false, "refresh untrusted")
		eq(t, l.rig.pm.configured, []string{""}, "global-only")
	})
	tw(t, f, "loudly warns and records a review entry when untrusted", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(false)
		start(t, l, "startup", ctx)
		eq(t, l.logger.warns, []string{UntrustedProjectMessage}, "warn")
		eq(t, len(l.logger.reviews), 1, "one review entry")
		eq(t, logJSON(l.logger.reviews[0]), `project_trust.skipped {"cwd":"/test/project","phase":"session_start"}`, "review")
	})
	tw(t, f, "does not warn when the project is trusted", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, len(l.logger.warns), 0, "warns")
	})
	tw(t, f, "logs resolved config paths", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, l.rig.store.logged, []string{ctx.Cwd}, "logged once")
	})
	tw(t, f, "resolves agent name from ctx", func(t *testing.T) {
		l := makeLifecycle()
		agent := "reviewer"
		l.session.agentNameLookup = func(ActiveAgentContext) (*string, error) { return &agent, nil }
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, l.rig.pm.issuesArg, &agent, "the issues are read for the resolved agent")
	})
	tw(t, f, "notifies each policy issue", func(t *testing.T) {
		l := makeLifecycle("issue A", "issue B")
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, l.logger.warns, []string{"issue A", "issue B"}, "warns")
	})
	tw(t, f, "does not warn when there are no policy issues", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, len(l.logger.warns), 0, "warns")
	})
	tw(t, f, "writes lifecycle.reload debug log when reason is reload", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true, func(c *SessionContext) { c.Cwd = "/proj" })
		start(t, l, "reload", ctx)
		eq(t, len(l.logger.debugs), 1, "one debug entry")
		eq(t, logJSON(l.logger.debugs[0]), `lifecycle.reload {"triggeredBy":"session_start","reason":"reload","cwd":"/proj"}`, "debug")
	})
	tw(t, f, "does not write lifecycle.reload debug log for non-reload reasons", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, len(l.logger.debugs), 0, "debugs")
	})
	tw(t, f, "activates the service for the session with ctx", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, len(l.service.activated), 1, "activated once")
		eq(t, l.service.activated[0].Cwd, ctx.Cwd, "with ctx")
	})
	tw(t, f, "calls resetForNewSession before refreshConfig", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, l.order.calls[:3], []string{"resetForNewSession", "refreshConfig", "store.refresh"}, "order")
	})
	tw(t, f, "reports them, so one present before the session starts is shown", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, l.issues.reports, 1, "reports")
	})
	tw(t, f, "reports after the config is refreshed, not before", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		start(t, l, "startup", ctx)
		eq(t, l.order.calls, []string{"resetForNewSession", "refreshConfig", "store.refresh", "report"}, "order")
	})

	tw(t, f, "does nothing when reason is not reload", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		l.handler.HandleResourcesDiscover(ResourcesDiscoverPayload{Reason: "startup"}, ctx)
		eq(t, len(l.rig.pm.configured), 0, "no reload")
	})
	tw(t, f, "reloads the session with the trust flag on reload", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		l.session.Activate(ctx)
		l.handler.HandleResourcesDiscover(ResourcesDiscoverPayload{Reason: "reload"}, ctx)
		eq(t, l.rig.pm.configured, []string{ctx.Cwd}, "reloaded for the cwd")
	})
	tw(t, f, "withholds the project scope and warns on an untrusted reload", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(false, func(c *SessionContext) { c.Cwd = "/proj" })
		l.handler.HandleResourcesDiscover(ResourcesDiscoverPayload{Reason: "reload"}, ctx)
		eq(t, l.rig.pm.configured, []string{""}, "global-only")
		eq(t, l.logger.warns, []string{UntrustedProjectMessage}, "warn")
		eq(t, logJSON(l.logger.reviews[0]), `project_trust.skipped {"cwd":"/proj","phase":"resources_discover"}`, "review")
	})
	tw(t, f, "writes lifecycle.reload debug log on reload", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true, func(c *SessionContext) { c.Cwd = "/proj" })
		l.session.Activate(ctx)
		l.handler.HandleResourcesDiscover(ResourcesDiscoverPayload{Reason: "reload"}, ctx)
		eq(t, logJSON(l.logger.debugs[0]), `lifecycle.reload {"triggeredBy":"resources_discover","reason":"reload","cwd":"/proj"}`, "debug")
	})
	tw(t, f, "logs cwd as null when runtimeContext is null on reload", func(t *testing.T) {
		l := makeLifecycle()
		ctx, _ := trustedCtx(true)
		l.handler.HandleResourcesDiscover(ResourcesDiscoverPayload{Reason: "reload"}, ctx)
		eq(t, logJSON(l.logger.debugs[0]), `lifecycle.reload {"triggeredBy":"resources_discover","reason":"reload","cwd":null}`, "debug")
	})

	tw(t, f, "clears UI status when runtime context is present", func(t *testing.T) {
		l := makeLifecycle()
		ctx, n := trustedCtx(true)
		l.session.Activate(ctx)
		l.handler.HandleSessionShutdown()
		eq(t, n.statuses, [][2]string{{"pi-permission-system", ""}}, "statuses")
	})
	tw(t, f, "does not throw when runtime context is null", func(t *testing.T) {
		l := makeLifecycle()
		l.handler.HandleSessionShutdown()
	})
	tw(t, f, "calls shutdown on the session", func(t *testing.T) {
		l := makeLifecycle()
		l.handler.HandleSessionShutdown()
		eq(t, l.rig.fwd.stopped, 1, "forwarding stopped by the session shutdown")
	})
	tw(t, f, "calls serviceLifecycle.teardown", func(t *testing.T) {
		l := makeLifecycle()
		l.handler.HandleSessionShutdown()
		eq(t, l.service.tornDown, 1, "teardown")
	})
	tw(t, f, "writes the decision-audit summary to the logger", func(t *testing.T) {
		l := makeLifecycle()
		l.handler.HandleSessionShutdown()
		eq(t, l.audit.logger == AuditLogger(l.logger), true, "the audit got the handler's logger")
	})
	tw(t, f, "releases every ask still waiting for an answer", func(t *testing.T) {
		l := makeLifecycle()
		l.handler.HandleSessionShutdown()
		eq(t, l.dialogs.reasons, []string{SessionEndedReason}, "reasons")
	})
	tw(t, f, "releases pending asks before shutting the session down", func(t *testing.T) {
		l := makeLifecycle()
		l.handler.HandleSessionShutdown()
		// A drain awaiting a forwarded ask can only write its response once that ask is settled, and shutdown stops the forwarding
		// lifecycle.
		eq(t, l.order.calls, []string{"release", "shutdown", "teardown"}, "order")
	})
}

func TestLifecycleHandlerFailsOnAgentNameError(t *testing.T) {
	// not an upstream case: an unreadable session never reads as "no agent", and the service is not published for it
	l := makeLifecycle()
	l.session.agentNameLookup = func(ActiveAgentContext) (*string, error) { return nil, errTest }
	ctx, _ := trustedCtx(true)
	if err := l.handler.HandleSessionStart(SessionStartPayload{Reason: "startup"}, ctx); err == nil {
		t.Fatal("the error was swallowed")
	}
	eq(t, len(l.service.activated), 0, "not activated")
}

type turnPrepRig struct {
	prep    *SessionTurnPrep
	rig     *sessionRig
	order   *recordedOrder
	warmed  int
	issues  *fakeIssueReporter
	announc *fakeAnnouncer
}

type fakeAnnouncer struct {
	ctxs  []SessionContext
	order *recordedOrder
}

func (a *fakeAnnouncer) AnnounceReady(ctx SessionContext) {
	a.ctxs = append(a.ctxs, ctx)
	a.order.add("announceReady")
}

func makeTurnPrep() *turnPrepRig {
	r := makeRealSession(nil)
	order := &recordedOrder{}
	p := &turnPrepRig{rig: r, order: order, issues: &fakeIssueReporter{order: order}, announc: &fakeAnnouncer{order: order}}
	session := NewPermissionSession(r.paths, r.fwd, r.pm, r.rules, orderStore{r.store, order}, r.auth, PathFlavorForPlatform("linux"))
	p.prep = NewSessionTurnPrep(orderSession{session, order}, func() { p.warmed++ }, p.announc, p.issues)
	return p
}

func TestSessionTurnPrepTwins(t *testing.T) {
	const f = "handlers/session-turn-prep"
	tw(t, f, "triggers the bash-parser warm-up", func(t *testing.T) {
		p := makeTurnPrep()
		ctx, _ := trustedCtx(true)
		_ = p.prep.Prepare(ctx)
		eq(t, p.warmed, 1, "warmed")
	})
	tw(t, f, "activates the session with ctx", func(t *testing.T) {
		p := makeTurnPrep()
		ctx, _ := trustedCtx(true)
		_ = p.prep.Prepare(ctx)
		// the real session.activate calls forwarding.start
		eq(t, len(p.rig.fwd.started), 1, "forwarding started")
		eq(t, p.rig.fwd.started[0].Cwd, ctx.Cwd, "with ctx")
	})
	tw(t, f, "refreshes config with ctx, gated on project trust", func(t *testing.T) {
		p := makeTurnPrep()
		ctx, _ := trustedCtx(true)
		_ = p.prep.Prepare(ctx)
		eq(t, p.rig.store.refreshed[0].cwd, ctx.Cwd, "cwd")
		eq(t, p.rig.store.refreshed[0].trusted, true, "trusted")
	})
	tw(t, f, "withholds the project scope when the project is untrusted", func(t *testing.T) {
		p := makeTurnPrep()
		ctx, _ := trustedCtx(false)
		_ = p.prep.Prepare(ctx)
		eq(t, p.rig.store.refreshed[0].trusted, false, "untrusted")
	})
	tw(t, f, "announces the node as ready, on the same ctx", func(t *testing.T) {
		p := makeTurnPrep()
		ctx, _ := trustedCtx(true)
		_ = p.prep.Prepare(ctx)
		eq(t, len(p.announc.ctxs), 1, "announced")
		eq(t, p.announc.ctxs[0].Cwd, ctx.Cwd, "ctx")
	})
	tw(t, f, "announces only after the session is up to date for the turn", func(t *testing.T) {
		p := makeTurnPrep()
		ctx, _ := trustedCtx(true)
		_ = p.prep.Prepare(ctx)
		eq(t, p.order.calls[0], "refreshConfig", "refresh first")
		eq(t, p.order.calls[len(p.order.calls)-1], "announceReady", "announce last")
	})
	tw(t, f, "reports them each turn, so one created mid-session is shown", func(t *testing.T) {
		p := makeTurnPrep()
		ctx, _ := trustedCtx(true)
		_ = p.prep.Prepare(ctx)
		eq(t, p.issues.reports, 1, "reports")
	})
	tw(t, f, "reports after the refresh and before the ready announcement", func(t *testing.T) {
		p := makeTurnPrep()
		ctx, _ := trustedCtx(true)
		_ = p.prep.Prepare(ctx)
		eq(t, p.order.calls, []string{"refreshConfig", "store.refresh", "report", "announceReady"}, "order")
	})
	tw(t, f, "delegates the once-per-session guard to the announcer", func(t *testing.T) {
		p := makeTurnPrep()
		ctx, _ := trustedCtx(true)
		_ = p.prep.Prepare(ctx)
		_ = p.prep.Prepare(ctx)
		eq(t, len(p.announc.ctxs), 2, "announced per prepare")
	})
}
