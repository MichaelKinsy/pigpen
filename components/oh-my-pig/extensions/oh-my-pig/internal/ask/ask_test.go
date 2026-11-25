package ask

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
)

const dbFormHTML = `<form>
<fieldset><legend>Database</legend>
<p>Which store should <code>ledger</code> use?</p>
<label><input type="radio" name="db" value="sqlite" checked> SQLite</label><br>
<label><input type="radio" name="db" value="pg"> Postgres</label>
</fieldset>
<fieldset><legend>Extras</legend>
<label><input type="checkbox" name="extras" value="ci" checked> CI</label>
<label><input type="checkbox" name="extras" value="docker"> Docker</label>
<label>Region <select name="region"><option value="us">US</option><option value="eu">EU</option><option value="ap">AP</option></select></label>
<label>Workers <input type="number" name="workers" value="4" min="1" max="8" required></label>
<label>Notes<textarea name="notes" placeholder="anything else"></textarea></label>
</fieldset>
</form>`

func decodeResult(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode tool result %s: %v", string(raw), err)
	}
	return m
}

func TestAskRegistrationSchemaAndHeadlessHiding(t *testing.T) {
	ext := sdk.New("oh-my-pig-test")
	Register(ext)
	h := startHost(t, ext, hostOptions{})
	tl, ok := h.tools["ask"]
	if !ok {
		t.Fatalf("ask tool not registered")
	}
	if tl.Label != "Ask" {
		t.Errorf("Label = %q, want %q", tl.Label, "Ask")
	}
	if tl.ExecutionMode != "sequential" {
		t.Errorf("ExecutionMode = %q, want %q", tl.ExecutionMode, "sequential")
	}
	if tl.Description != strings.TrimSpace(askPrompt) {
		t.Errorf("Description does not match embedded ask.md")
	}
	props, _ := tl.Parameters["properties"].(map[string]any)
	htmlProp, _ := props["html"].(map[string]any)
	if len(props) != 2 || props["i"] == nil || htmlProp["type"] != "string" || htmlProp["description"] != "one HTML <form>" {
		t.Errorf("unexpected schema: %+v", tl.Parameters)
	}
	req, _ := tl.Parameters["required"].([]any)
	if len(req) != 2 || req[0] != "html" || req[1] != "i" {
		t.Errorf("unexpected required: %+v", req)
	}

	// Headless session_start removes ask from active tools (A6)
	var setTools []any
	ext2 := sdk.New("oh-my-pig-test")
	Register(ext2)
	h2 := startHost(t, ext2, hostOptions{
		Mode: "print",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "getActiveTools":
				return map[string]any{"tools": []any{"read", "ask", "bash"}}, ""
			case "setActiveTools":
				setTools, _ = args["tools"].([]any)
			}
			return map[string]any{}, ""
		},
	})
	h2.emitEvent(sdk.EventSessionStart, nil)
	if len(setTools) != 2 || setTools[0] != "read" || setTools[1] != "bash" {
		t.Fatalf("expected ask removed from active tools in headless mode, got %v", setTools)
	}
}

