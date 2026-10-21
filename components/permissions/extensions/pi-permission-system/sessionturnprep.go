package pi_permission_system

// Port of src/handlers/session-turn-prep.ts.

// TurnPrepSession is the session surface the turn-prep routine drives.
type TurnPrepSession interface {
	Activate(ctx SessionContext) error
	RefreshConfig(ctx *SessionContext, projectTrusted bool)
}

// TurnPreparation is what a before_agent_start handler asks for; SessionTurnPrep provides it.
type TurnPreparation interface {
	Prepare(ctx SessionContext) error
}

// SessionTurnPrep brings this node up to date for the turn about to start. Everything that must be true before the node answers a
// permission question this turn lives here, so the before_agent_start handler is left with the one job its name describes: filtering
// tools and sanitizing the prompt.
//
// Dependencies: the session (activated with the turn's context, then refreshed from disk); warmParser (warms the bash parser so the
// synchronous advisory bash path can decompose at gate parity; before_agent_start precedes any tool call, so triggering it here closes
// the pre-warm window, #309); configIssues (reports what the refresh found, latched per issue, so an issue the operator created
// mid-session is shown on the next turn without an unchanged one being repeated, #933); readyAnnouncer (re-announces permissions:ready
// once per session, ADR 0012 decision 3).
type SessionTurnPrep struct {
	session        TurnPrepSession
	warmParser     func()
	readyAnnouncer ReadyAnnouncer
	configIssues   ConfigIssueReporting
}

// NewSessionTurnPrep is the constructor.
func NewSessionTurnPrep(session TurnPrepSession, warmParser func(), readyAnnouncer ReadyAnnouncer, configIssues ConfigIssueReporting) *SessionTurnPrep {
	return &SessionTurnPrep{session: session, warmParser: warmParser, readyAnnouncer: readyAnnouncer, configIssues: configIssues}
}

// Prepare activates the session with ctx, refreshes the config under the current trust, reports what it found and announces ready. An
// error is a failing activation (the original's rejection): nothing after it runs.
func (p *SessionTurnPrep) Prepare(ctx SessionContext) error {
	// Fire-and-forget: warming is idempotent and best effort, so it never delays agent start. A bash advisory query before it completes
	// falls back to whole-string matching.
	p.warmParser()
	if err := p.session.Activate(ctx); err != nil {
		return err
	}
	// Gate the mid-session runtime-config refresh on project trust too, so an untrusted project cannot slip its runtime config (for
	// example yoloMode) in right before agent start after session_start withheld it (#644). The session_start handler already warned;
	// do not re-warn on every start.
	p.session.RefreshConfig(&ctx, ctx.ProjectTrusted())
	// Say what that refresh found, before the announce below hands this node to a consumer: the operator hears about a config they just
	// broke on the next turn, and hears an unchanged issue only once.
	p.configIssues.Report()
	// Announce last: the node is up to date for the turn, so a consumer that resolves the service in its ready handler queries current
	// policy. The once-per-session guard lives in the announcer, not here.
	p.readyAnnouncer.AnnounceReady(ctx)
	return nil
}
