package ansi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// Native views for remaining omp tools: `web_search`, `lsp`, `github`, `debug`,
// `resolve`, `reject`, `propose`, `report_issue`, `retain`, `recall`, `reflect`,
// `think`, `goal`, `yield`, `checkpoint`, `rewind`, `browser`, and generic fallback.
//
// upstream:
//   packages/tui/src/tools/web-search.ts (describeCall, describeResult, sourceRow)
//   packages/tui/src/tools/lsp.ts (describeCall, describeResult, describeLspDiagnostics, describeLspReferences, describeLspLocations, describeLspSymbols)
//   packages/tui/src/tools/github.ts (describeCall, describeResult, describeWatch, describeFallback)
//   packages/tui/src/tools/debug.ts (describeCall, describeResult)
//   packages/tui/src/tools/resolve.ts (textArgumentRenderer, resolutionRenderer)
//   packages/tui/src/tools/report-tool-issue.ts
//   packages/tui/src/tools/memory.ts (retainToolRenderer, recallToolRenderer, reflectToolRenderer)
//   packages/tui/src/tools/think.ts
//   packages/tui/src/tools/goal.ts
//   packages/tui/src/tools/yield.ts
//   packages/tui/src/tools/default-renderer.ts (describeDefaultToolExecution)

func init() {
	registerNative("web_search", Builder{Call: nativeWebSearchCall, Result: nativeWebSearchResult})
	registerNative("lsp", Builder{Call: nativeLspCall, Result: nativeLspResult})
	registerNative("github", Builder{Call: nativeGitHubCall, Result: nativeGitHubResult})
	registerNative("debug", Builder{Call: nativeDebugCall, Result: nativeDebugResult})
	registerNative("resolve", Builder{Call: nativeResolveCall("apply"), Result: nativeResolveResult})
	registerNative("reject", Builder{Call: nativeResolveCall("discard"), Result: nativeResolveResult})
	registerNative("propose", Builder{Call: nativeTextArgCall("propose", "title"), Result: nativeTextArgResult("propose")})
	registerNative("report_issue", Builder{Call: nativeTextArgCall("report_issue", "report"), Result: nativeTextArgResult("report_issue")})
	registerNative("retain", Builder{Call: nativeRetainCall, Result: nativeRetainResult})
	registerNative("recall", Builder{Call: nativeRecallCall, Result: nativeRecallResult})
	registerNative("reflect", Builder{Call: nativeReflectCall, Result: nativeReflectResult})
	registerNative("think", Builder{Call: nativeThinkCall, Result: nativeThinkResult})
	registerNative("goal", Builder{Call: nativeGoalCall, Result: nativeGoalResult})
	registerNative("yield", Builder{Call: nativeYieldCall, Result: nativeYieldResult})
	registerNative("checkpoint", Builder{Call: nativeDefaultCall("checkpoint"), Result: nativeDefaultResult("checkpoint")})
	registerNative("rewind", Builder{Call: nativeDefaultCall("rewind"), Result: nativeDefaultResult("rewind")})
	registerNative("browser", Builder{Call: nativeDefaultCall("browser"), Result: nativeDefaultResult("browser")})
}

// ── web_search (tools/web-search.ts) ─────────────────────────────────────────

func nativeWebSearchCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.web_search.call")), nil
}

func nativeWebSearchResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	// upstream: packages/tui/src/tools/web-search.ts:334-401 — describeResult
	// always uses card frame (describeCall is inline), with tone="error" on
	// details.error / rc.IsError and tone="warning" when response or sources are empty.
	if errMsg := nvStr(details, "error"); errMsg != "" {
		return sc.container(sc.claim("omp.tool.web_search"),
			sc.text(sc.claim("omp.head.frame"), "card"),
			sc.text(sc.claim("omp.head.tone"), "error"),
			nvErrorNode(sc, sc.claim("omp.tool.error"), errMsg),
		), nil
	}
	if rc.IsError {
		msg := strings.TrimSpace(nvResultText(res))
		if msg == "" {
			msg = "Web search failed"
		}
		return sc.container(sc.claim("omp.tool.web_search"),
			sc.text(sc.claim("omp.head.frame"), "card"),
			sc.text(sc.claim("omp.head.tone"), "error"),
			nvErrorNode(sc, sc.claim("omp.tool.error"), msg),
		), nil
	}

	rawText := strings.TrimSpace(nvResultText(res))
	resp := nvMap(details, "response")
	if resp == nil {
		msg := rawText
		if msg == "" {
			msg = "No response data"
		}
		return sc.container(sc.claim("omp.tool.web_search"),
			sc.text(sc.claim("omp.head.frame"), "card"),
			sc.text(sc.claim("omp.head.tone"), "warning"),
			sc.text(sc.claim("omp.tool.notice"), nvPlainText(msg)),
		), nil
	}
	answer := strings.TrimSpace(nvPlainText(nvStr(resp, "answer")))
	rawSources := nvSlice(resp, "sources")
	children := []kit.Node{
		sc.text(sc.claim("omp.head.frame"), "card"),
	}
	if len(rawSources) == 0 {
		children = append(children, sc.text(sc.claim("omp.head.tone"), "warning"))
	}

	if answer != "" {
		children = append(children, sc.md(sc.claim("omp.tool.web_search.answer"), answer))
	} else if len(rawSources) == 0 {
		msg := rawText
		if msg == "" {
			msg = "No answer text returned"
		}
		children = append(children, sc.text(sc.claim("omp.tool.notice"), nvPlainText(msg)))
	}

	if len(rawSources) > 0 {
		var sourceNodes []kit.Node
		for i, rs := range rawSources {
			if !sc.canAdd(4) {
				sourceNodes = append(sourceNodes, sc.text(sc.claim("omp.tool.stats"), fmt.Sprintf("+%d more sources", len(rawSources)-i)))
				break
			}
			sm := nvToMap(rs)
			if sm == nil {
				continue
			}
			rawURL := nvStr(sm, "url")
			title := nvPlainLine(nvStr(sm, "title"))
			if title == "" {
				if rawURL != "" {
					title = rawURL
				} else {
					title = "Untitled"
				}
			}
			domain := nvGetDomain(rawURL)
			age := ""
			if ageSec := nvInt(sm, "ageSeconds"); ageSec > 0 {
				age = nvFormatAge(ageSec)
			} else if pd := nvStr(sm, "publishedDate"); pd != "" {
				age = nvPlainLine(pd)
			}
			meta := strings.Join(nvCompactStrings([]string{domain, age}), " · ")
			headText := title
			if meta != "" {
				headText += " · " + meta
			}
			if rawURL != "" && rawURL != title {
				headText += " " + rawURL
			}

			srcChildren := []kit.Node{
				sc.text(sc.indexed("omp.tool.source.head", i), headText),
			}
			// Excerpts are included when there is no synthesized answer (tools/web-search.ts:388).
			if answer == "" {
				if snippet := strings.TrimSpace(nvPlainText(nvStr(sm, "snippet"))); snippet != "" {
					lines := strings.Split(snippet, "\n")
					if len(lines) > 3 {
						lines = lines[:3]
					}
					srcChildren = append(srcChildren, sc.text(sc.indexed("omp.tool.excerpt", i), strings.Join(lines, "\n")))
				}
			}
			sourceNodes = append(sourceNodes, sc.vstack(sc.indexed("omp.tool.source", i), 0, srcChildren...))
		}
		children = append(children, sc.vstack(sc.claim("omp.tool.sources"), 0, sourceNodes...))
	} else {
		children = append(children, sc.text(sc.claim("omp.tool.notice"), "No sources returned"))
	}

	// Usage / provider stats footnote if present.
	if usage := nvMap(resp, "usage"); usage != nil {
		var uParts []string
		if v := nvInt(usage, "inputTokens"); v > 0 {
			uParts = append(uParts, fmt.Sprintf("in %d", v))
		}
		if v := nvInt(usage, "outputTokens"); v > 0 {
			uParts = append(uParts, fmt.Sprintf("out %d", v))
		}
		if v := nvInt(usage, "totalTokens"); v > 0 {
			uParts = append(uParts, fmt.Sprintf("total %d", v))
		}
		if v := nvInt(usage, "searchRequests"); v > 0 {
			uParts = append(uParts, fmt.Sprintf("search %d", v))
		}
		if statsNode := nvStatsNode(sc, uParts); statsNode != nil {
			children = append(children, statsNode)
		}
	}

	return sc.container(sc.claim("omp.tool.web_search"), children...), nil
}

// ── lsp (tools/lsp.ts) ───────────────────────────────────────────────────────

