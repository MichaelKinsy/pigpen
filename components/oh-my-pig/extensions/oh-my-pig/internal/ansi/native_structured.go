package ansi

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/agents"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
)

// Native views for structured tools (`todo`, `ask`, `task`/`subagent`, `wait`).
//
// upstream:
//   packages/tui/src/tools/todo.ts (describeCall, describeResult, todoChecklistPhases)
//   packages/tui/src/tools/ask.ts (describeAskCall, describeAskResult, describeAnswer)
//   packages/tui/src/native/ask-card.ts (askCard, openQuestion, foldQuestion, foldPills, pillRow)
//   packages/tui/src/tools/task.ts (describeTaskCall, describeTaskResult, describeAgents, describeProgressAgent, describeResultAgent)
//   packages/tui/src/tools/wait.ts (describeCall, describeResult, describeJobsResult)
//   packages/tui/src/tools/job-widget.ts (describeJobTiles, describeJobTile, tileVisual, tileNotes)

func init() {
	registerNative("todo", Builder{Call: nativeTodoCall, Result: nativeTodoResult})
	registerNative("ask", Builder{Call: nativeAskCall, Result: nativeAskResult})
	registerNative("task", Builder{Call: nativeTaskCall, Result: nativeTaskResult})
	registerNative("subagent", Builder{Call: nativeTaskCall, Result: nativeTaskResult})
	registerNative("wait", Builder{Call: nativeWaitCall, Result: nativeWaitResult})
}

// ── todo (tools/todo.ts) ─────────────────────────────────────────────────────

func nvIsClosedTodo(status string) bool {
	return status == "completed" || status == "abandoned"
}

func nvTodoCheckbox(status string) string {
	switch status {
	case "completed":
		return "☑"
	case "in_progress":
		return "◐"
	case "abandoned":
		return "☒"
	default:
		return "☐"
	}
}

