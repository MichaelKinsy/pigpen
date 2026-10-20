package warden

import (
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The security lane's wiring: the runaway guard (message_start / message_update, recovery at agent_end), the
// restatement measure and the prose check at agent_end, desktop notifications, and the security-write steer. The
// pure logic is in runaway.go, prose.go, notify.go and output_security.go; extension.ts line numbers below are the
// original's.

// pendingRunaway is a stopped run waiting for agent_end (extension.ts pendingRunaway).
type pendingRunaway struct {
	nudge   string
	recover bool
}

// notifierProbe is the notifier detection of one session: probed once, on first use.
type notifierProbe struct {
	once sync.Once
	name NotifierName
}

// securityState is the per-session state of the security lane; one field on warden.
type securityState struct {
	mu      sync.Mutex
	runaway *RunawayMonitor
	// stops: runs stopped by the runaway guard for the current user prompt; the first gets a recovery turn, later
	// ones wait for the user.
	stops   int
	pending *pendingRunaway
	prose   *ProseTrend
	finals  *RestatementWindow
	// Desktop notifier, probed on first use; "" after the probe means this machine has no route to the desktop.
	notifier       *notifierProbe
	lastNotifiedAt time.Time
	// sending tracks fire-and-forget notification goroutines (a test waits on it).
	sending sync.WaitGroup
	// slopCounts: how often each slop symptom was steered this session; the third repeat becomes a standing rule.
	slopCounts map[string]int
}

func newSecurityState() *securityState {
	return &securityState{runaway: NewRunawayMonitor(), prose: NewProseTrend(), finals: NewRestatementWindow(4), notifier: &notifierProbe{}, slopCounts: map[string]int{}}
}

// resetSession is the security part of session_start (extension.ts 1033, 1053-1058).
func (s *securityState) resetSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prose.Reset()
	s.finals.Reset()
	s.runaway.Reset()
	s.stops, s.pending = 0, nil
	s.notifier, s.lastNotifiedAt = &notifierProbe{}, time.Time{}
	s.slopCounts = map[string]int{}
}

// resetPrompt is the security part of before_agent_start (1115-1120): a new user prompt starts a new restatement
// window and a new runaway budget; answering the user is never a restatement.
func (s *securityState) resetPrompt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finals.Reset()
	s.runaway.Reset()
	s.stops, s.pending = 0, nil
}

// ---------------------------------------------------------------------------
// Runaway: per token the stream only appends to a buffer; every 256 characters the buffer is checked for
// identical blocks, with code only.

// onRunawayMessageStart is the message_start handler: each assistant message is judged on its own, and Pi does not
// forward the stream's own "start" event, so this is the reset (1317).
func (w *warden) onRunawayMessageStart(_ sdk.Context, data map[string]any) (any, error) {
	if m, _ := data["message"].(map[string]any); m != nil {
		if role, _ := m["role"].(string); role == "assistant" {
			w.sec.mu.Lock()
			w.sec.runaway.Reset()
			w.sec.mu.Unlock()
		}
	}
	return nil, nil
}

// onRunawayMessageUpdate is the message_update handler (1359-1380).
func (w *warden) onRunawayMessageUpdate(ctx sdk.Context, data map[string]any) (any, error) {
	ev, _ := data["assistantMessageEvent"].(map[string]any)
	if ev == nil {
		return nil, nil
	}
	typ, _ := ev["type"].(string)
	event := StreamEvent{Type: typ}
	if d, ok := ev["delta"].(string); ok {
		event.Delta = &d
	}
	s := w.sec
	s.mu.Lock()
	kind := s.runaway.Feed(event)
	stopped := s.runaway.Stopped()
	s.mu.Unlock()
	if kind == "" || stopped {
		return nil, nil
	}
	cfg := w.config(ctx)
	if !cfg.Enabled || !cfg.Runaway.Enabled {
		return nil, nil
	}
	s.mu.Lock()
	verdict := s.runaway.Check(kind, cfg.Runaway)
	if verdict == nil {
		s.mu.Unlock()
		return nil, nil
	}
	s.stops++
	recover := cfg.Runaway.Recover && s.stops == 1
	nudge := RunawayNudge(*verdict, recover)
	s.pending = &pendingRunaway{nudge: nudge, recover: recover}
	s.mu.Unlock()
	w.bump(func(st *stats) { st.Runaway++ })
	w.record(ctx, cfg, GuardRunaway, FormatRunaway(*verdict, recover, cfg.Widget.Runaway), RunawayDetails(*verdict, nudge, recover), nil)
	if ctx.HasUI() {
		tail := " (not restarted: second time for this prompt)"
		if recover {
			tail = " (agent gets one follow-up turn)"
		}
		ctx.Notify("warden · runaway: the same "+string(verdict.Kind)+" block repeated "+itoaInt(verdict.Count)+" times in "+itoaInt(verdict.Chars)+" chars; run stopped"+tail, "error")
	}
	second := "Second time for this prompt; the agent is waiting for you."
	if recover {
		second = "The agent gets one recovery turn."
	}
	w.notifyDesktop(ctx, cfg, "Runaway stopped: the same "+string(verdict.Kind)+" block repeated "+itoaInt(verdict.Count)+" times. "+second)
	// Interactive Pi restores the user's queued messages to the editor before aborting; the follow-up is queued in
	// agent_end, after that.
	w.runawayAborted()
	ctx.Abort()
	return nil, nil
}