var (
	nvLspDiagCountRE   = regexp.MustCompile(`\d+\s+(?:error|warning)\(s\)`)
	nvLspDiagAtRE      = regexp.MustCompile(`^\d+:\d+\s+\[`)
	nvLspDiagSummaryRE = regexp.MustCompile(`^\d+ \w+\(s\)(?:, \d+ \w+\(s\))*:?$`)
	nvLspFileSummaryRE = regexp.MustCompile(`: (?:no issues|\d+ \w+\(s\)(?:, \d+ \w+\(s\))*)$`)
	nvLspLocLineRE     = regexp.MustCompile(`^(.+?):(\d+):(\d+)\b(.*)$`)
	nvLspFenceRE       = regexp.MustCompile("(?s)```\\w*\\n.*?```")
	nvLspSymbolsHdrRE  = regexp.MustCompile(`Symbols in (.+):`)
)

func nativeLspCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.lsp.call")), nil
}

func nativeLspResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	action := nvStr(rc.Args, "action")
	if action == "" {
		action = nvStr(details, "action")
	}
	text := nvResultText(res)
	if rc.IsError {
		msg := text
		if msg == "" {
			msg = "LSP request failed"
		}
		return sc.container(sc.claim("omp.tool.lsp"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}
	if strings.TrimSpace(text) == "" {
		return sc.container(sc.claim("omp.tool.lsp"), sc.text(sc.claim("omp.tool.notice"), "No result")), nil
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	// Hover / markdown fenced response: tools/lsp.ts:1099-1101.
	if nvLspFenceRE.MatchString(text) {
		return sc.container(sc.claim("omp.tool.lsp"), sc.md(sc.claim("omp.tool.lsp.body"), nvPlainText(text))), nil
	}

	// Diagnostics response: tools/lsp.ts:955-985, 1102-1108.
	if action == "diagnostics" || nvLspDiagCountRE.MatchString(text) {
		var children []kit.Node
		var otherLines []string
		diagIdx := 0
		errIdx := 0
		curFile := ""
		for lineIdx, line := range lines {
			if m := nvGroupedHeaderRE.FindStringSubmatch(line); m != nil {
				body := strings.TrimSpace(m[2])
				if !strings.HasSuffix(body, "/") {
					curFile = nvHashTagSuffixRE.ReplaceAllString(body, "")
				}
				continue
			}
			t := strings.TrimSpace(nvPlainText(line))
			if t == "" || t == "OK" || nvLspDiagSummaryRE.MatchString(t) || nvLspFileSummaryRE.MatchString(t) {
				continue
			}
			if !sc.canAdd(2) {
				children = append(children, sc.text(sc.claim("omp.tool.stats"), fmt.Sprintf("+%d more lines", len(lines)-lineIdx)))
				break
			}
			switch {
			case strings.Contains(t, "all language servers failed"):
				children = append(children, sc.text(sc.indexed("omp.tool.error", errIdx), t))
				errIdx++
			case strings.Contains(t, "some servers failed") || strings.Contains(t, "no diagnostics report yet"):
				children = append(children, sc.text(sc.indexed("omp.tool.diagnostic", diagIdx), t))
				diagIdx++
			default:
				cand := t
				if curFile != "" && nvLspDiagAtRE.MatchString(t) {
					cand = curFile + ":" + t
				}
				if d := nvParseDiagnosticMessage(cand); d != nil {
					row := fmt.Sprintf("[%s] %s:%d:%d", d.severity, nvDisplayPath(d.filePath, rc.Cwd), d.line, d.col)
					if d.source != "" {
						row += " [" + d.source + "]"
					}
					row += " " + d.message
					children = append(children, sc.text(sc.indexed("omp.tool.diagnostic", diagIdx), row))
					diagIdx++
				} else {
					otherLines = append(otherLines, t)
				}
			}
		}
		if len(otherLines) > 0 {
			children = append(children, sc.lines(sc.claim("omp.tool.lsp.body"), otherLines))
		}
		return sc.container(sc.claim("omp.tool.lsp"), children...), nil
	}

	// Symbols response: tools/lsp.ts:1022-1048, 1109-1117.
	if nvLspSymbolsHdrRE.MatchString(text) {
		var symLines []string
		for _, line := range lines {
			if strings.Contains(line, "@") && strings.Contains(line, "line") {
				symLines = append(symLines, nvPlainText(line))
			}
		}
		if len(symLines) > 0 {
			return sc.container(sc.claim("omp.tool.lsp"), sc.lines(sc.claim("omp.tool.lsp.body"), symLines)), nil
		}
	}

	// Location / reference lines (`file:line:col ...`): tools/lsp.ts:992-1020, 1051-1071.
	var locFiles []string
	locsByFile := map[string][]string{}
	for _, line := range lines {
		t := strings.TrimSpace(nvPlainText(line))
		if m := nvLspLocLineRE.FindStringSubmatch(t); m != nil {
			f := m[1]
			if _, ok := locsByFile[f]; !ok {
				locFiles = append(locFiles, f)
			}
			locsByFile[f] = append(locsByFile[f], fmt.Sprintf(":%s:%s%s", m[2], m[3], m[4]))
		}
	}
	if len(locFiles) > 0 {
		var children []kit.Node
		for i, f := range locFiles {
			if !sc.canAdd(3) {
				children = append(children, sc.text(sc.claim("omp.tool.stats"), fmt.Sprintf("+%d more files", len(locFiles)-i)))
				break
			}
			entries := locsByFile[f]
			head := nvDisplayPath(f, rc.Cwd)
			children = append(children, sc.vstack(
				sc.indexed("omp.tool.search.file", i),
				0,
				sc.text(sc.indexed("omp.tool.file", i), head),
				sc.lines(sc.indexed("omp.tool.lsp.body", i), entries),
			))
		}
		return sc.container(sc.claim("omp.tool.lsp"), children...), nil
	}

	// Generic LSP output: tools/lsp.ts:1120-1126.
	return sc.container(sc.claim("omp.tool.lsp"), sc.lines(sc.claim("omp.tool.lsp.body"), nvSanitizeLines(text))), nil
}

// ── github (tools/github.ts) ─────────────────────────────────────────────────

func nativeGitHubCall(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	if strings.TrimSpace(nvStr(args, "op")) == "run_watch" {
		return sc.container(sc.claim("omp.tool.github.call"), sc.text(sc.claim("omp.tool.notice"), "waiting for workflow data...")), nil
	}
	return sc.container(sc.claim("omp.tool.github.call")), nil
}

func nativeGitHubResult(_ sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	if watch := nvMap(details, "watch"); watch != nil {
		return nvDescribeGitHubWatch(sc, watch, opts.Expanded), nil
	}

	output := strings.TrimSpace(nvResultText(res))
	if rc.IsError {
		msg := output
		if msg == "" {
			msg = "request failed"
		}
		return sc.container(sc.claim("omp.tool.github"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}
	if output == "" {
		return sc.container(sc.claim("omp.tool.github"), sc.text(sc.claim("omp.tool.notice"), "no output")), nil
	}
	// upstream: tools/github.ts:625 — md(plainText(output)).
	return sc.container(sc.claim("omp.tool.github"), sc.md(sc.claim("omp.tool.github.body"), nvPlainText(output))), nil
}

// nvDescribeGitHubWatch ports describeWatch in packages/tui/src/tools/github.ts:525-615.
func nvDescribeGitHubWatch(sc *nvScope, watch map[string]any, expanded bool) kit.Node {
	var watchChildren []kit.Node
	if note := strings.TrimSpace(nvPlainText(nvStr(watch, "note"))); note != "" {
		watchChildren = append(watchChildren, sc.text(sc.claim("omp.tool.notice"), note))
	}

	var runs []map[string]any
	if nvStr(watch, "mode") == "run" {
		if r := nvMap(watch, "run"); r != nil {
			runs = append(runs, r)
		}
	} else {
		for _, rr := range nvSlice(watch, "runs") {
			if rm := nvToMap(rr); rm != nil {
				runs = append(runs, rm)
			}
		}
		if len(runs) == 0 {
			watchChildren = append(watchChildren, sc.text(sc.claim("omp.tool.notice"), "waiting for workflow runs..."))
		}
	}

	jobIdx := 0
	cappedJobs := 0
	for rIdx, run := range runs {
		if !sc.canAdd(4) {
			for _, rem := range runs[rIdx:] {
				cappedJobs += max(1, len(nvSlice(rem, "jobs")))
			}
			break
		}
		jobs := nvSlice(run, "jobs")
		if len(jobs) == 0 {
			watchChildren = append(watchChildren, sc.text(sc.claim("omp.tool.notice"), "waiting for workflow jobs..."))
			continue
		}
		for jIdx, rj := range jobs {
			if !sc.canAdd(4) {
				cappedJobs += len(jobs) - jIdx
				break
			}
			jm := nvToMap(rj)
			if jm == nil {
				continue
			}
			name := nvPlainLine(nvStr(jm, "name"))
			state := nvStr(jm, "conclusion")
			if state == "" {
				state = nvStr(jm, "status")
			}
			parts := nvCompactStrings([]string{name, state})
			if dur, ok := nvFloat(jm, "durationSeconds"); ok {
				parts = append(parts, fmt.Sprintf("%.0fs", dur))
			}
			watchChildren = append(watchChildren, sc.text(sc.indexed("omp.tool.github.job", jobIdx), strings.Join(parts, " · ")))
			if expanded {
				steps := nvSlice(jm, "steps")
				for sIdx, rs := range steps {
					if !sc.canAdd(2) {
						watchChildren = append(watchChildren, sc.text(sc.claim("omp.tool.stats"), fmt.Sprintf("+%d more steps", len(steps)-sIdx)))
						break
					}
					sm := nvToMap(rs)
					if sm == nil {
						continue
					}
					sState := nvStr(sm, "conclusion")
					if sState == "" {
						sState = nvStr(sm, "status")
					}
					stepLine := fmt.Sprintf("#%d %s · %s", nvInt(sm, "number"), nvPlainLine(nvStr(sm, "name")), sState)
					if sd, ok := nvFloat(sm, "durationSeconds"); ok {
						stepLine += fmt.Sprintf(" %.0fs", sd)
					}
					jobIdx++
					watchChildren = append(watchChildren, sc.text(sc.indexed("omp.tool.github.job", jobIdx), stepLine))
				}
			}
			jobIdx++
		}
	}
	if cappedJobs > 0 {
		watchChildren = append(watchChildren, sc.text(sc.claim("omp.tool.stats"), fmt.Sprintf("+%d more jobs", cappedJobs)))
	}
	for i, rf := range nvSlice(watch, "failedLogs") {
		if !sc.canAdd(3) {
			break
		}
		fm := nvToMap(rf)
		if fm == nil {
			continue
		}
		jobName := nvPlainLine(nvStr(fm, "jobName"))
		runID := nvInt(fm, "runId")
		wf := nvPlainLine(nvStr(fm, "workflowName"))
		ctx := fmt.Sprintf("run #%d", runID)
		if wf != "" {
			ctx = fmt.Sprintf("%s #%d", wf, runID)
		}
		header := fmt.Sprintf("%s · %s", jobName, ctx)
		tail := nvStr(fm, "tail")
		if !nvBool(fm, "available") || strings.TrimSpace(tail) == "" {
			reason := nvPlainLine(nvStr(fm, "unavailableReason"))
			if reason == "" {
				reason = "log unavailable"
			}
			watchChildren = append(watchChildren, sc.lines(sc.indexed("omp.tool.github.log", i), []string{header, reason}))
		} else {
			logLines := append([]string{header}, nvSanitizeLines(tail)...)
			watchChildren = append(watchChildren, sc.lines(sc.indexed("omp.tool.github.log", i), logLines))
		}
	}

	return sc.container(sc.claim("omp.tool.github"), sc.vstack(sc.claim("omp.tool.github.watch"), 1, watchChildren...))
}

// ── debug (tools/debug.ts) ───────────────────────────────────────────────────

func nativeDebugCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.debug.call")), nil
}

// nativeDebugResult ports describeDebugResult in packages/tui/src/tools/debug.ts:134-174.
func nativeDebugResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	output := nvResultText(res)
	if rc.IsError {
		msg := output
		if msg == "" {
			msg = "Debug failed"
		}
		return sc.container(sc.claim("omp.tool.debug"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}

	details := nvToMap(res.Details)
	var children []kit.Node
	if snap := nvMap(details, "snapshot"); snap != nil {
		var loc string
		if src := nvStr(snap, "sourcePath", "sourceName"); src != "" {
			loc = src
			if line := nvInt(snap, "line"); line > 0 {
				loc += fmt.Sprintf(":%d", line)
				if col := nvInt(snap, "column"); col > 0 {
					loc += fmt.Sprintf(":%d", col)
				}
			}
		}
		var cfgNote string
		if nvBool(snap, "needsConfigurationDone") {
			cfgNote = "pending configurationDone; set breakpoints, then continue."
		}
		var exitStr string
		if ec := nvIntPtr(snap, "exitCode"); ec != nil {
			exitStr = fmt.Sprintf("%d", *ec)
		}
		pairs := [][2]string{
			{"Session", nvStr(snap, "id")},
			{"Adapter", nvStr(snap, "adapter")},
			{"Status", nvStr(snap, "status")},
			{"CWD", nvStr(snap, "cwd")},
			{"Program", nvStr(snap, "program")},
			{"Stop reason", nvStr(snap, "stopReason")},
			{"Frame", nvStr(snap, "frameName")},
			{"Instruction pointer", nvStr(snap, "instructionPointerReference")},
			{"Location", loc},
			{"Configuration", cfgNote},
			{"Exit code", exitStr},
		}
		var items []kit.ListItem
		for _, pair := range pairs {
			if v := nvPlainLine(pair[1]); v != "" {
				items = append(items, kit.ListItem{Label: pair[0], Columns: []string{v}})
			}
		}
		if len(items) > 0 {
			children = append(children, sc.listLines(sc.claim("omp.tool.debug.snapshot"), items))
		}
	}

	if strings.TrimSpace(output) != "" {
		children = append(children, sc.lines(sc.claim("omp.tool.debug.output"), nvSanitizeLines(output)))
	} else {
		children = append(children, sc.text(sc.claim("omp.tool.notice"), "No output"))
	}
	return sc.container(sc.claim("omp.tool.debug"), children...), nil
}

// ── resolve / reject / propose / report_issue (tools/resolve.ts) ─────────────

func nativeResolveCall(defaultAction string) CallFn {
	return func(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
		sc := newNVScope()
		var children []kit.Node
		if reason := strings.TrimSpace(nvPlainText(nvStr(args, "reason"))); reason != "" {
			children = append(children, sc.text(sc.claim("omp.tool.stats"), reason))
		}
		_ = defaultAction
		return sc.container(sc.claim("omp.tool.resolve.call"), children...), nil
	}
}

func nativeResolveResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	reason := strings.TrimSpace(nvPlainText(nvStr(details, "reason")))
	if reason == "" {
		reason = "No reason provided"
	}
	if rc.IsError {
		errText := strings.TrimSpace(nvResultText(res))
		if errText != "" {
			return sc.container(sc.claim("omp.tool.resolve"), nvErrorNode(sc, sc.claim("omp.tool.error"), errText)), nil
		}
	}
	return sc.container(sc.claim("omp.tool.resolve"), sc.text(sc.claim("omp.tool.stats"), reason)), nil
}

func nativeTextArgCall(toolName, field string) CallFn {
	return func(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
		sc := newNVScope()
		_ = field
		return sc.container(sc.claim("omp.tool." + toolName + ".call")), nil
	}
}

func nativeTextArgResult(toolName string) ResultFn {
	return func(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
		sc := newNVScope()
		output := strings.TrimSpace(nvPlainText(nvResultText(res)))
		var children []kit.Node
		if output != "" {
			if rc.IsError {
				children = append(children, nvErrorNode(sc, sc.claim("omp.tool.error"), output))
			} else {
				children = append(children, sc.text(sc.claim("omp.tool.output"), output))
			}
		}
		return sc.container(sc.claim("omp.tool."+toolName), children...), nil
	}
}

// ── memory: retain / recall / reflect (tools/memory.ts) ──────────────────────

func nvRetainContents(args map[string]any) []string {
	var out []string
	for _, raw := range nvSlice(args, "items") {
		if im := nvToMap(raw); im != nil {
			if content := strings.TrimSpace(nvPlainText(nvStr(im, "content"))); content != "" {
				out = append(out, content)
			}
		}
	}
	return out
}

func nativeRetainCall(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	var children []kit.Node
	for i, c := range nvRetainContents(args) {
		if !sc.canAdd(1) {
			break
		}
		children = append(children, sc.text(sc.indexed("omp.memory.item", i), c))
	}
	return sc.container(sc.claim("omp.tool.memory.call"), children...), nil
}

func nativeRetainResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	if rc.IsError {
		msg := strings.TrimSpace(nvResultText(res))
		if msg == "" {
			msg = "Retain failed"
		}
		return sc.container(sc.claim("omp.tool.memory"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}
	var children []kit.Node
	for i, c := range nvRetainContents(rc.Args) {
		if !sc.canAdd(1) {
			break
		}
		children = append(children, sc.text(sc.indexed("omp.memory.item", i), c))
	}
	return sc.container(sc.claim("omp.tool.memory"), children...), nil
}

func nativeRecallCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.memory.call")), nil
}

func nativeRecallResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	output := strings.TrimSpace(nvResultText(res))
	if rc.IsError {
		if output == "" {
			output = "Recall failed"
		}
		return sc.container(sc.claim("omp.tool.memory"), nvErrorNode(sc, sc.claim("omp.tool.error"), output)), nil
	}
	// Strip first "Found N relevant..." summary line, keep up to OUTPUT_EXPANDED lines.
	body := output
	if _, rest, ok := strings.Cut(output, "\n"); ok {
		body = strings.TrimLeft(rest, "\n")
	}
	lines := nvSanitizeLines(body)
	if len(lines) > nvOutputExpandedLines {
		lines = lines[:nvOutputExpandedLines]
	}
	var children []kit.Node
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		children = append(children, sc.text(sc.indexed("omp.memory.item", i), l))
	}
	return sc.container(sc.claim("omp.tool.memory"), children...), nil
}

func nativeReflectCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.memory.call")), nil
}

func nativeReflectResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	answer := strings.TrimSpace(nvResultText(res))
	if rc.IsError {
		if answer == "" {
			answer = "Reflect failed"
		}
		return sc.container(sc.claim("omp.tool.memory"), nvErrorNode(sc, sc.claim("omp.tool.error"), answer)), nil
	}
	lines := nvSanitizeLines(answer)
	if len(lines) > nvOutputExpandedLines {
		lines = lines[:nvOutputExpandedLines]
	}
	shown := strings.TrimSpace(strings.Join(lines, "\n"))
	var children []kit.Node
	if shown != "" {
		children = append(children, sc.md(sc.claim("omp.memory.reflect"), shown))
	}
	return sc.container(sc.claim("omp.tool.memory"), children...), nil
}

// ── think (tools/think.ts) ───────────────────────────────────────────────────

func nativeThinkCall(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	thoughts := nvPlainText(nvStr(args, "thoughts"))
	if thoughts == "" {
		return sc.container(sc.claim("omp.tool.think")), nil
	}
	return sc.container(sc.claim("omp.tool.think"), sc.md(sc.claim("omp.think"), thoughts)), nil
}

func nativeThinkResult(_ sdk.Context, _ sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	// upstream: tools/think.ts:43-45 — describeResult returns undefined.
	return kit.NewContainer(), nil
}

