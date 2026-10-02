package jev

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func (x *ext) note(ctx sdk.Context, level, plain, rich string) {
	if x.plain() {
		ctx.Notify(plain, level)
		return
	}
	ctx.Notify(rich, level)
}

func (x *ext) command(ctx sdk.Context, args string) error {
	trimmed := strings.TrimSpace(args)
	fields := strings.Fields(strings.ToLower(trimmed))
	sub, value := "", ""
	if len(fields) > 0 {
		sub = fields[0]
	}
	if len(fields) > 1 {
		value = fields[1]
	}
	switch sub {
	case "on":
		x.turnOn(ctx)
	case "off":
		x.mu.Lock()
		x.on, x.gateOn, x.outputOn = false, false, false
		x.mu.Unlock()
		x.setStatus(ctx, "", "off")
		x.note(ctx, "info", "pi-jev: gate off", "pi-jev: gate off")
	case "mode":
		x.setMode(ctx, value)
	case "last":
		x.showLast(ctx)
	case "output":
		x.showOutput(ctx)
	case "check":
		x.check(ctx, strings.TrimSpace(trimmed[len("check"):]))
	default:
		x.status(ctx)
	}
	return nil
}

func (x *ext) turnOn(ctx sdk.Context) {
	s := x.snapshot()
	if s.be == nil {
		reason := "Jev cannot start"
		x.mu.Lock()
		if x.beEr != nil {
			reason = x.beEr.Error()
		}
		x.mu.Unlock()
		ctx.Notify("pi-jev: "+x.redact(reason), "warning")
		return
	}
	if !s.cfg.Enabled && !s.on {
		// Turning it on sends content off the machine: ask, with the disclosure.
		ok, err := ctx.Confirm("Turn on Jev for this session?", disclosure(s.cfg, s.be.Destination())+"\n\nTurn Jev on?")
		if err != nil || !ok {
			ctx.Notify("pi-jev: still off; nothing is sent", "info")
			return
		}
	}
	x.mu.Lock()
	x.on, x.gateOn, x.outputOn = true, true, true
	x.mu.Unlock()
	x.baseStatus(ctx)
	x.registerAsk(ctx)
	x.note(ctx, "info", "pi-jev: gate on", "pi-jev: gate on")
}

func (x *ext) setMode(ctx sdk.Context, value string) {
	if value != "shadow" && value != "enforce" {
		s := x.snapshot()
		ctx.Notify(fmt.Sprintf("pi-jev: mode is %s (usage: /jev mode shadow|enforce)", s.mode), "warning")
		return
	}
	x.mu.Lock()
	x.mode = value
	x.mu.Unlock()
	x.baseStatus(ctx)
	tail := " (asks before running flagged calls)"
	if value == "shadow" {
		tail = " (reports, never blocks)"
	}
	ctx.Notify("pi-jev: "+value+tail, "info")
}

func (x *ext) showLast(ctx sdk.Context) {
	x.mu.Lock()
	l, fail := x.last, x.lastFailure
	x.mu.Unlock()
	if l == nil {
		msg := "pi-jev: no verdicts yet"
		if fail != "" {
			msg += " (the last attempt failed: " + fail + ")"
		}
		ctx.Notify(msg, "info")
		return
	}
	ctx.Notify(fmt.Sprintf("pi-jev: %s - %s | %s", l.tool, l.verdict.summary(), describeAnswers(l.verdict.Resp, gateQuestions)), "info")
}

func (x *ext) showOutput(ctx sdk.Context) {
	x.mu.Lock()
	l := x.lastOutput
	x.mu.Unlock()
	if l == nil {
		ctx.Notify("pi-jev: no tool output judged yet", "info")
		return
	}
	v := l.verdict
	class, conf := v.FailureClass, "n/a"
	if class == "" {
		class = "none"
	}
	if v.HasClass {
		conf = toFixed2(v.ClassConfidence)
	}
	ctx.Notify(fmt.Sprintf("pi-jev output: %s - %s | leak %s | class %s at %s", l.tool, v.Kind, toFixed2(v.LeaksSecret), class, conf), "info")
}

