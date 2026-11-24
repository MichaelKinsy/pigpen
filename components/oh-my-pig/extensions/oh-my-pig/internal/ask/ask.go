// Package ask ports omp's interactive `ask` tool to PiG's Go SDK.
//
// Upstream: packages/coding-agent/src/tools/ask.ts,
// packages/coding-agent/src/prompts/tools/ask.md,
// packages/tui/src/render/render-utils.ts.
package ask

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

//go:embed ask.md
var askPromptMarkdown string

const (
	// otherOption is the runtime sentinel label appended by omp to every
	// question so the user can enter a custom free-text response.
	// upstream: packages/coding-agent/src/tools/ask.ts:51
	otherOption = "Other (type your own)"

	chatAboutThisOption = "Chat about this"
	nextOption          = "Next →"

	// doneOptionLabel mirrors getDoneOptionLabel() under omp's default unicode
	// symbol preset (`status.success` = "✔").
	// upstream: packages/coding-agent/src/tools/ask.ts:139-141, packages/tui/src/theme/symbols.ts:393
	doneOptionLabel = "✔ Done selecting"

	recommendedSuffix = " (Recommended)"

	// defaultTabWidth matches omp's DEFAULT_TAB_WIDTH used by replaceTabs.
	// upstream: packages/utils/src/tab-spacing.ts:11
	defaultTabWidth = 3

	// errorLineWidth matches omp's TRUNCATE_LENGTHS.LINE for formatErrorValue.
	// upstream: packages/tui/src/render/render-utils.ts:193
	errorLineWidth = 110

	// timeoutDetectionTolerance matches omp's TIMEOUT_DETECTION_TOLERANCE_MS:
	// an `undefined` (ok=false) select reply arriving right around the deadline
	// is attributed to a UI-enforced timeout rather than a user Esc cancel.
	// upstream: packages/coding-agent/src/tools/ask.ts:137, 300-307
	timeoutDetectionTolerance = 1000 * time.Millisecond
)

var reservedOptionLabels = map[string]bool{
	otherOption:         true,
	chatAboutThisOption: true,
	nextOption:          true,
}

// config configures optional runtime overrides for tests.
// Zero values match omp's defaults (no timeout, real monotonic clock).
type config struct {
	// timeout is the per-question dialog timeout (omp's `ask.timeout` in seconds,
	// converted to duration). Zero disables auto-selection timeout, matching omp's
	// default `ask.timeout: 0`.
	// upstream: packages/coding-agent/src/modes/settings.ts:1239-1267
	timeout time.Duration

	// now overrides time.Now for deterministic timeout detection in tests.
	now func() time.Time
}

// Register registers omp's `ask` tool on e.
func Register(e *sdk.Extension) {
	registerWithConfig(e, config{})
}
func registerWithConfig(e *sdk.Extension, cfg config) {
	e.RegisterTool(sdk.ToolDefinition{
		Name:          "ask",
		Label:         "Ask",
		Description:   strings.TrimSpace(askPromptMarkdown),
		PromptSnippet: "Ask the user a clarifying question",
		Parameters:    askSchema(),
		ExecutionMode: "sequential",
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return executeAsk(ctx, params, cfg)
		},
	})
}

// askSchema returns the JSON Schema emitted by omp's arkType definition of askSchema.
// upstream: packages/coding-agent/src/tools/ask.ts:60-83
func askSchema() sdk.Schema {
	optionItem := map[string]any{
		"type":     "object",
		"required": []string{"label"},
		"properties": map[string]any{
			"label":       map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"preview":     map[string]any{"type": "string", "description": "rich preview"},
		},
	}
	questionItem := map[string]any{
		"type":     "object",
		"required": []string{"id", "question", "options"},
		"properties": map[string]any{
			"id":       map[string]any{"type": "string"},
			"question": map[string]any{"type": "string"},
			"header":   map[string]any{"type": "string", "description": "display chip"},
			"options": map[string]any{
				"type":  "array",
				"items": optionItem,
			},
			"multi":       map[string]any{"type": "boolean"},
			"recommended": map[string]any{"type": "number", "description": "0-based default index"},
		},
	}
	return sdk.Schema{
		"type":     "object",
		"required": []string{"questions"},
		"properties": map[string]any{
			"questions": map[string]any{
				"type":     "array",
				"items":    questionItem,
				"minItems": 1,
			},
		},
	}
}