// ── goal (tools/goal.ts) ─────────────────────────────────────────────────────

func nativeGoalCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.goal.call")), nil
}

func nativeGoalResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	if rc.IsError {
		msg := strings.TrimSpace(nvResultText(res))
		if msg == "" {
			msg = "Goal tool failed"
		}
		return sc.container(sc.claim("omp.tool.goal"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg)), nil
	}
	details := nvToMap(res.Details)
	goal := nvMap(details, "goal")
	if goal == nil {
		return sc.container(sc.claim("omp.tool.goal")), nil
	}
	var children []kit.Node
	if obj := strings.TrimSpace(nvPlainText(nvStr(goal, "objective"))); obj != "" {
		children = append(children, sc.text(sc.claim("omp.tool.context"), fmt.Sprintf("%q", obj)))
	}
	used := nvInt(goal, "tokensUsed")
	var meta []string
	if budget, ok := nvFloat(goal, "tokenBudget"); ok && budget > 0 {
		b := int(budget)
		left := max(0, b-used)
		meta = append(meta, fmt.Sprintf("%s / %s tokens (%s left)", nvFormatNumber(used), nvFormatNumber(b), nvFormatNumber(left)))
	} else {
		meta = append(meta, fmt.Sprintf("%s tokens", nvFormatNumber(used)))
	}
	if timeSec, ok := nvFloat(goal, "timeUsedSeconds"); ok && timeSec > 0 {
		meta = append(meta, fmt.Sprintf("%s elapsed", nvFormatDuration(int64(timeSec*1000))))
	}
	if statsNode := nvStatsNode(sc, meta); statsNode != nil {
		children = append(children, statsNode)
	}
	if report := strings.TrimSpace(nvPlainText(nvStr(details, "completionBudgetReport"))); report != "" {
		children = append(children, sc.text(sc.claim("omp.tool.notice"), report))
	}
	return sc.container(sc.claim("omp.tool.goal"), children...), nil
}

