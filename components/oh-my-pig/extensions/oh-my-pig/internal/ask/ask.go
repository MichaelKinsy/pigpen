// Package ask ports omp's HTML <form> ask tool onto PiG's Go extension SDK.
package ask

import (
	_ "embed"
	"errors"
	"fmt"
	"html"
	"math"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

//go:embed ask.md
var askPrompt string

//go:embed ask-headless-fork.md
var askHeadlessForkPrompt string

//go:embed ask-result.md
var askResultTemplate string

//go:embed ask-split-radio-groups.md
var askSplitRadioGroupsTemplate string

// Setting reads an omp setting by key (e.g. "ask.timeout", "ask.notify").
// Nil falls back to omp's defaults. Assigned by the integrator.
var Setting func(ctx sdk.Context, key string) any

const (
	// DefaultAskTimeoutMs matches omp's default `ask.timeout` setting (0 = disabled).
	// upstream: packages/coding-agent/src/modes/settings.ts:1443-1457
	DefaultAskTimeoutMs = 0

	// timeoutDetectionToleranceMs is the window after the deadline within which
	// a nil dialog result is attributed to a UI-enforced timeout rather than Esc.
	// upstream: packages/coding-agent/src/tools/ask.ts:74
	timeoutDetectionToleranceMs = 1000

	otherRowLabel = OtherLabel + " (type your own)"
	// nativeOtherRowLabel is the free-text row's label in Tern.
	// upstream: packages/tui/src/overlays/ask-dialog.ts:109 (OTHER_LABEL)
	nativeOtherRowLabel = OtherLabel + "…"
	doneRowLabel        = "✔ Done selecting"

	noNamedControlsError  = "Error: the form has no named controls. Put at least one <input>, <select> or <textarea> with a `name` attribute inside the <form>."
	askTimedOutUnanswered = "The ask timed out before the user answered its required fields; no answer was given."
	invalidAskError       = "Error: ask takes one `html` string holding a <form>."
)

// AskToolDetails matches omp's AskToolDetails in packages/tui/src/tools/ask.ts:24-31,
// plus the parsed Form so a Tern-native frontend can render the whole form at once.
type AskToolDetails struct {
	Answers  []FormAnswer `json:"answers"`
	Notes    []OptionNote `json:"notes,omitempty"`
	TimedOut bool         `json:"timedOut,omitempty"`
	Form     *HtmlForm    `json:"form,omitempty"`
}

// Config configures optional ask behavior.
type Config struct {
	// TimeoutMs overrides ask.timeout in milliseconds when > 0 (0 reads Setting("ask.timeout")).
	TimeoutMs int
	// AdvertiseHeadlessAsk keeps `ask` advertised in headless sessions and returns
	// the headless-fork guidance instead of aborting when ctx.HasUI() is false.
	AdvertiseHeadlessAsk bool
}

// Register registers the `ask` tool and headless visibility hook on the PiG extension.
func Register(e *sdk.Extension) {
	RegisterWithConfig(e, Config{TimeoutMs: DefaultAskTimeoutMs})
}

// RegisterWithConfig registers the `ask` tool with explicit configuration.
func RegisterWithConfig(e *sdk.Extension, cfg Config) {
	askDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:             "ask",
		Label:            "Ask",
		Description:      strings.TrimSpace(askPrompt),
		Parameters:       askToolSchema(),
		ExecutionMode:    "sequential",
		PrepareArguments: prepareAskArguments,
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return executeAsk(ctx, params, cfg)
		},
	})
	e.RegisterTool(intent.Tool(askDef))
	intent.RegisterSync(e, askDef)
	// upstream: packages/coding-agent/src/tools/ask.ts:443-445 (AskTool.createIf)
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if ctx.HasUI() || cfg.AdvertiseHeadlessAsk {
			return nil, nil
		}
		active, err := ctx.GetActiveTools()
		if err != nil {
			return nil, nil
		}
		filtered := make([]string, 0, len(active))
		changed := false
		for _, name := range active {
			if name == "ask" {
				changed = true
				continue
			}
			filtered = append(filtered, name)
		}
		if changed {
			ctx.SetActiveTools(filtered)
		}
		return nil, nil
	})
}

