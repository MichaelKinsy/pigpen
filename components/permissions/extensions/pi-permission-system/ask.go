package pi_permission_system

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The ask flow: the approval dialog (the original's authority/permission-prompt-component.ts and
// local-user-authorizer.ts, rendered here with PiG's select/input dialogs instead of the original's inline keybind
// component), its denial text (presentation/agent-renderer.ts renderUserDenial and renderUnavailableDenial, in the
// port's compact clause style), and the session grants a "for this session" approval records
// (session/session-rules.ts, presentation/pattern-suggest.ts).

// AskSession is what an approval "for this session" records: one wildcard pattern on one surface, with the label
// the dialog offers it under. An empty Surface means the ask offers no session option, as the original's skill
// gates (which carry no SessionApproval) do not.
type AskSession struct {
	Surface, Pattern, Label string
}

// Ask is a call the policy asks about: everything the dialog and a session approval need. Every field is a
// comparable string so a verdict carries value equality into tests.
type Ask struct {
	// Surface is the gated surface: the tool name, "bash", "skill", "path" or "external_directory" (with the
	// directional member when one is proven).
	Surface string
	// Pattern is the rule pattern that asked, "" when only the universal default fired (as in the original's
	// matchedPattern, which is undefined for the universal fallback).
	Pattern string
	// Value is the decision value shown to the human: a bash command, a path, a skill name. It is never rendered
	// back to the model (agent-renderer.ts §7 renders the command nowhere).
	Value string
	// Tool is the invoked tool's name when the surface is not it (a skill-read ask, a shell alias).
	Tool string
	// Session is the "for this session" option; its Surface is "" when there is none.
	Session AskSession
}

// toolAsk builds the ask for a decided rule on a valueless surface (a tool by name).
func toolAsk(surface string, r Rule) Ask {
	return Ask{Surface: surface, Pattern: patternOf(r), Session: AskSession{Surface: surface, Pattern: "*", Label: `Yes, allow tool "` + surface + `" for this session`}}
}

// bashAsk builds the ask for the bash surface: the command is the dialog's value and the session grant's
// vocabulary (suggestSessionPattern: deriveSuggestionValue reads check.command for bash).
func bashAsk(command string, r Rule) Ask {
	pattern := suggestBashPattern(command)
	return Ask{Surface: "bash", Pattern: patternOf(r), Value: jsTrim(command),
		Session: AskSession{Surface: "bash", Pattern: pattern, Label: `Yes, allow bash "` + pattern + `" for this session`}}
}

// skillAsk builds the ask on the skill surface (the skill-read and skill-input gates); the original carries no
// session approval on either, so there is no "for this session" option.
func skillAsk(skill, tool string) Ask {
	return Ask{Surface: "skill", Value: skill, Tool: tool}
}

func patternOf(r Rule) string {
	if r.Layer == "config" || r.Layer == "session" {
		return r.Pattern
	}
	return ""
}

// suggestBashPattern is suggestBashPattern for a command string (the floor asks, whose deciding rule carries no
// command): a one-token command is exact, anything else is `<first token> *`.
func suggestBashPattern(command string) string {
	words := strings.Fields(jsTrim(command))
	switch len(words) {
	case 0:
		return "*"
	case 1:
		return words[0]
	}
	return words[0] + " *"
}

// floorAsk is the ask a rule's allow is floored to when the port cannot stand in for what the original does with it
// (a wrapper, a guarded command, a command it cannot read): yoloMode grants it, as it grants every ask.
func (p *Policy) floorAsk(a Ask) Verdict {
	if p.yolo {
		return Verdict{}
	}
	return Verdict{Ask: &a}
}

// pathAsk is the ask on a path surface (the path and external_directory gates), carrying the session grant of the
// resolved path on the gate's own surface.
func pathAsk(surface, pattern, value string) Verdict {
	label := `Yes, allow path "` + value + `" for this session`
	if surfaceFamilyOf(surface) == "external_directory" {
		label = `Yes, allow access to external directory "` + value + `" for this session`
	}
	return Verdict{Ask: &Ask{Surface: surface, Pattern: pattern, Value: value,
		Session: AskSession{Surface: surface, Pattern: value, Label: label}}}
}

// ── the dialog ───────────────────────────────────────────────────────────────

const (
	approveOption        = "Approve"
	denyOption           = "Deny"
	denyWithReasonOption = "Deny with a reason"
)

// askTitle is the dialog title: the surface and, when there is one, the value the human is approving. Truncated so
// a long command cannot take the whole picker.
func askTitle(a Ask) string {
	title := extensionID + ": " + a.Surface
	if a.Value != "" {
		v := a.Value
		if w := []rune(v); len(w) > 160 {
			v = string(w[:160]) + "..."
		}
		title += ": " + v
	}
	return title
}

// askDenial is the agent-facing render of a human's denial (renderUserDenial in the port's compact clause style).
// The command is never echoed back; a path or skill name is, because the model cannot recover which operand fired.
func askDenial(a Ask, reason string) string {
	var b strings.Builder
	b.WriteString("The user denied this '" + a.Surface + "' call")
	if a.Value != "" && a.Surface != "bash" {
		b.WriteString(" '" + a.Value + "'")
	}
	if a.Tool != "" && a.Tool != a.Surface {
		b.WriteString(" for tool '" + a.Tool + "'")
	}
	if a.Pattern != "" {
		b.WriteString(" (rule '" + a.Pattern + "')")
	}
	b.WriteString(".")
	out := tagged(b.String())
	if reason != "" {
		out += " Reason: " + reason + "."
	}
	return out
}

// askUnavailable is the render when no interactive UI could answer (renderUnavailableDenial, compact style).
func askUnavailable(a Ask) string {
	return tagged("This '" + a.Surface + "' call requires approval, but no interactive UI is available.")
}

// askDecision is what the dialog came back with.
type askDecision struct {
	approved, forSession bool
	reason               string // a denial reason the human typed, "" for none
	unavailable          bool   // no UI could answer; the refusal says so
}

// promptAsk runs the approval dialog. Anything but an explicit choice denies (a dismissal, a timeout, a host UI
// failure), fail-closed as the original denies when no live authority can answer.
func promptAsk(ctx sdk.Context, a Ask, sessionable bool) askDecision {
	if !ctx.HasUI() {
		return askDecision{unavailable: true}
	}
	options := []string{approveOption}
	if sessionable && a.Session.Surface != "" {
		options = append(options, a.Session.Label)
	}
	options = append(options, denyOption, denyWithReasonOption)
	selected, ok, err := ctx.Select(askTitle(a), options)
	if err != nil || !ok {
		return askDecision{}
	}
	switch choice, _ := selected.(string); choice {
	case approveOption:
		return askDecision{approved: true}
	case denyWithReasonOption:
		reason, rok, _ := ctx.Input("Denial reason", "")
		if r, is := reason.(string); rok && is && jsTrim(r) != "" {
			return askDecision{reason: jsTrim(r)}
		}
		return askDecision{}
	default:
		if a.Session.Surface != "" && sessionable && choice == a.Session.Label {
			return askDecision{approved: true, forSession: true}
		}
		return askDecision{}
	}
}

// sessionGrants is the session rules an approval records: one allow rule per grant, on the bare path families
// expanded to both directional members (SessionRules.approve), so a grant sits on a surface the next check names.
func (a Ask) sessionGrants() Ruleset {
	if a.Session.Surface == "" || a.Session.Pattern == "" {
		return nil
	}
	var out Ruleset
	for _, surface := range sessionSurfaces(a.Session.Surface) {
		out = append(out, Rule{Surface: surface, Pattern: a.Session.Pattern, Action: "allow", Layer: "session", Origin: "session"})
	}
	return out
}

// sessionSurfaces is surfaceFamilyMembers: the two directional members of a bare path family, else the surface.
func sessionSurfaces(surface string) []string {
	switch surface {
	case "path":
		return []string{"path_read", "path_write"}
	case "external_directory":
		return []string{"external_directory_read", "external_directory_write"}
	}
	return []string{surface}
}