func TestParseHtmlFormUpstreamCases(t *testing.T) {
	// upstream: packages/tui/test/html-form.test.ts: "joins radios split across a decision table into one field labeled by column headers"
	form := ParseHtmlForm(`<form><table>
		<tr><th>Test</th><th>Fix</th><th>Skip</th></tr>
		<tr><td>session refresh</td><td><input type="radio" name="t1" value="fix" checked></td><td><input type="radio" name="t1" value="skip"></td></tr>
	</table></form>`)
	if len(form.Fields) != 1 || form.Fields[0].Kind != "choice" || len(form.Fields[0].Options) != 2 {
		t.Fatalf("unexpected table form fields: %+v", form.Fields)
	}
	if form.Fields[0].Options[0].Value != "fix" || SpansText(form.Fields[0].Options[0].Label) != "Fix" ||
		form.Fields[0].Options[1].Value != "skip" || SpansText(form.Fields[0].Options[1].Label) != "Skip" {
		t.Fatalf("unexpected table options: %+v", form.Fields[0].Options)
	}
	if form.Sections[0].Blocks[0].Kind != "text" || SpansText(form.Sections[0].Blocks[0].Spans) != "session refresh" {
		t.Fatalf("unexpected row text block: %+v", form.Sections[0].Blocks[0])
	}

	// upstream: packages/tui/test/html-form.test.ts: "offers Other on radio and checkbox groups, but not on selects, lone checkboxes or groups with their own"
	form = ParseHtmlForm(`<form>
		<label><input type="radio" name="a" value="1"> One</label><label><input type="radio" name="a" value="2"> Two</label>
		<label><input type="radio" name="b" value="1"> One</label><label><input type="radio" name="b" value="x"> Other (say which)</label>
		<label><input type="checkbox" name="c" value="on"> Lone</label>
		<select name="d"><option>One</option><option>Two</option></select></form>`)
	wantOther := []struct {
		name  string
		other bool
	}{{"a", true}, {"b", false}, {"c", false}, {"d", false}}
	if len(form.Fields) != 4 {
		t.Fatalf("got %d fields, want 4", len(form.Fields))
	}
	for i, w := range wantOther {
		if form.Fields[i].Name != w.name || form.Fields[i].Other != w.other {
			t.Errorf("field[%d] = (%q, other=%v), want (%q, other=%v)", i, form.Fields[i].Name, form.Fields[i].Other, w.name, w.other)
		}
	}

	// upstream: packages/tui/test/html-form.test.ts: "keeps pictures as blocks; a picture inside a label is its alt text"
	form = ParseHtmlForm(`<form><p>Before <img src="a.png" alt="flow"> after</p>
		<svg viewBox="0 0 10 10"><title>Box</title><rect width="10" height="10"/></svg>
		<label><input type="radio" name="logo" value="new"> <img src="new.png" alt="New logo"></label>
		<label><input type="radio" name="logo" value="old"> Old</label></form>`)
	blocks := form.Sections[0].Blocks
	if len(blocks) != 5 || blocks[1].Kind != "image" || blocks[1].Src != "a.png" || blocks[1].Alt != "flow" ||
		blocks[3].Kind != "svg" || blocks[3].Alt != "Box" {
		t.Fatalf("unexpected picture blocks: %+v", blocks)
	}
	if got := SpansText(form.Fields[0].Options[0].Label); got != "New logo" {
		t.Fatalf("image in label got %q, want %q", got, "New logo")
	}

	// upstream: packages/tui/test/html-form.test.ts: "drops buttons and layout markup, keeps hidden inputs as data, and labels by `for`"
	form = ParseHtmlForm(`<p>intro</p><form><input type="hidden" name="kind" value="deploy">
		<label for="d">Domain</label><input id="d" name="domain" placeholder="app.example.com">
		<button type="submit">Go</button></form>`)
	if len(form.Fields) != 1 || form.Fields[0].Name != "domain" || SpansText(form.Fields[0].Label) != "Domain" {
		t.Fatalf("label for failed: %+v", form.Fields)
	}
	if got := DefaultFormData(form).MarshalJSONString(false); got != `{"kind":"deploy","domain":""}` {
		t.Fatalf("defaults JSON = %s", got)
	}

	// upstream: packages/tui/test/html-form.test.ts: "reports defaults with HTML semantics: groups as arrays, numbers as numbers, single select's first option"
	form = ParseHtmlForm(dbFormHTML)
	if got := DefaultFormData(form).MarshalJSONString(false); got != `{"db":"sqlite","extras":["ci"],"region":"us","workers":4,"notes":""}` {
		t.Fatalf("DB_FORM defaults = %s", got)
	}

	// upstream: packages/tui/test/html-form.test.ts: "keeps a control named __proto__ as an ordinary key of the defaults"
	form = ParseHtmlForm(`<form><input type="hidden" name="__proto__" value="x"><label>Target <input name="target" value="prod"></label></form>`)
	if got := DefaultFormData(form).MarshalJSONString(false); got != `{"__proto__":"x","target":"prod"}` {
		t.Fatalf("__proto__ defaults = %s", got)
	}

	// upstream: packages/tui/test/html-form.test.ts: "validates required, numeric bounds and anchored patterns; ignores an invalid pattern"
	form = ParseHtmlForm(`<form>
		<input name="n" type="number" min="1" max="8" required>
		<input name="code" pattern="[A-Z]{3}">
		<input name="bad" pattern="[">
		<label><input type="checkbox" name="pick" value="a" required> A</label>
		<label><input type="checkbox" name="pick" value="b"> B</label></form>`)
	if err := FieldError(form.Fields[0], "", true); err != "Required" {
		t.Errorf("n empty = %q", err)
	}
	if err := FieldError(form.Fields[0], "9", true); err != "Must be at most 8" {
		t.Errorf("n 9 = %q", err)
	}
	if err := FieldError(form.Fields[0], "x", true); err != "Enter a number" {
		t.Errorf("n x = %q", err)
	}
	if err := FieldError(form.Fields[1], "ABCD", true); !strings.HasPrefix(err, "Must match") {
		t.Errorf("code ABCD = %q", err)
	}
	if err := FieldError(form.Fields[1], "ABC", true); err != "" {
		t.Errorf("code ABC = %q", err)
	}
	if err := FieldError(form.Fields[2], "anything", true); err != "" {
		t.Errorf("bad pattern = %q", err)
	}
	if err := FieldError(form.Fields[3], []string{}, true); err != "Select at least one" {
		t.Errorf("pick empty = %q", err)
	}
}

