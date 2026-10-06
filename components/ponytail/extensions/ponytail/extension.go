package ponytail

import (
	"fmt"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The Ponytail extension. upstream: pi-extension/index.js.

type command struct{ Type, Mode, Reason string }

// parsePonytailCommand reads the argument of /ponytail: nothing sets the default level (full when that is off), a
// level sets it, `status` reports, `default <level>` saves one. review is a session level only, never a default (#377).
func parsePonytailCommand(text, dflt string) command {
	fallback := normalizePersistedMode(dflt)
	if fallback == "" {
		fallback = defaultMode
	}
	words := strings.FieldsFunc(strings.ToLower(jsTrim(text)), isJSSpace)
	if len(words) == 0 {
		if fallback == "off" {
			fallback = "full"
		}
		return command{Type: "set-mode", Mode: fallback}
	}
	switch words[0] {
	case "status":
		return command{Type: "status"}
	case "default":
		second := ""
		if len(words) > 1 {
			second = words[1]
		}
		if m := normalizeMode(second); m != "" {
			return command{Type: "set-default", Mode: m}
		}
		return command{Type: "invalid", Reason: "invalid-default-mode"}
	}
	if m := normalizeMode(words[0]); m != "" {
		return command{Type: "set-mode", Mode: m}
	}
	return command{Type: "invalid", Reason: "invalid-mode", Mode: words[0]}
}

// resolveSessionMode is the latest mode a `ponytail-mode` entry of the session recorded, else the fallback.
// entries is a list of session entries; anything else yields the fallback.
func resolveSessionMode(entries any, fallbackMode string) string {
	fallback := normalizePersistedMode(fallbackMode)
	if fallback == "" {
		fallback = defaultMode
	}
	var list []map[string]any
	switch v := entries.(type) {
	case []map[string]any:
		list = v
	case []any:
		for _, e := range v {
			m, _ := e.(map[string]any)
			list = append(list, m)
		}
	default:
		return fallback
	}
	for i := len(list) - 1; i >= 0; i-- {
		e := list[i]
		if e["type"] != "custom" || e["customType"] != "ponytail-mode" {
			continue
		}
		data, _ := e["data"].(map[string]any)
		mode, _ := data["mode"].(string)
		if m := normalizePersistedMode(mode); m != "" {
			return m
		}
	}
	return fallback
}

type app struct {
	mu         sync.Mutex
	mode       string
	dflt       string
	hideStatus bool
	active     bool
	last       *sdk.Context
}

// commandDescription is the help line of /ponytail.
func commandDescription() string {
	return "Set mode: " + strings.Join(runtimeModes, "|") + ". Commands: status, default <mode>"
}

// Extension returns the Ponytail extension.
func Extension() *sdk.Extension {
	e := sdk.New("ponytail")
	a := &app{mode: defaultMode, dflt: getDefaultMode(), hideStatus: getHideStatus()}
	a.register(e)
	return e
}

// syncStatus draws the status-bar indicator: a dot (filled while the agent works), the level and its icon. Plain text:
// the Go SDK has no theme to colour it with.
func (a *app) syncStatus(ctx *sdk.Context) {
	a.mu.Lock()
	if ctx != nil {
		a.last = ctx
	} else {
		ctx = a.last
	}
	hide, mode, active := a.hideStatus, a.mode, a.active
	a.mu.Unlock()
	if hide || ctx == nil {
		return
	}
	if mode == "off" {
		ctx.SetStatus("ponytail", "")
		return
	}
	icon := map[string]string{"lite": "🌿", "full": "⚡", "ultra": "🔥"}[mode]
	dot := "○"
	if active {
		dot = "●"
	}
	ctx.SetStatus("ponytail", dot+" 🐴 ponytail: "+icon+" "+strings.ToUpper(mode))
}

// setMode records a mode in the session and shows it. notify is false when the change did not come from a command.
func (a *app) setMode(mode string, ctx *sdk.Context, notify bool) {
	n := normalizePersistedMode(mode)
	if n == "" {
		return
	}
	a.mu.Lock()
	a.mode = n
	a.mu.Unlock()
	if ctx != nil {
		_ = ctx.AppendEntry("ponytail-mode", map[string]any{"mode": n})
	}
	a.syncStatus(ctx)
	if notify && ctx != nil {
		ctx.Notify(fmt.Sprintf("Ponytail mode set to %s.", n), "info")
	}
}

func (a *app) alias(skill string) sdk.CommandFunc {
	return func(ctx sdk.Context, _ string) error {
		message := "/skill:" + skill
		if idle, err := ctx.IsIdle(); err == nil && !idle {
			_ = ctx.SendUserMessage(message, "followUp")
			ctx.Notify(message[len("/skill:"):]+" queued as follow-up.", "info")
			return nil
		}
		return ctx.SendUserMessage(message, "")
	}
}

func (a *app) register(e *sdk.Extension) {
	e.Command("ponytail", commandDescription(), func(ctx sdk.Context, args string) error {
		a.mu.Lock()
		mode, dflt := a.mode, a.dflt
		a.mu.Unlock()
		p := parsePonytailCommand(args, dflt)
		switch p.Type {
		case "status":
			ctx.Notify(fmt.Sprintf("Ponytail: current %s • default %s", mode, dflt), "info")
		case "set-default":
			written, err := writeDefaultMode(p.Mode)
			if err != nil {
				ctx.Notify("Failed to save default mode: "+err.Error(), "error")
				return nil
			}
			if written != "" {
				now := getDefaultMode()
				a.mu.Lock()
				a.dflt = now
				a.mu.Unlock()
				if now == written {
					ctx.Notify(fmt.Sprintf("Default Ponytail mode set to %s.", written), "info")
				} else {
					ctx.Notify(fmt.Sprintf("Saved default %s, but env override keeps default at %s.", written, now), "info")
				}
			}
		case "set-mode":
			a.setMode(p.Mode, &ctx, true)
		default:
			ctx.Notify("Unknown or unsupported /ponytail mode.", "warning")
		}
		return nil
	})
	for _, n := range []string{"review", "audit", "gain", "debt", "help"} {
		// The Skills of this Package are named pigpen-ponytail-*; the aliases name them.
		e.Command("ponytail-"+n, "Run /skill:pigpen-ponytail-"+n, a.alias("pigpen-ponytail-"+n))
	}
	e.OnEvent(sdk.EventInput, func(ctx sdk.Context, data map[string]any) (any, error) {
		if data["source"] == "extension" {
			return nil, nil
		}
		text, _ := data["text"].(string)
		a.mu.Lock()
		on := a.mode != "off"
		a.mu.Unlock()
		if on && isDeactivationCommand(text) {
			a.setMode("off", &ctx, false)
		}
		return nil, nil
	})
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		entries, _ := ctx.SessionManager().GetBranch(nil)
		a.mu.Lock()
		a.dflt = getDefaultMode()
		a.hideStatus = getHideStatus()
		a.mode = resolveSessionMode(entries, a.dflt)
		mode := a.mode
		a.mu.Unlock()
		a.syncStatus(&ctx)
		if !getQuietStartup() {
			ctx.Notify("Ponytail loaded: "+mode, "info")
		}
		return nil, nil
	})
	for event, active := range map[string]bool{sdk.EventAgentStart: true, sdk.EventAgentEnd: false} {
		e.OnEvent(event, func(ctx sdk.Context, _ map[string]any) (any, error) {
			a.mu.Lock()
			a.active = active
			a.mu.Unlock()
			a.syncStatus(&ctx)
			return nil, nil
		})
	}
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		a.mu.Lock()
		mode := a.mode
		a.mu.Unlock()
		if mode == "" || mode == "off" {
			return nil, nil
		}
		base := ""
		if s, _ := data["systemPrompt"].(string); s != "" {
			base = s + "\n\n"
		}
		return map[string]any{"systemPrompt": base + getPonytailInstructions(mode)}, nil
	})
}