type askOption struct {
	Label       string
	Description string
	Preview     string
}

type askQuestion struct {
	ID             string
	Question       string
	Header         string
	Options        []askOption
	Multi          bool
	Recommended    int
	HasRecommended bool
}

type askParams struct {
	Questions []askQuestion
}

// questionResult matches omp's QuestionResult details shape.
// upstream: packages/tui/src/tools/ask.ts:31-42
type questionResult struct {
	ID              string   `json:"id"`
	Question        string   `json:"question"`
	Options         []string `json:"options"`
	Multi           bool     `json:"multi"`
	SelectedOptions []string `json:"selectedOptions"`
	CustomInput     *string  `json:"customInput,omitempty"`
	Note            *string  `json:"note,omitempty"`
	TimedOut        bool     `json:"timedOut,omitempty"`
}

// askToolDetails matches omp's AskToolDetails shape.
// upstream: packages/tui/src/tools/ask.ts:45-61
type askToolDetails struct {
	Question        string           `json:"question,omitempty"`
	Options         []string         `json:"options,omitempty"`
	Multi           *bool            `json:"multi,omitempty"`
	SelectedOptions []string         `json:"selectedOptions,omitempty"`
	CustomInput     *string          `json:"customInput,omitempty"`
	Note            *string          `json:"note,omitempty"`
	TimedOut        bool             `json:"timedOut,omitempty"`
	Results         []questionResult `json:"results,omitempty"`
	ChatRedirect    bool             `json:"chatRedirect,omitempty"`
	Questions       []string         `json:"questions,omitempty"`
}

// selectionResult holds the outcome of asking one question.
// upstream: packages/coding-agent/src/tools/ask.ts:174-181
type selectionResult struct {
	selectedOptions []string
	customInput     *string
	timedOut        bool
	cancelled       bool
}

type dialogUI interface {
	Select(title string, options []string, timeout time.Duration) (string, bool, error)
	Editor(title string) (string, bool, error)
	Input(title string, timeout time.Duration) (string, bool, error)
}

type sdkDialogUI struct {
	ctx sdk.Context
}

func (u sdkDialogUI) Select(title string, options []string, timeout time.Duration) (string, bool, error) {
	var (
		val any
		ok  bool
		err error
	)
	if timeout > 0 {
		val, ok, err = u.ctx.SelectWithOptions(title, options, sdk.DialogOptions{
			Timeout: float64(timeout) / float64(time.Millisecond),
		})
	} else {
		val, ok, err = u.ctx.Select(title, options)
	}
	if err != nil || !ok {
		return "", false, err
	}
	s, ok := val.(string)
	if !ok {
		return "", false, nil
	}
	return s, true, nil
}

func (u sdkDialogUI) Editor(title string) (string, bool, error) {
	val, ok, err := u.ctx.EditorWithoutPrefill(title)
	if err != nil || !ok {
		return "", false, err
	}
	s, ok := val.(string)
	if !ok {
		return "", false, nil
	}
	return s, true, nil
}

func (u sdkDialogUI) Input(title string, timeout time.Duration) (string, bool, error) {
	var (
		val any
		ok  bool
		err error
	)
	if timeout > 0 {
		val, ok, err = u.ctx.InputWithoutPlaceholderWithOptions(title, sdk.DialogOptions{
			Timeout: float64(timeout) / float64(time.Millisecond),
		})
	} else {
		val, ok, err = u.ctx.InputWithoutPlaceholder(title)
	}
	if err != nil || !ok {
		return "", false, err
	}
	s, ok := val.(string)
	if !ok {
		return "", false, nil
	}
	return s, true, nil
}