// prepareAskArguments mirrors omp's lenient ask validation before PiG's host
// JSON Schema validator runs:
//   - A valid string `html` passes through (extra root keys are stripped by
//     intent's lenient wrapper on schema-valid calls, or kept only if valid).
//   - A legacy `questions` payload is rebuilt into `{"html": ...}`.
//   - Any other malformed shape (missing `html`, non-string `html` such as
//     null, unknown keys without `html`, invalid legacy `questions`) is refused
//     verbatim with omp's INVALID_ASK_ERROR so PiG's host validator never
//     answers in PiG's wording or coerces `{"html": null}` to `{"html": "null"}`.
//
// upstream: packages/coding-agent/src/tools/ask.ts:69,87-120,429,520-523
func prepareAskArguments(params map[string]any) (map[string]any, error) {
	if params == nil {
		return nil, errors.New(invalidAskError)
	}
	if rawHTML, ok := RecoverAskHTML(params); ok {
		if _, hasHTML := params["html"].(string); hasHTML {
			return params, nil
		}
		return map[string]any{"html": rawHTML}, nil
	}
	return nil, errors.New(invalidAskError)
}

// askToolSchema matches omp's askSchema in packages/coding-agent/src/tools/ask.ts:76-78.
func askToolSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"html": map[string]any{
				"type":        "string",
				"description": "one HTML <form>",
			},
		},
		"required":             []any{"html"},
		"additionalProperties": false,
	}
}

// RecoverAskHTML extracts the form HTML from params, or rebuilds it from a legacy
// pre-HTML `questions` list.
// upstream: packages/coding-agent/src/tools/ask.ts:87-115
func RecoverAskHTML(params map[string]any) (string, bool) {
	return RecoverAskHTMLValue(params)
}

// RecoverAskHTMLValue recovers form HTML from a bare string, an `{html}` object,
// or a legacy `{questions: [...]}` object.
// upstream: packages/coding-agent/src/tools/ask.ts:87-115
func RecoverAskHTMLValue(raw any) (string, bool) {
	if s, ok := raw.(string); ok {
		return s, true
	}
	params, ok := raw.(map[string]any)
	if !ok || params == nil {
		return "", false
	}
	if h, ok := params["html"].(string); ok {
		return h, true
	}
	if qs, ok := params["questions"].([]any); ok && len(qs) > 0 {
		return legacyQuestionsHTML(qs)
	}
	return "", false
}

func legacyQuestionsHTML(questions []any) (string, bool) {
	var fieldsets []string
	for idx, raw := range questions {
		item, ok := raw.(map[string]any)
		if !ok {
			return "", false
		}
		q, ok := item["question"].(string)
		if !ok {
			return "", false
		}
		id, _ := item["id"].(string)
		if id == "" {
			id = fmt.Sprintf("q%d", idx+1)
		}
		name := html.EscapeString(id)
		kind := "radio"
		if m, ok := item["multi"].(bool); ok && m {
			kind = "checkbox"
		}
		recIdx := -1
		if rec, ok := item["recommended"].(float64); ok {
			recIdx = int(rec)
		}
		var inputs []string
		if opts, ok := item["options"].([]any); ok {
			for oi, opt := range opts {
				var label, desc string
				switch ov := opt.(type) {
				case string:
					label = ov
				case map[string]any:
					l, ok := ov["label"].(string)
					if !ok {
						return "", false
					}
					label = l
					if d, ok := ov["description"].(string); ok && d != "" {
						desc = " — " + html.EscapeString(d)
					}
				default:
					return "", false
				}
				checked := ""
				if recIdx == oi {
					checked = " checked"
				}
				esc := html.EscapeString(label)
				inputs = append(inputs, fmt.Sprintf(`<label><input type="%s" name="%s" value="%s"%s> %s%s</label>`, kind, name, esc, checked, esc, desc))
			}
		}
		control := strings.Join(inputs, "")
		if control == "" {
			control = fmt.Sprintf(`<textarea name="%s"></textarea>`, name)
		}
		fieldsets = append(fieldsets, fmt.Sprintf(`<fieldset><legend>%s</legend>%s</fieldset>`, html.EscapeString(q), control))
	}
	return "<form>" + strings.Join(fieldsets, "") + "</form>", true
}

type splitRadioFieldset struct {
	Legend string
	Names  []string
}

