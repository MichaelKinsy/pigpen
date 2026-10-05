package websearch

import (
	"fmt"
	"strconv"
)

// One-line labels of tool calls (the renderCall of index.ts) as plain text lines: the host
// applies its own styling.

func truncateLabel(s string, max int) string {
	if jsLen(s) > max {
		return jsSlice(s, 0, max-3) + "..."
	}
	return s
}

func moreLines(prefix string, items []string, max int) []string {
	var out []string
	for _, it := range items[:min(max, len(items))] {
		out = append(out, prefix+it)
	}
	if len(items) > max {
		out = append(out, fmt.Sprintf("%s... and %d more", prefix, len(items)-max))
	}
	return out
}

// RenderCall labels one call of the named tool (its public name).
func (r *Runtime) RenderCall(tool string, args map[string]any) []string {
	switch tool {
	case r.cfg.Names.WebSearch:
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
			return []string{"search (no query)"}
		case 1:
			return []string{`search "` + queries[0] + `"`}
		}
		quoted := make([]string, len(queries))
		for i, q := range queries {
			quoted[i] = `"` + q + `"`
		}
		return append([]string{fmt.Sprintf("search %d queries", len(queries))}, moreLines("  ", quoted, 5)...)
	case r.cfg.Names.FetchContent:
		n, err := NormalizeFetchContentParams(args)
		if err != nil {
			return []string{"fetch (invalid parameters)"}
		}
		if len(n.URLList) == 0 {
			return []string{"fetch (no URL)"}
		}
		var lines []string
		if len(n.URLList) == 1 {
			lines = []string{"fetch " + n.URLList[0]}
		} else {
			lines = append([]string{fmt.Sprintf("fetch %d URLs", len(n.URLList))}, moreLines("  ", n.URLList, 5)...)
		}
		o := n.Options
		if o.Mode != "" && o.Mode != "readable" {
			lines = append(lines, "  mode: "+o.Mode)
		}
		if o.Timestamp != "" {
			lines = append(lines, "  timestamp: "+o.Timestamp)
		}
		if o.Frames != 0 {
			lines = append(lines, "  frames: "+strconv.Itoa(o.Frames))
		}
		if o.Prompt != "" {
			lines = append(lines, `  prompt: "`+truncateLabel(o.Prompt, 250)+`"`)
		}
		if o.Model != "" {
			lines = append(lines, "  model: "+o.Model)
		}
		if o.AnswerModel != "" {
			lines = append(lines, "  answer model: "+o.AnswerModel)
		}
		if o.Auth != nil {
			if o.Auth.True {
				lines = append(lines, "  auth: true")
			} else {
				lines = append(lines, "  auth: "+o.Auth.Name)
			}
		}
		return lines
	case r.cfg.Names.GetSearchContent:
		target := ""
		id, _ := args["responseId"].(string)
		switch {
		case str(args["query"]) != "":
			target = `query="` + str(args["query"]) + `"`
		case args["queryIndex"] != nil:
			target = "queryIndex=" + inputValue(args["queryIndex"])
		case str(args["url"]) != "":
			target = truncateLabel(str(args["url"]), 30)
			if jsLen(str(args["url"])) > 30 {
				target = jsSlice(str(args["url"]), 0, 27) + "..."
			}
		case args["urlIndex"] != nil:
			target = "urlIndex=" + inputValue(args["urlIndex"])
		}
		if args["offset"] != nil {
			if target != "" {
				target += " @ " + inputValue(args["offset"])
			} else {
				target = "offset=" + inputValue(args["offset"])
			}
		}
		if ft, ok := args["findText"]; ok && ft != nil {
			n := 1
			if list, ok := ft.([]any); ok {
				n = len(list)
			}
			if target != "" {
				target += " · "
			}
			target += fmt.Sprintf("find %d", n)
		}
		if target == "" {
			target = jsSlice(id, 0, 8)
		}
		return []string{"get_content " + target}
	}
	return []string{tool}
}
