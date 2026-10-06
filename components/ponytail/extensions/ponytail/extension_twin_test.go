package ponytail

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// rig is a fake host for the extension: it answers the session reads and idle checks, and keeps the calls.
type rig struct {
	*Host
	mu      sync.Mutex
	entries []obj
	idle    bool
}

func startRig(t *testing.T, entries ...obj) *rig {
	t.Helper()
	r := &rig{entries: entries, idle: true}
	r.Host = StartHost(t, Extension(), HostOptions{OnCallValue: r.answer})
	return r
}

func (r *rig) answer(method string, args map[string]any) (any, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch method {
	case "sessionRead":
		if args["method"] == "getBranch" || args["method"] == "getEntries" {
			if r.entries == nil {
				return []obj{}, ""
			}
			return r.entries, ""
		}
	case "isIdle":
		return obj{"idle": r.idle}, ""
	}
	return obj{}, ""
}

func (r *rig) start(reason string) { r.Host.Fire("session_start", obj{"reason": reason}) }
func (r *rig) command(n, a string) { r.Host.Command(n, a) }
func (r *rig) input(text string)   { r.Host.Fire("input", obj{"text": text, "source": "interactive"}) }
func (r *rig) agentStart()         { r.Host.Fire("agent_start", obj{}) }

// prompt runs before_agent_start with a base system prompt and returns the prompt the handler sets ("" when it sets none).
func (r *rig) prompt(base any) (string, bool) {
	data := obj{"prompt": "hello", "systemPromptOptions": obj{}}
	if base != nil {
		data["systemPrompt"] = base
	}
	raw := r.Host.Fire("before_agent_start", data)
	var res struct {
		Result *struct {
			SystemPrompt *string `json:"systemPrompt"`
		} `json:"_pigPromptResult"`
	}
	json.Unmarshal(raw, &res)
	if res.Result == nil || res.Result.SystemPrompt == nil {
		return "", false
	}
	return *res.Result.SystemPrompt, true
}

func (r *rig) statuses() []string {
	var out []string
	for _, c := range r.CallsTo("ui.setStatus") {
		if c.Args["key"] == "ponytail" {
			out = append(out, c.Args["text"].(string))
		}
	}
	return out
}

func (r *rig) notices() []string {
	var out []string
	for _, c := range r.CallsTo("ui.notify") {
		out = append(out, c.Args["message"].(string))
	}
	return out
}