// ── yield (tools/yield.ts) ───────────────────────────────────────────────────

func nvDescribeYieldData(sc *nvScope, data any, hasData bool) kit.Node {
	if !hasData {
		return nil
	}
	if data == nil {
		return sc.lines(sc.claim("omp.tool.yield.data"), []string{"null"})
	}
	if s, ok := data.(string); ok {
		clean := strings.TrimSpace(nvPlainText(s))
		if clean == "" {
			return sc.text(sc.claim("omp.tool.notice"), "(empty)")
		}
		return sc.md(sc.claim("omp.tool.yield.data"), clean)
	}
	return sc.lines(sc.claim("omp.tool.yield.data"), nvPrettyJSON(data))
}

func nativeYieldCall(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	var children []kit.Node
	if errMsg := strings.TrimSpace(nvStr(args, "error")); errMsg != "" {
		children = append(children, nvErrorNode(sc, sc.claim("omp.tool.error"), errMsg))
	}
	if data, ok := args["data"]; ok {
		if dn := nvDescribeYieldData(sc, data, true); dn != nil {
			children = append(children, dn)
		}
	}
	return sc.container(sc.claim("omp.tool.yield.call"), children...), nil
}

func nativeYieldResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	var errMsg string
	if rc.IsError {
		errMsg = strings.TrimSpace(nvResultText(res))
		if errMsg == "" {
			errMsg = "Submit Result failed"
		}
	} else if nvStr(details, "status") == "aborted" {
		errMsg = strings.TrimSpace(nvStr(details, "error"))
		if errMsg == "" {
			errMsg = strings.TrimSpace(nvResultText(res))
		}
		if errMsg == "" {
			errMsg = "Aborted"
		}
	} else {
		errMsg = strings.TrimSpace(nvStr(rc.Args, "error"))
	}

	var data any
	hasData := false
	if !rc.IsError && nvHasKey(details, "data") {
		data = details["data"]
		hasData = true
	} else if rc.Args != nil {
		if v, ok := rc.Args["data"]; ok {
			data = v
			hasData = true
		}
	}

	var children []kit.Node
	if errMsg != "" {
		children = append(children, nvErrorNode(sc, sc.claim("omp.tool.error"), errMsg))
	}
	if hasData {
		if dn := nvDescribeYieldData(sc, data, true); dn != nil {
			children = append(children, dn)
		}
	} else if errMsg == "" {
		// upstream: tools/yield.ts:86-90
		rawType := details["type"]
		if rawType == nil {
			rawType = rc.Args["type"]
		}
		note := "Ends with the sections submitted earlier, or the last message"
		if arr, ok := rawType.([]any); ok && len(arr) > 0 {
			note = "Takes the last message as this section"
		}
		children = append(children, sc.text(sc.claim("omp.tool.notice"), note))
	}
	return sc.container(sc.claim("omp.tool.yield"), children...), nil
}