func TestEntitiesUnicodeWhitespaceMalformedAndValidation(t *testing.T) {
	// A5: Semicolon-less query params must NOT be corrupted by entity decoding
	form := ParseHtmlForm(`<form><input name="u" value="https://x.test/?a=1&region=eu&copy=2&notes=1"></form>`)
	if form.Fields[0].Value != "https://x.test/?a=1&region=eu&copy=2&notes=1" {
		t.Fatalf("entity decoding corrupted URL value: %q", form.Fields[0].Value)
	}

	// A8: Unicode whitespace (NBSP) is collapsed like JS /\s/
	form = ParseHtmlForm(`<form><label><input type="radio" name="k" value="1">&nbsp;One&nbsp;thing&nbsp;</label><label><input type="radio" name="k" value="2">Two</label></form>`)
	if got := SpansText(form.Fields[0].Options[0].Label); got != "One thing" {
		t.Fatalf("NBSP not collapsed: %q", got)
	}

	// A9: Unclosed <thead> closes before <tbody> so headerRows == 1
	form = ParseHtmlForm(`<form><table><thead><tr><th>H1</th><th>H2</th></tr><tbody><tr><td>A</td><td>B</td></tr><tr><td>C</td><td>D</td></tr></tbody></table><input name="x"></form>`)
	if form.Sections[0].Blocks[0].Kind != "table" || form.Sections[0].Blocks[0].HeaderRows != 1 {
		t.Fatalf("unclosed thead headerRows = %d, want 1", form.Sections[0].Blocks[0].HeaderRows)
	}

	// A10: JS hex number literal and WHATWG URL schemes (file:, a:)
	numF := &FormField{Kind: "text", Name: "n", Type: "number"}
	if got := TextValue(numF, "0x1A"); got != float64(26) {
		t.Fatalf("0x1A got %#v, want 26", got)
	}
	urlF := &FormField{Kind: "text", Name: "u", Type: "url"}
	if err := FieldError(urlF, "file:///tmp/x", true); err != "" {
		t.Errorf("file:///tmp/x rejected: %q", err)
	}
	if err := FieldError(urlF, "a:", true); err != "" {
		t.Errorf("a: rejected: %q", err)
	}
	if err := FieldError(urlF, "not a url", true); err != "Enter a full URL" {
		t.Errorf("invalid url got %q", err)
	}

	// Malformed HTML: unclosed options, mismatched closing tags, unclosed comment/textarea
	form = ParseHtmlForm(`<form><select name=env><option value=dev>Dev<option value=prod selected>Prod</select><textarea name=body>a &amp; b`)
	if len(form.Fields) != 2 || !form.Fields[0].Options[1].Checked || form.Fields[0].Options[1].Value != "prod" || form.Fields[1].Value != "a & b" {
		t.Fatalf("malformed HTML parse mismatch: %+v", form.Fields)
	}
}