// splitRadioFieldsets detects <fieldset>s whose radios each have a different name.
// upstream: packages/coding-agent/src/tools/ask.ts:128-141
func splitRadioFieldsets(form *HtmlForm) []splitRadioFieldset {
	var split []splitRadioFieldset
	for _, s := range form.Sections {
		if !s.Fieldset {
			continue
		}
		var names []string
		for _, blk := range s.Blocks {
			if blk.Kind != "field" || blk.Field == nil {
				continue
			}
			f := blk.Field
			if f.Kind != "choice" || f.Multi || f.Select {
				continue
			}
			if f.Name != "" && len(f.Options) == 1 {
				names = append(names, f.Name)
			}
		}
		if len(names) > 1 {
			split = append(split, splitRadioFieldset{Legend: SpansText(s.Title), Names: names})
		}
	}
	return split
}

// formatSplitRadioError renders the embedded ask-split-radio-groups.md template.
// upstream: packages/coding-agent/src/prompts/tools/ask-split-radio-groups.md
func formatSplitRadioError(fieldsets []splitRadioFieldset) string {
	tmpl := strings.TrimRight(askSplitRadioGroupsTemplate, "\n")
	const startTag = "{{#each fieldsets}}\n"
	const endTag = "\n{{/each}}"
	sIdx := strings.Index(tmpl, startTag)
	eIdx := strings.Index(tmpl, endTag)
	if sIdx < 0 || eIdx < sIdx {
		return tmpl
	}
	var rows []string
	for _, fs := range fieldsets {
		if fs.Legend != "" {
			rows = append(rows, fmt.Sprintf(`- <fieldset> "%s": %s`, fs.Legend, strings.Join(fs.Names, ", ")))
		} else {
			rows = append(rows, fmt.Sprintf("- An untitled <fieldset>: %s", strings.Join(fs.Names, ", ")))
		}
	}
	return tmpl[:sIdx] + strings.Join(rows, "\n") + tmpl[eIdx+len(endTag):]
}

// disambiguateDisplayLabels appends " (2)", " (3)", ... when labels collide.
// upstream: packages/tui/src/render/render-utils.ts
func disambiguateDisplayLabels(labels []string, reserved []string) []string {
	used := make(map[string]bool, len(reserved)+len(labels))
	for _, r := range reserved {
		used[r] = true
	}
	out := make([]string, len(labels))
	for i, base := range labels {
		cand := base
		if used[cand] {
			for n := 2; ; n++ {
				cand = fmt.Sprintf("%s (%d)", base, n)
				if !used[cand] {
					break
				}
			}
		}
		used[cand] = true
		out[i] = cand
	}
	return out
}

// FormatAskResult renders the embedded ask-result.md template with ordered,
// non-HTML-escaped JSON matching omp's prompt.render(askResultTemplate, ...).
// upstream: packages/coding-agent/src/prompts/tools/ask-result.md
func FormatAskResult(data *FormData, timedOut bool, interrupted bool, notes []OptionNote) string {
	tmpl := strings.TrimRight(askResultTemplate, "\n")
	lines := strings.Split(tmpl, "\n")
	header := renderAskResultHeader(lines[0], interrupted, timedOut)
	out := strings.Replace(header+"\n"+lines[1], "{{json}}", data.MarshalJSONString(true), 1)
	if len(notes) == 0 {
		return out
	}
	notesIdx := strings.Index(tmpl, "{{#if notes}}\n")
	if notesIdx < 0 {
		return out
	}
	notesSection := strings.TrimSuffix(tmpl[notesIdx+len("{{#if notes}}\n"):], "\n{{/if}}")
	eachStart := strings.Index(notesSection, "{{#each notes}}\n")
	eachEnd := strings.Index(notesSection, "\n{{/each}}")
	if eachStart < 0 || eachEnd < eachStart {
		return out
	}
	heading := strings.TrimRight(notesSection[:eachStart], "\n")
	rowTmpl := notesSection[eachStart+len("{{#each notes}}\n") : eachEnd]
	var b strings.Builder
	b.WriteString(out)
	b.WriteByte('\n')
	b.WriteString(heading)
	for _, n := range notes {
		row := strings.NewReplacer(
			"{{question}}", n.Question,
			"{{option}}", n.Option,
			"{{name}}", n.Name,
			"{{jsonStringify value}}", jsonStringNoEscape(n.Value),
			"{{text}}", oneLine(n.Text),
		).Replace(rowTmpl)
		b.WriteByte('\n')
		b.WriteString(row)
	}
	return b.String()
}

