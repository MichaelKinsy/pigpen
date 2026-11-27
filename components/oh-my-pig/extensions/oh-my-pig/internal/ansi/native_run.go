package ansi

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// Native views for `bash` and `eval`.
//
// upstream:
//   packages/tui/src/tools/bash.ts (describeCall, describeResult, stripBashNotices, formatBashCommandText)
//   packages/tui/src/tools/tool-commands.ts (describeToolCommandNode)
//   packages/tui/src/tools/eval.ts (describeCall, describeResult, evalCellBox, evalOutputNodes, evalStatusSection)
//   packages/tui/src/tools/native-view.ts (runBox, runInput, runOutput, runFoot)

func init() {
	registerNative("bash", Builder{Call: nativeBashCall, Result: nativeBashResult})
	registerNative("eval", Builder{Call: nativeEvalCall, Result: nativeEvalResult})
}

// ── bash ─────────────────────────────────────────────────────────────────────

var (
	nvBashBgNoticeRE       = regexp.MustCompile(`^\[?(?:Backgrounded as job |Command moved to background|Running in background)`)
	nvBashExitCodeRE       = regexp.MustCompile(`^Command exited with code (-?\d+)$`)
	nvBashWallTimeRE       = regexp.MustCompile(`^Wall time:\s+`)
	nvBashRawArtifactRE    = regexp.MustCompile(`(?i)^\[(?:raw output:\s*(?:local://(\d+)\.log|artifact://([^\s\]]+))|Read (?:local://(\d+)\.log|artifact://([^\s\]]+)) for (?:full|raw) output)\]$`)
	nvBashShellUnsafeChars = regexp.MustCompile(`[^\w@%+=:,./-]`)
)

func nvShellQuote(s string) string {
	if s == "" {
		return `""`
	}
	if !nvBashShellUnsafeChars.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// nvFormatBashCommandText mirrors formatBashCommandText in tools/bash.ts:338-346:
// plain (unstyled) `cd <dir> && NAME="…" <command>` (no `$` prompt chrome).
func nvFormatBashCommandText(args map[string]any, cwd string) string {
	var parts []string
	if dir := nvStr(args, "cwd"); dir != "" {
		disp := nvDisplayPath(dir, cwd)
		if disp != "" {
			sep := " &&"
			if nvStr(args, "cdSeparator") == ";" {
				sep = ";"
			}
			parts = append(parts, "cd "+nvShellQuote(disp)+sep)
		}
	}
	if envMap := nvMap(args, "env"); len(envMap) > 0 {
		keys := make([]string, 0, len(envMap))
		for k := range envMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := envMap[k]
			if v == nil {
				continue
			}
			parts = append(parts, fmt.Sprintf(`%s=%q`, k, fmt.Sprint(v)))
		}
	}
	cmd := nvSanitize(nvStr(args, "command"))
	if cmd == "" {
		cmd = "…"
	}
	parts = append(parts, cmd)
	return strings.Join(parts, " ")
}