func TestAskResultJSONOrderingAndNoHTMLEscape(t *testing.T) {
	// A1: keys stay in document order (hidden first, then fields), no HTML escaping
	form := ParseHtmlForm(`<form>
		<input type="hidden" name="h" value="1">
		<label>Zeta <input name="zeta" value="a < b && c -> d"></label>
		<label>Alpha <input name="alpha" value="ok"></label>
	</form>`)
	data := DefaultFormData(form)
	got := FormatAskResult(data, false, false, nil)
	want := "User answers:\n{\n  \"h\": \"1\",\n  \"zeta\": \"a < b && c -> d\",\n  \"alpha\": \"ok\"\n}"
	if got != want {
		t.Fatalf("FormatAskResult mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestAskWithoutFormSurfaceUpstreamCases(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/ask.test.ts: "titles each prompt with the form text around its field"
	var selectTitles, inputTitles []string
	ext := sdk.New("oh-my-pig-test")
	Register(ext)
	h := startHost(t, ext, hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			title, _ := args["title"].(string)
			switch method {
			case "ui.select":
				selectTitles = append(selectTitles, title)
				return map[string]any{"ok": true, "selected": "Share it"}, ""
			case "ui.input":
				inputTitles = append(inputTitles, title)
				return map[string]any{"ok": true, "text": "4"}, ""
			}
			return map[string]any{}, ""
		},
	})
	rawRes, errMsg := h.callTool("ask", map[string]any{
		"html": `<form>
  <p>Both jobs read the same base image.</p>
  <ul><li>build: needs the toolchain</li><li>test: only the runtime</li></ul>
  <pre>FROM debian:12</pre>
  <table><tr><th>Job</th><th>Time</th></tr><tr><td>build</td><td>4m</td></tr></table>
  <p>How should these two jobs use the image?</p>
  <label><input type="radio" name="mode" value="share" checked> Share it</label>
  <label><input type="radio" name="mode" value="split"> Split it</label>
  <fieldset><legend>Caching</legend>
    <p>Layers are cached per runner.</p>
    <label>Cache size (GB) <input type="number" name="cache"></label>
  </fieldset>
  <p>Both answers can change later.</p>
</form>`,
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	wantSelectTitle := strings.Join([]string{
		"Both jobs read the same base image.",
		"- build: needs the toolchain\n- test: only the runtime",
		"FROM debian:12",
		"Job | Time\nbuild | 4m",
		"How should these two jobs use the image?",
	}, "\n\n")
	if len(selectTitles) != 1 || selectTitles[0] != wantSelectTitle {
		t.Fatalf("select title mismatch:\ngot:  %q\nwant: %q", selectTitles, wantSelectTitle)
	}
	wantInputTitle := strings.Join([]string{
		"Caching",
		"Layers are cached per runner.",
		"Both answers can change later.",
		"Cache size (GB)",
	}, "\n\n")
	if len(inputTitles) != 1 || inputTitles[0] != wantInputTitle {
		t.Fatalf("input title mismatch:\ngot:  %q\nwant: %q", inputTitles, wantInputTitle)
	}
	res := decodeResult(t, rawRes)
	details, _ := res["details"].(map[string]any)
	answers, _ := details["answers"].([]any)
	if len(answers) != 2 || answers[0].(map[string]any)["answer"] != "Share it" || answers[1].(map[string]any)["answer"] != "4" {
		t.Fatalf("unexpected details.answers: %+v", answers)
	}
	if details["form"] == nil {
		t.Errorf("expected parsed form in details")
	}

	// upstream: packages/coding-agent/test/tools/ask.test.ts: "keeps the text before a field that sits between another group's options with that field"
	var interTitles []string
	ext2 := sdk.New("oh-my-pig-test")
	Register(ext2)
	h2 := startHost(t, ext2, hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				title, _ := args["title"].(string)
				interTitles = append(interTitles, title)
				opts, _ := args["options"].([]any)
				return map[string]any{"ok": true, "selected": opts[0]}, ""
			}
			return map[string]any{}, ""
		},
	})
	_, errMsg = h2.callTool("ask", map[string]any{
		"html": `<form>
  <label><input type="radio" name="plan" value="basic" checked> Basic</label>
  <p>Choose a compliant region</p>
  <label>Region <select name="region"><option value="us">US</option><option value="eu">EU</option></select></label>
  <label><input type="radio" name="plan" value="premium"> Premium</label>
</form>`,
	})
	if errMsg != "" || len(interTitles) != 2 || interTitles[0] != "plan" || interTitles[1] != "Choose a compliant region\n\nRegion" {
		t.Fatalf("intervening field titles = %q err=%q", interTitles, errMsg)
	}

	// upstream: packages/coding-agent/test/tools/ask.test.ts: "takes a radio's typed Other text as its value and appends a checkbox group's"
	picks := []string{"Other (type your own)", "Other (type your own)", "[ ] CI", "[ ] Other (type your own)", doneRowLabel}
	typed := []string{"  ", " MySQL 8 ", "terraform"}
	ext3 := sdk.New("oh-my-pig-test")
	Register(ext3)
	h3 := startHost(t, ext3, hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				v := picks[0]
				picks = picks[1:]
				return map[string]any{"ok": true, "selected": v}, ""
			case "ui.input":
				v := typed[0]
				typed = typed[1:]
				return map[string]any{"ok": true, "text": v}, ""
			}
			return map[string]any{}, ""
		},
	})
	rawRes, errMsg = h3.callTool("ask", map[string]any{
		"html": `<form>
  <fieldset><legend>Database</legend>
    <label><input type="radio" name="db" value="postgres" checked> Postgres</label>
    <label><input type="radio" name="db" value="sqlite"> SQLite</label>
  </fieldset>
  <fieldset><legend>Tools</legend>
    <label><input type="checkbox" name="tools" value="ci"> CI</label>
    <label><input type="checkbox" name="tools" value="lint"> Lint</label>
  </fieldset>
</form>`,
	})
	if errMsg != "" {
		t.Fatalf("other flow error: %s", errMsg)
	}
	wantOtherRes := "User answers:\n{\n  \"db\": \"MySQL 8\",\n  \"tools\": [\n    \"ci\",\n    \"terraform\"\n  ]\n}"
	if got := decodeResult(t, rawRes)["content"].(string); got != wantOtherRes {
		t.Fatalf("other result mismatch:\ngot:\n%s\nwant:\n%s", got, wantOtherRes)
	}

	// upstream: packages/coding-agent/test/tools/ask.test.ts: "re-asks a text field with its validation error until the answer is valid"
	var valTitles []string
	valAnswers := []string{"", "abc", "12"}
	ext4 := sdk.New("oh-my-pig-test")
	Register(ext4)
	h4 := startHost(t, ext4, hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.input" {
				tStr, _ := args["title"].(string)
				valTitles = append(valTitles, tStr)
				v := valAnswers[0]
				valAnswers = valAnswers[1:]
				return map[string]any{"ok": true, "text": v}, ""
			}
			return map[string]any{}, ""
		},
	})
	rawRes, errMsg = h4.callTool("ask", map[string]any{"html": `<form><label>Port <input type="number" name="port" required></label></form>`})
	if errMsg != "" || len(valTitles) != 3 || valTitles[0] != "Port" || !strings.Contains(valTitles[1], "Required") || !strings.Contains(valTitles[2], "Enter a number") {
		t.Fatalf("re-ask titles = %q err=%q", valTitles, errMsg)
	}
	if got := decodeResult(t, rawRes)["content"].(string); got != "User answers:\n{\n  \"port\": 12\n}" {
		t.Fatalf("unexpected validated content: %q", got)
	}

	// upstream: packages/coding-agent/test/tools/ask.test.ts: "omits a lone checkbox the user leaves unchecked"
	ext5 := sdk.New("oh-my-pig-test")
	Register(ext5)
	h5 := startHost(t, ext5, hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				return map[string]any{"ok": true, "selected": doneRowLabel}, ""
			}
			return map[string]any{}, ""
		},
	})
	rawRes, errMsg = h5.callTool("ask", map[string]any{"html": `<form><label><input type="checkbox" name="force" value="yes"> Force push</label></form>`})
	if errMsg != "" || decodeResult(t, rawRes)["content"].(string) != "User answers:\n{}" {
		t.Fatalf("lone unchecked checkbox got err=%q res=%s", errMsg, string(rawRes))
	}

	// upstream: packages/coding-agent/test/tools/ask.test.ts: "aborts the turn when the user cancels a later field"
	ext6 := sdk.New("oh-my-pig-test")
	Register(ext6)
	h6 := startHost(t, ext6, hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				return map[string]any{"ok": true, "selected": "SQLite"}, ""
			case "ui.editor":
				return map[string]any{"ok": false}, ""
			}
			return map[string]any{}, ""
		},
	})
	_, errMsg = h6.callTool("ask", map[string]any{
		"html": `<form><label><input type="radio" name="db" value="sqlite"> SQLite</label><label><input type="radio" name="db" value="pg"> PG</label><label>Notes <textarea name="notes"></textarea></label></form>`,
	})
	if !strings.Contains(errMsg, "Ask tool was cancelled by the user") || len(h6.callsTo("abort")) != 1 {
		t.Fatalf("cancel later field got err=%q aborts=%d", errMsg, len(h6.callsTo("abort")))
	}
}