func executeAsk(ctx sdk.Context, raw map[string]any, cfg config) (any, error) {
	// Headless fallback: matches omp's check (`!context?.hasUI || !context.ui`).
	// upstream: packages/coding-agent/src/tools/ask.ts:651-654
	if !ctx.HasUI() {
		ctx.Abort()
		return nil, errors.New("Ask tool requires interactive mode")
	}

	params, errText := parseAndSanitizeParams(raw)
	if errText != "" {
		return sdk.ToolResult{
			Content: errText,
			Details: map[string]any{},
		}, nil
	}

	if errText := validateParams(params); errText != "" {
		return sdk.ToolResult{
			Content: errText,
			Details: map[string]any{},
		}, nil
	}

	if len(params.Questions) == 0 {
		return sdk.ToolResult{
			Content: "Error: questions must not be empty",
			Details: map[string]any{},
		}, nil
	}

	nowFn := cfg.now
	if nowFn == nil {
		nowFn = time.Now
	}

	ui := sdkDialogUI{ctx: ctx}

	if len(params.Questions) == 1 {
		q := params.Questions[0]
		res, err := askSingleQuestion(ctx, ui, q, "", cfg.timeout, nowFn)
		if err != nil {
			return nil, err
		}
		if !res.timedOut && (res.cancelled || (len(res.selectedOptions) == 0 && res.customInput == nil)) {
			ctx.Abort()
			return nil, errors.New("Ask tool was cancelled by the user")
		}
		optionLabels := optionLabelsOf(q.Options)
		multi := q.Multi
		details := askToolDetails{
			Question:        q.Question,
			Options:         optionLabels,
			Multi:           &multi,
			SelectedOptions: nonNilSlice(res.selectedOptions),
			CustomInput:     res.customInput,
			TimedOut:        res.timedOut,
		}
		text := formatSingleQuestionResponse(res.selectedOptions, res.customInput, nil, res.timedOut, q.Multi)
		return sdk.ToolResult{Content: text, Details: details}, nil
	}

	results := make([]questionResult, 0, len(params.Questions))
	for i, q := range params.Questions {
		progress := fmt.Sprintf("%d/%d", i+1, len(params.Questions))
		res, err := askSingleQuestion(ctx, ui, q, progress, cfg.timeout, nowFn)
		if err != nil {
			return nil, err
		}
		if res.cancelled && !res.timedOut {
			ctx.Abort()
			return nil, errors.New("Ask tool was cancelled by the user")
		}
		results = append(results, questionResult{
			ID:              q.ID,
			Question:        q.Question,
			Options:         optionLabelsOf(q.Options),
			Multi:           q.Multi,
			SelectedOptions: nonNilSlice(res.selectedOptions),
			CustomInput:     res.customInput,
			TimedOut:        res.timedOut,
		})
	}

	lines := make([]string, len(results))
	for i, r := range results {
		lines[i] = formatQuestionResult(r)
	}
	details := askToolDetails{Results: results}
	text := "User answers:\n" + strings.Join(lines, "\n")
	return sdk.ToolResult{Content: text, Details: details}, nil
}

// askSingleQuestion drives a single question via PiG's blocking UI dialogs,
// mirroring omp's degraded selector path (askSingleQuestion in ask.ts:225-479).
func askSingleQuestion(
	ctx sdk.Context,
	ui dialogUI,
	q askQuestion,
	progressText string,
	timeout time.Duration,
	nowFn func() time.Time,
) (selectionResult, error) {
	promptWithProgress := q.Question
	if progressText != "" {
		promptWithProgress = fmt.Sprintf("%s (%s)", q.Question, progressText)
	}

	// Pure free-text question (no predefined options): open the text editor
	// directly rather than forcing a single-row "Other (type your own)" menu.
	// On timeout with no options, auto-selection is empty (same as omp's
	// getAutoSelectionOnTimeout([]) -> []).
	if len(q.Options) == 0 {
		val, ok, timedOut, err := promptFreeText(ctx, ui, promptWithProgress, timeout, nowFn)
		if err != nil {
			return selectionResult{}, err
		}
		if timedOut {
			return selectionResult{selectedOptions: []string{}, timedOut: true}, nil
		}
		if !ok {
			return selectionResult{selectedOptions: []string{}, cancelled: true}, nil
		}
		return selectionResult{selectedOptions: []string{}, customInput: &val}, nil
	}

	if q.Multi {
		return askMultiQuestion(ctx, ui, q, promptWithProgress, timeout, nowFn)
	}
	return askSingleChoiceQuestion(ctx, ui, q, promptWithProgress, timeout, nowFn)
}