// runawayAborted is where upstream bumps conscienceGeneration (1376), so an in-flight conscience assessment of the
// aborted message is dropped. The conscience lane fills it in; it does nothing until then.
func (w *warden) runawayAborted() {}

// runawayRecovery is agent_end slot 3 (2305-2311): a stopped run queues its follow-up (a recovery turn once per
// prompt; otherwise a note that starts nothing). True means the rest of agent_end is skipped.
func (w *warden) runawayRecovery(ctx sdk.Context, cfg Config) bool {
	w.sec.mu.Lock()
	pending := w.sec.pending
	w.sec.pending = nil
	w.sec.mu.Unlock()
	if pending == nil {
		return false
	}
	display := true
	if pending.recover {
		w.steer(cfg, []SteerGuard{SteerGuardRunaway}, ctx, pending.nudge, SteerOptions{DeliverAs: "followUp", TriggerTurn: true, Display: &display})
	} else {
		w.steer(cfg, []SteerGuard{SteerGuardRunaway}, ctx, pending.nudge, SteerOptions{Display: &display})
	}
	return true
}

// ---------------------------------------------------------------------------
// Restatement and the prose check at agent_end.

// restatement is agent_end slot 5 (2314-2330): measured in code before any judging, recorded in the trace, never a
// steer. A nudge cannot retract the reply and would cost the very turn it warns against.
func (w *warden) restatement(ctx sdk.Context, cfg Config, final string) {
	if final == "" {
		return
	}
	w.sec.mu.Lock()
	share := w.sec.finals.Share(final)
	w.sec.finals.Record(final)
	w.sec.mu.Unlock()
	sentences := SubstantiveSentences(final)
	if share < RestateShare || sentences < RestateMinSentences {
		return
	}
	w.bump(func(st *stats) { st.Restatements++ })
	pct := strconv.Itoa(jsRound(share * 100))
	w.record(ctx, cfg, GuardProse, "warden · prose · restated "+pct+"% of "+strconv.Itoa(sentences)+" sentences · recorded only", []string{
		pct + "% of this reply's substantive sentences were already sent in an earlier reply of this run",
		"no steer: a nudge cannot retract the reply and would cost the turn it warns against",
	}, nil)
	if ctx.HasUI() && cfg.Notices {
		ctx.Notify("warden · prose: the final reply restates "+pct+"% of what was already said this run (recorded, not steered)", "warning")
	}
}

// startProse begins the prose check (2415-2417) and returns the function that settles it (2421-2434). The caller
// runs the done check between the two: Pi shows the agent as working until agent_end returns, so the two
// independent checks share one round trip. A nil judge or a reply shorter than prose.minChars starts nothing.
func (w *warden) startProse(ctx sdk.Context, cfg Config, final string, judge Judge) (settle func()) {
	if !cfg.Slop.Enabled || !cfg.Slop.Prose.Enabled || utf16Len(final) < cfg.Slop.Prose.MinChars || judge == nil {
		return func() {}
	}
	task, _ := w.latestUserPrompt(ctx)
	done := make(chan ProseVerdict, 1)
	go func() {
		done <- EvaluateProse(newContext(), task, final, ProseOptions{Config: cfg.Slop.Prose, Judge: judge, Timeout: w.timeout(cfg)})
	}()
	w.bump(func(st *stats) { st.ProseChecks++ })
	return func() {
		verdict := <-done
		if verdict.Error != "" {
			w.noteError(ctx, verdict.Error, verdict.ErrorCode)
		}
		w.sec.mu.Lock()
		var due []ProseSymptom
		if verdict.Error == "" {
			w.sec.prose.Record(verdict.Flagged)
			due = w.sec.prose.Due(cfg.Slop.Prose.Trend)
		}
		nudge := ""
		if len(due) > 0 {
			nudge = ProseNudge(due, cfg.Slop.Prose.Audience, w.sec.prose.Counts)
			verdict.Nudged = true
			w.sec.prose.MarkNudged()
		}
		w.sec.mu.Unlock()
		if nudge != "" {
			w.bump(func(st *stats) { st.ProseNudges++ })
		}
		w.record(ctx, cfg, GuardProse, RenderTemplate(cfg.Widget.Prose, ProseTokens(verdict, w.opts.Now())), ProseDetails(verdict, final, cfg.Slop.Prose.Audience, nudge), nil)
		if nudge == "" {
			return
		}
		if ctx.HasUI() && cfg.Notices {
			names := make([]string, len(due))
			for i, s := range due {
				names[i] = string(s)
			}
			ctx.Notify("warden · prose: "+strings.Join(names, ", ")+" in "+strconv.Itoa(cfg.Slop.Prose.Trend)+" of the last 3 replies (agent nudged for the next reply)", "warning")
		}
		if w.adaptiveSend(ctx, cfg, SteerKindProse) && w.steer(cfg, []SteerGuard{SteerGuardProse}, ctx, nudge, SteerOptions{DeliverAs: "nextTurn"}) {
			w.watchSteer(ctx, cfg, []SteerKind{SteerKindProse}, SteerSubject{}, true)
		}
	}
}