// nvStripBashNotices strips LLM-facing notices from bash output in omp's exact
// order: background notice → output truncation notice → exit-code notice →
// wall-time notice → raw-output artifact reference.
// upstream: packages/tui/src/tools/bash.ts:354-373
func nvStripBashNotices(rawOutput string, details map[string]any) (text string, artifactID string) {
	cur := strings.ReplaceAll(rawOutput, "\r\n", "\n")

	// 1. Background notice (upstream: packages/tui/src/tools/bash.ts:81-84, 359-362).
	if asyncMap := nvMap(details, "async"); nvStr(asyncMap, "state") == "running" {
		jobID := nvStr(asyncMap, "jobId")
		prefix := "Backgrounded as job " + jobID
		cur = nvStripTrailingNotice(cur, func(line string) bool {
			t := strings.TrimSpace(line)
			if jobID != "" && strings.HasPrefix(t, prefix) && len(t) > len(prefix) && (t[len(prefix)] == ';' || t[len(prefix)] == ' ') {
				return true
			}
			return nvBashBgNoticeRE.MatchString(t)
		})
	}

	// 2. Output truncation / summary notice.
	cur = nvStripOutputNotice(cur, nvMap(details, "meta"))

	// 3. Exit-code notice.
	if exitCode := nvIntPtr(details, "exitCode"); exitCode != nil {
		want := fmt.Sprintf("Command exited with code %d", *exitCode)
		cur = nvStripTrailingNotice(cur, func(line string) bool {
			t := strings.TrimSpace(line)
			return t == want || nvBashExitCodeRE.MatchString(t)
		})
	}

	// 4. Wall-time notice.
	if nvHasKey(details, "wallTimeMs") && (!nvHasKey(details, "wallTimeNoticeEmitted") || nvBool(details, "wallTimeNoticeEmitted")) {
		cur = nvStripTrailingNotice(cur, func(line string) bool {
			return nvBashWallTimeRE.MatchString(strings.TrimSpace(line))
		})
	}

	// 5. Raw-output artifact reference (`[raw output: local://<id>.log]`).
	// upstream: packages/tui/src/tools/output-meta.ts:205-226
	cur = nvStripTrailingNotice(cur, func(line string) bool {
		if m := nvBashRawArtifactRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			for _, g := range m[1:] {
				if g != "" {
					artifactID = g
					break
				}
			}
			return true
		}
		return false
	})
	return strings.TrimRight(cur, " \t\n"), artifactID
}

// nvShellFootParts ports shellFootParts in tools/bash.ts:487-500.
func nvShellFootParts(details map[string]any, artifactID string) []string {
	var parts []string
	if asyncMap := nvMap(details, "async"); nvStr(asyncMap, "state") == "running" {
		if jobID := nvStr(asyncMap, "jobId"); jobID != "" {
			parts = append(parts, "Job "+jobID)
		}
	}
	if svc := nvMap(details, "service"); svc != nil {
		if name := nvStr(svc, "name"); name != "" {
			parts = append(parts, "Service "+name)
		}
		if state := nvStr(svc, "state"); state != "" {
			parts = append(parts, state)
		}
		switch {
		case nvBool(svc, "ready"):
			parts = append(parts, "ready")
		case nvBool(svc, "timedOut"):
			parts = append(parts, "ready timed out")
		default:
			parts = append(parts, "not ready")
		}
		if pid := nvIntPtr(svc, "pid"); pid != nil {
			parts = append(parts, fmt.Sprintf("PID %d", *pid))
		}
	}
	if artifactID != "" {
		parts = append(parts, "Artifact "+artifactID)
	}
	if meta := nvMap(details, "meta"); meta != nil {
		if minMap := nvMap(meta, "minimized"); minMap != nil {
			filter := nvStr(minMap, "filter")
			inB := nvInt64(minMap, "inputBytes")
			outB := nvInt64(minMap, "outputBytes")
			if filter != "" || inB > 0 {
				parts = append(parts, fmt.Sprintf("Minimized %s (%s→%s)", filter, nvFormatBytes(inB), nvFormatBytes(outB)))
			}
		}
	}
	return parts
}

func nvShellRunState(details map[string]any, isError, isPartial bool) nvRunState {
	if asyncMap := nvMap(details, "async"); nvStr(asyncMap, "state") == "running" {
		return nvRunStateBackground
	}
	if isPartial {
		return nvRunStateRunning
	}
	if nvBool(details, "timedOut") {
		return nvRunStateTimedOut
	}
	if isError {
		return nvRunStateFailed
	}
	if ec := nvIntPtr(details, "exitCode"); ec != nil && *ec != 0 {
		return nvRunStateFailed
	}
	return nvRunStateDone
}

// nvDescribeBashSteps renders traced step rows when present in details.steps.
// upstream: packages/tui/src/tools/bash.ts:513-542
func nvDescribeBashSteps(sc *nvScope, details map[string]any) []kit.Node {
	steps := nvSlice(details, "steps")
	if len(steps) == 0 {
		return nil
	}
	var nodes []kit.Node
	for i, raw := range steps {
		if !sc.canAdd(4) {
			break
		}
		sm := nvToMap(raw)
		if sm == nil {
			continue
		}
		label := nvPlainLine(nvStr(sm, "label", "name"))
		note := nvPlainLine(nvStr(sm, "note", "status"))
		rowChildren := []kit.Node{
			sc.text(sc.indexed("omp.tool.bash.step.label", i), label),
		}
		if note != "" {
			rowChildren = append(rowChildren, sc.text(sc.indexed("omp.tool.bash.step.note", i), note))
		}
		nodes = append(nodes, sc.hstack(sc.indexed("omp.tool.bash.step", i), 1, rowChildren...))
	}
	return nodes
}