func askSingleChoiceQuestion(
	ctx sdk.Context,
	ui dialogUI,
	q askQuestion,
	promptWithProgress string,
	timeout time.Duration,
	nowFn func() time.Time,
) (selectionResult, error) {
	baseLabels := addRecommendedSuffix(q.Options, q.Recommended, q.HasRecommended)
	// upstream: ask.ts:404-410 — disambiguate badged display labels against
	// OTHER_OPTION and each other so we map the user's pick back by index.
	displayLabels := disambiguateDisplayLabels(baseLabels, []string{otherOption})
	dialogOptions := formatSelectDisplayOptions(q.Options, displayLabels)
	dialogOptions = append(dialogOptions, otherOption)

	for {
		choice, ok, timedOut, err := runSelect(ctx, ui, promptWithProgress, dialogOptions, timeout, nowFn)
		if err != nil {
			return selectionResult{}, err
		}
		if !ok {
			if !timedOut {
				return selectionResult{cancelled: true}, nil
			}
			return selectionResult{
				selectedOptions: autoSelectionOnTimeout(q.Options, q.Recommended, q.HasRecommended),
				timedOut:        true,
			}, nil
		}
		if choice == otherOption {
			if timedOut {
				return selectionResult{
					selectedOptions: autoSelectionOnTimeout(q.Options, q.Recommended, q.HasRecommended),
					timedOut:        true,
				}, nil
			}
			custom, customOK, err := runEditor(ctx, ui, promptWithProgress)
			if err != nil {
				return selectionResult{}, err
			}
			if !customOK {
				// Dismissing the custom editor returns to the option selector.
				// upstream: packages/coding-agent/src/tools/ask.ts:450-452
				continue
			}
			return selectionResult{
				selectedOptions: []string{},
				customInput:     &custom,
			}, nil
		}

		// Map displayed choice back to the offered option by index/identity so
		// labels intrinsically ending with " (Recommended)" are preserved intact.
		// upstream: packages/coding-agent/src/tools/ask.ts:457-464
		picked := mapDisplayChoiceToLabel(choice, dialogOptions[:len(q.Options)], displayLabels, q.Options)
		return selectionResult{
			selectedOptions: []string{picked},
			timedOut:        timedOut,
		}, nil
	}
}