// check runs the gate questions against text the user supplies. It sends that text
// to the judge, so it needs the same opt-in as everything else.
func (x *ext) check(ctx sdk.Context, text string) {
	s := x.snapshot()
	if text == "" {
		ctx.Notify("pi-jev: usage /jev check <text>", "warning")
		return
	}
	if s.be == nil || !s.on {
		reason := "Jev is off (run /jev on first: /jev check sends your text to the judge)"
		x.mu.Lock()
		if s.on && x.beEr != nil {
			reason = x.beEr.Error()
		}
		x.mu.Unlock()
		ctx.Notify("pi-jev: "+x.redact(reason), "warning")
		return
	}
	gctx, cancel := goContext(ctx)
	defer cancel()
	resp, err := s.be.Ask(gctx, ctx, jsonString(text), true, gateQuestions)
	if err != nil {
		ctx.Notify("pi-jev: "+x.redact(err.Error()), "error")
		return
	}
	v := evaluateGate(resp, s.cfg)
	x.mu.Lock()
	x.last = &lastGate{"check", v}
	x.mu.Unlock()
	ctx.Notify(fmt.Sprintf("pi-jev check: %s | %s", v.summary(), describeAnswers(resp, gateQuestions)), "info")
}

// status is /jev with no argument.
func (x *ext) status(ctx sdk.Context) {
	s := x.snapshot()
	x.mu.Lock()
	beErr, last, lastOut, fail := x.beEr, x.last, x.lastOutput, x.lastFailure
	x.mu.Unlock()
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	if s.cfg.Display == "plain" {
		keySource := s.cfg.KeySource
		if keySource == "" {
			keySource = "missing (" + apiKeyEnv + ")"
		}
		ctx.Notify(fmt.Sprintf("pi-jev: %s, out %s, mode %s, model %s, key %s, judging %s, out tools %s, cache %d/%d",
			onOff(s.gateOn), onOff(s.outputOn), s.mode, s.cfg.Model, keySource,
			strings.Join(s.cfg.Gate.Tools, "/"), strings.Join(s.cfg.Output.Tools, "/"), x.gateMemo.size(), x.outMemo.size()), "info")
		return
	}
	var b strings.Builder
	dest := ""
	if s.be != nil {
		dest = s.be.Destination()
	}
	if !s.on {
		b.WriteString("Jev is off. Nothing is judged and nothing is sent.\n")
		b.WriteString("Content sent for judgment would leave this machine; /jev on shows exactly what, and where, before it turns anything on.\n")
		if beErr != nil {
			fmt.Fprintf(&b, "Setup: %s\n", x.redact(beErr.Error()))
		} else if dest != "" {
			fmt.Fprintf(&b, "Judge: %s\n", dest)
		}
		ctx.Notify(strings.TrimRight(b.String(), "\n"), "info")
		return
	}
	fmt.Fprintf(&b, "Jev is ON — %s mode, output judge %s\n", s.mode, onOff(s.outputOn))
	fmt.Fprintf(&b, "  Judge     %s\n", dest)
	if s.cfg.Backend == backendTypeSafe {
		ks := s.cfg.KeySource
		if ks == "" {
			ks = "missing"
		}
		fmt.Fprintf(&b, "  Key       %s\n", ks)
	}
	fmt.Fprintf(&b, "  Gate      %s (%s)\n", strings.Join(s.cfg.Gate.Tools, ", "), onOff(s.gateOn))
	fmt.Fprintf(&b, "  Output    %s (%s)\n", strings.Join(s.cfg.Output.Tools, ", "), onOff(s.outputOn))
	b.WriteString("  Sends     working directory, tool name, your last message, tool arguments, tool output, jev_ask text — cut to your limits\n")
	b.WriteString("  If down   tool calls run unjudged (fails open)\n")
	if last != nil {
		fmt.Fprintf(&b, "  Last      %s: %s\n", last.tool, last.verdict.summary())
	}
	if lastOut != nil {
		fmt.Fprintf(&b, "  Output    %s: %s\n", lastOut.tool, lastOut.verdict.Kind)
	}
	if fail != "" {
		fmt.Fprintf(&b, "  Failure   %s\n", fail)
	}
	b.WriteString("  Commands  /jev on|off · /jev mode shadow|enforce · /jev last · /jev output · /jev check <text>")
	ctx.Notify(b.String(), "info")
}