// nvDescribeToolCommands renders nested tool-command dispatches recorded in
// bash details.toolCommands.
// upstream: packages/tui/src/tools/tool-commands.ts:223-246
func nvDescribeToolCommands(sc *nvScope, details map[string]any, soleOutput string) (nodes []kit.Node, sole bool) {
	dispatches := nvSlice(details, "toolCommands")
	if len(dispatches) == 0 {
		return nil, false
	}
	sole = len(dispatches) == 1 && nvBool(details, "toolCommandsSole")
	for i, raw := range dispatches {
		if !sc.canAdd(4) {
			break
		}
		dm := nvToMap(raw)
		if dm == nil {
			continue
		}
		toolName := nvPlainLine(nvStr(dm, "tool"))
		headText := toolName
		if ec := nvIntPtr(dm, "exitCode"); ec != nil && *ec != 0 {
			headText = fmt.Sprintf("%s exit %d", headText, *ec)
		}
		var subChildren []kit.Node
		subChildren = append(subChildren, sc.text(sc.indexed("omp.tool.bash.command-tool.head", i), headText))
		out := nvStr(dm, "output")
		if out == "" && sole {
			out = soleOutput
		}
		if strings.TrimSpace(out) != "" {
			subChildren = append(subChildren, sc.lines(sc.indexed("omp.tool.output", i), nvSanitizeLines(out)))
		}
		nodes = append(nodes, sc.vstack(sc.indexed("omp.tool.bash.command-tool", i), 1, subChildren...))
	}
	return nodes, sole
}

// nativeBashCall ports describeCall in packages/tui/src/tools/bash.ts:934-970.
func nativeBashCall(_ sdk.Context, args map[string]any, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	cmd := nvFormatBashCommandText(args, rc.Cwd)
	runChildren := []kit.Node{
		sc.lines(sc.claim("omp.tool.bash.command"), nvRawLines(cmd)),
	}
	if rc.ExecutionStarted {
		runChildren = append(runChildren, nvRunFoot(sc, nvRunFootInput{
			state:     nvRunStateRunning,
			elapsedMs: rc.DurationMs,
		}))
	}
	box := sc.vstack(sc.claim("omp.run"), 0, runChildren...)
	return sc.container(sc.claim("omp.tool.bash.call"), box), nil
}

// nativeBashResult ports describeResult in packages/tui/src/tools/bash.ts:972-1077.
func nativeBashResult(_ sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	rawOutput := nvResultText(res)
	output, artifactID := nvStripBashNotices(rawOutput, details)
	cmd := nvFormatBashCommandText(rc.Args, rc.Cwd)
	state := nvShellRunState(details, rc.IsError, opts.IsPartial)

	var elapsedMs *int64
	switch {
	case opts.IsPartial:
		elapsedMs = rc.DurationMs
	case state == nvRunStateBackground:
		elapsedMs = nil
	default:
		if wall := nvInt64Ptr(details, "wallTimeMs"); wall != nil {
			elapsedMs = wall
		} else {
			elapsedMs = rc.DurationMs
		}
	}

	var runChildren []kit.Node
	runChildren = append(runChildren, sc.lines(sc.claim("omp.tool.bash.command"), nvRawLines(cmd)))

	// Nested tool commands (if any).
	cmdNodes, soleToolCmd := nvDescribeToolCommands(sc, details, output)
	runChildren = append(runChildren, cmdNodes...)

	// Traced steps (if any).
	stepNodes := nvDescribeBashSteps(sc, details)
	runChildren = append(runChildren, stepNodes...)

	// Output or no-output notice.
	if !soleToolCmd {
		trimmed := strings.TrimSpace(output)
		if trimmed != "" && trimmed != "(no output)" && trimmed != "(no output, exit code 0)" {
			runChildren = append(runChildren, sc.lines(sc.claim("omp.tool.bash.output"), nvRawLines(output)))
		} else if len(stepNodes) > 0 && trimmed == "" {
			emptyMsg := "Nothing written"
			if opts.IsPartial {
				emptyMsg = "Nothing written yet"
			}
			runChildren = append(runChildren, sc.text(sc.claim("omp.tool.bash.nooutput"), emptyMsg))
		}
	}

	runChildren = append(runChildren, nvRunFoot(sc, nvRunFootInput{
		state:     state,
		exitCode:  nvIntPtr(details, "exitCode"),
		elapsedMs: elapsedMs,
		facts:     nvShellFootParts(details, artifactID),
		meta:      nvMap(details, "meta"),
	}))

	box := sc.vstack(sc.claim("omp.run"), 0, runChildren...)
	return sc.container(sc.claim("omp.tool.bash"), box), nil
}