func askMultiQuestion(
	ctx sdk.Context,
	ui dialogUI,
	q askQuestion,
	promptWithProgress string,
	timeout time.Duration,
	nowFn func() time.Time,
) (selectionResult, error) {
	selectedSet := make(map[string]bool, len(q.Options))
	var selectedOrder []string

	for {
		dialogOptions := make([]string, 0, len(q.Options)+2)
		optionDisplay := make([]string, len(q.Options))
		for i, opt := range q.Options {
			marker := "[ ] "
			if selectedSet[opt.Label] {
				marker = "[x] "
			}
			disp := marker + formatOptionDisplay(opt.Label, opt.Description)
			optionDisplay[i] = disp
			dialogOptions = append(dialogOptions, disp)
		}
		if len(selectedSet) > 0 {
			dialogOptions = append(dialogOptions, doneOptionLabel)
		}
		dialogOptions = append(dialogOptions, otherOption)

		prefix := ""
		if len(selectedSet) > 0 {
			prefix = fmt.Sprintf("(%d selected) ", len(selectedSet))
		}
		prompt := prefix + promptWithProgress

		choice, ok, timedOut, err := runSelect(ctx, ui, prompt, dialogOptions, timeout, nowFn)
		if err != nil {
			return selectionResult{}, err
		}
		if !ok {
			if timedOut {
				selected := selectedOrder
				if len(selected) == 0 {
					selected = autoSelectionOnTimeout(q.Options, q.Recommended, q.HasRecommended)
				}
				return selectionResult{selectedOptions: selected, timedOut: true}, nil
			}
			return selectionResult{selectedOptions: selectedOrder, cancelled: true}, nil
		}
		if choice == doneOptionLabel {
			return selectionResult{selectedOptions: selectedOrder, timedOut: timedOut}, nil
		}
		if choice == otherOption {
			if timedOut {
				selected := selectedOrder
				if len(selected) == 0 {
					selected = autoSelectionOnTimeout(q.Options, q.Recommended, q.HasRecommended)
				}
				return selectionResult{selectedOptions: selected, timedOut: true}, nil
			}
			custom, customOK, err := runEditor(ctx, ui, prompt)
			if err != nil {
				return selectionResult{}, err
			}
			if !customOK {
				// Dismissing custom input returns to the multi-select list with
				// existing ticks intact. upstream: ask.ts:377-379
				continue
			}
			return selectionResult{
				selectedOptions: selectedOrder,
				customInput:     &custom,
			}, nil
		}

		// Resolve toggled option by display row or bare label (fake hosts and
		// RPC clients may echo either the formatted row or the raw label).
		label, matched := resolveMultiChoice(choice, optionDisplay, q.Options)
		if matched {
			if selectedSet[label] {
				delete(selectedSet, label)
				selectedOrder = removeString(selectedOrder, label)
			} else {
				selectedSet[label] = true
				selectedOrder = append(selectedOrder, label)
			}
		}

		if timedOut {
			selected := selectedOrder
			if len(selected) == 0 {
				selected = autoSelectionOnTimeout(q.Options, q.Recommended, q.HasRecommended)
			}
			return selectionResult{selectedOptions: selected, timedOut: true}, nil
		}
	}
}

func runSelect(
	ctx sdk.Context,
	ui dialogUI,
	prompt string,
	options []string,
	timeout time.Duration,
	nowFn func() time.Time,
) (choice string, ok bool, timedOut bool, err error) {
	if isAborted(ctx) {
		return "", false, false, errors.New("Ask input was cancelled")
	}
	started := nowFn()
	choice, ok, err = ui.Select(prompt, options, timeout)
	if err != nil {
		if isAbortErr(ctx, err) {
			return "", false, false, errors.New("Ask input was cancelled")
		}
		return "", false, false, err
	}
	if !ok && timeout > 0 {
		// PiG's SDK `SelectWithOptions` forwards `timeout` to the host but has no
		// `onTimeout` callback. Mirror omp's deadline-window detection
		// (ask.ts:300-307) so a UI-enforced timeout auto-selects while an early
		// Esc or a cancel well past the deadline remains a user cancel.
		elapsed := nowFn().Sub(started)
		if elapsed >= timeout && elapsed <= timeout+timeoutDetectionTolerance {
			timedOut = true
		}
	}
	return choice, ok, timedOut, nil
}

func runEditor(ctx sdk.Context, ui dialogUI, title string) (string, bool, error) {
	if isAborted(ctx) {
		return "", false, errors.New("Ask input was cancelled")
	}
	text, ok, err := ui.Editor(title)
	if err != nil {
		if isAbortErr(ctx, err) {
			return "", false, errors.New("Ask input was cancelled")
		}
		return "", false, err
	}
	return text, ok, nil
}

