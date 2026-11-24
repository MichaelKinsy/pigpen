package frontend

import (
	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// A trial: with PIG_TERN_ROLES=omp the member names its transcript and
// dock nodes with omp's roles and shapes, which Tern's built-in styles and
// chat styles (Reader, Spine, Console) target: the user card, assistant
// text, thinking, the working row with Stencil's Spindle, and the footer.
// The surface role, the hello's app and the editor keep PiG's names, so
// Tern does not take the pane for omp (its transcript reader, restore and
// hand-off key on those). Off by default.
const ompRolesEnv = "PIG_TERN_ROLES"

// ompRolesOn reports whether the trial is on.
func ompRolesOn(getenv func(string) string) bool {
	return getenv(ompRolesEnv) == "omp"
}

// footerRoles are omp's status segment roles for the footer's segments
// that have one; extension statuses and the experimental badge have none.
var footerRoles = map[string]string{
	"cwd":     "omp.status.path",
	"branch":  "omp.status.git",
	"session": "omp.status.session_name",
	"usage":   "omp.status.cost",
	"context": "omp.status.context_pct",
	"model":   "omp.status.model",
}

// ompWorkingElement is the working indicator as omp draws it: a row with
// role omp.working holding a starburst spinner, which Tern draws as the
// Spindle under that parent, and the message as the label child. Later
// changes of the message and tone go to the label (see queueWorking).
func ompWorkingElement(id string, working fe.Working) tern.Element {
	h := workingHot(working)
	label := ui.Text{Common: ui.Common{Key: "label", Role: "omp.working.label", Tone: h.tone}, Text: h.text}
	children := []tern.Element{label}
	if !spinnerless(working) {
		spinner := ui.Spinner{Common: ui.Common{Key: "spinner", Tone: ui.ToneAccent}, Style: ui.SpinnerStarburst}
		children = []tern.Element{spinner, label}
	}
	return ui.Row{Common: ui.Common{Key: id, Role: "omp.working"}, Gap: ui.GapSM, Children: children}
}
