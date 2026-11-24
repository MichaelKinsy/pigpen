package frontend

import (
	"reflect"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// A click on the footer's model runs /model and one on its effort glyph
// runs /thinking, through PiG's send path, while the editor takes a
// prompt and PiG is idle; while PiG works, a click sends nothing.
func TestFooterModelAndEffortClicksRunCommands(t *testing.T) {
	h := ready(t)
	h.apply(
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Node: fe.Editor{Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 1, Node: fe.Footer{Cwd: "~/src", Model: "m", ThinkingLevel: "medium"}},
	)
	h.messages()
	click := func(id string) {
		h.event(`{"ev":"action","sf":"pig","id":"` + id + `","act":"click"}`)
		h.synced()
	}
	click("dock.footer.model")
	click("dock.footer.model.effort")
	h.mu.Lock()
	sends := append([]string(nil), h.sends...)
	h.mu.Unlock()
	if !reflect.DeepEqual(sends, []string{"/model", "/thinking"}) {
		t.Fatalf("sends = %q, want /model then /thinking", sends)
	}

	h.apply(workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Working"}))
	click("dock.footer.model")
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sends) != 2 {
		t.Fatalf("a click while working sent %q", h.sends[2:])
	}
}