// ── eval ─────────────────────────────────────────────────────────────────────

const (
	nvEvalRunStatusTail   = 8
	nvEvalStatusValMaxLen = 200
)

type nvEvalCellSpec struct {
	language string
	code     string
	title    string
}

func nvEvalArgCells(args map[string]any) []nvEvalCellSpec {
	if rawCells := nvSlice(args, "cells"); len(rawCells) > 0 {
		out := make([]nvEvalCellSpec, 0, len(rawCells))
		for _, raw := range rawCells {
			cm := nvToMap(raw)
			if cm == nil {
				continue
			}
			lang := nvStr(cm, "language")
			if lang == "" {
				lang = nvStr(args, "language")
			}
			out = append(out, nvEvalCellSpec{
				language: nvNormalizeEvalLang(lang),
				code:     nvStr(cm, "code"),
				title:    nvStr(cm, "title"),
			})
		}
		return out
	}
	code := nvStr(args, "code")
	lang := nvNormalizeEvalLang(nvStr(args, "language"))
	title := nvStr(args, "title")
	if code == "" && lang == "" && title == "" {
		return nil
	}
	return []nvEvalCellSpec{{language: lang, code: code, title: title}}
}

func nvNormalizeEvalLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "py", "python":
		return "python"
	case "js", "javascript", "ts", "typescript":
		return "js"
	default:
		return strings.TrimSpace(lang)
	}
}

func nvEvalCellRunState(status string) (nvRunState, bool) {
	switch status {
	case "pending", "":
		return "", false
	case "running":
		return nvRunStateRunning, true
	case "complete":
		return nvRunStateDone, true
	case "error":
		return nvRunStateFailed, true
	default:
		return nvRunStateDone, true
	}
}

// nvEvalSplitOutput mirrors splitEvalOutput in packages/tui/src/tools/eval.ts:785-804.
type nvEvalOutputPart struct {
	isView bool
	text   string
	view   map[string]any
}

func nvEvalSplitOutput(output string, views []any) []nvEvalOutputPart {
	var parts []nvEvalOutputPart
	pushText := func(chunk string, trimStart, trimEnd bool) {
		t := chunk
		if trimStart {
			t = strings.TrimLeft(t, "\n")
		}
		if trimEnd {
			t = strings.TrimRight(t, "\n")
		}
		if strings.TrimSpace(t) != "" {
			parts = append(parts, nvEvalOutputPart{text: t})
		}
	}
	cursor := 0
	for _, raw := range views {
		entry := nvToMap(raw)
		if entry == nil {
			continue
		}
		matchText := nvStr(entry, "text")
		viewObj := nvMap(entry, "view")
		if matchText == "" || viewObj == nil {
			continue
		}
		idx := strings.Index(output[cursor:], matchText)
		if idx < 0 {
			continue
		}
		at := cursor + idx
		pushText(output[cursor:at], cursor > 0, true)
		parts = append(parts, nvEvalOutputPart{isView: true, view: viewObj})
		cursor = at + len(matchText)
	}
	pushText(output[cursor:], cursor > 0, cursor > 0)
	return parts
}