func TestAskErrorsTimeoutAndHeadless(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/ask.test.ts: "refuses a <fieldset> whose radios each carry their own name, without showing it"
	ext := sdk.New("oh-my-pig-test")
	Register(ext)
	h := startHost(t, ext, hostOptions{})
	rawRes, errMsg := h.callTool("ask", map[string]any{
		"html": `<form><fieldset><legend>Pick "one"</legend><label><input type="radio" name="a" value="1"> A</label><label><input type="radio" name="b" value="2"> B</label></fieldset></form>`,
	})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	content := decodeResult(t, rawRes)["content"].(string)
	// Raw legend interpolation (A3)
	if !strings.Contains(content, `- <fieldset> "Pick "one"": a, b`) {
		t.Fatalf("split radio error mismatch:\n%s", content)
	}

	// upstream: packages/coding-agent/test/tools/ask.test.ts: "tells the model a form without named controls needs one, without showing it"
	rawRes, _ = h.callTool("ask", map[string]any{"html": `<form><p>Just text</p></form>`})
	if decodeResult(t, rawRes)["content"].(string) != noNamedControlsError {
		t.Fatalf("unexpected no-named-controls message: %s", string(rawRes))
	}

	// upstream: packages/coding-agent/test/tools/ask.test.ts: "aborts the turn in an ordinary session"
	extH := sdk.New("oh-my-pig-test")
	Register(extH)
	hH := startHost(t, extH, hostOptions{Mode: "print"})
	_, errMsg = hH.callTool("ask", map[string]any{"html": `<form><input name="x"></form>`})
	if !strings.Contains(errMsg, "Ask tool requires interactive mode") || len(hH.callsTo("abort")) != 1 {
		t.Fatalf("headless ordinary session got err=%q aborts=%d", errMsg, len(hH.callsTo("abort")))
	}

	// upstream: packages/coding-agent/test/tools/ask.test.ts: "tells a headless fork's model to answer in its final message instead of aborting"
	extF := sdk.New("oh-my-pig-test")
	RegisterWithConfig(extF, Config{AdvertiseHeadlessAsk: true})
	hF := startHost(t, extF, hostOptions{Mode: "print"})
	rawRes, errMsg = hF.callTool("ask", map[string]any{"html": `<form><input name="x"></form>`})
	if errMsg != "" || !strings.Contains(decodeResult(t, rawRes)["content"].(string), "background session") || len(hF.callsTo("abort")) != 0 {
		t.Fatalf("headless fork got err=%q res=%s aborts=%d", errMsg, string(rawRes), len(hF.callsTo("abort")))
	}

	// upstream: packages/coding-agent/test/ask-timeout.test.ts: "keeps answered fields and defaults the rest when a later field times out"
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })
	Setting = func(_ sdk.Context, key string) any {
		if key == "ask.timeout" {
			return 0.03
		}
		return nil
	}
	extT := sdk.New("oh-my-pig-test")
	Register(extT)
	hT := startHost(t, extT, hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				title, _ := args["title"].(string)
				if title == "Which database?" {
					return map[string]any{"ok": true, "selected": "SQLite"}, ""
				}
				time.Sleep(45 * time.Millisecond)
				return map[string]any{"ok": false}, ""
			}
			return map[string]any{}, ""
		},
	})
	rawRes, errMsg = hT.callTool("ask", map[string]any{
		"html": `<form>
  <p>Which database?</p>
  <label><input type="radio" name="db" value="sqlite"> SQLite</label>
  <label><input type="radio" name="db" value="postgres" checked> Postgres</label>
  <p>Which auth?</p>
  <label><input type="radio" name="auth" value="jwt" checked> JWT</label>
  <label><input type="radio" name="auth" value="oauth"> OAuth</label>
  <label>Notes <textarea name="notes">none</textarea></label>
</form>`,
	})
	if errMsg != "" {
		t.Fatalf("timeout error: %s", errMsg)
	}
	wantTimeout := "No answer before the timeout; submitted the form's current values:\n{\n  \"db\": \"sqlite\",\n  \"auth\": \"jwt\",\n  \"notes\": \"none\"\n}"
	if got := decodeResult(t, rawRes)["content"].(string); got != wantTimeout {
		t.Fatalf("timeout content mismatch:\ngot:\n%s\nwant:\n%s", got, wantTimeout)
	}

	// upstream: packages/coding-agent/test/tools/ask.test.ts: "gives no answer when a timeout leaves a required field empty"
	extR := sdk.New("oh-my-pig-test")
	RegisterWithConfig(extR, Config{TimeoutMs: 30})
	hR := startHost(t, extR, hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.input" {
				time.Sleep(45 * time.Millisecond)
				return map[string]any{"ok": false}, ""
			}
			return map[string]any{}, ""
		},
	})
	rawRes, errMsg = hR.callTool("ask", map[string]any{"html": `<form><label>Name <input name="name" required></label></form>`})
	if errMsg != "" || decodeResult(t, rawRes)["content"].(string) != askTimedOutUnanswered {
		t.Fatalf("required timeout got err=%q res=%s", errMsg, string(rawRes))
	}
}