func renderAskResultHeader(line string, interrupted, timedOut bool) string {
	const (
		p1 = "{{#if interrupted}}"
		p2 = "{{else}}{{#if timedOut}}"
		p3 = "{{else}}"
		p4 = "{{/if}}{{/if}}"
	)
	i1 := strings.Index(line, p1)
	i2 := strings.Index(line, p2)
	if i1 < 0 || i2 < 0 {
		return line
	}
	i3 := strings.Index(line[i2+len(p2):], p3)
	i4 := strings.Index(line, p4)
	if i3 < 0 || i4 < 0 {
		return line
	}
	i3 += i2 + len(p2)
	if interrupted {
		return line[i1+len(p1) : i2]
	}
	if timedOut {
		return line[i2+len(p2) : i3]
	}
	return line[i3+len(p3) : i4]
}

// resolveAskTimeoutMs returns the dialog timeout in ms, or 0 in plan mode or when disabled.
// upstream: packages/coding-agent/src/tools/ask.ts:541-542
func resolveAskTimeoutMs(ctx sdk.Context, cfg Config) int {
	if gate.PlanActive(ctx) {
		return 0
	}
	if cfg.TimeoutMs > 0 {
		return cfg.TimeoutMs
	}
	if Setting == nil {
		return DefaultAskTimeoutMs
	}
	v := Setting(ctx, "ask.timeout")
	switch n := v.(type) {
	case int:
		if n > 0 {
			return n * 1000
		}
	case float64:
		if n > 0 && !math.IsNaN(n) && !math.IsInf(n, 0) {
			return int(math.Round(n * 1000))
		}
	}
	return DefaultAskTimeoutMs
}

type fieldText struct {
	question string
	context  string
}

func promptTitle(ft fieldText, line string) string {
	if ft.context != "" {
		return ft.context + "\n\n" + line
	}
	return line
}

type choiceAnswer struct {
	checked map[int]bool
	other   string
}

func isTimeoutElapsed(start time.Time, timeoutMs int) bool {
	if timeoutMs <= 0 {
		return false
	}
	elapsed := time.Since(start).Milliseconds()
	return elapsed >= int64(timeoutMs) && elapsed <= int64(timeoutMs+timeoutDetectionToleranceMs)
}

func selectWithTimeout(ctx sdk.Context, title string, options []string, timeoutMs int) (string, bool, bool, error) {
	start := time.Now()
	var val any
	var ok bool
	var err error
	if timeoutMs > 0 {
		val, ok, err = ctx.SelectWithOptions(title, options, sdk.DialogOptions{Timeout: float64(timeoutMs)})
	} else {
		val, ok, err = ctx.Select(title, options)
	}
	if err != nil {
		return "", false, false, err
	}
	if !ok || val == nil {
		return "", false, isTimeoutElapsed(start, timeoutMs), nil
	}
	s, _ := val.(string)
	return s, true, false, nil
}

func inputWithTimeout(ctx sdk.Context, title, placeholder string, timeoutMs int) (string, bool, bool, error) {
	start := time.Now()
	var val any
	var ok bool
	var err error
	if timeoutMs > 0 {
		if placeholder != "" {
			val, ok, err = ctx.InputWithOptions(title, placeholder, sdk.DialogOptions{Timeout: float64(timeoutMs)})
		} else {
			val, ok, err = ctx.InputWithoutPlaceholderWithOptions(title, sdk.DialogOptions{Timeout: float64(timeoutMs)})
		}
	} else {
		if placeholder != "" {
			val, ok, err = ctx.Input(title, placeholder)
		} else {
			val, ok, err = ctx.InputWithoutPlaceholder(title)
		}
	}
	if err != nil {
		return "", false, false, err
	}
	if !ok || val == nil {
		return "", false, isTimeoutElapsed(start, timeoutMs), nil
	}
	s, _ := val.(string)
	return s, true, false, nil
}

