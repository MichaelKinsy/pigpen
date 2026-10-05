package warden

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// Widget templates and tokens (src/widget.ts): one line per verdict, segments joined by " · ", a segment
// dropped when any of its tokens has no value.

// Templates are the per-guard verdict line templates.
type Templates struct {
	Action string `json:"action"`
	Stuck  string `json:"stuck"`
	Done   string `json:"done"`
}

// DefaultTemplates are the upstream data-style templates.
var DefaultTemplates = Templates{
	Action: "warden · {tool} · irreversible {irreversible} · off-task {offTask} · {scope} · slop: {slop} · patterns: {patterns} · {flags} · {level}",
	Stuck:  "warden · stuck · {failures} failures · same strategy {sameStrategy} · change {approachChange} · progress {progress} · {flags} · {status}",
	Done:   "warden · done-check · {changes} changes · {checksPassed}/{checks} checks passed · claims done {claimsDone} · claims verified {claimsVerified} · checks apply {checksApply} · {outcome} · {status}",
}

// Tokens are a verdict's template values; a missing key or an empty value drops its segment.
type Tokens map[string]string

const separator = " · "

var reToken = lazyRE(`\{([a-zA-Z]+)\}`)

// RenderTemplate fills `{token}` placeholders; a segment is dropped when any of its tokens is empty.
func RenderTemplate(template string, tokens Tokens) string {
	var segments []string
	for _, segment := range strings.Split(template, separator) {
		missing := false
		rendered := reToken.ReplaceAllStringFunc(segment, func(m string) string {
			v := tokens[m[1:len(m)-1]]
			if v == "" {
				missing = true
			}
			return v
		})
		if !missing && jsTrim(rendered) != "" {
			segments = append(segments, jsTrim(rendered))
		}
	}
	return strings.Join(segments, separator)
}

// toFixed is JavaScript's Number.prototype.toFixed: a tie rounds up in magnitude, where Go rounds to even.
func toFixed(x float64, digits int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return strconv.FormatFloat(x, 'f', digits, 64)
	}
	exact := big.NewFloat(math.Abs(x)).Text('f', 80)
	dot := strings.IndexByte(exact, '.')
	tail := exact[dot+1+digits:]
	if len(tail) > 0 && tail[0] == '5' && strings.Trim(tail[1:], "0") == "" {
		toward := math.Inf(1)
		if x < 0 {
			toward = math.Inf(-1)
		}
		x = math.Nextafter(x, toward)
	}
	return strconv.FormatFloat(x, 'f', digits, 64)
}

func percent(v float64) string { return toFixed(v, 2) }

func fixedp(v *float64) string {
	if v == nil {
		return ""
	}
	return toFixed(*v, 2)
}

func put(t Tokens, key, value string) {
	if value != "" {
		t[key] = value
	}
}

func clock(at time.Time) string { return at.Format("15:04:05") }

func actionTokens(v Verdict, at time.Time) Tokens {
	var flags []string
	if v.ApprovedByUser {
		flags = append(flags, "user approved")
	}
	if v.IntentMismatch {
		flags = append(flags, "off plan")
	}
	if v.OffTaskSteer {
		flags = append(flags, "off task")
	}
	if v.Source == "error" {
		flags = append(flags, "typesafe error")
	}
	if v.Source == "read-only" {
		flags = append(flags, "read-only")
	}
	t := Tokens{"guard": "action", "time": clock(at), "tool": v.Summary.Tool, "level": string(v.Level), "source": v.Source}
	if j := v.Judgment; j != nil {
		t["irreversible"] = toFixed(j.Irreversible, 2)
		t["offTask"] = toFixed(j.OffTask, 2)
		put(t, "scope", strings.ReplaceAll(j.Scope, "_", " "))
		put(t, "approved", fixedp(j.Approved))
		put(t, "intent", fixedp(j.IntentMismatch))
		put(t, "visible", fixedp(j.Visible))
		put(t, "model", j.Model)
		t["ms"] = strconv.Itoa(j.ElapsedMs)
	}
	if v.Plan != "" {
		plan := v.Plan
		if utf16Len(plan) > 80 {
			plan = utf16Slice(plan, 0, 80) + "…"
		}
		t["plan"] = reWhitespaceRun.ReplaceAllString(plan, " ")
	}
	if len(v.Patterns) > 0 {
		ids := make([]string, len(v.Patterns))
		for i, h := range v.Patterns {
			ids[i] = h.ID
		}
		t["patterns"] = strings.Join(ids, ", ")
	}
	if len(v.Reasons) > 0 {
		t["reasons"] = strings.Join(v.Reasons, "; ")
	}
	put(t, "path", v.Summary.Path)
	put(t, "flags", strings.Join(flags, ", "))
	return t
}