func nvRomanNumeral(n int) string {
	if n <= 0 {
		return ""
	}
	vals := []struct {
		v int
		s string
	}{
		{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
		{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
		{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
	}
	var b strings.Builder
	for _, pair := range vals {
		for n >= pair.v {
			b.WriteString(pair.s)
			n -= pair.v
		}
	}
	return b.String()
}

func nativeTodoCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	// upstream: tools/todo.ts:792-795 — head only (`Todo <ops>`), no body.
	return sc.container(sc.claim("omp.tool.todo.call")), nil
}

// nvTodoChecklistStatus maps omp's todo status to Tern's checklist item status.
// upstream: packages/tui/src/tools/todo.ts:533-538
func nvTodoChecklistStatus(status string) string {
	switch status {
	case "completed":
		return "done"
	case "in_progress":
		return "active"
	case "abandoned":
		return "dropped"
	default:
		return "pending"
	}
}

// nativeTodoResult ports describeTodoResult in tools/todo.ts:569-595.
// Emits omp.todo.checklist > omp.todo.phase#<p> > omp.todo.phase.head#<p> +
// omp.todo.item#<p>.<t> (with status encoded in omp.todo.item.status#<p>.<t>
// or checkbox prefix) so pig-tern/frontend builds one native Tern `checklist`
// node (`mode:"full"`, per-phase `collapsed`, and head target `"closed/total"`).
func nativeTodoResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	if rc.IsError {
		msg := strings.TrimSpace(nvResultText(res))
		if msg == "" {
			msg = "Todo operation failed"
		}
		return sc.container(sc.claim("omp.tool.todo"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}

	details := nvToMap(res.Details)
	rawPhases := nvSlice(details, "phases")

	type todoPhase struct {
		name  string
		tasks []map[string]any
	}
	var phases []todoPhase
	total := 0
	for _, raw := range rawPhases {
		pm := nvToMap(raw)
		if pm == nil {
			continue
		}
		tasksRaw := nvSlice(pm, "tasks")
		if len(tasksRaw) == 0 {
			continue
		}
		var tasks []map[string]any
		for _, tr := range tasksRaw {
			if tm := nvToMap(tr); tm != nil {
				tasks = append(tasks, tm)
			}
		}
		if len(tasks) > 0 {
			phases = append(phases, todoPhase{name: nvStr(pm, "name"), tasks: tasks})
			total += len(tasks)
		}
	}

	if total == 0 {
		msg := strings.TrimSpace(nvPlainText(nvResultText(res)))
		if msg == "" {
			msg = "No todos"
		}
		return sc.container(sc.claim("omp.tool.todo"), sc.text(sc.claim("omp.tool.notice"), msg)), nil
	}

	var phaseNodes []kit.Node
	for pIdx, phase := range phases {
		if !sc.canAdd(4) {
			phaseNodes = append(phaseNodes, sc.text(sc.claim("omp.tool.stats"), fmt.Sprintf("+%d more phases", len(phases)-pIdx)))
			break
		}
		var pChildren []kit.Node
		// Plain phase title without Roman numerals (upstream: tools/todo.ts:547-557).
		if title := nvPlainLine(phase.name); title != "" {
			pChildren = append(pChildren, sc.text(sc.indexed("omp.todo.phase.head", pIdx), title))
		}
		// Always emit every item so the frontend computes accurate closed/total
		// and per-phase `collapsed` state for the native Tern `checklist` node
		// (upstream: tools/todo.ts:550-560, 580-594).
		shown := 0
		for tIdx, tm := range phase.tasks {
			if !sc.canAdd(3) {
				break
			}
			status := nvStr(tm, "status")
			content := nvPlainLine(nvStr(tm, "content"))
			pChildren = append(pChildren,
				sc.text(
					sc.subIndexed("omp.todo.item", pIdx, tIdx),
					fmt.Sprintf("%s %s", nvTodoCheckbox(status), content),
				),
				sc.text(
					sc.subIndexed("omp.todo.item.status", pIdx, tIdx),
					nvTodoChecklistStatus(status),
				),
			)
			shown++
		}
		if hidden := len(phase.tasks) - shown; hidden > 0 {
			pChildren = append(pChildren, sc.text(sc.indexed("omp.todo.more", pIdx), fmt.Sprintf("… %s", nvFormatCount("more todo", hidden))))
		}
		phaseNodes = append(phaseNodes, sc.vstack(sc.indexed("omp.todo.phase", pIdx), 0, pChildren...))
	}

	checklist := sc.vstack(sc.claim("omp.todo.checklist"), 0, phaseNodes...)
	return sc.container(sc.claim("omp.tool.todo"), checklist), nil
}

// ── ask (tools/ask.ts + native/ask-card.ts) ──────────────────────────────────

const (
	nvAskTimedOutNote   = "auto-selected after timeout — not a user choice"
	nvAskNoAnswer       = "(no answer)"
	nvAskPillLimit      = 4
	nvAskPillLabelWidth = 20
)

var (
	nvAskTagRE      = regexp.MustCompile(`<[^>]+>`)
	nvAskFieldsetRE = regexp.MustCompile(`(?is)<fieldset\b[^>]*>(.*?)</fieldset>`)
	nvAskLegendRE   = regexp.MustCompile(`(?is)<legend\b[^>]*>(.*?)</legend>`)
	nvAskLabelRE    = regexp.MustCompile(`(?is)<label\b[^>]*>(.*?)</label>`)
	nvAskInputRE    = regexp.MustCompile(`(?is)<(input|select|textarea)\b([^>]*)>`)
	nvAskSelectRE   = regexp.MustCompile(`(?is)<select\b([^>]*)>(.*?)</select>`)
	nvAskOptionRE   = regexp.MustCompile(`(?is)<option\b([^>]*)>(.*?)</option>`)
	nvAskAttrRE     = regexp.MustCompile(`(?i)\b([a-z0-9_-]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+)))?`)
)

type nvAskFormField struct {
	name        string
	kind        string // "choice" | "text" | "password"
	question    string
	placeholder string
	options     []string
	selected    map[string]bool
	errorMsg    string
}

func nvParseHTMLAttrs(raw string) map[string]string {
	attrs := map[string]string{}
	for _, m := range nvAskAttrRE.FindAllStringSubmatch(raw, -1) {
		k := strings.ToLower(m[1])
		v := m[2]
		if v == "" {
			v = m[3]
		}
		if v == "" {
			v = m[4]
		}
		attrs[k] = v
	}
	return attrs
}

func nvStripHTMLText(raw string) string {
	return nvPlainLine(nvAskTagRE.ReplaceAllString(raw, " "))
}

// nvParseAskFormFields extracts form fields from either details.form (written by
// internal/ask/ask.go) or args.html so ask CallView/ResultView can render the
// complete omp.ask.card tree from native/ask-card.ts.
func nvParseAskFormFields(formMap map[string]any, htmlStr string) []nvAskFormField {
	if formMap != nil {
		if rawFields := nvSlice(formMap, "fields"); len(rawFields) > 0 {
			var out []nvAskFormField
			for _, rf := range rawFields {
				fm := nvToMap(rf)
				if fm == nil {
					continue
				}
				name := nvStr(fm, "name")
				if name == "" {
					continue
				}
				kind := nvStr(fm, "kind")
				typ := strings.ToLower(nvStr(fm, "type"))
				if typ == "password" {
					kind = "password"
				}
				q := nvPlainLine(nvStr(fm, "question", "label", "legend"))
				if q == "" {
					q = name
				}
				f := nvAskFormField{
					name:        name,
					kind:        kind,
					question:    q,
					placeholder: nvPlainLine(nvStr(fm, "placeholder")),
					selected:    map[string]bool{},
					errorMsg:    nvPlainLine(nvStr(fm, "error")),
				}
				for _, ro := range nvSlice(fm, "options") {
					if om := nvToMap(ro); om != nil {
						lbl := nvPlainLine(nvStr(om, "label", "text", "value"))
						if lbl != "" {
							f.options = append(f.options, lbl)
							if nvBool(om, "selected", "checked") {
								f.selected[lbl] = true
							}
						}
					} else if s, ok := ro.(string); ok && strings.TrimSpace(s) != "" {
						f.options = append(f.options, nvPlainLine(s))
					}
				}
				if len(f.options) > 0 && f.kind == "" {
					f.kind = "choice"
				}
				out = append(out, f)
			}
			if len(out) > 0 {
				return out
			}
		}
	}

	if strings.TrimSpace(htmlStr) == "" {
		return nil
	}

	// Lightweight extraction from <fieldset>/<label>/<input>/<select>/<textarea>.
	byName := map[string]*nvAskFormField{}
	var order []string
	ensure := func(name, question, kind string) *nvAskFormField {
		if f, ok := byName[name]; ok {
			if f.question == "" && question != "" {
				f.question = question
			}
			return f
		}
		if question == "" {
			question = name
		}
		f := &nvAskFormField{name: name, question: question, kind: kind, selected: map[string]bool{}}
		byName[name] = f
		order = append(order, name)
		return f
	}

	// Scan fieldsets first so legends supply question titles for radio/checkbox groups.
	for _, fm := range nvAskFieldsetRE.FindAllStringSubmatch(htmlStr, -1) {
		body := fm[1]
		legend := ""
		if lm := nvAskLegendRE.FindStringSubmatch(body); lm != nil {
			legend = nvStripHTMLText(lm[1])
		}
		for _, im := range nvAskInputRE.FindAllStringSubmatch(body, -1) {
			tag := strings.ToLower(im[1])
			attrs := nvParseHTMLAttrs(im[2])
			name := attrs["name"]
			if name == "" {
				continue
			}
			typ := strings.ToLower(attrs["type"])
			switch {
			case tag == "input" && (typ == "radio" || typ == "checkbox"):
				f := ensure(name, legend, "choice")
				opt := nvPlainLine(attrs["value"])
				if opt != "" {
					f.options = append(f.options, opt)
					if _, checked := attrs["checked"]; checked {
						f.selected[opt] = true
					}
				}
			case tag == "input" && typ == "password":
				f := ensure(name, legend, "password")
				f.placeholder = nvPlainLine(attrs["placeholder"])
			default:
				f := ensure(name, legend, "text")
				f.placeholder = nvPlainLine(attrs["placeholder"])
			}
		}
	}

	// Scope <option> parsing to each <select> element by name so multiple selects
	// in one form never share options.
	selectBodies := map[string]string{}
	for _, sm := range nvAskSelectRE.FindAllStringSubmatch(htmlStr, -1) {
		sa := nvParseHTMLAttrs(sm[1])
		if name := sa["name"]; name != "" {
			selectBodies[name] = sm[2]
		}
	}

	// Also pick up standalone inputs/selects/textareas outside fieldsets.
	labels := nvAskLabelRE.FindAllStringSubmatch(htmlStr, -1)
	labelIdx := 0
	for _, im := range nvAskInputRE.FindAllStringSubmatch(htmlStr, -1) {
		tag := strings.ToLower(im[1])
		attrs := nvParseHTMLAttrs(im[2])
		name := attrs["name"]
		if name == "" {
			continue
		}
		if _, exists := byName[name]; exists {
			continue
		}
		q := ""
		if labelIdx < len(labels) {
			q = nvStripHTMLText(labels[labelIdx][1])
			labelIdx++
		}
		typ := strings.ToLower(attrs["type"])
		kind := "text"
		if typ == "password" {
			kind = "password"
		} else if tag == "select" || typ == "radio" || typ == "checkbox" {
			kind = "choice"
		}
		f := ensure(name, q, kind)
		f.placeholder = nvPlainLine(attrs["placeholder"])
		if tag == "select" {
			if body, ok := selectBodies[name]; ok {
				for _, om := range nvAskOptionRE.FindAllStringSubmatch(body, -1) {
					lbl := nvStripHTMLText(om[2])
					if lbl != "" {
						f.options = append(f.options, lbl)
						oa := nvParseHTMLAttrs(om[1])
						if _, sel := oa["selected"]; sel {
							f.selected[lbl] = true
						}
					}
				}
			}
		}
	}
	out := make([]nvAskFormField, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out
}

// nvBuildAskFormCard builds omp.ask.card with all roles from native/ask-card.ts:
// omp.ask.card > omp.ask.card.head (omp.ask.card.tag, omp.ask.card.progress),
// omp.ask.card.body (omp.ask.open / omp.ask.fold > omp.ask.fold.head
// (omp.ask.fold.mark, omp.ask.fold.question), omp.ask.pills (omp.ask.pill,
// omp.ask.pill.more, omp.ask.fold.note) | omp.ask.fold.answer | omp.ask.fold.well,
// omp.ask.error), optional omp.ask.skip.
// upstream: packages/tui/src/native/ask-card.ts:72-227
func nvBuildAskFormCard(sc *nvScope, fields []nvAskFormField, answersByName map[string]string, notesByName map[string]string, skipMsg string) kit.Node {
	answeredCount := 0
	for _, f := range fields {
		if strings.TrimSpace(answersByName[f.name]) != "" || len(f.selected) > 0 {
			answeredCount++
		}
	}

	headChildren := []kit.Node{
		sc.text(sc.claim("omp.ask.card.tag"), "ask"),
	}
	if len(fields) > 0 {
		headChildren = append(headChildren, sc.text(
			sc.claim("omp.ask.card.progress"),
			fmt.Sprintf("%d of %d answered", answeredCount, len(fields)),
		))
	}
	head := sc.hstack(sc.claim("omp.ask.card.head"), 1, headChildren...)

	var bodyChildren []kit.Node
	for i, f := range fields {
		if !sc.canAdd(10) {
			break
		}
		ans := answersByName[f.name]
		if f.kind == "password" && ans != "" {
			n := min(len([]rune(ans)), 12)
			ans = strings.Repeat("•", n)
		}
		answered := strings.TrimSpace(ans) != "" || len(f.selected) > 0
		mark := "○"
		if f.errorMsg != "" {
			mark = "!"
		} else if answered {
			mark = "✓"
		}

		foldHead := sc.hstack(sc.indexed("omp.ask.fold.head", i), 1,
			sc.text(sc.indexed("omp.ask.fold.mark", i), mark),
			sc.text(sc.indexed("omp.ask.fold.question", i), f.question),
		)

		var summaryNode kit.Node
		note := strings.TrimSpace(notesByName[f.name])
		switch {
		case len(f.options) > 0:
			// Folded pills: chosen options always kept, up to nvAskPillLimit, then +N.
			// upstream: native/ask-card.ts:168-222
			chosenSet := map[string]bool{}
			for k := range f.selected {
				chosenSet[k] = true
			}
			if ans != "" {
				for part := range strings.SplitSeq(ans, ",") {
					if t := strings.TrimSpace(part); t != "" {
						chosenSet[t] = true
					}
				}
			}
			chosenCount := 0
			for _, opt := range f.options {
				if chosenSet[opt] {
					chosenCount++
				}
			}
			room := max(0, nvAskPillLimit-chosenCount)
			var shown []string
			hasChosen := false
			for _, opt := range f.options {
				if len(shown) >= nvAskPillLimit {
					break
				}
				if chosenSet[opt] {
					shown = append(shown, opt)
					hasChosen = true
				} else if room > 0 {
					shown = append(shown, opt)
					room--
				}
			}
			var pillNodes []kit.Node
			for pIdx, opt := range shown {
				cut := nvTruncateRunes(opt, nvAskPillLabelWidth)
				pillNodes = append(pillNodes, sc.text(sc.subIndexed("omp.ask.pill", i, pIdx), cut))
			}
			if hidden := len(f.options) - len(shown); hidden > 0 {
				pillNodes = append(pillNodes, sc.text(sc.indexed("omp.ask.pill.more", i), fmt.Sprintf("+%d", hidden)))
			}
			if hasChosen && note != "" {
				pillNodes = append(pillNodes, sc.text(sc.indexed("omp.ask.fold.note", i), note))
			}
			summaryNode = sc.hstack(sc.indexed("omp.ask.pills", i), 1, pillNodes...)

		case strings.TrimSpace(ans) != "":
			ansNode := sc.text(sc.indexed("omp.ask.fold.answer", i), nvPlainLine(ans))
			if note != "" {
				summaryNode = sc.hstack(sc.indexed("omp.ask.pills", i), 1,
					ansNode,
					sc.text(sc.indexed("omp.ask.fold.note", i), note),
				)
			} else {
				summaryNode = ansNode
			}

		default:
			ph := f.placeholder
			if ph == "" {
				ph = " "
			}
			summaryNode = sc.text(sc.indexed("omp.ask.fold.well", i), ph)
		}

		foldChildren := []kit.Node{foldHead, summaryNode}
		if f.errorMsg != "" {
			foldChildren = append(foldChildren, sc.text(sc.indexed("omp.ask.error", i), f.errorMsg))
		}
		// First unanswered question is open while pending (answersByName == nil);
		// all others fold.
		if answersByName == nil && i == 0 {
			bodyChildren = append(bodyChildren, sc.vstack(sc.indexed("omp.ask.open", i), 1, foldChildren...))
		} else {
			bodyChildren = append(bodyChildren, sc.vstack(sc.indexed("omp.ask.fold", i), 0, foldChildren...))
		}
	}

	var cardChildren []kit.Node
	cardChildren = append(cardChildren, head)
	if len(bodyChildren) > 0 {
		cardChildren = append(cardChildren, sc.vstack(sc.claim("omp.ask.card.body"), 1, bodyChildren...))
	} else {
		cardChildren = append(cardChildren, sc.text(sc.claim("omp.ask.card.body"), "Waiting for your answer…"))
	}
	if strings.TrimSpace(skipMsg) != "" {
		cardChildren = append(cardChildren, sc.text(sc.claim("omp.ask.skip"), "⚠ "+nvPlainText(skipMsg)))
	}
	return sc.vstack(sc.claim("omp.ask.card"), 1, cardChildren...)
}

// nativeAskCall ports describeAskCall in packages/tui/src/tools/ask.ts:160-167
// plus the native ask-card structure when form HTML is present.
func nativeAskCall(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	fields := nvParseAskFormFields(nil, nvStr(args, "html"))
	if len(fields) > 0 {
		card := nvBuildAskFormCard(sc, fields, nil, nil, "")
		return sc.container(sc.claim("omp.tool.ask.call"), card), nil
	}
	return sc.container(sc.claim("omp.tool.ask.call"), sc.text(sc.claim("omp.ask.card.body"), "Waiting for your answer…")), nil
}

// nativeAskResult ports describeAskResult in packages/tui/src/tools/ask.ts:170-209.
func nativeAskResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	rawAnswers := nvSlice(details, "answers")
	// internal/ask returns empty non-nil Answers: []FormAnswer{} on errors,
	// malformed forms, and unanswered timeouts (internal/ask/ask.go:686,698,713,719,743).
	// Fall back to result text whenever there are no answers or the tool errored.
	if len(rawAnswers) == 0 {
		var children []kit.Node
		fallback := strings.TrimSpace(nvPlainText(nvResultText(res)))
		if fallback != "" {
			if rc.IsError {
				children = append(children, nvErrorNode(sc, sc.claim("omp.tool.error"), fallback))
			} else {
				children = append(children, sc.text(sc.claim("omp.tool.notice"), fallback))
			}
		} else if nvBool(details, "timedOut") {
			children = append(children, sc.text(sc.claim("omp.tool.notice"), nvAskTimedOutNote))
		}
		return sc.container(sc.claim("omp.tool.ask"), children...), nil
	}

	// Collect password field names from details.form or args.html so secrets stay masked.
	fields := nvParseAskFormFields(nvMap(details, "form"), nvStr(rc.Args, "html"))
	passwords := map[string]bool{}
	for _, f := range fields {
		if f.kind == "password" {
			passwords[f.name] = true
		}
	}

	type optNote struct {
		name     string
		question string
		option   string
		text     string
	}
	var notes []optNote
	for _, rn := range nvSlice(details, "notes") {
		if nm := nvToMap(rn); nm != nil {
			notes = append(notes, optNote{
				name:     nvStr(nm, "name"),
				question: nvStr(nm, "question"),
				option:   nvStr(nm, "option"),
				text:     nvStr(nm, "text"),
			})
		}
	}

	var foldChildren []kit.Node
	for i, ra := range rawAnswers {
		if !sc.canAdd(6) {
			break
		}
		am := nvToMap(ra)
		if am == nil {
			continue
		}
		name := nvStr(am, "name")
		question := nvPlainLine(nvStr(am, "question"))
		rawAnswer := nvStr(am, "answer")
		shown := nvPlainText(rawAnswer)
		if passwords[name] {
			n := min(len([]rune(rawAnswer)), 12)
			shown = strings.Repeat("•", n)
		}

		// upstream: tools/ask.ts:170-185 — answered section shows the question text
		// over omp.tool.answer (which carries its own check icon in Tern) or
		// omp.tool.answer.off, with no duplicate omp.ask.fold.mark checkmark.
		qNode := sc.text(sc.indexed("omp.ask.fold.question", i), question)
		var ansNode kit.Node
		if strings.TrimSpace(shown) != "" {
			ansNode = sc.text(sc.indexed("omp.tool.answer", i), shown)
		} else {
			ansNode = sc.text(sc.indexed("omp.tool.answer.off", i), nvAskNoAnswer)
		}

		itemChildren := []kit.Node{qNode, ansNode}

		noteIdx := 0
		for _, n := range notes {
			if n.name == name && n.question == nvStr(am, "question") {
				itemChildren = append(itemChildren, sc.text(
					sc.subIndexed("omp.tool.answer.note", i, noteIdx),
					fmt.Sprintf("%s: %s", nvPlainLine(n.option), nvPlainText(n.text)),
				))
				noteIdx++
			}
		}
		foldChildren = append(foldChildren, sc.vstack(sc.indexed("omp.ask.fold", i), 0, itemChildren...))
	}

	var children []kit.Node
	if len(foldChildren) > 0 {
		children = append(children, sc.vstack(sc.claim("omp.ask.card.body"), 1, foldChildren...))
	}
	if nvBool(details, "timedOut") {
		children = append(children, sc.text(sc.claim("omp.tool.notice"), nvAskTimedOutNote))
	}
	return sc.container(sc.claim("omp.tool.ask"), children...), nil
}

// ── task / subagent (tools/task.ts + tools/agent-tree.ts) ────────────────────

const nvMaxNestedTaskDepth = 8

func nvTaskSummary(raw string) string {
	clean := strings.TrimSpace(nvPlainText(raw))
	if clean == "" {
		return ""
	}
	first, _, _ := strings.Cut(clean, "\n")
	return nvPlainLine(first)
}

func nvFormatAgentRowSummary(id, agent, status, model, thinking, activity string, requests, tokens int, cost float64, durMs int64, isolated, background bool) string {
	var parts []string
	head := nvPlainLine(id)
	if agent != "" && agent != id {
		head += " (" + nvPlainLine(agent) + ")"
	}
	if head != "" {
		parts = append(parts, head)
	}
	if status != "" {
		parts = append(parts, status)
	}
	if model != "" {
		m := nvPlainLine(model)
		if thinking != "" {
			m += " [" + nvPlainLine(thinking) + "]"
		}
		parts = append(parts, m)
	}
	if background {
		parts = append(parts, "background")
	}
	if isolated {
		parts = append(parts, "isolated")
	}
	if activity != "" {
		parts = append(parts, nvPlainLine(activity))
	}
	if requests > 0 {
		parts = append(parts, fmt.Sprintf("%s req", nvFormatNumber(requests)))
	}
	if tokens > 0 {
		parts = append(parts, fmt.Sprintf("%s tok", nvFormatNumber(tokens)))
	}
	if cost > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", cost))
	}
	if durMs > 0 {
		parts = append(parts, nvFormatDuration(durMs))
	}
	return strings.Join(parts, " · ")
}

// nvThinkingToken maps a configured thinking level to omp's palette token name.
// upstream: packages/tui/src/tools/task.ts:1790-1806
func nvThinkingToken(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off":
		return "thinkingOff"
	case "minimal":
		return "thinkingMinimal"
	case "low":
		return "thinkingLow"
	case "medium":
		return "thinkingMedium"
	case "high":
		return "thinkingHigh"
	case "xhigh", "max":
		return "thinkingXhigh"
	default:
		return ""
	}
}

// nvFormatTaskID matches formatTaskId in packages/tui/src/tools/task.ts.
func nvFormatTaskID(id string) string {
	return nvPlainLine(id)
}

// nvAgentMetaFields carries structured per-agent fields emitted as child nodes
// inside `omp.task.agent#<idx>` so pig-tern/frontend constructs a native Tern
// `agent` node (`name`, `agent`, `status`, `task`, `model`, `thinking`,
// `stats`, `badges`, `depth`, `hovercard`).
// upstream: packages/tui/src/tools/task.ts:1890-2041
type nvAgentMetaFields struct {
	name          string
	agent         string
	status        string
	task          string
	model         string
	thinking      string
	requests      int
	tokens        int
	toolCount     int
	contextTokens int
	contextWindow int
	cost          float64
	durMs         int64
	running       bool
	curTool       string
	curToolIntent string
	badges        []string
	depth         int
	rowText       string
}

func nvAppendAgentMetaNodes(sc *nvScope, idx int, m nvAgentMetaFields, out []kit.Node) []kit.Node {
	addText := func(role, val string) {
		if val != "" && sc.canAdd(1) {
			out = append(out, sc.text(sc.indexed(role, idx), val))
		}
	}
	addText("omp.task.agent.row", m.rowText)
	addText("omp.task.agent.name", m.name)
	addText("omp.task.agent.kind", m.agent)
	addText("omp.task.agent.status", m.status)
	addText("omp.task.agent.task", m.task)
	addText("omp.task.agent.model", m.model)
	addText("omp.task.agent.thinking", m.thinking)
	if m.requests > 0 {
		addText("omp.task.agent.requests", strconv.Itoa(m.requests))
	}
	if m.tokens > 0 {
		addText("omp.task.agent.tokens", strconv.Itoa(m.tokens))
	}
	if m.toolCount > 0 {
		addText("omp.task.agent.tools", strconv.Itoa(m.toolCount))
	}
	if m.contextTokens > 0 {
		addText("omp.task.agent.context_tokens", strconv.Itoa(m.contextTokens))
	}
	if m.contextWindow > 0 {
		addText("omp.task.agent.context_window", strconv.Itoa(m.contextWindow))
	}
	if m.cost > 0 {
		addText("omp.task.agent.cost", strconv.FormatFloat(m.cost, 'f', -1, 64))
	}
	if m.durMs > 0 || !m.running {
		if m.running {
			addText("omp.task.agent.age", strconv.FormatInt(m.durMs, 10))
		} else if m.durMs > 0 {
			addText("omp.task.agent.took", strconv.FormatInt(m.durMs, 10))
		}
	}
	if m.running && m.curTool != "" {
		addText("omp.task.agent.tool", m.curTool)
		addText("omp.task.agent.tool_intent", m.curToolIntent)
	}
	if len(m.badges) > 0 {
		addText("omp.task.agent.badges", strings.Join(m.badges, ","))
	}
	if m.depth > 0 {
		addText("omp.task.agent.depth", strconv.Itoa(m.depth))
	}
	return out
}

// nativeTaskCall ports describeTaskCall in packages/tui/src/tools/task.ts:2058-2105.
func nativeTaskCall(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	var children []kit.Node

	if ctxText := nvPlainLine(nvStr(args, "context")); ctxText != "" {
		children = append(children, sc.text(sc.claim("omp.tool.context"), ctxText))
	}

	idx := 0
	addPending := func(name, agent, brief string, isolated bool) {
		if !sc.canAdd(10) {
			return
		}
		if name == "" {
			name = "agent"
		}
		summary := nvTaskSummary(brief)
		rowText := nvFormatAgentRowSummary(name, agent, "pending", "", "", summary, 0, 0, 0, 0, isolated, false)
		var badges []string
		if isolated {
			badges = append(badges, "isolated")
		}
		agentChildren := nvAppendAgentMetaNodes(sc, idx, nvAgentMetaFields{
			name:    nvFormatTaskID(name),
			agent:   nvPlainLine(agent),
			status:  "pending",
			task:    summary,
			badges:  badges,
			rowText: rowText,
		}, nil)
		if trimmed := strings.TrimSpace(nvPlainText(brief)); trimmed != "" {
			agentChildren = append(agentChildren, sc.md(sc.indexed("omp.task.assignment", idx), trimmed))
		}
		children = append(children, sc.vstack(sc.indexed("omp.task.agent", idx), 0, agentChildren...))
		idx++
	}

	flatName := strings.TrimSpace(nvStr(args, "name"))
	flatTask := nvStr(args, "task")
	if flatName != "" || nvTaskSummary(flatTask) != "" {
		addPending(flatName, nvStr(args, "agent"), flatTask, nvBool(args, "isolated"))
	}
	for i, raw := range nvSlice(args, "tasks") {
		tm := nvToMap(raw)
		if tm == nil {
			continue
		}
		name := strings.TrimSpace(nvStr(tm, "name"))
		if name == "" {
			name = fmt.Sprintf("#%d", i+1)
		}
		addPending(name, nvStr(tm, "agent"), nvStr(tm, "task"), nvBool(tm, "isolated"))
	}

	return sc.container(sc.claim("omp.tool.task.call"), children...), nil
}

// nvTaskSessionID returns the session ID from ctx if available.
func nvTaskSessionID(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	sid, err := ctx.SessionManager().GetSessionID()
	if err != nil {
		return ""
	}
	return sid
}

// nvTrackAsyncTaskCard subscribes a rendered background `task` card to live
// registry and job-completion updates so the card redraws when spawned children
// transition from pending -> running -> done/failed/aborted (Finding 7).
// Unsubscribes automatically once all tracked agents settle or when replaced.
func nvTrackAsyncTaskCard(ctx sdk.Context, rc sdk.ToolRenderContext, sessionID string, pendingIDs []string) {
	if rc.Invalidate == nil || rc.State == nil || len(pendingIDs) == 0 {
		return
	}
	const stateKey = "ansiTaskWatchUnsub"
	if _, already := rc.State[stateKey]; already {
		return
	}
	idSet := make(map[string]bool, len(pendingIDs))
	for _, id := range pendingIDs {
		if id != "" {
			idSet[id] = true
		}
	}
	if len(idSet) == 0 {
		return
	}
	inv := rc.Invalidate
	var unsubReg, unsubJobs func()
	if rt := agents.DefaultRuntime(); rt != nil && rt.Registry != nil {
		unsubReg = rt.Registry.OnChange(func() {
			inv()
		})
	}
	unsubJobs = jobs.OnFinish(func(sid string, j jobs.Job) {
		if j == nil {
			return
		}
		if sessionID != "" && sid != "" && sid != sessionID {
			return
		}
		if idSet[j.ID()] {
			inv()
		}
	})
	rc.State[stateKey] = func() {
		if unsubReg != nil {
			unsubReg()
		}
		if unsubJobs != nil {
			unsubJobs()
		}
	}
}

func nvUntrackAsyncTaskCard(rc sdk.ToolRenderContext) {
	if rc.State == nil {
		return
	}
	const stateKey = "ansiTaskWatchUnsub"
	if fn, ok := rc.State[stateKey].(func()); ok && fn != nil {
		delete(rc.State, stateKey)
		fn()
	}
}

// nvRefreshAsyncProgress overlays live job/registry state onto a snapshot
// progress entry returned when the background task was spawned.
// Returns the refreshed map and whether the agent is still non-terminal.
func nvRefreshAsyncProgress(sessionID string, pm map[string]any) (map[string]any, bool) {
	id := nvStr(pm, "id")
	if id == "" {
		return pm, false
	}
	status := nvStr(pm, "status")
	if status == "completed" || status == "done" || status == "failed" || status == "aborted" {
		return pm, false
	}
	// Copy so we never mutate caller's Details map.
	merged := make(map[string]any, len(pm)+8)
	for k, v := range pm {
		merged[k] = v
	}
	// 1. Check live jobs registry (carries latest AgentProgress + final Status).
	if j, ok := jobs.Get(sessionID, id); ok && j != nil {
		if det, ok := j.(jobs.JobDetailer); ok {
			if jd := nvToMap(det.Details()); jd != nil {
				if progs := nvSlice(jd, "progress"); len(progs) > 0 {
					if live := nvToMap(progs[0]); live != nil {
						for k, v := range live {
							merged[k] = v
						}
					}
				}
			}
			if ended := det.Ended(); !ended.IsZero() {
				if ms := ended.Sub(j.Started()).Milliseconds(); ms > 0 && nvInt64(merged, "durationMs") == 0 {
					merged["durationMs"] = ms
				}
			}
		}
		switch j.Status() {
		case "completed":
			merged["status"] = "completed"
		case "failed":
			merged["status"] = "failed"
		case "cancelled":
			merged["status"] = "aborted"
		case "running":
			if nvStr(merged, "status") == "" || nvStr(merged, "status") == "pending" {
				merged["status"] = "running"
			}
		}
	}
	// 2. Fallback to agents registry when job entry expired or was not found.
	// Use CollectPeerRosterForSession which snapshots AgentRef.Status under ref.mu
	// so concurrent SetStatusInSession transitions never data-race.
	curStatus := nvStr(merged, "status")
	if curStatus == "" || curStatus == "pending" || curStatus == "running" {
		if rt := agents.DefaultRuntime(); rt != nil && rt.Registry != nil {
			roster := rt.Registry.CollectPeerRosterForSession(sessionID, "")
			foundLive := false
			for _, p := range roster.Peers {
				if p.ID != id {
					continue
				}
				foundLive = true
				switch p.Status {
				case "idle":
					merged["status"] = "completed"
				case "running":
					if curStatus == "" || curStatus == "pending" {
						merged["status"] = "running"
					}
				}
				break
			}
			if !foundLive {
				if ref, ok := rt.Registry.GetInSession(sessionID, id); ok && ref != nil && ref.IsResumable() {
					merged["status"] = "completed"
				}
			}
		}
	}
	finalStatus := nvStr(merged, "status")
	stillLive := finalStatus == "" || finalStatus == "pending" || finalStatus == "running"
	return merged, stillLive
}

func nvDescribeTaskAgents(sc *nvScope, sessionID string, details map[string]any, expanded, background bool, depth int, counter *int, pendingIDs *[]string) []kit.Node {
	if details == nil || depth > nvMaxNestedTaskDepth {
		return nil
	}
	var out []kit.Node
	seenIDs := map[string]bool{}

	// 1. Settled results.
	results := nvSlice(details, "results")
	for rIdx, raw := range results {
		if !sc.canAdd(16) {
			out = append(out, sc.text(sc.claim("omp.tool.stats"), fmt.Sprintf("+%d more agents", len(results)-rIdx)))
			break
		}
		rm := nvToMap(raw)
		if rm == nil {
			continue
		}
		id := nvStr(rm, "id")
		seenIDs[id] = true
		idx := *counter
		*counter++

		aborted := nvBool(rm, "aborted")
		exitCode := nvInt(rm, "exitCode")
		errText := nvStr(rm, "error")
		mergeFailed := !aborted && exitCode == 0 && errText != ""
		success := !aborted && exitCode == 0 && errText == ""
		status := "done"
		switch {
		case aborted:
			status = "aborted"
		case exitCode != 0:
			status = "failed"
		}
		var cost float64
		if usage := nvMap(rm, "usage"); usage != nil {
			if cm := nvMap(usage, "cost"); cm != nil {
				cost, _ = nvFloat(cm, "total")
			}
		}
		activity := nvTaskSummary(nvStr(rm, "description", "assignment", "task"))
		model := nvStr(rm, "resolvedModelIdentity", "resolvedModel")
		thinking := nvThinkingToken(nvStr(rm, "resolvedThinkingLevel"))
		isolated := nvBool(rm, "isolated")
		var badges []string
		if background {
			badges = append(badges, "background")
		}
		if isolated {
			badges = append(badges, "isolated")
		}
		if mergeFailed {
			badges = append(badges, "merge failed:warning")
		}
		if nvBool(rm, "truncated") {
			badges = append(badges, "truncated:warning")
		}
		rowText := nvFormatAgentRowSummary(
			id,
			nvStr(rm, "agent"),
			status,
			model,
			nvStr(rm, "resolvedThinkingLevel"),
			activity,
			nvInt(rm, "requests"),
			nvInt(rm, "tokens"),
			cost,
			nvInt64(rm, "durationMs"),
			isolated,
			background,
		)
		agentChildren := nvAppendAgentMetaNodes(sc, idx, nvAgentMetaFields{
			name:          nvFormatTaskID(id),
			agent:         nvPlainLine(nvStr(rm, "agent")),
			status:        status,
			task:          activity,
			model:         nvPlainLine(model),
			thinking:      thinking,
			requests:      nvInt(rm, "requests"),
			tokens:        nvInt(rm, "tokens"),
			contextTokens: nvInt(rm, "contextTokens"),
			contextWindow: nvInt(rm, "contextWindow"),
			cost:          cost,
			durMs:         nvInt64(rm, "durationMs"),
			running:       false,
			badges:        badges,
			depth:         depth,
			rowText:       rowText,
		}, nil)
		// Always emit omp.task.assignment so Tern can render both the agent child
		// and its row hovercard (upstream: tools/task.ts:1890-1898, 2013-2015).
		if assign := strings.TrimSpace(nvPlainText(nvStr(rm, "assignment", "task"))); assign != "" {
			agentChildren = append(agentChildren, sc.md(sc.indexed("omp.task.assignment", idx), assign))
		}
		if aborted {
			if reason := strings.TrimSpace(nvStr(rm, "abortReason")); reason != "" {
				agentChildren = append(agentChildren, nvErrorNode(sc, sc.subIndexed("omp.tool.error", idx, 0), reason))
			}
		}
		if errText != "" {
			agentChildren = append(agentChildren, nvErrorNode(sc, sc.subIndexed("omp.tool.error", idx, 1), errText))
		}
		if structOut := nvMap(rm, "structuredOutput"); structOut != nil && nvHasKey(structOut, "data") {
			agentChildren = append(agentChildren, sc.lines(sc.indexed("omp.task.output", idx), nvPrettyJSON(structOut["data"])))
		} else if output := strings.TrimSpace(nvPlainText(nvStr(rm, "output"))); output != "" {
			agentChildren = append(agentChildren, sc.md(sc.indexed("omp.task.output", idx), output))
		}
		if stderr := strings.TrimSpace(nvStr(rm, "stderr")); stderr != "" && !success {
			agentChildren = append(agentChildren, sc.lines(sc.indexed("omp.tool.output", idx), nvSanitizeLines(stderr)))
		}
		var metaParts []string
		if pp := nvStr(rm, "patchPath"); pp != "" && success {
			metaParts = append(metaParts, "Patch: "+nvShortenPath(pp))
		}
		if br := nvStr(rm, "branchName"); br != "" && success {
			metaParts = append(metaParts, "Branch: "+nvPlainLine(br))
		}
		if len(metaParts) > 0 {
			agentChildren = append(agentChildren, sc.text(sc.indexed("omp.task.meta", idx), strings.Join(metaParts, " · ")))
		}

		// Nested child task snapshots (extractedToolData.task).
		if extracted := nvMap(rm, "extractedToolData"); extracted != nil {
			for _, nestedRaw := range nvSlice(extracted, "task") {
				if nestedMap := nvToMap(nestedRaw); nestedMap != nil {
					if depth >= nvMaxNestedTaskDepth {
						agentChildren = append(agentChildren, sc.text(sc.claim("omp.tool.notice"), "… nested task depth limit reached"))
					} else {
						agentChildren = append(agentChildren, nvDescribeTaskAgents(sc, sessionID, nestedMap, expanded, background, depth+1, counter, pendingIDs)...)
					}
				}
			}
		}
		out = append(out, sc.vstack(sc.indexed("omp.task.agent", idx), 0, agentChildren...))
	}

	// 2. In-flight (or asynchronously settled) progress rows not yet in results.
	progressList := nvSlice(details, "progress")
	for pIdx, raw := range progressList {
		if !sc.canAdd(16) {
			out = append(out, sc.text(sc.claim("omp.tool.stats"), fmt.Sprintf("+%d more agents", len(progressList)-pIdx)))
			break
		}
		pm := nvToMap(raw)
		if pm == nil {
			continue
		}
		id := nvStr(pm, "id")
		if seenIDs[id] {
			continue
		}
		if background {
			var stillLive bool
			pm, stillLive = nvRefreshAsyncProgress(sessionID, pm)
			if stillLive && pendingIDs != nil {
				*pendingIDs = append(*pendingIDs, id)
			}
		}
		idx := *counter
		*counter++

		status := nvStr(pm, "status")
		if status == "completed" {
			status = "done"
		}
		running := status == "running"
		summary := nvTaskSummary(nvStr(pm, "description", "assignment", "task"))
		curTool := ""
		curToolIntent := ""
		activity := summary
		if ct := nvStr(pm, "currentTool"); ct != "" && running {
			curTool = nvPlainLine(ct)
			if intent := nvStr(pm, "currentToolIntent", "currentToolArgs"); intent != "" {
				curToolIntent = nvPlainLine(intent)
				activity = fmt.Sprintf("%s: %s", curTool, curToolIntent)
			} else {
				activity = curTool
			}
		}
		cost, _ := nvFloat(pm, "cost")
		model := nvStr(pm, "resolvedModelIdentity", "resolvedModel")
		thinking := nvThinkingToken(nvStr(pm, "resolvedThinkingLevel"))
		var badges []string
		if background {
			badges = append(badges, "background")
		}
		rowText := nvFormatAgentRowSummary(
			id,
			nvStr(pm, "agent"),
			status,
			model,
			nvStr(pm, "resolvedThinkingLevel"),
			activity,
			nvInt(pm, "requests"),
			nvInt(pm, "tokens"),
			cost,
			nvInt64(pm, "durationMs"),
			false,
			background,
		)
		agentChildren := nvAppendAgentMetaNodes(sc, idx, nvAgentMetaFields{
			name:          nvFormatTaskID(id),
			agent:         nvPlainLine(nvStr(pm, "agent")),
			status:        status,
			task:          summary,
			model:         nvPlainLine(model),
			thinking:      thinking,
			requests:      nvInt(pm, "requests"),
			tokens:        nvInt(pm, "tokens"),
			toolCount:     nvInt(pm, "toolCount"),
			contextTokens: nvInt(pm, "contextTokens"),
			contextWindow: nvInt(pm, "contextWindow"),
			cost:          cost,
			durMs:         nvInt64(pm, "durationMs"),
			running:       running,
			curTool:       curTool,
			curToolIntent: curToolIntent,
			badges:        badges,
			depth:         depth,
			rowText:       rowText,
		}, nil)
		// Always emit omp.task.assignment so Tern can render both the agent child
		// and its row hovercard (upstream: tools/task.ts:1890-1898, 1954-1956).
		// Progress agents omit recentOutput in native TSP (describeProgressAgent
		// attaches only assignment, resolvedModelRoute, retryFailure, nested tasks;
		// tools/task.ts:1954-1970).
		if assign := strings.TrimSpace(nvPlainText(nvStr(pm, "assignment", "task"))); assign != "" {
			agentChildren = append(agentChildren, sc.md(sc.indexed("omp.task.assignment", idx), assign))
		}
		if inflight := nvMap(pm, "inflightTaskDetails"); inflight != nil {
			if depth >= nvMaxNestedTaskDepth {
				agentChildren = append(agentChildren, sc.text(sc.claim("omp.tool.notice"), "… nested task depth limit reached"))
			} else {
				agentChildren = append(agentChildren, nvDescribeTaskAgents(sc, sessionID, inflight, expanded, background, depth+1, counter, pendingIDs)...)
			}
		}
		out = append(out, sc.vstack(sc.indexed("omp.task.agent", idx), 0, agentChildren...))
	}

	return out
}

// nativeTaskResult ports describeTaskResult in packages/tui/src/tools/task.ts:2107-2194.
func nativeTaskResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	var children []kit.Node

	if ctxText := nvPlainLine(nvStr(rc.Args, "context")); ctxText != "" {
		children = append(children, sc.text(sc.claim("omp.tool.context"), ctxText))
	}

	fallback := strings.TrimSpace(nvResultText(res))
	if details == nil {
		nvUntrackAsyncTaskCard(rc)
		if rc.IsError {
			msg := fallback
			if msg == "" {
				msg = "Task failed"
			}
			children = append(children, nvErrorNode(sc, sc.claim("omp.tool.error"), msg))
		} else if fallback != "" {
			children = append(children, sc.text(sc.claim("omp.tool.notice"), nvPlainText(fallback)))
		}
		return sc.container(sc.claim("omp.tool.task"), children...), nil
	}

	sessionID := nvTaskSessionID(ctx)
	background := nvMap(details, "async") != nil
	counter := 0
	var pendingIDs []string
	agentNodes := nvDescribeTaskAgents(sc, sessionID, details, opts.Expanded, background, 0, &counter, &pendingIDs)
	if background && len(pendingIDs) > 0 {
		nvTrackAsyncTaskCard(ctx, rc, sessionID, pendingIDs)
	} else {
		nvUntrackAsyncTaskCard(rc)
	}
	children = append(children, agentNodes...)

	// When details carries empty results/progress (e.g. validation errors from
	// internal/agents/task_tool.go), surface the result content text so errors
	// are never dropped (tools/task.ts:1549-1559, 2190).
	if len(agentNodes) == 0 {
		msg := fallback
		if msg == "" {
			msg = "No results"
		}
		if rc.IsError {
			children = append(children, nvErrorNode(sc, sc.claim("omp.tool.error"), msg))
		} else {
			children = append(children, sc.text(sc.claim("omp.tool.notice"), nvPlainText(msg)))
		}
	}

	// Summary stats line.
	results := nvSlice(details, "results")
	if len(results) > 0 {
		succeeded, failed, aborted, requests := 0, 0, 0, 0
		for _, raw := range results {
			rm := nvToMap(raw)
			if rm == nil {
				continue
			}
			requests += nvInt(rm, "requests")
			switch {
			case nvBool(rm, "aborted"):
				aborted++
			case nvInt(rm, "exitCode") != 0:
				failed++
			case nvStr(rm, "error") == "":
				succeeded++
			}
		}
		var parts []string
		if succeeded > 0 {
			parts = append(parts, fmt.Sprintf("%d succeeded", succeeded))
		}
		if failed > 0 {
			parts = append(parts, fmt.Sprintf("%d failed", failed))
		}
		if aborted > 0 {
			parts = append(parts, fmt.Sprintf("%d aborted", aborted))
		}
		if requests > 0 {
			parts = append(parts, fmt.Sprintf("%s req", nvFormatNumber(requests)))
		}
		if totalDur := nvInt64(details, "totalDurationMs"); totalDur > 0 {
			parts = append(parts, nvFormatDuration(totalDur))
		}
		if statsNode := nvStatsNode(sc, parts); statsNode != nil {
			children = append(children, statsNode)
		}
	}

	return sc.container(sc.claim("omp.tool.task"), children...), nil
}