func TestRecoverAskHtmlAndPrepareArguments(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/ask.test.ts: "recovers the form of an html call, a bare string and a legacy question call"
	if got, ok := RecoverAskHTMLValue(map[string]any{"html": dbFormHTML}); !ok || got != dbFormHTML {
		t.Fatalf("html recovery failed")
	}
	if got, ok := RecoverAskHTMLValue("<form></form>"); !ok || got != "<form></form>" {
		t.Fatalf("bare string recovery failed")
	}
	legacyArgs := map[string]any{
		"questions": []any{
			map[string]any{
				"id":          "db",
				"question":    "Which <db>?",
				"recommended": float64(1),
				"options":     []any{map[string]any{"label": "pg"}, map[string]any{"label": "sqlite"}},
			},
		},
	}
	wantLegacy := `<form><fieldset><legend>Which &lt;db&gt;?</legend><label><input type="radio" name="db" value="pg"> pg</label><label><input type="radio" name="db" value="sqlite" checked> sqlite</label></fieldset></form>`
	if got, ok := RecoverAskHTMLValue(legacyArgs); !ok || got != wantLegacy {
		t.Fatalf("legacy recovery got (%v, %q), want %q", ok, got, wantLegacy)
	}
	if _, ok := RecoverAskHTMLValue(map[string]any{"questions": []any{map[string]any{"id": "q"}}}); ok {
		t.Fatalf("invalid legacy questions should not recover")
	}

	// PrepareArguments converts legacy questions before schema validation (D)
	ext := sdk.New("oh-my-pig-test")
	Register(ext)
	h := startHost(t, ext, hostOptions{})
	prep, errMsg := h.prepareArgs("ask", legacyArgs)
	if errMsg != "" || prep["html"] != wantLegacy {
		t.Fatalf("prepareArgs got err=%q prep=%+v", errMsg, prep)
	}
}

