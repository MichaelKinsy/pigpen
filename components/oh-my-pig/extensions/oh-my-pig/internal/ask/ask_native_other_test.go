package ask

import (
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// In Tern omp labels the free-text row "Other…" and still takes the typed text
// as the answer.
// upstream: packages/tui/src/overlays/ask-dialog.ts:109,886
func TestAskOtherRowLabelInTern(t *testing.T) {
	surface.ResetForTests()
	t.Cleanup(surface.ResetForTests)
	surface.SetNative(true)

	var offered []any
	ext := sdk.New("oh-my-pig-test")
	Register(ext)
	h := startHost(t, ext, hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				offered, _ = args["options"].([]any)
				return map[string]any{"ok": true, "selected": "Other…"}, ""
			case "ui.input":
				return map[string]any{"ok": true, "text": "MySQL 8"}, ""
			}
			return map[string]any{}, ""
		},
	})
	rawRes, errMsg := h.callTool("ask", map[string]any{
		"html": `<form>
  <fieldset><legend>Database</legend>
    <label><input type="radio" name="db" value="postgres"> Postgres</label>
    <label><input type="radio" name="db" value="sqlite"> SQLite</label>
  </fieldset>
</form>`,
	})
	if errMsg != "" {
		t.Fatalf("ask error: %s", errMsg)
	}
	if len(offered) != 3 || offered[2] != "Other…" {
		t.Fatalf("offered options = %q, want the last to be \"Other…\"", offered)
	}
	want := "User answers:\n{\n  \"db\": \"MySQL 8\"\n}"
	if got := decodeResult(t, rawRes)["content"].(string); got != want {
		t.Fatalf("result:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