func nvEvalTableNode(sc *nvScope, id string, view map[string]any) kit.Node {
	colsRaw := nvSlice(view, "columns")
	rowsRaw := nvSlice(view, "rows")
	var headers []string
	var aligns []string
	hasNumeric := false
	for _, raw := range colsRaw {
		if cm := nvToMap(raw); cm != nil {
			headers = append(headers, nvPlainLine(nvStr(cm, "head")))
			if num, _ := cm["numeric"].(bool); num {
				aligns = append(aligns, "end")
				hasNumeric = true
			} else {
				aligns = append(aligns, "start")
			}
		}
	}
	var items []kit.ListItem
	var rows []string
	if len(headers) > 0 {
		detail := ""
		if hasNumeric && len(rowsRaw) > 0 {
			detail = "align=" + strings.Join(aligns, ",")
		}
		items = append(items, kit.ListItem{
			Label:   headers[0],
			Detail:  detail,
			Columns: headers[1:],
		})
		rows = append(rows, strings.Join(headers, " · "))
	}
	for _, rawRow := range rowsRaw {
		cells := nvToStrSliceAny(rawRow)
		if len(cells) == 0 {
			continue
		}
		clean := make([]string, len(cells))
		for i, c := range cells {
			clean[i] = nvPlainLine(c)
		}
		items = append(items, kit.ListItem{
			Label:   clean[0],
			Columns: clean[1:],
		})
		rows = append(rows, strings.Join(clean, " · "))
	}
	if len(items) > nvMaxLinesPerNode {
		items = items[:nvMaxLinesPerNode]
		rows = rows[:nvMaxLinesPerNode]
	}
	sc.add(1)
	if len(items) == 0 {
		return &kit.Lines{ID: id, Lines: []string{}}
	}
	return &kit.Lines{
		ID:    id,
		Lines: rows,
		List:  &kit.List{Items: items, Selected: -1},
	}
}

func nvToStrSliceAny(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, len(s))
		for i, el := range s {
			if el != nil {
				out[i] = fmt.Sprint(el)
			}
		}
		return out
	default:
		return nil
	}
}

func nvEvalOutputNodes(sc *nvScope, cellIdx int, multi bool, output string, views []any) []kit.Node {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	parts := nvEvalSplitOutput(output, views)
	var nodes []kit.Node
	for i, part := range parts {
		if !sc.canAdd(2) {
			break
		}
		if !part.isView {
			var id string
			if multi || i > 0 {
				id = sc.subIndexed("omp.tool.eval.output", cellIdx, i)
			} else {
				id = sc.claim("omp.tool.eval.output")
			}
			nodes = append(nodes, sc.lines(id, nvRawLines(part.text)))
			continue
		}
		kind := nvStr(part.view, "kind")
		switch kind {
		case "table":
			nodes = append(nodes, nvEvalTableNode(sc, sc.subIndexed("omp.tool.eval.table", cellIdx, i), part.view))
		case "json":
			nodes = append(nodes, sc.lines(sc.subIndexed("omp.tool.eval.output", cellIdx, i), nvPrettyJSON(part.view["value"])))
		default:
			mdText := nvPlainText(nvStr(part.view, "text"))
			nodes = append(nodes, sc.md(sc.subIndexed("omp.tool.eval.markdown", cellIdx, i), mdText))
		}
	}
	return nodes
}