func TestAskMultiSessionAndDoubleRegister(t *testing.T) {
	// Register called twice in one process; two live sessions remain isolated and
	// stopping one leaves the other working.
	extA := sdk.New("oh-my-pig-test-A")
	Register(extA)
	extB := sdk.New("oh-my-pig-test-B")
	Register(extB)

	hA := startHost(t, extA, hostOptions{
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "ui.input" {
				return map[string]any{"ok": true, "text": "fromA"}, ""
			}
			return map[string]any{}, ""
		},
	})
	hB := startHost(t, extB, hostOptions{
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "ui.input" {
				return map[string]any{"ok": true, "text": "fromB"}, ""
			}
			return map[string]any{}, ""
		},
	})

	rawA, errA := hA.callTool("ask", map[string]any{"html": `<form><input name="v"></form>`})
	if errA != "" || !strings.Contains(decodeResult(t, rawA)["content"].(string), `"fromA"`) {
		t.Fatalf("session A failed: err=%q res=%s", errA, string(rawA))
	}

	rawB, errB := hB.callTool("ask", map[string]any{"html": `<form><input name="v"></form>`})
	if errB != "" || !strings.Contains(decodeResult(t, rawB)["content"].(string), `"fromB"`) {
		t.Fatalf("session B failed: err=%q res=%s", errB, string(rawB))
	}
}

func TestAskSchemaMatchesOmpContent(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "schemaorder", "omp_schemas.json"))
	if err != nil {
		t.Fatalf("read omp_schemas.json: %v", err)
	}
	var captured map[string]struct {
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &captured); err != nil {
		t.Fatalf("unmarshal omp_schemas.json: %v", err)
	}
	want := captured["ask"].Parameters
	if want == nil {
		t.Fatalf("omp_schemas.json missing ask entry")
	}
	gotRaw, err := json.Marshal(askToolSchema())
	if err != nil {
		t.Fatalf("marshal askToolSchema: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(gotRaw, &got); err != nil {
		t.Fatalf("unmarshal askToolSchema: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ask schema drift:\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestAskPlanModeDisablesTimeoutAndNoDefaultBell(t *testing.T) {
	// upstream: packages/coding-agent/test/tools/ask.test.ts: "passes the configured timeout, except in plan mode"
	t.Cleanup(gate.ResetForTests)
	origSetting := Setting
	t.Cleanup(func() { Setting = origSetting })
	Setting = func(_ sdk.Context, key string) any {
		if key == "ask.timeout" {
			return 5
		}
		return nil
	}

	var sawOpts map[string]any
	ext := sdk.New("oh-my-pig-test")
	Register(ext)
	h := startHost(t, ext, hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				sawOpts, _ = args["opts"].(map[string]any)
				return map[string]any{"ok": true, "selected": "SQLite"}, ""
			}
			return map[string]any{}, ""
		},
	})

	// Outside plan mode: timeout is passed; no audible bell emitted.
	gate.SetPlanActive(func(_ sdk.Context) bool { return false })
	_, errMsg := h.callTool("ask", map[string]any{"html": `<form><label><input type="radio" name="db" value="sqlite"> SQLite</label><label><input type="radio" name="db" value="pg"> PG</label></form>`})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if sawOpts == nil || sawOpts["timeout"] != float64(5000) {
		t.Fatalf("expected timeout=5000 outside plan mode, got %+v", sawOpts)
	}
	if bells := h.callsTo("ui.bell"); len(bells) != 0 {
		t.Fatalf("expected no audible bell by default, got %d calls", len(bells))
	}

	// Inside plan mode: timeout is suppressed (no opts.timeout).
	sawOpts = nil
	gate.SetPlanActive(func(_ sdk.Context) bool { return true })
	_, errMsg = h.callTool("ask", map[string]any{"html": `<form><label><input type="radio" name="db" value="sqlite"> SQLite</label><label><input type="radio" name="db" value="pg"> PG</label></form>`})
	if errMsg != "" {
		t.Fatalf("unexpected error in plan mode: %s", errMsg)
	}
	if sawOpts != nil && sawOpts["timeout"] != nil {
		t.Fatalf("expected no timeout in plan mode, got %+v", sawOpts)
	}
}