func promptFreeText(
	ctx sdk.Context,
	ui dialogUI,
	title string,
	timeout time.Duration,
	nowFn func() time.Time,
) (string, bool, bool, error) {
	if isAborted(ctx) {
		return "", false, false, errors.New("Ask input was cancelled")
	}
	started := nowFn()
	if timeout > 0 {
		text, ok, err := ui.Input(title, timeout)
		if err != nil {
			if isAbortErr(ctx, err) {
				return "", false, false, errors.New("Ask input was cancelled")
			}
			return "", false, false, err
		}
		if !ok {
			elapsed := nowFn().Sub(started)
			if elapsed >= timeout && elapsed <= timeout+timeoutDetectionTolerance {
				return "", false, true, nil
			}
			return "", false, false, nil
		}
		return text, true, false, nil
	}
	text, ok, err := ui.Editor(title)
	if err != nil {
		if isAbortErr(ctx, err) {
			return "", false, false, errors.New("Ask input was cancelled")
		}
		return "", false, false, err
	}
	return text, ok, false, nil
}

func isAborted(ctx sdk.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	if sig := ctx.Signal(); sig != nil && sig.Err() != nil {
		return true
	}
	return false
}

func isAbortErr(ctx sdk.Context, err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return isAborted(ctx)
}

// parseAndSanitizeParams decodes raw tool arguments and strips `\r` runs
// before validation and rendering.
// upstream: packages/coding-agent/src/tools/ask.ts:1010-1026
func parseAndSanitizeParams(raw map[string]any) (askParams, string) {
	if raw == nil {
		return askParams{}, "Error: questions must not be empty"
	}
	rawQuestions, ok := raw["questions"].([]any)
	if !ok {
		return askParams{}, "Error: questions must not be empty"
	}
	questions := make([]askQuestion, 0, len(rawQuestions))
	for _, entry := range rawQuestions {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		q := askQuestion{}
		if id, ok := m["id"].(string); ok {
			q.ID = sanitizeCarriageReturns(id)
		}
		if text, ok := m["question"].(string); ok {
			q.Question = sanitizeCarriageReturns(text)
		}
		if header, ok := m["header"].(string); ok {
			q.Header = sanitizeCarriageReturns(header)
		}
		if multi, ok := m["multi"].(bool); ok {
			q.Multi = multi
		}
		if rec, ok := asIntIndex(m["recommended"]); ok {
			q.Recommended = rec
			q.HasRecommended = true
		}
		if rawOpts, ok := m["options"].([]any); ok {
			q.Options = make([]askOption, 0, len(rawOpts))
			for _, rawOpt := range rawOpts {
				om, ok := rawOpt.(map[string]any)
				if !ok {
					continue
				}
				opt := askOption{}
				if label, ok := om["label"].(string); ok {
					opt.Label = sanitizeCarriageReturns(label)
				}
				if desc, ok := om["description"].(string); ok {
					opt.Description = strings.TrimSpace(sanitizeCarriageReturns(desc))
				}
				if preview, ok := om["preview"].(string); ok {
					opt.Preview = sanitizeCarriageReturns(preview)
				}
				q.Options = append(q.Options, opt)
			}
		}
		questions = append(questions, q)
	}
	return askParams{Questions: questions}, ""
}

