package ask_user_question

import "testing"

const fSessionLoad = "ask-user-question.session-load"

func TestSessionLoad(t *testing.T) {
	tw(t, fSessionLoad, "loads the real session graph and reaches ctx.ui.custom when the module is healthy", func(t *testing.T) {
		f := newFakeUI(t)
		f.customResult = map[string]any{"answers": []any{}, "cancelled": true}
		_, d := f.run(askArgsOf(askQ("Which?", "Pick", false, labels("A", "B")...)))
		eq(t, f.customCalls, 1, "custom calls")
		matchObject(t, d, map[string]any{"cancelled": true}, "details")
		if _, has := d["error"]; has {
			t.Fatalf("unexpected error in %#v", d)
		}
	})
}