func TestAskNumberUnderscoreRejectedAndResultTemplateRendered(t *testing.T) {
	// N5: JS Number("1_000") is NaN, so underscore numbers must be rejected and kept as string
	numF := &FormField{Kind: "text", Name: "n", Type: "number"}
	if err := FieldError(numF, "1_000", true); err != "Enter a number" {
		t.Fatalf("FieldError(1_000) = %q, want %q", err, "Enter a number")
	}
	if got := TextValue(numF, "1_000"); got != "1_000" {
		t.Fatalf("TextValue(1_000) = %#v, want string %q", got, "1_000")
	}

	// N3: FormatAskResult renders all 3 branches and notes from ask-result.md
	d := NewFormData()
	d.Set("db", "pg")
	if got := FormatAskResult(d, false, true, nil); !strings.HasPrefix(got, "Ask interrupted by message. Answers the user gave before it:\n") {
		t.Fatalf("interrupted header mismatch: %q", got)
	}
	if got := FormatAskResult(d, true, false, nil); !strings.HasPrefix(got, "No answer before the timeout; submitted the form's current values:\n") {
		t.Fatalf("timedOut header mismatch: %q", got)
	}
	withNotes := FormatAskResult(d, false, false, []OptionNote{{
		Name: "db", Question: "Database", Option: "Postgres", Value: "a<b", Text: "fast\nreliable",
	}})
	wantNotes := "User answers:\n{\n  \"db\": \"pg\"\n}\nOption notes:\n- Database: Postgres [db=\"a<b\"] — fast reliable"
	if withNotes != wantNotes {
		t.Fatalf("notes rendering mismatch:\ngot:\n%s\nwant:\n%s", withNotes, wantNotes)
	}
}

func TestAskPrepareArgumentsMalformedShapes(t *testing.T) {
	// upstream: packages/coding-agent/src/tools/ask.ts:69,87-120,429,520-523
	// Malformed shapes that fail omp's schema validation fall through to
	// AskTool.execute() via lenientArgValidation and return INVALID_ASK_ERROR.
	// PrepareArguments must return the exact same bytes before PiG's host
	// validator runs (preventing PiG-worded validation errors and null->string coercion).
	ext := sdk.New("oh-my-pig-test")
	Register(ext)
	h := startHost(t, ext, hostOptions{})

	refusedCases := []struct {
		name string
		args map[string]any
	}{
		{name: "empty object", args: map[string]any{}},
		{name: "html is null", args: map[string]any{"html": nil}},
		{name: "multiple wrong string keys", args: map[string]any{"wrong": "abc", "other": "def"}},
		{name: "wrong non-string key", args: map[string]any{"wrong": 5}},
		{name: "empty questions array", args: map[string]any{"questions": []any{}}},
		{name: "questions missing question field", args: map[string]any{"questions": []any{map[string]any{"id": "q1"}}}},
		{name: "questions non-object item", args: map[string]any{"questions": []any{"not an object"}}},
		{name: "questions option missing label", args: map[string]any{"questions": []any{map[string]any{"question": "Pick", "options": []any{map[string]any{"description": "no label"}}}}}},
		{name: "questions non-array non-string", args: map[string]any{"questions": 42}},
	}

	for _, tc := range refusedCases {
		t.Run(tc.name, func(t *testing.T) {
			prep, errMsg := h.prepareArgs("ask", tc.args)
			if errMsg != invalidAskError {
				t.Fatalf("prepareArgs(%v) err = %q (prep=%+v), want exact %q", tc.args, errMsg, prep, invalidAskError)
			}
		})
	}

	// Direct call with nil params (defensive).
	if prep, err := prepareAskArguments(nil); err == nil || err.Error() != invalidAskError {
		t.Fatalf("prepareAskArguments(nil) = (%+v, %v), want %q", prep, err, invalidAskError)
	}

	// Valid legacy questions still recovers to {"html": ...}.
	legacyPrep, errMsg := h.prepareArgs("ask", map[string]any{
		"questions": []any{
			map[string]any{"id": "env", "question": "Target?", "options": []any{"dev", "prod"}},
		},
	})
	if errMsg != "" {
		t.Fatalf("legacy questions unexpected err: %s", errMsg)
	}
	wantLegacy := `<form><fieldset><legend>Target?</legend><label><input type="radio" name="env" value="dev"> dev</label><label><input type="radio" name="env" value="prod"> prod</label></fieldset></form>`
	if legacyPrep["html"] != wantLegacy {
		t.Fatalf("legacy questions html = %q, want %q", legacyPrep["html"], wantLegacy)
	}

	// Valid html with extra root key is accepted (extra root key stripped for PiG host).
	extraPrep, errMsg := h.prepareArgs("ask", map[string]any{"html": "<form><input name=\"a\"></form>", "zz": 1})
	if errMsg != "" || extraPrep["html"] != `<form><input name="a"></form>` {
		t.Fatalf("extra root key prep = (%+v, %q)", extraPrep, errMsg)
	}
	if _, hasExtra := extraPrep["zz"]; hasExtra {
		t.Fatalf("extra root key zz was not stripped: %+v", extraPrep)
	}

	// Single-string-remap: omp's validator remaps a lone string property (e.g. {"questions": "not-an-array"})
	// into {"html": "not-an-array"}, so it passes schema validation and reaches executeAsk.
	remapPrep, errMsg := h.prepareArgs("ask", map[string]any{"questions": "not-an-array"})
	if errMsg != "" || remapPrep["html"] != "not-an-array" {
		t.Fatalf("single-string-remap prep = (%+v, %q), want html=not-an-array", remapPrep, errMsg)
	}
}