// ── wait (tools/wait.ts + tools/job-widget.ts) ───────────────────────────────

const nvWaitTilesShown = 12

func nvWaitStatusOrder(status string) int {
	switch status {
	case "running":
		return 0
	case "failed":
		return 1
	case "cancelled":
		return 2
	default:
		return 3
	}
}

func nativeWaitCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	// upstream: tools/wait.ts:885-887 — inline head `Wait`, empty body.
	return sc.container(sc.claim("omp.tool.wait.call")), nil
}

// nativeWaitResult ports describeResult + describeJobsResult + describeJobTiles in
// packages/tui/src/tools/wait.ts:798-905 and packages/tui/src/tools/job-widget.ts:403-608.
func nativeWaitResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	if rc.IsError {
		msg := strings.TrimSpace(nvResultText(res))
		if msg == "" {
			msg = "Wait failed"
		}
		return sc.container(sc.claim("omp.tool.wait"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}

	details := nvToMap(res.Details)
	if nvBool(details, "interrupted") {
		return sc.container(sc.claim("omp.tool.wait"), sc.text(sc.claim("omp.tool.notice"), "interrupted by message")), nil
	}

	rawJobs := nvSlice(details, "jobs")
	jobs := make([]map[string]any, 0, len(rawJobs))
	for _, rj := range rawJobs {
		if jm := nvToMap(rj); jm != nil {
			jobs = append(jobs, jm)
		}
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		oi := nvWaitStatusOrder(nvStr(jobs[i], "status"))
		oj := nvWaitStatusOrder(nvStr(jobs[j], "status"))
		if oi != oj {
			return oi < oj
		}
		return nvInt64(jobs[i], "durationMs") > nvInt64(jobs[j], "durationMs")
	})

	var children []kit.Node
	if len(jobs) > 0 {
		shown := jobs
		if len(shown) > nvWaitTilesShown {
			shown = shown[:nvWaitTilesShown]
		}
		var tileNodes []kit.Node
		for i, job := range shown {
			if !sc.canAdd(8) {
				break
			}
			tileNodes = append(tileNodes, nvDescribeJobTile(sc, i, job))
		}
		children = append(children, sc.vstack(sc.claim("omp.job.tiles"), 1, tileNodes...))
		if rest := len(jobs) - len(shown); rest > 0 {
			children = append(children, sc.text(
				sc.claim("omp.wait.more"),
				fmt.Sprintf("+%d more %s · /jobs", rest, map[bool]string{true: "job", false: "jobs"}[rest == 1]),
			))
		}
	}

	agentsList := nvSlice(details, "agents")
	for i, ra := range agentsList {
		if !sc.canAdd(3) {
			children = append(children, sc.text(sc.claim("omp.tool.stats"), fmt.Sprintf("+%d more agents", len(agentsList)-i)))
			break
		}
		am := nvToMap(ra)
		if am == nil {
			continue
		}
		badge := "agent"
		if !nvBool(am, "live") {
			badge = "agent · no turn"
		}
		var parts []string
		parts = append(parts, badge, nvPlainLine(nvStr(am, "id")))
		if act := nvPlainLine(nvStr(am, "activity")); act != "" {
			parts = append(parts, act)
		}
		if parent := nvPlainLine(nvStr(am, "parentId")); parent != "" {
			parts = append(parts, "← "+parent)
		}
		if ageMs := nvInt64(am, "ageMs"); ageMs > 0 {
			parts = append(parts, nvFormatDuration(ageMs))
		}
		children = append(children, sc.text(sc.indexed("omp.wait.agent", i), strings.Join(parts, " · ")))
	}

	if len(children) == 0 {
		if fallback := strings.TrimSpace(nvPlainText(nvResultText(res))); fallback != "" {
			children = append(children, sc.text(sc.claim("omp.tool.notice"), fallback))
		}
	}

	return sc.container(sc.claim("omp.tool.wait"), children...), nil
}