// ---------------------------------------------------------------------------
// The security-write steer (1584-1586): the proposed write may introduce a security weakness.

const securityWriteSteer = "pi-warden: the proposed write may introduce a security weakness. Check for embedded credentials, disabled TLS, unsafe command/SQL interpolation, broad permissions, or bypassed verification; use a safe implementation instead."

// securityWrite steers when the judge scored the write at or above security.threshold. path is the written path,
// "" when the call has none.
func (w *warden) securityWrite(ctx sdk.Context, cfg Config, securityRisk *float64, path string) {
	if securityRisk == nil || *securityRisk < cfg.Security.Threshold || !w.adaptiveSend(ctx, cfg, SteerKindSecurityWrite) {
		return
	}
	if w.steer(cfg, []SteerGuard{SteerGuardSecurity}, ctx, securityWriteSteer, SteerOptions{DeliverAs: "steer"}) {
		w.watchSteer(ctx, cfg, []SteerKind{SteerKindSecurityWrite}, SteerSubject{Path: path}, false)
	}
}

// ---------------------------------------------------------------------------
// Desktop notifications (924-935): interactive sessions only; a headless run has nobody at the desktop.

func (w *warden) notifyRunner() NotifyRunner {
	if w.opts.NotifyRunner != nil {
		return w.opts.NotifyRunner
	}
	return RunNotifierCommand
}

func (w *warden) notifierPlatform() string {
	if w.opts.NotifierPlatform != "" {
		return w.opts.NotifierPlatform
	}
	return nodePlatform()
}

// detectedNotifier probes once per session; "" means this machine has no route to the desktop.
func (w *warden) detectedNotifier() NotifierName {
	w.sec.mu.Lock()
	probe := w.sec.notifier
	w.sec.mu.Unlock()
	probe.once.Do(func() { probe.name = DetectNotifier(w.notifierPlatform(), w.notifyRunner(), 3*time.Second) })
	return probe.name
}

// notifyDesktop shows a desktop notification for a moment that needs the user back at the terminal. Fire and
// forget: the probe and the send run off the handler, and a failure is silent.
func (w *warden) notifyDesktop(ctx sdk.Context, cfg Config, body string) {
	if !ctx.HasUI() || !cfg.Notify.Enabled {
		return
	}
	now := w.opts.Now()
	w.sec.mu.Lock()
	if !w.sec.lastNotifiedAt.IsZero() && now.Sub(w.sec.lastNotifiedAt) < time.Duration(cfg.Notify.CooldownMs)*time.Millisecond {
		w.sec.mu.Unlock()
		return
	}
	w.sec.lastNotifiedAt = now
	w.sec.sending.Add(1)
	w.sec.mu.Unlock()
	note := Notification{Title: "pi-warden", Body: body}
	if len(cfg.Notify.Command) > 0 && w.opts.NotifyRunner == nil {
		// A configured command spawns in this call, as Node's execFile does: the process is running when the hook
		// returns, and only the wait for its exit is off the handler.
		command := BuildNotifierCommand(CustomNotifier(cfg.Notify.Command), Notification{Title: note.Title, Body: ClipBody(note.Body, 180)})
		if command.File == "" {
			w.sec.sending.Done()
			return
		}
		wait := StartNotifierCommand(command.File, command.Args, 8*time.Second, command.Env)
		go func() { defer w.sec.sending.Done(); wait() }()
		return
	}
	run := w.notifyRunner()
	go func() {
		defer w.sec.sending.Done()
		if len(cfg.Notify.Command) > 0 {
			SendNotification(CustomNotifier(cfg.Notify.Command), note, run, 8*time.Second)
			return
		}
		if name := w.detectedNotifier(); name != "" {
			SendNotification(NamedNotifier(name), note, run, 8*time.Second)
		}
	}()
}

// notificationsStatus is the status line about desktop notifications.
func (w *warden) notificationsStatus(cfg Config) string {
	if !cfg.Notify.Enabled {
		return `Desktop notifications: off ("notify": { "enabled": true } in the config turns them on).`
	}
	via := "no notifier found on this machine"
	if len(cfg.Notify.Command) > 0 {
		via = "command " + cfg.Notify.Command[0]
	} else if name := w.detectedNotifier(); name != "" {
		via = string(name)
	}
	return "Desktop notifications: on (" + via + "; cooldown " + strconv.Itoa(cfg.Notify.CooldownMs) + " ms)."
}