func askChoice(ctx sdk.Context, f *FormField, ft fieldText, timeoutMs int) (*choiceAnswer, bool, error) {
	type optEntry struct {
		index int
		label string
	}
	var opts []optEntry
	for i, opt := range f.Options {
		if opt.Disabled {
			continue
		}
		lbl := oneLine(SpansText(opt.Label))
		if lbl == "" {
			lbl = opt.Value
		}
		opts = append(opts, optEntry{index: i, label: lbl})
	}
	rawLabels := make([]string, len(opts))
	for i, o := range opts {
		rawLabels[i] = o.label
	}
	labels := disambiguateDisplayLabels(rawLabels, nil)
	var otherRow string
	choiceRows := append([]string(nil), labels...)
	if f.Other {
		label := otherRowLabel
		if surface.Native() {
			label = nativeOtherRowLabel
		}
		otherRow = disambiguateDisplayLabels([]string{label}, labels)[0]
		choiceRows = append(choiceRows, otherRow)
	}

	if !f.Multi {
		for {
			picked, ok, timedOut, err := selectWithTimeout(ctx, promptTitle(ft, ft.question), choiceRows, timeoutMs)
			if err != nil {
				return nil, false, err
			}
			if timedOut {
				return nil, true, nil
			}
			if !ok {
				return nil, false, nil
			}
			if f.Other && picked == otherRow {
				typed, tok, tTimedOut, terr := inputWithTimeout(ctx, promptTitle(ft, ft.question), "", timeoutMs)
				if terr != nil {
					return nil, false, terr
				}
				if tTimedOut {
					return nil, true, nil
				}
				if !tok || trimJSSpace(typed) == "" {
					continue
				}
				return &choiceAnswer{checked: map[int]bool{}, other: typed}, false, nil
			}
			for i, l := range labels {
				if l == picked {
					return &choiceAnswer{checked: map[int]bool{opts[i].index: true}}, false, nil
				}
			}
			return nil, false, nil
		}
	}

	doneLabel := disambiguateDisplayLabels([]string{doneRowLabel}, choiceRows)[0]
	checked := make(map[int]bool)
	for i, opt := range f.Options {
		if opt.Checked && !opt.Disabled {
			checked[i] = true
		}
	}
	otherText := ""
	hasOther := false

	for {
		answered := len(checked)
		if hasOther {
			answered++
		}
		canFinish := !f.Required || answered > 0
		rows := make([]string, 0, len(choiceRows)+1)
		for i, l := range labels {
			mark := "[ ] "
			if checked[opts[i].index] {
				mark = "[x] "
			}
			rows = append(rows, mark+l)
		}
		if f.Other {
			mark := "[ ] "
			if hasOther {
				mark = "[x] "
			}
			rows = append(rows, mark+otherRow)
		}
		if canFinish {
			rows = append(rows, doneLabel)
		}
		prefix := ""
		if answered > 0 {
			prefix = fmt.Sprintf("(%d selected) ", answered)
		}
		picked, ok, timedOut, err := selectWithTimeout(ctx, promptTitle(ft, prefix+ft.question), rows, timeoutMs)
		if err != nil {
			return nil, false, err
		}
		if timedOut {
			return &choiceAnswer{checked: checked, other: otherText}, true, nil
		}
		if !ok {
			return nil, false, nil
		}
		if picked == doneLabel {
			return &choiceAnswer{checked: checked, other: otherText}, false, nil
		}
		if f.Other && (picked == "[ ] "+otherRow || picked == "[x] "+otherRow) {
			if hasOther {
				hasOther = false
				otherText = ""
				continue
			}
			typed, tok, tTimedOut, terr := inputWithTimeout(ctx, promptTitle(ft, ft.question), "", timeoutMs)
			if terr != nil {
				return nil, false, terr
			}
			if tTimedOut {
				return &choiceAnswer{checked: checked}, true, nil
			}
			if tok && trimJSSpace(typed) != "" {
				hasOther = true
				otherText = typed
			}
			continue
		}
		for i, l := range labels {
			if picked == "[ ] "+l || picked == "[x] "+l {
				idx := opts[i].index
				if checked[idx] {
					delete(checked, idx)
				} else {
					checked[idx] = true
				}
				break
			}
		}
	}
}