// nvDescribeJobTile ports describeJobTile in packages/tui/src/tools/job-widget.ts:403-608.
func nvDescribeJobTile(sc *nvScope, idx int, job map[string]any) kit.Node {
	id := nvPlainLine(nvStr(job, "id"))
	typ := nvPlainLine(nvStr(job, "type"))
	status := nvPlainLine(nvStr(job, "status"))
	label := nvPlainLine(nvStr(job, "label"))
	widget := nvMap(job, "widget")
	// upstream: packages/tui/src/tools/job-widget.ts:404 — figure and blocks
	// render only when widget.state === "live"; any state may lend title/stage.
	var liveWidget map[string]any
	if nvStr(widget, "state") == "live" {
		liveWidget = widget
	}

	title := id
	if typ != "task" {
		if wt := nvPlainLine(nvStr(widget, "title")); wt != "" {
			title = wt
		} else if first, _, _ := strings.Cut(label, "\n"); strings.TrimSpace(first) != "" {
			title = strings.TrimSpace(first)
		}
	}

	// upstream: packages/tui/src/tools/job-widget.ts:370-375 (TILE_GLYPH).
	dot := "●"
	switch status {
	case "completed":
		dot = "✓"
	case "failed":
		dot = "✕"
	case "cancelled":
		dot = "■"
	}

	headChildren := []kit.Node{
		sc.text(sc.indexed("omp.job.tile.dot", idx), dot),
		sc.text(sc.indexed("omp.job.tile.title", idx), title),
	}
	if id != "" && id != title {
		headChildren = append(headChildren, sc.text(sc.indexed("omp.job.tile.id", idx), id))
	}
	if pct, ok := nvFloat(job, "completionPercent"); ok {
		headChildren = append(headChildren, sc.progressLines(sc.indexed("omp.job.tile.ring", idx), fmt.Sprintf("%.0f%%", pct), pct, 100))
	}
	if durMs := nvInt64(job, "durationMs"); durMs > 0 {
		headChildren = append(headChildren, sc.text(sc.indexed("omp.run.time", idx), nvFormatDuration(durMs)))
	}

	tileChildren := []kit.Node{
		sc.hstack(sc.indexed("omp.job.tile.head", idx), 1, headChildren...),
	}

	hasFigure := false
	if hl := nvMap(liveWidget, "headline"); hl != nil && sc.canAdd(2) {
		var fp []string
		if lbl := nvPlainLine(nvStr(hl, "label")); lbl != "" {
			fp = append(fp, lbl)
		}
		if txt := nvPlainLine(nvStr(hl, "text")); txt != "" {
			fp = append(fp, txt)
		}
		if tr := nvMap(hl, "trend"); tr != nil {
			if tt := nvPlainLine(nvStr(tr, "text")); tt != "" {
				fp = append(fp, tt)
			}
		}
		if len(fp) > 0 {
			tileChildren = append(tileChildren, sc.text(sc.indexed("omp.job.tile.figure", idx), strings.Join(fp, " ")))
			hasFigure = true
		}
	}

	hasVisual := false
	blocks := nvSlice(liveWidget, "blocks")
	if len(blocks) > 0 && sc.canAdd(6) {
		if b0 := nvToMap(blocks[0]); b0 != nil {
			if vis := nvDescribeJobTileVisual(sc, idx, b0); vis != nil {
				tileChildren = append(tileChildren, vis)
				hasVisual = true
			}
		}
	}

	// Stats, text notes, stage/exitCode.
	stage := nvPlainLine(nvStr(widget, "stage"))
	if stage == "" {
		stage = nvPlainLine(nvStr(job, "stage", "intent"))
	}
	var failedExit string
	if ec := nvIntPtr(job, "exitCode"); ec != nil && *ec != 0 {
		failedExit = fmt.Sprintf("exit %d", *ec)
	}
	leadMode := !hasFigure && !hasVisual
	var noteNodes []kit.Node
	for bIdx, rawB := range blocks {
		if !sc.canAdd(2) {
			noteNodes = append(noteNodes, sc.text(sc.indexed("omp.job.tile.more", idx), fmt.Sprintf("+%d more", len(blocks)-bIdx)))
			break
		}
		bm := nvToMap(rawB)
		if bm == nil {
			continue
		}
		switch nvStr(bm, "kind") {
		case "stat":
			var sp []string
			for _, ri := range nvSlice(bm, "items") {
				if im := nvToMap(ri); im != nil {
					sp = append(sp, strings.TrimSpace(nvPlainLine(nvStr(im, "label"))+" "+nvPlainLine(nvStr(im, "text"))))
				}
			}
			if len(sp) > 0 {
				noteNodes = append(noteNodes, sc.text(sc.subIndexed("omp.job.tile.stats", idx, bIdx), strings.Join(sp, " · ")))
			}
		case "text":
			if txt := nvPlainLine(nvStr(bm, "text")); txt != "" && txt != stage {
				noteNodes = append(noteNodes, sc.text(sc.subIndexed("omp.job.tile.note", idx, bIdx), txt))
			}
		}
	}
	if stage != "" || failedExit != "" {
		parts := nvCompactStrings([]string{failedExit, stage})
		stageText := strings.Join(parts, " · ")
		if leadMode {
			noteNodes = append([]kit.Node{sc.text(sc.indexed("omp.job.tile.lead", idx), nvTruncateRunes(stageText, 120))}, noteNodes...)
		} else {
			noteNodes = append(noteNodes, sc.text(sc.indexed("omp.job.tile.stage", idx), stageText))
		}
	}
	tileChildren = append(tileChildren, noteNodes...)

	return sc.vstack(sc.indexed("omp.job.tile", idx), 0, tileChildren...)
}