func stuckTokens(v StuckVerdict, at time.Time) Tokens {
	var flags []string
	if v.Source == "repeat" && v.Stuck {
		switch {
		case v.SuccessRepeat:
			flags = append(flags, "successful repeat")
		case v.Churn:
			flags = append(flags, "churn")
		default:
			flags = append(flags, "exact repeat")
		}
	}
	if v.Source == "error" {
		flags = append(flags, "typesafe error")
	}
	status := "ok"
	if v.Stuck {
		status = "stuck"
	}
	t := Tokens{"guard": "stuck", "time": clock(at), "failures": strconv.Itoa(v.Failures), "status": status, "source": v.Source}
	if j := v.Judgment; j != nil {
		t["sameStrategy"] = toFixed(j.SameStrategy, 2)
		t["approachChange"] = toFixed(j.ApproachChange, 1) + "/2"
		t["progress"] = toFixed(j.Progress, 2)
		put(t, "model", j.Model)
		t["ms"] = strconv.Itoa(j.ElapsedMs)
	}
	if len(v.Reasons) > 0 {
		t["reasons"] = strings.Join(v.Reasons, "; ")
	}
	put(t, "flags", strings.Join(flags, ", "))
	return t
}

func doneTokens(v DoneVerdict, at time.Time) Tokens {
	checks := FreshChecks(v.Evidence)
	passed := 0
	for _, c := range checks {
		if c.Passed {
			passed++
		}
	}
	status := "ok"
	switch {
	case v.FalseClaim:
		status = "false claim"
	case v.Unverified:
		status = "unverified"
	}
	mutations := 0
	if v.Evidence != nil {
		mutations = v.Evidence.Mutations
	}
	t := Tokens{"guard": "done", "time": clock(at), "changes": strconv.Itoa(mutations), "checks": strconv.Itoa(len(checks)), "checksPassed": strconv.Itoa(passed), "status": status}
	if j := v.Judgment; j != nil {
		t["claimsDone"] = toFixed(j.ClaimsDone, 2)
		t["claimsVerified"] = toFixed(j.ClaimsVerified, 2)
		t["checksApply"] = toFixed(j.VerificationApplies, 2)
		put(t, "outcome", j.Outcome)
		put(t, "model", j.Model)
		t["ms"] = strconv.Itoa(j.ElapsedMs)
	}
	if len(v.Reasons) > 0 {
		t["reasons"] = strings.Join(v.Reasons, "; ")
	}
	if v.Error != "" {
		t["flags"] = "typesafe error"
	}
	return t
}

// FormatVerdict is the one-line rendering of an action verdict for widgets and logs. It includes no
// command text.
func FormatVerdict(v Verdict) string {
	return RenderTemplate(DefaultTemplates.Action, actionTokens(v, time.Now()))
}

// FormatStuck is the one-line rendering of a stuck verdict.
func FormatStuck(v StuckVerdict) string {
	return RenderTemplate(DefaultTemplates.Stuck, stuckTokens(v, time.Now()))
}

// FormatDone is the one-line rendering of a done-check verdict.
func FormatDone(v DoneVerdict) string {
	return RenderTemplate(DefaultTemplates.Done, doneTokens(v, time.Now()))
}