func askText(ctx sdk.Context, f *FormField, ft fieldText, timeoutMs int) (string, bool, bool, error) {
	useEditor := f.Multiline || f.Value != ""
	errMsg := ""
	current := f.Value
	for {
		line := ft.question
		if errMsg != "" {
			line = fmt.Sprintf("%s (%s)", ft.question, errMsg)
		}
		title := promptTitle(ft, line)
		var val string
		var ok, timedOut bool
		var err error
		if useEditor {
			var raw any
			if current != "" {
				raw, ok, err = ctx.Editor(title, current)
			} else {
				raw, ok, err = ctx.EditorWithoutPrefill(title)
			}
			if err != nil {
				return "", false, false, err
			}
			if ok && raw != nil {
				val, _ = raw.(string)
			}
		} else {
			val, ok, timedOut, err = inputWithTimeout(ctx, title, f.Placeholder, timeoutMs)
			if err != nil {
				return "", false, false, err
			}
		}
		if timedOut {
			return "", false, true, nil
		}
		if !ok {
			return "", false, false, nil
		}
		if verr := FieldError(f, val, true); verr != "" {
			errMsg = verr
			current = val
			continue
		}
		return val, true, false, nil
	}
}

func askFieldByField(ctx sdk.Context, form *HtmlForm, timeoutMs int) (*FormData, bool, bool, error) {
	data := DefaultFormData(form)
	questions := FieldQuestions(form)
	contexts := FieldContexts(form)
	for _, f := range form.Fields {
		if f.Name == "" {
			continue
		}
		q := questions[f]
		if q == "" {
			q = f.Name
		}
		ft := fieldText{question: q, context: contexts[f]}
		if f.Kind == "choice" {
			ans, timedOut, err := askChoice(ctx, f, ft, timeoutMs)
			if err != nil {
				return nil, false, false, err
			}
			if ans != nil {
				if val, has := ChoiceValue(f, ans.checked, ans.other); has {
					data.Set(f.Name, val)
				} else {
					data.Delete(f.Name)
				}
			}
			if timedOut {
				return data, true, true, nil
			}
			if ans == nil {
				return nil, false, false, nil
			}
			continue
		}
		val, ok, timedOut, err := askText(ctx, f, ft, timeoutMs)
		if err != nil {
			return nil, false, false, err
		}
		if timedOut {
			return data, true, true, nil
		}
		if !ok {
			return nil, false, false, nil
		}
		data.Set(f.Name, TextValue(f, val))
	}
	return data, true, false, nil
}

func executeAsk(ctx sdk.Context, params map[string]any, cfg Config) (any, error) {
	if !ctx.HasUI() {
		if cfg.AdvertiseHeadlessAsk {
			return sdk.ToolResult{
				Content: strings.TrimSpace(askHeadlessForkPrompt),
				Details: AskToolDetails{Answers: []FormAnswer{}},
				IsError: true,
			}, nil
		}
		ctx.Abort()
		return nil, errors.New("Ask tool requires interactive mode")
	}

	rawHTML, ok := RecoverAskHTML(params)
	if !ok {
		return sdk.ToolResult{
			Content: invalidAskError,
			Details: AskToolDetails{Answers: []FormAnswer{}},
			IsError: true,
		}, nil
	}

	form := ParseHtmlForm(rawHTML)
	var named []*FormField
	for _, f := range form.Fields {
		if f.Name != "" {
			named = append(named, f)
		}
	}
	if len(named) == 0 {
		return sdk.ToolResult{
			Content: noNamedControlsError,
			Details: AskToolDetails{Answers: []FormAnswer{}, Form: form},
		}, nil
	}
	if split := splitRadioFieldsets(form); len(split) > 0 {
		return sdk.ToolResult{
			Content: formatSplitRadioError(split),
			Details: AskToolDetails{Answers: []FormAnswer{}, Form: form},
		}, nil
	}

	timeoutMs := resolveAskTimeoutMs(ctx, cfg)
	data, submitted, timedOut, err := askFieldByField(ctx, form, timeoutMs)
	if err != nil {
		return nil, err
	}
	if !submitted {
		ctx.Abort()
		return nil, errors.New("Ask tool was cancelled by the user")
	}

	if timedOut {
		for _, f := range named {
			val, present := data.Get(f.Name)
			if FieldError(f, val, present) != "" {
				return sdk.ToolResult{
					Content: askTimedOutUnanswered,
					Details: AskToolDetails{Answers: []FormAnswer{}, TimedOut: true, Form: form},
				}, nil
			}
		}
	}

	details := AskToolDetails{
		Answers:  FormAnswers(form, data),
		TimedOut: timedOut,
		Form:     form,
	}
	text := FormatAskResult(data, timedOut, false, nil)
	return sdk.ToolResult{
		Content: text,
		Details: details,
	}, nil
}