func nvDescribeJobTileVisual(sc *nvScope, tileIdx int, block map[string]any) kit.Node {
	switch nvStr(block, "kind") {
	case "spark":
		vals := nvSlice(block, "values")
		if len(vals) == 0 {
			return nil
		}
		lbl := nvPlainLine(nvStr(block, "label"))
		last := nvPlainLine(nvStr(block, "last"))
		summary := strings.TrimSpace(lbl + " " + last)
		return sc.text(sc.indexed("omp.job.tile.chart", tileIdx), summary)
	case "meter":
		val, _ := nvFloat(block, "value")
		txt := nvPlainLine(nvStr(block, "text", "label"))
		return sc.progressLines(sc.indexed("omp.job.tile.meter", tileIdx), txt, val, 1)
	case "bars":
		items := nvSlice(block, "items")
		if len(items) == 0 {
			return nil
		}
		maxVal, _ := nvFloat(block, "max")
		if maxVal <= 0 {
			maxVal = 1
		}
		limit := min(len(items), 3)
		var rows []kit.Node
		for bIdx := range limit {
			im := nvToMap(items[bIdx])
			if im == nil {
				continue
			}
			v, _ := nvFloat(im, "value")
			rows = append(rows, sc.hstack(sc.subIndexed("omp.job.tile.bar", tileIdx, bIdx), 1,
				sc.text(sc.subIndexed("omp.job.tile.key", tileIdx, bIdx), nvPlainLine(nvStr(im, "label"))),
				sc.progressLines(sc.subIndexed("omp.job.tile.meter", tileIdx, bIdx), "", v, maxVal),
				sc.text(sc.subIndexed("omp.job.tile.num", tileIdx, bIdx), nvPlainLine(nvStr(im, "text"))),
			))
		}
		if rest := len(items) - limit; rest > 0 {
			rows = append(rows, sc.text(sc.indexed("omp.job.tile.more", tileIdx), fmt.Sprintf("+%d more", rest)))
		}
		return sc.vstack(sc.indexed("omp.job.tile.bars", tileIdx), 0, rows...)
	case "grid":
		items := nvSlice(block, "items")
		if len(items) == 0 {
			return nil
		}
		var cells []string
		for _, ri := range items {
			if im := nvToMap(ri); im != nil {
				cells = append(cells, "● "+nvPlainLine(nvStr(im, "label")))
			}
		}
		return sc.text(sc.indexed("omp.job.tile.grid", tileIdx), strings.Join(cells, "   "))
	default:
		return nil
	}
}