// nvEvalStatusSection ports evalStatusSection + describeStatusEvent in
// packages/tui/src/tools/eval.ts:723-752, 865-880.
func nvEvalStatusSection(sc *nvScope, cellIdx int, multi bool, events []any, elided int, expanded bool) kit.Node {
	if len(events) == 0 && elided == 0 {
		return nil
	}
	shownFrom := 0
	if !expanded && len(events) > nvEvalRunStatusTail {
		shownFrom = len(events) - nvEvalRunStatusTail
	}
	hidden := shownFrom + elided
	var children []kit.Node
	if hidden > 0 {
		var earlierID string
		if multi {
			earlierID = sc.indexed("omp.tool.stats", cellIdx)
		} else {
			earlierID = sc.claim("omp.tool.stats")
		}
		children = append(children, sc.text(earlierID, fmt.Sprintf("%d earlier", hidden)))
	}
	for i := shownFrom; i < len(events); i++ {
		if !sc.canAdd(3) {
			break
		}
		ev := nvToMap(events[i])
		if ev == nil {
			continue
		}
		op := nvPlainLine(nvStr(ev, "op"))
		keys := make([]string, 0, len(ev))
		for k := range ev {
			if k == "op" || k == "preview" || k == "resolvedThinkingLevel" {
				continue
			}
			if op == "mcp_progress" && k != "tool" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := []string{op}
		for _, k := range keys {
			v := ev[k]
			switch val := v.(type) {
			case string:
				parts = append(parts, fmt.Sprintf("%s=%s", k, nvTruncateRunes(nvPlainLine(val), nvEvalStatusValMaxLen)))
			case float64:
				parts = append(parts, fmt.Sprintf("%s=%s", k, strconv.FormatFloat(val, 'f', -1, 64)))
			case int:
				parts = append(parts, fmt.Sprintf("%s=%d", k, val))
			case bool:
				parts = append(parts, fmt.Sprintf("%s=%v", k, val))
			}
		}
		evIdx := i - shownFrom
		lineID := sc.subIndexed("omp.tool.eval.status", cellIdx, evIdx)
		lineNode := sc.text(lineID, strings.Join(parts, " "))

		preview := ""
		if expanded {
			preview = strings.TrimRight(nvStr(ev, "preview"), " \t\n")
		}
		if preview == "" {
			children = append(children, lineNode)
			continue
		}
		prevLines := nvSanitizeLines(preview)
		if len(prevLines) > nvOutputExpandedLines {
			prevLines = prevLines[:nvOutputExpandedLines]
		}
		eventID := sc.subIndexed("omp.tool.eval.status.event", cellIdx, evIdx)
		prevID := sc.subIndexed("omp.tool.eval.status.preview", cellIdx, evIdx)
		children = append(children, sc.vstack(eventID, 0, lineNode, sc.lines(prevID, prevLines)))
	}
	var statusID string
	if multi {
		statusID = sc.indexed("omp.run.status", cellIdx)
	} else {
		statusID = sc.claim("omp.run.status")
	}
	return sc.vstack(statusID, 0, children...)
}

func nvEvalCellBox(sc *nvScope, idx, total int, code, title string, outputs []kit.Node, status kit.Node, foot *nvRunFootInput) kit.Node {
	multi := total > 1
	var children []kit.Node
	if multi {
		capText := fmt.Sprintf("%d/%d", idx+1, total)
		if t := strings.TrimSpace(nvPlainText(title)); t != "" {
			capText += " " + t
		}
		children = append(children, sc.text(sc.indexed("omp.run.caption", idx), capText))
	}
	if strings.TrimSpace(code) != "" {
		var inID string
		if multi {
			inID = sc.indexed("omp.tool.eval.input", idx)
		} else {
			inID = sc.claim("omp.tool.eval.input")
		}
		children = append(children, sc.lines(inID, nvRawLines(strings.TrimRight(code, " \t\n"))))
	}
	children = append(children, outputs...)
	if status != nil {
		children = append(children, status)
	}
	if foot != nil {
		f := *foot
		f.indexed = multi
		f.index = idx
		children = append(children, nvRunFoot(sc, f))
	}
	var runID string
	if multi {
		runID = sc.indexed("omp.run", idx)
	} else {
		runID = sc.claim("omp.run")
	}
	return sc.vstack(runID, 0, children...)
}

// nativeEvalCall ports describeCall in packages/tui/src/tools/eval.ts:1281-1296.
func nativeEvalCall(_ sdk.Context, args map[string]any, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	cells := nvEvalArgCells(args)
	var startFoot *nvRunFootInput
	if rc.ExecutionStarted {
		startFoot = &nvRunFootInput{
			state:     nvRunStateRunning,
			elapsedMs: rc.DurationMs,
		}
	}
	var children []kit.Node
	for i, cell := range cells {
		if !sc.canAdd(5) {
			break
		}
		var foot *nvRunFootInput
		if i == 0 {
			foot = startFoot
		}
		children = append(children, nvEvalCellBox(sc, i, len(cells), cell.code, cell.title, nil, nil, foot))
	}
	return sc.container(sc.claim("omp.tool.eval.call"), children...), nil
}

// nativeEvalResult ports describeResult in packages/tui/src/tools/eval.ts:1298-1381.
func nativeEvalResult(_ sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	var callFacts []string
	if notice := nvStr(details, "notice"); notice != "" {
		callFacts = append(callFacts, notice)
	}
	callMeta := nvMap(details, "meta")

	var children []kit.Node
	cellResults := nvSlice(details, "cells")
	if len(cellResults) > 0 {
		total := len(cellResults)
		multi := total > 1
		// Find last cell with a started state so call-level facts attach there.
		factsIndex := -1
		states := make([]nvRunState, total)
		hasState := make([]bool, total)
		for i, raw := range cellResults {
			cm := nvToMap(raw)
			if st, ok := nvEvalCellRunState(nvStr(cm, "status")); ok {
				states[i] = st
				hasState[i] = true
				factsIndex = i
			}
		}

		var earlierMs int64
		for i, raw := range cellResults {
			if !sc.canAdd(6) {
				break
			}
			cm := nvToMap(raw)
			if cm == nil {
				continue
			}
			outputs := nvEvalOutputNodes(sc, i, multi, nvStr(cm, "output"), nvSlice(cm, "views"))
			statusNode := nvEvalStatusSection(sc, i, multi, nvSlice(cm, "statusEvents"), nvInt(cm, "statusEventsElided"), opts.Expanded)
			var foot *nvRunFootInput
			if hasState[i] {
				var elapsedMs *int64
				if states[i] == nvRunStateRunning || states[i] == nvRunStateCancelled {
					if rc.DurationMs != nil {
						v := max(int64(0), *rc.DurationMs-earlierMs)
						elapsedMs = &v
					}
				} else {
					elapsedMs = nvInt64Ptr(cm, "durationMs")
				}
				f := nvRunFootInput{
					state:     states[i],
					exitCode:  nvIntPtr(cm, "exitCode"),
					elapsedMs: elapsedMs,
				}
				if i == factsIndex {
					f.facts = callFacts
					f.meta = callMeta
				}
				foot = &f
			}
			if d := nvInt64Ptr(cm, "durationMs"); d != nil {
				earlierMs += *d
			}
			children = append(children, nvEvalCellBox(sc, i, total, nvStr(cm, "code"), nvStr(cm, "title"), outputs, statusNode, foot))
		}
	} else {
		// Fallback when details.cells is absent (older details / error without cells).
		argCells := nvEvalArgCells(rc.Args)
		rawOut := nvStripOutputNotice(nvResultText(res), callMeta)
		state := nvRunStateDone
		if opts.IsPartial {
			state = nvRunStateRunning
		} else if rc.IsError || nvBool(details, "isError") {
			state = nvRunStateFailed
		}
		if len(argCells) == 0 {
			argCells = []nvEvalCellSpec{{language: nvNormalizeEvalLang(nvStr(details, "language")), code: ""}}
		}
		total := len(argCells)
		multi := total > 1
		for i, cell := range argCells {
			if !sc.canAdd(6) {
				break
			}
			var outputs []kit.Node
			var statusNode kit.Node
			var foot *nvRunFootInput
			if i == total-1 {
				outputs = nvEvalOutputNodes(sc, i, multi, rawOut, nil)
				statusNode = nvEvalStatusSection(sc, i, multi, nvSlice(details, "statusEvents"), nvInt(details, "statusEventsElided"), opts.Expanded)
				foot = &nvRunFootInput{
					state:     state,
					elapsedMs: rc.DurationMs,
					facts:     callFacts,
					meta:      callMeta,
				}
			}
			children = append(children, nvEvalCellBox(sc, i, total, cell.code, cell.title, outputs, statusNode, foot))
		}
	}

	// Render any attached result images (e.g. matplotlib figures).
	for i, img := range nvExtractImages(res) {
		if !sc.canAdd(1) {
			break
		}
		imgNode := kit.NewImage(img.Data, img.MimeType)
		imgNode.ID = sc.indexed("omp.tool.read.image", i)
		imgNode.Filename = fmt.Sprintf("eval-image-%d", i+1)
		sc.add(1)
		children = append(children, imgNode)
	}

	return sc.container(sc.claim("omp.tool.eval"), children...), nil
}