// ── default / checkpoint / rewind / browser (tools/default-renderer.ts) ──────

func nvFormatArgsInline(args map[string]any, maxChars int) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		if k == "i" || k == "__partialJson" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	var pairs []string
	for _, k := range keys {
		b, err := json.Marshal(args[k])
		val := fmt.Sprint(args[k])
		if err == nil {
			val = string(b)
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, nvPlainLine(val)))
	}
	return nvTruncateRunes(strings.Join(pairs, ", "), maxChars)
}

func nativeDefaultCall(toolName string) CallFn {
	return func(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
		sc := newNVScope()
		var children []kit.Node
		if summary := nvFormatArgsInline(args, 160); summary != "" {
			children = append(children, sc.text(sc.claim("omp.tool.args"), summary))
		}
		return sc.container(sc.claim("omp.tool."+toolName+".call"), children...), nil
	}
}

// nvIsBenignSkipped mirrors packages/tui/src/chat/tool-execution.ts:2440-2444:
// a tool skipped due to an interrupt is rendered as info/output, not error.
func nvIsBenignSkipped(details map[string]any) bool {
	if nvStr(details, "source") != "interrupt_skipped" {
		return false
	}
	return nvBool(details, "__synthetic") || (nvBool(details, "__interrupted") && nvStr(details, "execution") == "started")
}

func nativeDefaultResult(toolName string) ResultFn {
	return func(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
		sc := newNVScope()
		details := nvToMap(res.Details)
		skipped := nvIsBenignSkipped(details)
		output := strings.TrimRight(nvResultText(res), " \t\n")
		if strings.TrimSpace(output) == "" {
			return sc.container(sc.claim("omp.tool."+toolName), sc.text(sc.claim("omp.tool.notice"), "(no output)")), nil
		}
		if rc.IsError && !skipped {
			return sc.container(sc.claim("omp.tool."+toolName), nvErrorNode(sc, sc.claim("omp.tool.error"), output)), nil
		}
		// Pretty-print JSON objects/arrays cleanly; otherwise raw sanitized lines.
		trimmed := strings.TrimSpace(output)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
			(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			var parsed any
			if json.Unmarshal([]byte(trimmed), &parsed) == nil {
				return sc.container(sc.claim("omp.tool."+toolName), sc.lines(sc.claim("omp.tool.output"), nvPrettyJSON(parsed))), nil
			}
		}
		return sc.container(sc.claim("omp.tool."+toolName), sc.lines(sc.claim("omp.tool.output"), nvSanitizeLines(output))), nil
	}
}