func asIntIndex(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

// validateParams enforces omp's runtime validation guards after sanitization:
// reserved labels, multi-select Done collision, unique question IDs, and
// unique option labels within a question.
// upstream: packages/coding-agent/src/tools/ask.ts:663-732
func validateParams(params askParams) string {
	for _, q := range params.Questions {
		for _, opt := range q.Options {
			if reservedOptionLabels[opt.Label] {
				return fmt.Sprintf("Error: option labels must not collide with reserved runtime labels: %s", formatErrorValue(opt.Label))
			}
		}
	}
	for _, q := range params.Questions {
		if !q.Multi {
			continue
		}
		for _, opt := range q.Options {
			if opt.Label == doneOptionLabel {
				return fmt.Sprintf("Error: option labels must not collide with reserved runtime labels: %s", formatErrorValue(opt.Label))
			}
		}
	}
	seenIDs := make(map[string]bool, len(params.Questions))
	for _, q := range params.Questions {
		if seenIDs[q.ID] {
			return fmt.Sprintf("Error: question ids must be unique: %s", formatErrorValue(q.ID))
		}
		seenIDs[q.ID] = true

		seenLabels := make(map[string]bool, len(q.Options))
		for _, opt := range q.Options {
			if seenLabels[opt.Label] {
				return fmt.Sprintf("Error: option labels must be unique within a question: %s", formatErrorValue(opt.Label))
			}
			seenLabels[opt.Label] = true
		}
	}
	return ""
}

// sanitizeCarriageReturns normalizes CRLF to LF and collapses stray CR runs
// into a single space.
// upstream: packages/tui/src/render/render-utils.ts:35-38
func sanitizeCarriageReturns(text string) string {
	if !strings.ContainsRune(text, '\r') {
		return text
	}
	s := strings.ReplaceAll(text, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	inCR := false
	for i := range len(s) {
		if s[i] == '\r' {
			if !inCR {
				b.WriteByte(' ')
				inCR = true
			}
			continue
		}
		inCR = false
		b.WriteByte(s[i])
	}
	return b.String()
}

// disambiguateDisplayLabels returns unique, action-safe display labels while
// preserving slice order and indices.
// upstream: packages/tui/src/render/render-utils.ts:50-59
func disambiguateDisplayLabels(rawLabels []string, reserved []string) []string {
	taken := make(map[string]bool, len(reserved)+len(rawLabels))
	for _, r := range reserved {
		taken[r] = true
	}
	out := make([]string, len(rawLabels))
	for i, raw := range rawLabels {
		base := sanitizeCarriageReturns(raw)
		candidate := base
		for suffix := 2; taken[candidate]; suffix++ {
			candidate = fmt.Sprintf("%s (%d)", base, suffix)
		}
		taken[candidate] = true
		out[i] = candidate
	}
	return out
}

// addRecommendedSuffix appends " (Recommended)" to the recommended option
// unless its label already ends with that suffix.
// upstream: packages/coding-agent/src/tools/ask.ts:144-155
func addRecommendedSuffix(options []askOption, recommended int, hasRecommended bool) []string {
	labels := make([]string, len(options))
	for i, opt := range options {
		label := opt.Label
		if hasRecommended && i == recommended && recommended >= 0 && recommended < len(options) && !strings.HasSuffix(label, recommendedSuffix) {
			label += recommendedSuffix
		}
		labels[i] = label
	}
	return labels
}

// autoSelectionOnTimeout picks the recommended option if in range, otherwise
// the first option, or empty if no options exist.
// upstream: packages/coding-agent/src/tools/ask.ts:157-163
func autoSelectionOnTimeout(options []askOption, recommended int, hasRecommended bool) []string {
	if len(options) == 0 {
		return []string{}
	}
	if hasRecommended && recommended >= 0 && recommended < len(options) {
		return []string{options[recommended].Label}
	}
	return []string{options[0].Label}
}

func stripRecommendedSuffix(label string) string {
	return strings.TrimSuffix(label, recommendedSuffix)
}

func formatOptionDisplay(label, description string) string {
	if description == "" {
		return label
	}
	return label + " — " + description
}

func formatSelectDisplayOptions(options []askOption, displayLabels []string) []string {
	out := make([]string, len(options))
	for i, opt := range options {
		out[i] = formatOptionDisplay(displayLabels[i], opt.Description)
	}
	return out
}

func mapDisplayChoiceToLabel(choice string, formattedRows []string, displayLabels []string, options []askOption) string {
	for i, row := range formattedRows {
		if row == choice {
			return options[i].Label
		}
	}
	for i, lbl := range displayLabels {
		if lbl == choice {
			return options[i].Label
		}
	}
	for _, opt := range options {
		if opt.Label == choice {
			return opt.Label
		}
	}
	return stripRecommendedSuffix(choice)
}

func resolveMultiChoice(choice string, optionDisplay []string, options []askOption) (string, bool) {
	for i, row := range optionDisplay {
		if row == choice {
			return options[i].Label, true
		}
	}
	for _, opt := range options {
		if opt.Label == choice {
			return opt.Label, true
		}
	}
	return "", false
}

// formatErrorValue expands tabs and clamps display width to TRUNCATE_LENGTHS.LINE.
// upstream: packages/coding-agent/src/tools/ask.ts:1006-1008
func formatErrorValue(value string) string {
	expanded := strings.ReplaceAll(value, "\t", strings.Repeat(" ", defaultTabWidth))
	return tui.TruncateToWidth(expanded, errorLineWidth, "…", false)
}

// formatQuestionResult formats one answer line in a multi-question response.
// upstream: packages/coding-agent/src/tools/ask.ts:481-492
func formatQuestionResult(result questionResult) string {
	noteSuffix := ""
	if result.Note != nil && *result.Note != "" {
		noteSuffix = fmt.Sprintf(" (note: %s)", *result.Note)
	}
	var custom string
	hasCustom := result.CustomInput != nil
	if hasCustom {
		// upstream uses literal `"${result.customInput}"` without Go escaping.
		custom = `"` + *result.CustomInput + `"`
	}
	if len(result.SelectedOptions) == 0 {
		if hasCustom {
			return fmt.Sprintf("%s: %s%s", result.ID, custom, noteSuffix)
		}
		if result.Multi {
			return fmt.Sprintf("%s: []%s", result.ID, noteSuffix)
		}
		return fmt.Sprintf("%s: (cancelled)%s", result.ID, noteSuffix)
	}
	var picked string
	if result.Multi {
		picked = "[" + strings.Join(result.SelectedOptions, ", ") + "]"
	} else {
		picked = result.SelectedOptions[0]
	}
	answer := picked
	if hasCustom {
		answer = picked + " + " + custom
	}
	timeoutSuffix := ""
	if result.TimedOut {
		timeoutSuffix = " (auto-selected after timeout)"
	}
	return fmt.Sprintf("%s: %s%s%s", result.ID, answer, timeoutSuffix, noteSuffix)
}

// formatSingleQuestionResponse formats the model-facing answer text for a
// single-question ask call.
// upstream: packages/coding-agent/src/tools/ask.ts:494-530
func formatSingleQuestionResponse(
	selectedOptions []string,
	customInput *string,
	note *string,
	timedOut bool,
	multi bool,
) string {
	var parts []string
	if len(selectedOptions) > 0 {
		var selectedText string
		if multi {
			selectedText = "User selected: " + strings.Join(selectedOptions, ", ")
		} else {
			selectedText = "User selected: " + selectedOptions[0]
		}
		if timedOut {
			selectedText += " (auto-selected after timeout)"
		}
		parts = append(parts, selectedText)
	}
	if customInput != nil {
		if strings.Contains(*customInput, "\n") {
			lines := strings.Split(*customInput, "\n")
			for i, line := range lines {
				lines[i] = "  " + line
			}
			parts = append(parts, "User provided custom input:\n"+strings.Join(lines, "\n"))
		} else {
			parts = append(parts, "User provided custom input: "+*customInput)
		}
	}
	if note != nil && *note != "" {
		if strings.Contains(*note, "\n") {
			lines := strings.Split(*note, "\n")
			for i, line := range lines {
				lines[i] = "  " + line
			}
			parts = append(parts, "User added note:\n"+strings.Join(lines, "\n"))
		} else {
			parts = append(parts, "User added note: "+*note)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	if multi {
		return "User did not select any options"
	}
	return "User cancelled the selection"
}

func optionLabelsOf(options []askOption) []string {
	labels := make([]string, len(options))
	for i, opt := range options {
		labels[i] = opt.Label
	}
	return labels
}

func nonNilSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func removeString(list []string, target string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}