func TestExtension(t *testing.T) {
	const f = "extension"
	tw(t, f, "extension registers Ponytail commands", func(t *testing.T) {
		configHome(t)
		r := startRig(t)
		var names []string
		for n := range r.cmds {
			names = append(names, n)
		}
		sort.Strings(names)
		eq(t, names, []string{"ponytail", "ponytail-audit", "ponytail-debt", "ponytail-gain", "ponytail-help", "ponytail-review"})
	})
	tw(t, f, "/ponytail updates session mode and injects instructions", func(t *testing.T) {
		configHome(t)
		r := startRig(t)
		r.start("startup")
		r.command("ponytail", "ultra")
		appended := r.CallsTo("appendEntry")
		eq(t, appended[len(appended)-1].Args, obj{"customType": "ponytail-mode", "data": obj{"mode": "ultra"}})
		p, ok := r.prompt("BASE")
		eq(t, ok, true)
		eq(t, strings.Contains(p, "PONYTAIL MODE ACTIVE"), true)
		eq(t, strings.Contains(p, "ultra"), true)
	})
	tw(t, f, "before_agent_start guards missing event and missing systemPrompt (#439, #440)", func(t *testing.T) {
		configHome(t)
		r := startRig(t)
		r.start("startup")
		// A null event cannot reach a Go handler (the host always sends an object); the missing and empty prompts can.
		for _, base := range []any{nil, ""} {
			p, ok := r.prompt(base)
			eq(t, ok, true)
			eq(t, strings.Contains(p, "PONYTAIL MODE ACTIVE"), true)
			eq(t, strings.Contains(p, "undefined"), false)
			eq(t, strings.HasPrefix(p, "undefined"), false)
		}
		p, _ := r.prompt("BASE")
		eq(t, strings.HasPrefix(p, "BASE\n\n"), true)
		eq(t, strings.Contains(p, "PONYTAIL MODE ACTIVE"), true)
	})
	tw(t, f, "session_start restores latest persisted mode", func(t *testing.T) {
		configHome(t)
		r := startRig(t, customEntry("lite"))
		r.start("resume")
		p, _ := r.prompt("BASE")
		eq(t, strings.Contains(p, "lite"), true)
	})
	tw(t, f, "skill alias commands delegate to Pi skill commands", func(t *testing.T) {
		configHome(t)
		r := startRig(t)
		for _, n := range []string{"review", "audit", "debt", "gain", "help"} {
			r.command("ponytail-"+n, "")
		}
		var got []string
		for _, c := range r.CallsTo("sendUserMessage") {
			got = append(got, c.Args["content"].(string))
		}
		// The Skills of this Package are named pigpen-ponytail-*, so the aliases name them.
		eq(t, got, []string{"/skill:pigpen-ponytail-review", "/skill:pigpen-ponytail-audit", "/skill:pigpen-ponytail-debt", "/skill:pigpen-ponytail-gain", "/skill:pigpen-ponytail-help"})
	})
	tw(t, f, "normal mode disables persistent instructions", func(t *testing.T) {
		configHome(t)
		r := startRig(t)
		r.start("startup")
		r.command("ponytail", "ultra")
		r.input("normal mode")
		_, ok := r.prompt("BASE")
		eq(t, ok, false)
	})
	tw(t, f, "a request mentioning normal mode stays active", func(t *testing.T) {
		configHome(t)
		r := startRig(t)
		r.start("startup")
		r.command("ponytail", "ultra")
		r.input("add a normal mode toggle next to dark mode")
		p, _ := r.prompt("BASE")
		eq(t, regexp.MustCompile(`PONYTAIL MODE ACTIVE`).MatchString(p), true)
	})
	tw(t, f, "status bar renders the mode and flips active on agent_start", func(t *testing.T) {
		configHome(t)
		r := startRig(t, customEntry("ultra"))
		r.start("resume")
		r.agentStart()
		s := r.statuses()
		if len(s) < 2 {
			t.Fatalf("want two status writes, got %q", s)
		}
		eq(t, regexp.MustCompile(`○.*ULTRA`).MatchString(s[len(s)-2]), true)
		eq(t, regexp.MustCompile(`●.*ULTRA`).MatchString(s[len(s)-1]), true)
	})
	tskip(t, f, "status bar stays silent when ui lacks a theme", "the Go SDK has no theme object: the status text is plain, so there is no theme whose absence could silence it (a host without UI ignores the status call)")
	tw(t, f, "PONYTAIL_HIDE_STATUS hides the indicator but keeps ponytail active (#324)", func(t *testing.T) {
		configHome(t)
		setEnv(t, "PONYTAIL_HIDE_STATUS", "1")
		r := startRig(t, customEntry("ultra"))
		r.start("resume")
		r.agentStart()
		p, _ := r.prompt("BASE")
		eq(t, len(r.statuses()), 0)
		eq(t, regexp.MustCompile(`PONYTAIL MODE ACTIVE`).MatchString(p), true)
	})
	tw(t, f, "config.hideStatus hides the indicator but keeps ponytail active (#324)", func(t *testing.T) {
		dir := configHome(t)
		writeConfig(t, dir, `{"hideStatus":true}`)
		r := startRig(t)
		r.start("startup")
		r.agentStart()
		p, _ := r.prompt("BASE")
		eq(t, len(r.statuses()), 0)
		eq(t, regexp.MustCompile(`PONYTAIL MODE ACTIVE`).MatchString(p), true)
	})
	tw(t, f, "PONYTAIL_HIDE_STATUS=0 does not hide the indicator", func(t *testing.T) {
		configHome(t)
		setEnv(t, "PONYTAIL_HIDE_STATUS", "0")
		r := startRig(t, customEntry("ultra"))
		r.start("resume")
		r.agentStart()
		eq(t, len(r.statuses()) > 0, true)
	})
}
