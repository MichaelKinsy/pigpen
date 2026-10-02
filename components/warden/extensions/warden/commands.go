package warden

import (
	"context"
	"fmt"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const help = `warden — guardrails that steer instead of interrupt
  /warden                     status: is it on, where judgments go, what it did
  /warden enable [backend]    turn it on; shows what leaves this machine first (typesafe | own-model | offline)
  /warden disable             turn it off
  /warden mode <m>            steer (hold and tell the agent) | confirm (ask you) | advise (warn only)
  /warden backend <b>         change where judgments go: typesafe | own-model | offline
  /warden test                run a few synthetic dangerous calls through it; nothing is executed
  /warden trace               the last things warden decided`

func (w *warden) command(ctx sdk.Context, args string) error {
	fields := strings.Fields(args)
	sub, rest := "status", []string{}
	if len(fields) > 0 {
		sub, rest = strings.ToLower(fields[0]), fields[1:]
	}
	switch sub {
	case "status", "":
		w.showStatus(ctx)
	case "help", "-h", "--help":
		w.show(ctx, help)
	case "enable", "on":
		return w.enable(ctx, rest)
	case "backend":
		if len(rest) == 0 {
			w.show(ctx, "usage: /warden backend typesafe | own-model | offline")
			return nil
		}
		return w.enable(ctx, rest)
	case "disable", "off":
		if _, err := w.update(ctx, func(c *Config) { c.Enabled, c.Consent = false, "" }); err != nil {
			return err
		}
		w.guard.Reset()
		w.refresh(ctx)
		w.show(ctx, "warden is off. Nothing is checked and nothing is sent. /warden enable turns it back on.")
	case "mode":
		return w.setMode(ctx, rest)
	case "test":
		w.selfTest(ctx)
	case "trace":
		w.showTrace(ctx)
	default:
		w.show(ctx, "unknown subcommand "+sub+".\n\n"+help)
	}
	return nil
}

// show puts a block of text in front of the user: a visible message that starts no turn.
func (w *warden) show(ctx sdk.Context, text string) {
	no := false
	if err := ctx.SendMessage(statusType, text, true, sdk.SendMessageOptions{TriggerTurn: &no}); err != nil {
		ctx.Notify(text, "info")
	}
}

func (w *warden) setMode(ctx sdk.Context, args []string) error {
	if len(args) != 1 || (args[0] != "steer" && args[0] != "confirm" && args[0] != "advise") {
		w.show(ctx, "usage: /warden mode steer | confirm | advise\n  steer   hold the call and tell the agent why, so it re-plans or asks you\n  confirm ask you in a dialog before the call runs\n  advise  never hold; warn only")
		return nil
	}
	if _, err := w.update(ctx, func(c *Config) { c.Mode = args[0] }); err != nil {
		return err
	}
	w.refresh(ctx)
	w.show(ctx, "warden mode: "+args[0])
	return nil
}

func (w *warden) showStatus(ctx sdk.Context) {
	cfg := w.config(ctx)
	w.mu.Lock()
	s := w.stats
	w.mu.Unlock()
	var b strings.Builder
	if !cfg.Enabled {
		b.WriteString("warden is off.\n\n" + OfflineNote + "\n\nRun /warden enable to turn it on; it shows exactly what leaves this machine before anything does.")
		w.show(ctx, b.String())
		return
	}
	target := w.targetOf(ctx, cfg.Backend)
	fmt.Fprintf(&b, "warden is on: %s, %s mode.\n", describeBackend(cfg.Backend, target), cfg.Mode)
	if cfg.Backend != BackendNone && cfg.Consent != cfg.Backend {
		b.WriteString("  ! the backend was not agreed to; running offline. /warden enable reviews it.\n")
	}
	if cfg.Backend == BackendTypeSafe && w.opts.Getenv("TYPESAFE_API_KEY") == "" {
		b.WriteString("  ! TYPESAFE_API_KEY is not set in this environment; judged checks are skipped.\n")
	}
	fmt.Fprintf(&b, "  checked %d calls, judged %d, held %d, warned %d, approved %d; %d steers sent (%d skipped as repeats or over budget), %d stuck, %d done-checks (%d unverified), %d judge errors\n",
		s.Inspected, s.Judged, s.Held, s.Warned, s.Approved, s.Steers, s.SteersSkipped, s.Stuck, s.DoneChecks, s.Unverified, s.Errors)
	fmt.Fprintf(&b, "  judge requests this session: %d of %d\n\n", w.budget.Used(), cfg.MaxRequests)
	b.WriteString(Disclosure(cfg.Backend, target))
	w.show(ctx, b.String())
}

func (w *warden) showTrace(ctx sdk.Context) {
	w.mu.Lock()
	var lines []string
	for _, t := range w.trace {
		lines = append(lines, t.At.Format("15:04:05")+" "+t.Line)
	}
	w.mu.Unlock()
	if len(lines) == 0 {
		w.show(ctx, "warden has not decided anything yet this session.")
		return
	}
	w.show(ctx, strings.Join(lines, "\n"))
}

// enable turns warden on. A backend that sends anything anywhere shows its data flow first and needs a yes.
func (w *warden) enable(ctx sdk.Context, args []string) error {
	cfg := w.config(ctx)
	backend := ""
	chosen := false
	if len(args) > 0 {
		chosen = true
		switch strings.ToLower(args[0]) {
		case "typesafe", "jev":
			backend = BackendTypeSafe
		case "own-model", "own", "session", "model":
			backend = BackendOwnModel
		case "offline", "none", "patterns":
			backend = BackendNone
		default:
			w.show(ctx, "unknown backend "+args[0]+": use typesafe, own-model or offline.")
			return nil
		}
	}
	if !chosen {
		if !ctx.HasUI() {
			w.show(ctx, "warden enable needs a choice when there is no dialog: /warden enable typesafe | own-model | offline. (A headless run can also set PIGPEN_WARDEN_ENABLED=1 and PIGPEN_WARDEN_BACKEND.)")
			return nil
		}
		const (
			optOwn     = "This session's model — nothing goes to a third party (own-model)"
			optTS      = "TypeSafe Jev — TypeSafe's hosted judge model; needs TYPESAFE_API_KEY (typesafe)"
			optOffline = "Offline patterns only — nothing leaves this machine"
			optCancel  = "Cancel"
		)
		pick, ok, err := ctx.Select("Where should warden's judgments come from?", []string{optOwn, optTS, optOffline, optCancel})
		if err != nil {
			return err
		}
		if !ok || pick == optCancel {
			w.show(ctx, "warden: nothing changed.")
			return nil
		}
		switch pick {
		case optOwn:
			backend = BackendOwnModel
		case optTS:
			backend = BackendTypeSafe
		}
	}
	if backend != BackendNone {
		target := w.targetOf(ctx, backend)
		if !ctx.HasUI() {
			w.show(ctx, "Turning on a judged backend needs you to read what it sends and agree, which takes a dialog. Run /warden enable "+backendWord(backend)+" in an interactive session, or set PIGPEN_WARDEN_ENABLED=1 and PIGPEN_WARDEN_BACKEND="+backend+" to agree in the environment.\n\n"+Disclosure(backend, target))
			return nil
		}
		agreed, err := ctx.Confirm("Send redacted call summaries to "+describeBackend(backend, target)+"?", Disclosure(backend, target))
		if err != nil {
			return err
		}
		if !agreed {
			w.show(ctx, "warden: nothing changed; nothing was sent.")
			return nil
		}
	}
	cfg, err := w.update(ctx, func(c *Config) { c.Enabled, c.Backend, c.Consent = true, backend, backend })
	if err != nil {
		return err
	}
	w.budget.Reset()
	w.guard.Reset()
	w.refresh(ctx)
	msg := "warden is on: " + describeBackend(backend, w.targetOf(ctx, backend)) + ", " + cfg.Mode + " mode."
	if backend == BackendTypeSafe && w.opts.Getenv("TYPESAFE_API_KEY") == "" {
		msg += "\n! TYPESAFE_API_KEY is not set in the environment PiG runs in, so judged checks are skipped until it is. Offline patterns are active."
	}
	w.show(ctx, msg+"\nTry /warden test to see it stop a dangerous call (nothing is executed).")
	return nil
}

func backendWord(b string) string {
	if b == BackendOwnModel {
		return "own-model"
	}
	return b
}

// selfTest runs synthetic dangerous calls through the real guard and shows what warden would do and what the
// agent would be told. It executes nothing.
func (w *warden) selfTest(ctx sdk.Context) {
	cfg := w.config(ctx)
	cfg.Enabled = true
	action := cfg.Action
	action.Enabled = true
	judge := w.judgeFor(ctx, cfg)
	cases := []struct{ label, tool, command, task string }{
		{"a force push", "bash", "git push --force origin main", "tidy up the readme"},
		{"a recursive delete of the home directory", "bash", "rm -rf ~/projects", "clean the build output"},
		{"a secrets file", "bash", "cat .env", "why does login fail"},
		{"an ordinary test run", "bash", "npm test", "fix the failing test"},
	}
	var b strings.Builder
	b.WriteString("warden test — synthetic calls, nothing was run.\n")
	if judge == nil {
		b.WriteString("Judge: none (offline patterns only). /warden enable adds a judge for off-task, off-plan and irreversible-by-context calls.\n")
	} else {
		b.WriteString("Judge: " + describeBackend(cfg.Backend, w.targetOf(ctx, cfg.Backend)) + ".\n")
	}
	for _, c := range cases {
		start := time.Now()
		v := EvaluateAction(context.Background(), ActionInput{Tool: c.tool, Input: map[string]any{"command": c.command}, Cwd: ctx.Cwd(), Task: c.task}, EvaluateOptions{Config: action, Judge: judge, Timeout: w.timeout(cfg), Git: w.opts.Git})
		fmt.Fprintf(&b, "\n  %s\n    $ %s\n    %s (%d ms)", c.label, c.command, verdictWord(v, w.mode(cfg, ctx)), time.Since(start).Milliseconds())
		if len(v.Reasons) > 0 {
			b.WriteString(": " + strings.Join(v.Reasons, "; "))
		}
		if v.Level == LevelConfirm && w.mode(cfg, ctx) == "steer" {
			b.WriteString("\n    the agent would read: " + clip(SteerReason(deliveryVerdict(v), judge != nil), 260))
		}
	}
	w.show(ctx, b.String())
}

func verdictWord(v Verdict, mode string) string {
	switch v.Level {
	case LevelDeny:
		return "BLOCKED"
	case LevelConfirm:
		switch mode {
		case "confirm":
			return "WOULD ASK YOU"
		case "advise":
			return "would warn"
		}
		return "HELD"
	case LevelWarn:
		return "would warn"
	}
	return "allowed"
}
