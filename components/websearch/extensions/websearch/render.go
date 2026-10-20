package websearch

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// The TUI renderers of the tools (renderCall and renderResult of index.ts:2172-2424, 2741-2878 and
// 3184-3264) and the shared error renderer renderSearchErrorPlan (index.ts:135-155). They return
// pi-tui component trees (kit nodes) whose text carries the theme's colors, so the host draws what
// the original's Text and Box components draw.

// plainTheme applies no styling: the plain text of a rendered call.
type plainTheme struct{}

func (plainTheme) Fg(_, text string) string { return text }
func (plainTheme) Bg(_, text string) string { return text }
func (plainTheme) Bold(text string) string  { return text }

// toolTitle is theme.fg("toolTitle", theme.bold(text)).
func toolTitle(th WidgetTheme, text string) string { return th.Fg("toolTitle", th.Bold(text)) }

func truncateLabel(s string, max int) string {
	if jsLen(s) > max {
		return jsSlice(s, 0, max-3) + "..."
	}
	return s
}

// truthy is JavaScript truthiness of a decoded JSON value.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true
}

// jsPrint is String(value) for a decoded JSON value, as a template literal prints it.
func jsPrint(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return jsNumber(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = jsPrint(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// jsOpt prints an optional number or string the way a template literal prints `details?.x`.
func jsOpt(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return "undefined"
	}
	return jsPrint(v)
}

func numField(m map[string]any, key string) (float64, bool) {
	f, ok := m[key].(float64)
	return f, ok
}

func moreLines(th WidgetTheme, token string, prefix string, items []string, max int) []string {
	var out []string
	for _, it := range items[:min(max, len(items))] {
		out = append(out, th.Fg(token, prefix+it))
	}
	if len(items) > max {
		out = append(out, th.Fg(token, fmt.Sprintf("%s... and %d more", prefix, len(items)-max)))
	}
	return out
}

// RenderCall is the plain text of renderCall for the tool with its public name, one entry per line.
func (r *Runtime) RenderCall(tool string, args map[string]any) []string {
	text, err := r.callText(tool, args, plainTheme{})
	if err != nil {
		return []string{tool}
	}
	return strings.Split(text, "\n")
}

// callText is the text of renderCall: the lines of the call label joined by newlines.
func (r *Runtime) callText(tool string, args map[string]any, th WidgetTheme) (string, error) {
	switch tool {
	case r.cfg.Names.WebSearch:
		return r.searchCallText(args, th), nil
	case r.cfg.Names.FetchContent:
		return fetchCallText(args, th), nil
	case r.cfg.Names.GetSearchContent:
		return getContentCallText(args, th)
	}
	return "", fmt.Errorf("no renderer for %s", tool)
}

func (r *Runtime) searchCallText(args map[string]any, th WidgetTheme) string {
	var raw []any
	if qs, ok := args["queries"].([]any); ok {
		raw = qs
	} else if q, ok := args["query"].(string); ok {
		for _, e := range expandQueryString(q) {
			raw = append(raw, e)
		}
	}
	queries := normalizeQueryList(raw)
	switch len(queries) {
	case 0:
		return toolTitle(th, "search ") + th.Fg("error", "(no query)")
	case 1:
		return toolTitle(th, "search ") + th.Fg("accent", `"`+queries[0]+`"`)
	}
	lines := []string{toolTitle(th, "search ") + th.Fg("accent", fmt.Sprintf("%d queries", len(queries)))}
	for _, q := range queries[:min(5, len(queries))] {
		lines = append(lines, th.Fg("muted", `  "`+q+`"`))
	}
	if len(queries) > 5 {
		lines = append(lines, th.Fg("muted", fmt.Sprintf("  ... and %d more", len(queries)-5)))
	}
	return strings.Join(lines, "\n")
}

func fetchCallText(args map[string]any, th WidgetTheme) string {
	n, err := NormalizeFetchContentParams(args)
	if err != nil {
		return toolTitle(th, "fetch ") + th.Fg("error", "(invalid parameters)")
	}
	if len(n.URLList) == 0 {
		return toolTitle(th, "fetch ") + th.Fg("error", "(no URL)")
	}
	var lines []string
	if len(n.URLList) == 1 {
		lines = append(lines, toolTitle(th, "fetch ")+th.Fg("accent", n.URLList[0]))
	} else {
		lines = append(lines, toolTitle(th, "fetch ")+th.Fg("accent", fmt.Sprintf("%d URLs", len(n.URLList))))
		lines = append(lines, moreLines(th, "muted", "  ", n.URLList, 5)...)
	}
	o := n.Options
	if o.Mode != "" && o.Mode != "readable" {
		lines = append(lines, th.Fg("dim", "  mode: ")+th.Fg("warning", o.Mode))
	}
	if o.Timestamp != "" {
		lines = append(lines, th.Fg("dim", "  timestamp: ")+th.Fg("warning", o.Timestamp))
	}
	if o.Frames != 0 {
		lines = append(lines, th.Fg("dim", "  frames: ")+th.Fg("warning", strconv.Itoa(o.Frames)))
	}
	if o.Prompt != "" {
		lines = append(lines, th.Fg("dim", "  prompt: ")+th.Fg("muted", `"`+truncateLabel(o.Prompt, 250)+`"`))
	}
	if o.Model != "" {
		lines = append(lines, th.Fg("dim", "  model: ")+th.Fg("warning", o.Model))
	}
	if o.AnswerModel != "" {
		lines = append(lines, th.Fg("dim", "  answer model: ")+th.Fg("warning", o.AnswerModel))
	}
	if o.Auth != nil {
		auth := o.Auth.Name
		if o.Auth.True {
			auth = "true"
		}
		lines = append(lines, th.Fg("dim", "  auth: ")+th.Fg("warning", auth))
	}
	return strings.Join(lines, "\n")
}

func getContentCallText(args map[string]any, th WidgetTheme) (string, error) {
	target := ""
	has := func(k string) bool { _, ok := args[k]; return ok }
	switch {
	case jsTruthy(args["query"]):
		target = `query="` + jsPrint(args["query"]) + `"`
	case has("queryIndex"):
		target = "queryIndex=" + jsPrint(args["queryIndex"])
	case jsTruthy(args["url"]):
		target = jsPrint(args["url"])
		if u, ok := args["url"].(string); ok && jsLen(u) > 30 {
			target = jsSlice(u, 0, 27) + "..."
		}
	case has("urlIndex"):
		target = "urlIndex=" + jsPrint(args["urlIndex"])
	}
	if has("offset") {
		if target != "" {
			target += " @ " + jsPrint(args["offset"])
		} else {
			target = "offset=" + jsPrint(args["offset"])
		}
	}
	if ft, ok := args["findText"]; ok {
		n := 1
		if list, isList := ft.([]any); isList {
			n = len(list)
		}
		if target != "" {
			target += " · "
		}
		target += fmt.Sprintf("find %d", n)
	}
	if target == "" {
		id, ok := args["responseId"].(string)
		if !ok {
			return "", fmt.Errorf("responseId is not a string")
		}
		target = jsSlice(id, 0, 8)
	}
	return toolTitle(th, "get_content ") + th.Fg("accent", target), nil
}

// ---- results -------------------------------------------------------------------------------------

// RenderedText is a Text component with no padding.
func textNode(s string) kit.Node { return kit.NewText(s, 0, 0) }

// searchErrorNode is renderSearchErrorPlan: the expanded plan as one text, the collapsed plan as a
// box on the error background with a hint to expand.
func searchErrorNode(plan *SearchErrorPlan, expanded bool, th WidgetTheme) kit.Node {
	if expanded {
		lines := make([]string, len(plan.Expanded))
		for i, l := range plan.Expanded {
			token := "toolOutput"
			if i == 0 {
				token = "error"
			}
			lines[i] = th.Fg(token, l)
		}
		return textNode(strings.Join(lines, "\n"))
	}
	box := kit.NewBox(1, 0)
	box.Bg = "toolErrorBg"
	box.AddChild(textNode(th.Fg("error", plan.Expanded[0])))
	for _, l := range plan.Collapsed {
		box.AddChild(textNode(th.Fg("dim", l)))
	}
	if plan.ExpandHint != nil {
		box.AddChild(textNode(th.Fg("muted", *plan.ExpandHint)))
	}
	return box
}

func firstText(content []map[string]any) string {
	for _, c := range content {
		if c["type"] == "text" {
			s, _ := c["text"].(string)
			return s
		}
	}
	return ""
}

func detailsMapOf(details any) map[string]any {
	m, _ := details.(map[string]any)
	return m
}

func progressBar(progress float64) (string, error) {
	n := int(math.Floor(progress * 10))
	if n < 0 || n > 10 {
		return "", fmt.Errorf("invalid progress %v", progress)
	}
	return strings.Repeat("\u2588", n) + strings.Repeat("\u2591", 10-n), nil
}

func previewText(text string, max int) string {
	if jsLen(text) > max {
		return jsSlice(text, 0, max) + "..."
	}
	return text
}

// RenderResult is renderResult of the tool with its public name.
func (r *Runtime) RenderResult(tool string, content []map[string]any, details any, expanded, partial bool, th WidgetTheme) (kit.Node, error) {
	d := detailsMapOf(details)
	switch tool {
	case r.cfg.Names.WebSearch:
		return r.searchResultNode(content, d, expanded, partial, th)
	case r.cfg.Names.FetchContent:
		return r.fetchResultNode(content, d, expanded, partial, th)
	case r.cfg.Names.GetSearchContent:
		return getContentResultNode(content, d, expanded, th)
	}
	return nil, fmt.Errorf("no renderer for %s", tool)
}

func errorPlanFrom(d map[string]any, extras []string) *SearchErrorPlan {
	msg, _ := d["error"].(string)
	return BuildSearchErrorPlan(&SearchErrorDetails{Error: &msg, ExtraLines: extras})
}

func (r *Runtime) searchResultNode(content []map[string]any, d map[string]any, expanded, partial bool, th WidgetTheme) (kit.Node, error) {
	phase, _ := d["phase"].(string)
	if partial {
		shortcut := r.cfg.CurateShortcut
		if s, ok := d["shortcut"].(string); ok {
			shortcut = s
		}
		timeout, hasTimeout := numField(d, "timeoutSeconds")
		hasTimeout = hasTimeout && timeout != 0
		url, _ := d["curatorUrl"].(string)
		switch phase {
		case "curator-fallback":
			lines := []string{th.Fg("warning", "Open the search curator manually:")}
			if url != "" {
				lines = append(lines, th.Fg("muted", "  "+url))
			}
			if e, _ := d["browserOpenError"].(string); e != "" {
				lines = append(lines, th.Fg("dim", "  auto-open failed: "+e))
			}
			if hasTimeout {
				lines = append(lines, th.Fg("dim", fmt.Sprintf("  auto-submits after %ss idle; %s reopens", jsNumber(timeout), shortcut)))
			} else {
				lines = append(lines, th.Fg("dim", "  "+shortcut+" reopens"))
			}
			return textNode(strings.Join(lines, "\n")), nil
		case "curating", "waiting-for-approval", "generating-summary":
			phaseText := "waiting for summary approval in browser..."
			switch phase {
			case "generating-summary":
				phaseText = "generating summary draft..."
			case "waiting-for-approval":
				phaseText = "summary draft ready; approve in browser..."
			}
			lines := []string{th.Fg("accent", phaseText)}
			if url != "" {
				lines = append(lines, th.Fg("muted", "  "+url))
			}
			if hasTimeout {
				lines = append(lines, th.Fg("dim", fmt.Sprintf("  auto-submits after %ss idle; %s reopens", jsNumber(timeout), shortcut)))
			} else {
				lines = append(lines, th.Fg("dim", "  "+shortcut+" reopens"))
			}
			return textNode(strings.Join(lines, "\n")), nil
		}
		progress, _ := numField(d, "progress")
		bar, err := progressBar(progress)
		if err != nil {
			return nil, err
		}
		if phase == "searching" {
			query, _ := d["currentQuery"].(string)
			if jsLen(query) > 40 {
				query = jsSlice(query, 0, 37) + "..."
			}
			return textNode(th.Fg("accent", fmt.Sprintf("[%s] %s", bar, query))), nil
		}
		label := phase
		if label == "" {
			label = "searching"
		}
		return textNode(th.Fg("accent", fmt.Sprintf("[%s] %s", bar, label))), nil
	}

	if jsTruthy(d["error"]) {
		if plan := BuildSearchErrorPlan(searchErrorDetailsFrom(d)); plan != nil {
			return searchErrorNode(plan, expanded, th), nil
		}
		return textNode(th.Fg("error", "Error: "+jsPrint(d["error"]))), nil
	}

	queryInfo := ""
	if n, ok := numField(d, "queryCount"); !ok || n != 1 {
		queryInfo = fmt.Sprintf("%s/%s queries, ", jsOpt(d, "successfulQueries"), jsOpt(d, "queryCount"))
	}
	total := "0"
	if v, ok := d["totalResults"]; ok && v != nil {
		total = jsPrint(v)
	}
	statusLine := th.Fg("success", queryInfo+total+" sources")
	if jsTruthy(d["curated"]) && jsTruthy(d["curatedFrom"]) {
		statusLine += th.Fg("muted", fmt.Sprintf(" (%s/%s queries curated)", jsOpt(d, "queryCount"), jsPrint(d["curatedFrom"])))
	}
	fetchURLs, hasFetchURLs := d["fetchUrls"].([]any)
	switch {
	case jsTruthy(d["fetchId"]) && hasFetchURLs:
		statusLine += th.Fg("muted", fmt.Sprintf(" (fetching %d URLs)", len(fetchURLs)))
	case jsTruthy(d["fetchId"]):
		statusLine += th.Fg("muted", " (content ready)")
	}

	lines := []string{statusLine}
	summary, _ := d["summary"].(map[string]any)
	summaryText, _ := summary["text"].(string)
	if summaryText != "" {
		lines = append(lines, "", th.Fg("accent", fmt.Sprintf("── Summary (%s) ", jsPrint(summary["workflow"]))+strings.Repeat("─", 32)), "")
		for _, line := range strings.Split(summaryText, "\n") {
			lines = append(lines, "  "+line)
		}
		lines = append(lines, "")
		model := "model=deterministic"
		if jsTruthy(summary["model"]) {
			model = "model=" + jsPrint(summary["model"])
		}
		fallback := "fallback=false"
		if jsTruthy(summary["fallbackUsed"]) {
			fallback = "fallback=true"
		}
		phaseMeta := ""
		if jsTruthy(summary["phase"]) {
			phaseMeta = "phase=" + jsPrint(summary["phase"])
		}
		edited := "edited=false"
		if jsTruthy(summary["edited"]) {
			edited = "edited=true"
		}
		parts := []string{model, "duration=" + jsOpt(summary, "durationMs") + "ms", "tokens~" + jsOpt(summary, "tokenEstimate"), fallback, phaseMeta, edited}
		if jsTruthy(summary["fallbackReason"]) {
			parts = append(parts, "reason="+jsPrint(summary["fallbackReason"]))
		}
		var nonEmpty []string
		for _, p := range parts {
			if p != "" {
				nonEmpty = append(nonEmpty, p)
			}
		}
		lines = append(lines, th.Fg("dim", "  "+strings.Join(nonEmpty, " · ")))
	}

	curated, _ := d["curatedQueries"].([]any)
	if len(curated) > 0 {
		kept := len(curated)
		from := kept
		if f, ok := numField(d, "curatedFrom"); ok && f != 0 {
			from = int(f)
		}
		lines = append(lines, "", th.Fg("accent", fmt.Sprintf("\u2500\u2500 Curated Results (%d of %d queries kept) ", kept, from)+strings.Repeat("\u2500", 24)))
		for _, item := range curated {
			cq, _ := item.(map[string]any)
			lines = append(lines, "")
			query, _ := cq["query"].(string)
			dq := query
			if jsLen(query) > 65 {
				dq = jsSlice(query, 0, 62) + "..."
			}
			providerLabel := ""
			if p, _ := cq["provider"].(string); p != "" {
				providerLabel = " (" + p + ")"
			}
			lines = append(lines, th.Fg("accent", `  "`+dq+`"`+providerLabel))
			answer, _ := cq["answer"].(string)
			switch {
			case jsTruthy(cq["error"]):
				lines = append(lines, th.Fg("error", "  "+jsPrint(cq["error"])))
			case answer != "":
				lines = append(lines, "")
				for _, line := range strings.Split(answer, "\n") {
					lines = append(lines, "  "+line)
				}
			}
			if sources, _ := cq["sources"].([]any); len(sources) > 0 {
				lines = append(lines, "")
				for _, src := range sources {
					s, _ := src.(map[string]any)
					url, _ := s["url"].(string)
					domain := domainOfLabel(url)
					t, _ := s["title"].(string)
					if jsLen(t) > 50 {
						t = jsSlice(t, 0, 47) + "..."
					}
					lines = append(lines, th.Fg("muted", "  \u25b8 "+t)+th.Fg("dim", " \u00b7 "+domain))
				}
			}
		}
		lines = append(lines, "")
	} else {
		for _, line := range strings.Split(previewText(firstText(content), 500), "\n") {
			lines = append(lines, th.Fg("dim", line))
		}
	}

	if len(fetchURLs) > 0 {
		if jsTruthy(d["curated"]) {
			lines = append(lines, th.Fg("muted", fmt.Sprintf("Fetching %d URLs in background", len(fetchURLs))))
		} else {
			lines = append(lines, th.Fg("muted", "Fetching:"))
			for _, u := range fetchURLs[:min(5, len(fetchURLs))] {
				s, _ := u.(string)
				lines = append(lines, th.Fg("dim", "  "+truncateLabel(s, 60)))
			}
			if len(fetchURLs) > 5 {
				lines = append(lines, th.Fg("dim", fmt.Sprintf("  ... and %d more", len(fetchURLs)-5)))
			}
		}
	}

	totalLines := len(lines)
	if expanded {
		return textNode(strings.Join(lines, "\n")), nil
	}

	box := kit.NewBox(1, 0)
	box.AddChild(textNode(statusLine))
	collapsedLines := 1
	summaryPreview := jsTrim(summaryText)
	switch {
	case summaryPreview != "":
		box.AddChild(textNode(th.Fg("dim", preview2(summaryPreview, 120))))
		collapsedLines++
	case len(curated) > 0:
		for _, item := range curated[:min(3, len(curated))] {
			cq, _ := item.(map[string]any)
			query, _ := cq["query"].(string)
			dq := query
			if jsLen(query) > 55 {
				dq = jsSlice(query, 0, 52) + "..."
			}
			sources, _ := cq["sources"].([]any)
			suffix := th.Fg("dim", fmt.Sprintf(" · %d sources", len(sources)))
			if jsTruthy(cq["error"]) {
				suffix = th.Fg("error", " (error)")
			}
			box.AddChild(textNode(th.Fg("accent", `  "`+dq+`"`) + suffix))
			collapsedLines++
		}
		if len(curated) > 3 {
			box.AddChild(textNode(th.Fg("dim", fmt.Sprintf("  ... and %d more", len(curated)-3))))
			collapsedLines++
		}
	default:
		var first string
		for _, l := range strings.Split(firstText(content), "\n") {
			t := jsTrim(l)
			if t != "" && !strings.HasPrefix(t, "[") && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "---") {
				first = l
				break
			}
		}
		fallback := strings.ReplaceAll(jsTrim(first), "**", "")
		if fallback != "" {
			box.AddChild(textNode(th.Fg("dim", preview2(fallback, 120))))
			collapsedLines++
		}
	}
	if more := max(0, totalLines-collapsedLines); more > 0 {
		box.AddChild(textNode(th.Fg("muted", fmt.Sprintf("\n... (%d more lines, %d total, ctrl+o to expand)", more, totalLines))))
	}
	return box, nil
}

// previewText2 shortens text to max UTF-16 units: the first max-3 plus "...".
func preview2(text string, max int) string {
	if jsLen(text) > max {
		return jsSlice(text, 0, max-3) + "..."
	}
	return text
}

// domainOfLabel is url.replace(/^https?:\/\//, "").replace(/\/.*$/, "").
func domainOfLabel(url string) string {
	url = httpSchemeRE.ReplaceAllString(url, "")
	if i := strings.Index(url, "/"); i >= 0 {
		url = url[:i]
	}
	return url
}

// searchErrorDetailsFrom reads the diagnostics of a web_search error result.
func searchErrorDetailsFrom(d map[string]any) *SearchErrorDetails {
	out := &SearchErrorDetails{}
	if e, ok := d["error"].(string); ok {
		out.Error = &e
	}
	out.Cancelled = d["cancelled"] == true
	out.CancelReason, _ = d["cancelReason"].(string)
	if b, ok := d["browserConnected"].(bool); ok {
		out.BrowserConnected = &b
	}
	if f, ok := d["lastHeartbeatAgeMs"].(float64); ok {
		out.LastHeartbeatAgeMs = &f
	}
	out.QueryCount, _ = d["queryCount"].(float64)
	if list, ok := d["cancelledQueries"].([]any); ok {
		for _, item := range list {
			m, _ := item.(map[string]any)
			q := CancelledQueryDetail{}
			q.Query, _ = m["query"].(string)
			q.Provider, _ = m["provider"].(string)
			q.Error, _ = m["error"].(string)
			if n, ok := m["resultCount"].(float64); ok {
				q.ResultCount = int(n)
			}
			out.CancelledQueries = append(out.CancelledQueries, q)
		}
	}
	if list, ok := d["extraLines"].([]any); ok {
		for _, item := range list {
			if s, ok := item.(string); ok {
				out.ExtraLines = append(out.ExtraLines, s)
			}
		}
	}
	return out
}

func (r *Runtime) fetchResultNode(content []map[string]any, d map[string]any, expanded, partial bool, th WidgetTheme) (kit.Node, error) {
	if partial {
		progress, _ := numField(d, "progress")
		bar, err := progressBar(progress)
		if err != nil {
			return nil, err
		}
		phase, _ := d["phase"].(string)
		if phase == "" {
			phase = "fetching"
		}
		return textNode(th.Fg("accent", fmt.Sprintf("[%s] %s", bar, phase))), nil
	}
	if jsTruthy(d["error"]) {
		var extras []string
		_, hasCount := d["urlCount"].(float64)
		_, hasOK := d["successful"].(float64)
		if hasCount || hasOK {
			ok, _ := numField(d, "successful")
			count, _ := numField(d, "urlCount")
			extras = append(extras, fmt.Sprintf("urls: %s/%s succeeded", jsNumber(ok), jsNumber(count)))
		}
		if id, _ := d["responseId"].(string); id != "" {
			extras = append(extras, "response id: "+id)
		}
		if urls, _ := d["urls"].([]any); len(urls) > 0 {
			for _, u := range urls[:min(8, len(urls))] {
				extras = append(extras, "  \u25b8 "+jsPrint(u))
			}
			if len(urls) > 8 {
				extras = append(extras, fmt.Sprintf("  ... and %d more", len(urls)-8))
			}
		}
		if plan := errorPlanFrom(d, extras); plan != nil {
			return searchErrorNode(plan, expanded, th), nil
		}
		return textNode(th.Fg("error", "Error: "+jsPrint(d["error"]))), nil
	}

	text := firstText(content)
	if n, ok := numField(d, "urlCount"); ok && n == 1 {
		t := "Untitled"
		if s, _ := d["title"].(string); s != "" {
			t = s
		}
		imgCount := 0.0
		if c, ok := numField(d, "imageCount"); ok {
			imgCount = c
		} else if jsTruthy(d["hasImage"]) {
			imgCount = 1
		}
		badge := ""
		switch {
		case imgCount > 1:
			badge = th.Fg("accent", fmt.Sprintf(" [%s images]", jsNumber(imgCount)))
		case imgCount == 1:
			badge = th.Fg("accent", " [image]")
		}
		chars := "0"
		if v, ok := d["totalChars"]; ok && v != nil {
			chars = jsPrint(v)
		}
		statusLine := th.Fg("success", t) + th.Fg("muted", fmt.Sprintf(" (%s chars)", chars)) + badge
		if jsTruthy(d["truncated"]) {
			statusLine += th.Fg("warning", " [truncated]")
		}
		if dur, ok := numField(d, "duration"); ok {
			statusLine += th.Fg("muted", " | "+FormatSeconds(int(math.Floor(dur)))+" total")
		}
		if !expanded {
			return textNode(statusLine + "\n" + th.Fg("dim", previewText(text, 200))), nil
		}
		lines := []string{statusLine}
		if p, _ := d["prompt"].(string); p != "" {
			lines = append(lines, th.Fg("dim", `  prompt: "`+truncateLabel(p, 250)+`"`))
		}
		if ts, _ := d["timestamp"].(string); ts != "" {
			lines = append(lines, th.Fg("dim", "  timestamp: "+ts))
		}
		if f, ok := numField(d, "frames"); ok {
			lines = append(lines, th.Fg("dim", "  frames: "+jsNumber(f)))
		}
		lines = append(lines, th.Fg("dim", previewText(text, 500)))
		return textNode(strings.Join(lines, "\n")), nil
	}

	token := "error"
	if ok, _ := numField(d, "successful"); ok > 0 {
		token = "success"
	}
	stored := " (content fetched)"
	if r.cfg.GetSearchContent {
		stored = " (content stored)"
	}
	statusLine := th.Fg(token, jsOpt(d, "successful")+"/"+jsOpt(d, "urlCount")+" URLs") + th.Fg("muted", stored)
	if !expanded {
		return textNode(statusLine), nil
	}
	return textNode(statusLine + "\n" + th.Fg("dim", previewText(text, 500))), nil
}

func getContentResultNode(content []map[string]any, d map[string]any, expanded bool, th WidgetTheme) (kit.Node, error) {
	if jsTruthy(d["error"]) {
		var extras []string
		if q, _ := d["query"].(string); q != "" {
			extras = append(extras, "query: "+q)
		}
		if u, _ := d["url"].(string); u != "" {
			extras = append(extras, "url: "+u)
		} else if t, _ := d["title"].(string); t != "" {
			extras = append(extras, "resource: "+t)
		}
		if plan := errorPlanFrom(d, extras); plan != nil {
			return searchErrorNode(plan, expanded, th), nil
		}
		return textNode(th.Fg("error", "Error: "+jsPrint(d["error"]))), nil
	}
	first := func(keys ...string) string {
		for _, k := range keys {
			if jsTruthy(d[k]) {
				return jsPrint(d[k])
			}
		}
		return ""
	}
	var statusLine string
	if matches, ok := numField(d, "matchCount"); ok {
		name := first("title", "query")
		if name == "" {
			name = "Content"
		}
		shown := "0"
		if v, ok := d["returnedMatches"]; ok && v != nil {
			shown = jsPrint(v)
		}
		statusLine = th.Fg("success", name) + th.Fg("muted", fmt.Sprintf(" (%s matches, %s shown)", jsNumber(matches), shown))
	} else if jsTruthy(d["query"]) {
		statusLine = th.Fg("success", `"`+jsPrint(d["query"])+`"`) + th.Fg("muted", fmt.Sprintf(" (%s results)", jsOpt(d, "resultCount")))
	} else {
		start, _ := numField(d, "offset")
		returned := 0.0
		if v, ok := numField(d, "returnedChars"); ok {
			returned = v
		} else if v, ok := numField(d, "contentLength"); ok {
			returned = v
		}
		end := start + returned
		slice := ""
		if _, has := d["nextOffset"]; has || start > 0 {
			slice = fmt.Sprintf(", showing %s-%s", jsNumber(start), jsNumber(end))
		}
		name := first("title")
		if name == "" {
			name = "Content"
		}
		length := "0"
		if v, ok := d["contentLength"]; ok && v != nil {
			length = jsPrint(v)
		}
		statusLine = th.Fg("success", name) + th.Fg("muted", fmt.Sprintf(" (%s chars%s)", length, slice))
	}
	if !expanded {
		return textNode(statusLine), nil
	}
	return textNode(statusLine + "\n" + th.Fg("dim", previewText(firstText(content), 500))), nil
}
