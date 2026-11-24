package ask

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type decodedToolResult struct {
	Content string         `json:"content"`
	Details askToolDetails `json:"details"`
}

func decodeResult(t *testing.T, raw json.RawMessage) decodedToolResult {
	t.Helper()
	var res decodedToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal tool result %s: %v", raw, err)
	}
	return res
}

func newTestExt(cfg config) *sdk.Extension {
	ext := sdk.New("test-ask")
	registerWithConfig(ext, cfg)
	return ext
}

func stringSlice(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestRegisterSchemaAndDescription(t *testing.T) {
	ext := sdk.New("oh-my-pig")
	Register(ext)
	h := startHost(t, ext, hostOptions{})

	tl, ok := h.tools["ask"]
	if !ok {
		t.Fatal("ask tool was not registered")
	}
	if tl.Label != "Ask" {
		t.Errorf("label = %q, want %q", tl.Label, "Ask")
	}
	if tl.ExecutionMode != "sequential" {
		t.Errorf("execution_mode = %q, want sequential", tl.ExecutionMode)
	}
	if !strings.Contains(tl.Description, "Ask only for materially different tradeoffs") {
		t.Errorf("unexpected description: %q", tl.Description)
	}
	if !strings.Contains(tl.Description, "Other (type your own)") {
		t.Errorf("description missing Other instruction: %q", tl.Description)
	}
	props, _ := tl.Parameters["properties"].(map[string]any)
	qSchema, _ := props["questions"].(map[string]any)
	if qSchema["type"] != "array" || qSchema["minItems"] != float64(1) {
		t.Errorf("unexpected questions schema: %+v", qSchema)
	}
	items, _ := qSchema["items"].(map[string]any)
	qProps, _ := items["properties"].(map[string]any)
	for _, key := range []string{"id", "question", "header", "options", "multi", "recommended"} {
		if _, exists := qProps[key]; !exists {
			t.Errorf("question schema missing property %q", key)
		}
	}
	optSchema, _ := qProps["options"].(map[string]any)
	optItems, _ := optSchema["items"].(map[string]any)
	optProps, _ := optItems["properties"].(map[string]any)
	for _, key := range []string{"label", "description", "preview"} {
		if _, exists := optProps[key]; !exists {
			t.Errorf("option schema missing property %q", key)
		}
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "aborts the turn when the user cancels selection"
func TestCancelSingleSelectionAbortsTurn(t *testing.T) {
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				return map[string]any{"ok": false, "selected": ""}, ""
			}
			return nil, ""
		},
	})

	_, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "confirm",
				"question": "Proceed?",
				"options":  []any{map[string]any{"label": "yes"}, map[string]any{"label": "no"}},
			},
		},
	})
	if failure != "Ask tool was cancelled by the user" {
		t.Fatalf("failure = %q, want %q", failure, "Ask tool was cancelled by the user")
	}
	if got := len(h.callsTo("abort")); got != 1 {
		t.Fatalf("abort calls = %d, want 1", got)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "defaults to no timeout when ask.timeout is unset"
func TestDefaultsToNoTimeout(t *testing.T) {
	var seenOpts any
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				seenOpts = args["opts"]
				opts := stringSlice(args["options"])
				return map[string]any{"ok": true, "selected": opts[0]}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "confirm",
				"question": "Proceed?",
				"options":  []any{map[string]any{"label": "yes"}, map[string]any{"label": "no"}},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	if seenOpts != nil {
		t.Fatalf("expected no timeout opts, got %+v", seenOpts)
	}
	res := decodeResult(t, raw)
	if res.Content != "User selected: yes" {
		t.Errorf("content = %q, want %q", res.Content, "User selected: yes")
	}
	if !reflect.DeepEqual(res.Details.SelectedOptions, []string{"yes"}) {
		t.Errorf("selectedOptions = %v, want [yes]", res.Details.SelectedOptions)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "still aborts when user explicitly cancels with timeout configured"
func TestExplicitCancelWithTimeoutConfiguredStillAborts(t *testing.T) {
	now := time.Unix(1000, 0)
	h := startHost(t, newTestExt(config{
		timeout: 30 * time.Second,
		now:     func() time.Time { return now },
	}), hostOptions{
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				// User cancels immediately (0s elapsed < 30s timeout).
				return map[string]any{"ok": false, "selected": ""}, ""
			}
			return nil, ""
		},
	})

	_, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "confirm",
				"question": "Proceed?",
				"options":  []any{map[string]any{"label": "yes"}, map[string]any{"label": "no"}},
			},
		},
	})
	if failure != "Ask tool was cancelled by the user" {
		t.Fatalf("failure = %q, want %q", failure, "Ask tool was cancelled by the user")
	}
	if got := len(h.callsTo("abort")); got != 1 {
		t.Fatalf("abort calls = %d, want 1", got)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "auto-selects the recommended option on ask timeout"
func TestAutoSelectsRecommendedOnTimeout(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1000, 0)
	var seenTimeout float64
	var seenOptions []string

	h := startHost(t, newTestExt(config{
		timeout: 5 * time.Second,
		now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
	}), hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				seenOptions = stringSlice(args["options"])
				if opts, ok := args["opts"].(map[string]any); ok {
					seenTimeout, _ = opts["timeout"].(float64)
				}
				mu.Lock()
				now = now.Add(5 * time.Second)
				mu.Unlock()
				return map[string]any{"ok": false, "selected": ""}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":          "confirm",
				"question":    "Proceed?",
				"options":     []any{map[string]any{"label": "yes"}, map[string]any{"label": "no"}},
				"recommended": 1,
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	if seenTimeout != 5000 {
		t.Errorf("timeout sent = %v, want 5000", seenTimeout)
	}
	wantOpts := []string{"yes", "no (Recommended)", otherOption}
	if !reflect.DeepEqual(seenOptions, wantOpts) {
		t.Errorf("options = %v, want %v", seenOptions, wantOpts)
	}
	res := decodeResult(t, raw)
	if res.Content != "User selected: no (auto-selected after timeout)" {
		t.Errorf("content = %q", res.Content)
	}
	if !reflect.DeepEqual(res.Details.SelectedOptions, []string{"no"}) {
		t.Errorf("selectedOptions = %v, want [no]", res.Details.SelectedOptions)
	}
	if !res.Details.TimedOut {
		t.Errorf("expected timedOut=true")
	}
	if got := len(h.callsTo("abort")); got != 0 {
		t.Errorf("abort calls = %d, want 0", got)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "auto-selects the first option when timeout elapses without a selected option"
func TestAutoSelectsFirstOptionOnTimeoutWithoutRecommended(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(2000, 0)

	h := startHost(t, newTestExt(config{
		timeout: 10 * time.Second,
		now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
	}), hostOptions{
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				mu.Lock()
				now = now.Add(10*time.Second + 50*time.Millisecond)
				mu.Unlock()
				return map[string]any{"ok": false, "selected": ""}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "confirm",
				"question": "Proceed?",
				"options":  []any{map[string]any{"label": "yes"}, map[string]any{"label": "no"}},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	res := decodeResult(t, raw)
	if res.Content != "User selected: yes (auto-selected after timeout)" {
		t.Errorf("content = %q", res.Content)
	}
	if !reflect.DeepEqual(res.Details.SelectedOptions, []string{"yes"}) {
		t.Errorf("selectedOptions = %v, want [yes]", res.Details.SelectedOptions)
	}
	if got := len(h.callsTo("abort")); got != 0 {
		t.Errorf("abort calls = %d, want 0", got)
	}
}

// upstream: packages/coding-agent/test/ask-timeout.test.ts: "auto-selects timed-out single-choice questions before advancing multi-question asks"
func TestTimeoutAdvancesMultiQuestionAsk(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(3000, 0)
	step := 0

	h := startHost(t, newTestExt(config{
		timeout: 5 * time.Second,
		now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
	}), hostOptions{
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				step++
				if step == 1 {
					mu.Lock()
					now = now.Add(5 * time.Second)
					mu.Unlock()
					return map[string]any{"ok": false, "selected": ""}, ""
				}
				return map[string]any{"ok": true, "selected": "OAuth"}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":          "db",
				"question":    "Which database?",
				"options":     []any{map[string]any{"label": "SQLite"}, map[string]any{"label": "Postgres"}},
				"recommended": 1,
			},
			map[string]any{
				"id":          "auth",
				"question":    "Which auth?",
				"options":     []any{map[string]any{"label": "JWT"}, map[string]any{"label": "OAuth"}},
				"recommended": 0,
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	res := decodeResult(t, raw)
	wantText := "User answers:\ndb: Postgres (auto-selected after timeout)\nauth: OAuth"
	if res.Content != wantText {
		t.Errorf("content = %q, want %q", res.Content, wantText)
	}
	if len(res.Details.Results) != 2 {
		t.Fatalf("results len = %d, want 2", len(res.Details.Results))
	}
	if !reflect.DeepEqual(res.Details.Results[0].SelectedOptions, []string{"Postgres"}) || !res.Details.Results[0].TimedOut {
		t.Errorf("first result = %+v", res.Details.Results[0])
	}
	if !reflect.DeepEqual(res.Details.Results[1].SelectedOptions, []string{"OAuth"}) || res.Details.Results[1].TimedOut {
		t.Errorf("second result = %+v", res.Details.Results[1])
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "aborts multi-question ask when any question is explicitly cancelled"
func TestAbortsMultiQuestionOnCancel(t *testing.T) {
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				title, _ := args["title"].(string)
				if strings.Contains(title, "First") {
					return map[string]any{"ok": true, "selected": "one"}, ""
				}
				return map[string]any{"ok": false, "selected": ""}, ""
			}
			return nil, ""
		},
	})

	_, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{"id": "first", "question": "First", "options": []any{map[string]any{"label": "one"}, map[string]any{"label": "two"}}},
			map[string]any{"id": "second", "question": "Second", "options": []any{map[string]any{"label": "alpha"}, map[string]any{"label": "beta"}}},
		},
	})
	if failure != "Ask tool was cancelled by the user" {
		t.Fatalf("failure = %q", failure)
	}
	if got := len(h.callsTo("abort")); got != 1 {
		t.Fatalf("abort calls = %d, want 1", got)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "passes descriptions to the selector while returning selected labels"
func TestOptionDescriptionsFormattedAndBareLabelReturned(t *testing.T) {
	var seenOptions []string
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				seenOptions = stringSlice(args["options"])
				return map[string]any{"ok": true, "selected": seenOptions[1]}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "auth",
				"question": "How should authentication continue?",
				"options": []any{
					map[string]any{"label": "Use local credentials", "description": "Authenticate with provider keys already configured under ~/.omp."},
					map[string]any{"label": "Set up in terminal", "description": "Launch the terminal setup flow to add credentials before continuing."},
				},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	if len(seenOptions) != 3 {
		t.Fatalf("options = %v, want 3 entries", seenOptions)
	}
	if !strings.Contains(seenOptions[0], "Use local credentials") || !strings.Contains(seenOptions[0], "Authenticate with provider keys") {
		t.Errorf("option 0 missing label or description: %q", seenOptions[0])
	}
	if seenOptions[2] != otherOption {
		t.Errorf("last option = %q, want %q", seenOptions[2], otherOption)
	}
	res := decodeResult(t, raw)
	if res.Content != "User selected: Set up in terminal" {
		t.Errorf("content = %q", res.Content)
	}
	if strings.Contains(res.Content, "Launch the terminal setup flow") {
		t.Errorf("response text must not contain description: %q", res.Content)
	}
	if !reflect.DeepEqual(res.Details.SelectedOptions, []string{"Set up in terminal"}) {
		t.Errorf("selectedOptions = %v", res.Details.SelectedOptions)
	}
	if !reflect.DeepEqual(res.Details.Options, []string{"Use local credentials", "Set up in terminal"}) {
		t.Errorf("options = %v", res.Details.Options)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "forwards descriptions through multi-select and returns bare labels"
func TestMultiSelectWithDescriptionsAndDone(t *testing.T) {
	step := 0
	var firstOptions []string
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				opts := stringSlice(args["options"])
				step++
				switch step {
				case 1:
					firstOptions = opts
					return map[string]any{"ok": true, "selected": opts[0]}, ""
				case 2:
					return map[string]any{"ok": true, "selected": opts[1]}, ""
				default:
					for _, o := range opts {
						if strings.Contains(o, "Done selecting") {
							return map[string]any{"ok": true, "selected": o}, ""
						}
					}
					return map[string]any{"ok": false, "selected": ""}, ""
				}
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "multi",
				"question": "Pick answers",
				"options": []any{
					map[string]any{"label": "alpha", "description": "First choice detail."},
					map[string]any{"label": "beta", "description": "Second choice detail."},
				},
				"multi": true,
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	if !strings.Contains(firstOptions[0], "First choice detail.") {
		t.Errorf("expected description in multi-select option 0: %q", firstOptions[0])
	}
	res := decodeResult(t, raw)
	if !reflect.DeepEqual(res.Details.SelectedOptions, []string{"alpha", "beta"}) {
		t.Errorf("selectedOptions = %v, want [alpha beta]", res.Details.SelectedOptions)
	}
	if res.Content != "User selected: alpha, beta" {
		t.Errorf("content = %q", res.Content)
	}
	if strings.Contains(res.Content, "First choice detail") {
		t.Errorf("description leaked into result: %q", res.Content)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "routes custom input through editor and preserves raw multiline strings"
func TestCustomInputMultilineThroughEditor(t *testing.T) {
	multiline := "first line\nsecond line"
	var editorTitle string
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				return map[string]any{"ok": true, "selected": otherOption}, ""
			case "ui.editor":
				editorTitle, _ = args["title"].(string)
				return map[string]any{"ok": true, "text": multiline}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "details",
				"question": "Share details",
				"options":  []any{map[string]any{"label": "yes"}, map[string]any{"label": "no"}},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	if editorTitle != "Share details" {
		t.Errorf("editor title = %q, want %q", editorTitle, "Share details")
	}
	res := decodeResult(t, raw)
	want := "User provided custom input:\n  first line\n  second line"
	if res.Content != want {
		t.Errorf("content = %q, want %q", res.Content, want)
	}
	if res.Details.CustomInput == nil || *res.Details.CustomInput != multiline {
		t.Errorf("customInput = %v, want %q", res.Details.CustomInput, multiline)
	}
	if len(res.Details.SelectedOptions) != 0 {
		t.Errorf("selectedOptions = %v, want []", res.Details.SelectedOptions)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "returns to the option selector when custom input is dismissed in single-question flow"
func TestDismissCustomInputReturnsToSelectorSingle(t *testing.T) {
	selectCalls := 0
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				selectCalls++
				if selectCalls == 1 {
					return map[string]any{"ok": true, "selected": otherOption}, ""
				}
				return map[string]any{"ok": true, "selected": "yes"}, ""
			case "ui.editor":
				return map[string]any{"ok": false, "text": ""}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "details",
				"question": "Share details",
				"options":  []any{map[string]any{"label": "yes"}, map[string]any{"label": "no"}},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	if selectCalls != 2 {
		t.Errorf("selectCalls = %d, want 2", selectCalls)
	}
	res := decodeResult(t, raw)
	if !reflect.DeepEqual(res.Details.SelectedOptions, []string{"yes"}) {
		t.Errorf("selectedOptions = %v, want [yes]", res.Details.SelectedOptions)
	}
	if res.Details.CustomInput != nil {
		t.Errorf("expected nil customInput, got %q", *res.Details.CustomInput)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "returns to the option selector when custom input is dismissed in multi-question flow"
func TestDismissCustomInputReturnsToSelectorMultiQuestion(t *testing.T) {
	detailsVisits := 0
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				title, _ := args["title"].(string)
				if strings.Contains(title, "First?") {
					return map[string]any{"ok": true, "selected": "one"}, ""
				}
				if strings.Contains(title, "Details?") {
					detailsVisits++
					if detailsVisits == 1 {
						return map[string]any{"ok": true, "selected": otherOption}, ""
					}
					return map[string]any{"ok": true, "selected": "short"}, ""
				}
			case "ui.editor":
				return map[string]any{"ok": false, "text": ""}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{"id": "first", "question": "First?", "options": []any{map[string]any{"label": "one"}, map[string]any{"label": "two"}}},
			map[string]any{"id": "details", "question": "Details?", "options": []any{map[string]any{"label": "short"}, map[string]any{"label": "long"}}},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	if detailsVisits != 2 {
		t.Errorf("detailsVisits = %d, want 2", detailsVisits)
	}
	res := decodeResult(t, raw)
	if res.Content != "User answers:\nfirst: one\ndetails: short" {
		t.Errorf("content = %q", res.Content)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "treats explicit empty-string custom input as submitted input"
func TestEmptyStringCustomInputAccepted(t *testing.T) {
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				return map[string]any{"ok": true, "selected": otherOption}, ""
			case "ui.editor":
				return map[string]any{"ok": true, "text": ""}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "details",
				"question": "Share details",
				"options":  []any{map[string]any{"label": "yes"}, map[string]any{"label": "no"}},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	res := decodeResult(t, raw)
	if res.Content != "User provided custom input: " {
		t.Errorf("content = %q", res.Content)
	}
	if res.Details.CustomInput == nil || *res.Details.CustomInput != "" {
		t.Errorf("customInput = %v, want empty string", res.Details.CustomInput)
	}
	if got := len(h.callsTo("abort")); got != 0 {
		t.Errorf("abort calls = %d, want 0", got)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "returns to the option selector when multi-select custom input is dismissed"
func TestDismissCustomInputMultiSelectKeepsCheckedOptions(t *testing.T) {
	step := 0
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				opts := stringSlice(args["options"])
				step++
				switch step {
				case 1:
					return map[string]any{"ok": true, "selected": opts[0]}, ""
				case 2:
					return map[string]any{"ok": true, "selected": otherOption}, ""
				default:
					return map[string]any{"ok": true, "selected": doneOptionLabel}, ""
				}
			case "ui.editor":
				return map[string]any{"ok": false, "text": ""}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "multi",
				"question": "Pick answers",
				"options":  []any{map[string]any{"label": "alpha"}, map[string]any{"label": "beta"}},
				"multi":    true,
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	res := decodeResult(t, raw)
	if !reflect.DeepEqual(res.Details.SelectedOptions, []string{"alpha"}) {
		t.Errorf("selectedOptions = %v, want [alpha]", res.Details.SelectedOptions)
	}
	if res.Details.CustomInput != nil {
		t.Errorf("expected nil customInput")
	}
	if res.Content != "User selected: alpha" {
		t.Errorf("content = %q", res.Content)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "reports ticked options together with Other text for a multi-select question"
func TestMultiSelectTickedOptionsPlusOtherInMultiQuestion(t *testing.T) {
	choices := []string{"alpha", "beta", otherOption}
	var editorTitle string
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				title, _ := args["title"].(string)
				if strings.Contains(title, "Features?") {
					pick := choices[0]
					choices = choices[1:]
					return map[string]any{"ok": true, "selected": pick}, ""
				}
				return map[string]any{"ok": true, "selected": "two"}, ""
			case "ui.editor":
				editorTitle, _ = args["title"].(string)
				return map[string]any{"ok": true, "text": "also X"}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "features",
				"question": "Features?",
				"options":  []any{map[string]any{"label": "alpha"}, map[string]any{"label": "beta"}, map[string]any{"label": "gamma"}},
				"multi":    true,
			},
			map[string]any{
				"id":       "summary",
				"question": "Summary?",
				"options":  []any{map[string]any{"label": "one"}, map[string]any{"label": "two"}},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	if editorTitle != "(2 selected) Features? (1/2)" {
		t.Errorf("editor title = %q, want %q", editorTitle, "(2 selected) Features? (1/2)")
	}
	res := decodeResult(t, raw)
	want := "User answers:\nfeatures: [alpha, beta] + \"also X\"\nsummary: two"
	if res.Content != want {
		t.Errorf("content = %q, want %q", res.Content, want)
	}
}

// TestFreeTextQuestionEmptyOptions verifies a question with options: [] opens
// the free-text editor directly and formats the custom answer like omp.
func TestFreeTextQuestionEmptyOptions(t *testing.T) {
	var editorCalled bool
	var selectCalled bool
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			switch method {
			case "ui.select":
				selectCalled = true
			case "ui.editor":
				editorCalled = true
				return map[string]any{"ok": true, "text": "my free-form answer"}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "notes",
				"question": "Any constraints?",
				"options":  []any{},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	if !editorCalled || selectCalled {
		t.Fatalf("editorCalled=%v selectCalled=%v, want true/false", editorCalled, selectCalled)
	}
	res := decodeResult(t, raw)
	if res.Content != "User provided custom input: my free-form answer" {
		t.Errorf("content = %q", res.Content)
	}
	if res.Details.CustomInput == nil || *res.Details.CustomInput != "my free-form answer" {
		t.Errorf("customInput = %v", res.Details.CustomInput)
	}
}

// TestNoUIRunAbortsAndErrors verifies headless (print/json, hasUI=false) behavior
// matches omp's ask.ts:651-654.
func TestNoUIRunAbortsAndErrors(t *testing.T) {
	h := startHost(t, newTestExt(config{}), hostOptions{
		Mode:  "print",
		HasUI: new(false),
	})

	_, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "q1",
				"question": "Continue?",
				"options":  []any{map[string]any{"label": "Yes"}, map[string]any{"label": "No"}},
			},
		},
	})
	if failure != "Ask tool requires interactive mode" {
		t.Fatalf("failure = %q, want %q", failure, "Ask tool requires interactive mode")
	}
	if got := len(h.callsTo("abort")); got != 1 {
		t.Fatalf("abort calls = %d, want 1", got)
	}
	if got := len(h.callsTo("ui.select")); got != 0 {
		t.Fatalf("ui.select calls = %d, want 0", got)
	}
}

// TestRPCModeUsesSelectAndEditor verifies RPC mode (hasUI=true, mode="rpc")
// answers via ui.select / ui.editor without failing the headless check.
func TestRPCModeUsesSelectAndEditor(t *testing.T) {
	h := startHost(t, newTestExt(config{}), hostOptions{
		Mode:  "rpc",
		HasUI: new(true),
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				return map[string]any{"ok": true, "selected": "Postgres"}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "storage",
				"question": "Database?",
				"options":  []any{map[string]any{"label": "SQLite"}, map[string]any{"label": "Postgres"}},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	res := decodeResult(t, raw)
	if res.Content != "User selected: Postgres" {
		t.Errorf("content = %q", res.Content)
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "rejects reserved-label collision in parameters validation"
// upstream: packages/coding-agent/test/tools/ask.test.ts: "fails closed when sanitization collapses a label into a reserved runtime label"
func TestRejectsReservedLabelsAndSanitizedCollisions(t *testing.T) {
	cases := []struct {
		name  string
		label string
		multi bool
	}{
		{"literal Other", otherOption, false},
		{"literal Chat", chatAboutThisOption, false},
		{"literal Next", nextOption, false},
		{"CR collapsed Other", "Other\r(type your own)", false},
		{"CR collapsed Done in multi", "✔\rDone selecting", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := startHost(t, newTestExt(config{}), hostOptions{
				OnCall: func(method string, _ map[string]any) (map[string]any, string) {
					if method == "ui.select" {
						t.Errorf("ui.select must not be called for invalid params")
					}
					return nil, ""
				},
			})
			raw, failure := h.callTool("ask", map[string]any{
				"questions": []any{
					map[string]any{
						"id":       "q1",
						"question": "Q?",
						"options":  []any{map[string]any{"label": tc.label}, map[string]any{"label": "OK"}},
						"multi":    tc.multi,
					},
				},
			})
			if failure != "" {
				t.Fatalf("expected error text result, got tool error %q", failure)
			}
			res := decodeResult(t, raw)
			if !strings.Contains(res.Content, "reserved runtime labels") {
				t.Errorf("content = %q, want reserved runtime labels error", res.Content)
			}
			if strings.Contains(res.Content, "\r") {
				t.Errorf("content still contains CR: %q", res.Content)
			}
		})
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "fails closed when sanitization merges distinct labels into one"
// upstream: packages/coding-agent/test/tools/ask.test.ts: "fails closed when sanitization merges distinct question ids"
// upstream: packages/coding-agent/test/tools/ask.test.ts: "expands tabs and clamps long values in validation errors"
func TestValidationUniquenessAndErrorClamping(t *testing.T) {
	h := startHost(t, newTestExt(config{}), hostOptions{})

	// Duplicate option labels after CR sanitization.
	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "q1",
				"question": "Q?",
				"options":  []any{map[string]any{"label": "Retry\rnow"}, map[string]any{"label": "Retry now"}},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	res := decodeResult(t, raw)
	if !strings.Contains(res.Content, "unique within a question") || !strings.Contains(res.Content, "Retry now") {
		t.Errorf("content = %q", res.Content)
	}

	// Duplicate question IDs after CR sanitization.
	raw, failure = h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{"id": "deploy\rmode", "question": "Deploy?", "options": []any{map[string]any{"label": "Yes"}}},
			map[string]any{"id": "deploy mode", "question": "Really?", "options": []any{map[string]any{"label": "No"}}},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	res = decodeResult(t, raw)
	if !strings.Contains(res.Content, "question ids must be unique") || !strings.Contains(res.Content, "deploy mode") {
		t.Errorf("content = %q", res.Content)
	}

	// Tab expansion and width clamping in validation errors.
	dupe := "Tab\there" + strings.Repeat("\t", 40) + strings.Repeat("x", 500)
	raw, failure = h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "q1",
				"question": "Q?",
				"options":  []any{map[string]any{"label": dupe}, map[string]any{"label": dupe}},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	res = decodeResult(t, raw)
	if !strings.Contains(res.Content, "unique within a question") {
		t.Errorf("content = %q", res.Content)
	}
	if strings.ContainsAny(res.Content, "\t\r") {
		t.Errorf("content must not contain tabs or CR: %q", res.Content)
	}
	if len(res.Content) >= 250 {
		t.Errorf("error content not clamped: len=%d", len(res.Content))
	}
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "preserves suffixed labels through the degraded selector"
// upstream: packages/coding-agent/test/tools/ask.test.ts: "disambiguates badged rows in the degraded selector"
func TestRecommendedSuffixPreservationAndDisambiguation(t *testing.T) {
	t.Run("preserves intrinsic Recommended suffix", func(t *testing.T) {
		h := startHost(t, newTestExt(config{}), hostOptions{
			OnCall: func(method string, args map[string]any) (map[string]any, string) {
				if method == "ui.select" {
					opts := stringSlice(args["options"])
					return map[string]any{"ok": true, "selected": opts[0]}, ""
				}
				return nil, ""
			},
		})
		raw, failure := h.callTool("ask", map[string]any{
			"questions": []any{
				map[string]any{
					"id":          "q1",
					"question":    "Cache?",
					"options":     []any{map[string]any{"label": "Use cache\r(Recommended)"}, map[string]any{"label": "Fetch anew"}},
					"recommended": 0,
				},
			},
		})
		if failure != "" {
			t.Fatalf("unexpected failure: %s", failure)
		}
		res := decodeResult(t, raw)
		if !reflect.DeepEqual(res.Details.SelectedOptions, []string{"Use cache (Recommended)"}) {
			t.Errorf("selectedOptions = %v", res.Details.SelectedOptions)
		}
		if res.Content != "User selected: Use cache (Recommended)" {
			t.Errorf("content = %q", res.Content)
		}
	})

	t.Run("disambiguates badged rows", func(t *testing.T) {
		var seenOptions []string
		h := startHost(t, newTestExt(config{}), hostOptions{
			OnCall: func(method string, args map[string]any) (map[string]any, string) {
				if method == "ui.select" {
					seenOptions = stringSlice(args["options"])
					return map[string]any{"ok": true, "selected": "Retry now (Recommended) (2)"}, ""
				}
				return nil, ""
			},
		})
		raw, failure := h.callTool("ask", map[string]any{
			"questions": []any{
				map[string]any{
					"id":          "q1",
					"question":    "Retry?",
					"options":     []any{map[string]any{"label": "Retry\rnow"}, map[string]any{"label": "Retry now (Recommended)"}},
					"recommended": 0,
				},
			},
		})
		if failure != "" {
			t.Fatalf("unexpected failure: %s", failure)
		}
		wantPrefix := []string{"Retry now (Recommended)", "Retry now (Recommended) (2)"}
		if len(seenOptions) < 2 || !reflect.DeepEqual(seenOptions[:2], wantPrefix) {
			t.Errorf("options = %v, want prefix %v", seenOptions, wantPrefix)
		}
		res := decodeResult(t, raw)
		if !reflect.DeepEqual(res.Details.SelectedOptions, []string{"Retry now (Recommended)"}) {
			t.Errorf("selectedOptions = %v", res.Details.SelectedOptions)
		}
	})
}

// upstream: packages/coding-agent/test/tools/ask.test.ts: "strips \\r runs from dialog input and result echo (GLM-style degenerate JSON strings)"
func TestSanitizesCarriageReturns(t *testing.T) {
	var seenTitle string
	var seenOptions []string
	h := startHost(t, newTestExt(config{}), hostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				seenTitle, _ = args["title"].(string)
				seenOptions = stringSlice(args["options"])
				return map[string]any{"ok": true, "selected": seenOptions[0]}, ""
			}
			return nil, ""
		},
	})

	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "q3a",
				"question": "Q3\r\rA\r\r —\r\r Fallback\r\r path\r\r.\r\r What\r\r happens\r\r?",
				"header":   "Fallback\r\r path",
				"options": []any{
					map[string]any{
						"label":       "Abort\r\r \r\r+\r\r log",
						"description": "The\r\r worker\r\r pool\r\r sees\r\r nothing\r\r.",
					},
					map[string]any{"label": "Continue\r\r anyway"},
				},
			},
		},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	if strings.Contains(seenTitle, "\r") || seenTitle != "Q3 A  —  Fallback  path .  What  happens ?" {
		t.Errorf("sanitized title = %q", seenTitle)
	}
	res := decodeResult(t, raw)
	if res.Content != "User selected: Abort   +  log" {
		t.Errorf("content = %q", res.Content)
	}
	if strings.Contains(res.Content, "\r") {
		t.Errorf("result still contains CR: %q", res.Content)
	}
}

func TestEmptyQuestionsReturnsErrorText(t *testing.T) {
	h := startHost(t, newTestExt(config{}), hostOptions{})
	raw, failure := h.callTool("ask", map[string]any{
		"questions": []any{},
	})
	if failure != "" {
		t.Fatalf("unexpected failure: %s", failure)
	}
	res := decodeResult(t, raw)
	if res.Content != "Error: questions must not be empty" {
		t.Errorf("content = %q", res.Content)
	}
}

func TestFormatQuestionResultEdges(t *testing.T) {
	empty := ""
	custom := "custom text"
	note := "my note"

	cases := []struct {
		name string
		in   questionResult
		want string
	}{
		{
			name: "single cancelled",
			in:   questionResult{ID: "q1", Multi: false, SelectedOptions: []string{}},
			want: "q1: (cancelled)",
		},
		{
			name: "multi empty",
			in:   questionResult{ID: "q1", Multi: true, SelectedOptions: []string{}},
			want: "q1: []",
		},
		{
			name: "custom only with note",
			in:   questionResult{ID: "q1", Multi: false, SelectedOptions: []string{}, CustomInput: &custom, Note: &note},
			want: `q1: "custom text" (note: my note)`,
		},
		{
			name: "empty custom string",
			in:   questionResult{ID: "q1", Multi: false, SelectedOptions: []string{}, CustomInput: &empty},
			want: `q1: ""`,
		},
		{
			name: "multi with custom and timeout",
			in:   questionResult{ID: "q1", Multi: true, SelectedOptions: []string{"A", "B"}, CustomInput: &custom, TimedOut: true},
			want: `q1: [A, B] + "custom text" (auto-selected after timeout)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatQuestionResult(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
