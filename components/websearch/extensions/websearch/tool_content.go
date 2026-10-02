package websearch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// get_search_content (index.ts:2880-3190): bounded pages of stored search results, fetched
// content and research artifacts, and findText.

func marshalIndentNoEscape(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return strings.TrimRight(b.String(), "\n")
}

func normalizeFindQueries(value any) ([]string, error) {
	var raw []any
	switch v := value.(type) {
	case []any:
		raw = v
	default:
		raw = []any{v}
	}
	var out []string
	for _, e := range raw {
		s, _ := e.(string)
		if t := jsTrim(s); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("findText must contain at least one non-empty string")
	}
	return out, nil
}

// number reads an optional numeric parameter; ok is false when it is absent or null.
func number(params map[string]any, key string) (v any, present bool) {
	x, ok := params[key]
	if !ok || x == nil {
		return nil, false
	}
	return x, true
}

func isJSInteger(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return 0, false
	}
	return int(f), true
}

func findDetails(base map[string]any, f FindResult) map[string]any {
	base["matchCount"] = f.MatchCount
	base["returnedMatches"] = f.ReturnedMatches
	base["queryResults"] = f.QueryResults
	return base
}

func (r *Runtime) getSearchContent(raw map[string]any, maxChars int) (ToolOutput, error) {
	params := map[string]any{}
	for k, v := range raw {
		params[k] = v
	}
	var findMode FindMode
	if v, ok := params["findMode"]; ok && v != nil {
		s, _ := v.(string)
		if s != "exact" && s != "case-insensitive" && s != "fuzzy" {
			return ToolOutput{}, errors.New(`findMode must be "exact", "case-insensitive", or "fuzzy"`)
		}
		findMode = FindMode(s)
	}
	if s, ok := params["query"].(string); ok && jsTrim(s) == "" {
		delete(params, "query")
	}
	if s, ok := params["url"].(string); ok && jsTrim(s) == "" {
		delete(params, "url")
	}
	findText, hasFind := number(params, "findText")
	if hasFind {
		delete(params, "offset")
		delete(params, "limit")
	}
	mode := findMode
	if mode == "" {
		mode = FindCaseInsensitive
	}
	responseID, _ := params["responseId"].(string)
	rid := inputValue(params["responseId"])

	if findMode != "" && !hasFind {
		return textOutput(fmt.Sprintf("findMode %s requires findText; provide findText or omit findMode.", inputValue(string(findMode))),
			map[string]any{"error": "findMode requires findText"}), nil
	}
	data := GetResult(responseID)
	if data == nil {
		return textOutput(fmt.Sprintf("Error: No stored results for responseId %s. Use a responseId returned by %s.", rid, r.storedContentSources()),
			map[string]any{"error": "Not found", "responseId": params["responseId"]}), nil
	}

	// page validates offset and limit; it returns the numbers or the error output.
	page := func(subject string, length int) (offset, limit int, out *ToolOutput) {
		offset, limit = 0, maxChars
		var off any = 0.0
		if v, ok := number(params, "offset"); ok {
			off = v
		}
		var lim any = float64(maxChars)
		if v, ok := number(params, "limit"); ok {
			lim = v
		}
		o, okO := isJSInteger(off)
		if !okO || o < 0 {
			t := textOutput(fmt.Sprintf("Invalid offset: received %s for %s; offset must be a non-negative integer. Use 0 or a larger integer.", inputValue(off), subject),
				map[string]any{"error": "Invalid offset", "offset": off})
			return 0, 0, &t
		}
		l, okL := isJSInteger(lim)
		if !okL || l <= 0 || l > maxChars {
			t := textOutput(fmt.Sprintf("Invalid limit: received %s for %s; limit must be an integer from 1 to %d. Use a value in that range.", inputValue(lim), subject, maxChars),
				map[string]any{"error": "Invalid limit", "limit": lim, "maxLimit": maxChars})
			return 0, 0, &t
		}
		return o, l, nil
	}
	nextOffset := func(hasMore bool, end int) any {
		if hasMore {
			return end
		}
		return nil
	}

	switch {
	case data.Type == "research":
		artifact := GetResearchArtifact(responseID)
		if artifact == nil {
			return textOutput(fmt.Sprintf("Error: stored research artifact for responseId %s was not found. Use a responseId returned by %s.", rid, r.storedContentSources()),
				map[string]any{"error": "Artifact not found", "responseId": params["responseId"]}), nil
		}
		serialized := marshalIndentNoEscape(artifact)
		total := jsLen(serialized)
		if hasFind {
			queries, err := normalizeFindQueries(findText)
			if err == nil {
				found := findContent(serialized, queries, mode)
				return textOutput(found.Text, findDetails(map[string]any{"responseId": artifact.ID, "type": "research", "contentLength": total, "findMode": string(mode)}, found)), nil
			}
			return textOutput(fmt.Sprintf("Unable to find %s in research artifact for responseId %s: %s. Check findText and use a supported findMode.", inputValue(findText), rid, err),
				map[string]any{"error": err.Error(), "responseId": params["responseId"], "type": "research"}), nil
		}
		subject := "responseId " + rid
		offset, limit, bad := page(subject, total)
		if bad != nil {
			return *bad, nil
		}
		if offset > total {
			return textOutput(fmt.Sprintf("Offset %d is out of range for responseId %s. Received offset %d; valid range is 0-%d. Use an offset within that range.", offset, rid, offset, total),
				map[string]any{"error": "Offset out of range", "offset": offset, "contentLength": total}), nil
		}
		end := offset + limit
		if total < end {
			end = total
		}
		slice := jsSlice(serialized, offset, end)
		hasMore := end < total
		return textOutput(slice, map[string]any{"responseId": artifact.ID, "type": "research", "contentLength": total, "offset": offset, "limit": limit,
			"returnedChars": jsLen(slice), "nextOffset": nextOffset(hasMore, end), "truncated": hasMore}), nil

	case data.Type == "search" && data.Queries != nil:
		return r.searchContent(data, params, hasFind, findText, mode, maxChars, page, nextOffset), nil

	case data.Type == "fetch" && data.URLs != nil:
		return r.fetchedContent(data, params, hasFind, findText, mode, page, nextOffset), nil
	}
	return textOutput(fmt.Sprintf("Invalid stored data for responseId %s: received type %s. Use a responseId returned by %s.", rid, inputValue(data.Type), r.storedContentSources()),
		map[string]any{"error": "Invalid data"}), nil
}

type pageFunc func(subject string, length int) (int, int, *ToolOutput)

func (r *Runtime) searchContent(data *StoredSearchData, params map[string]any, hasFind bool, findText any, mode FindMode, maxChars int, page pageFunc, nextOffset func(bool, int) any) ToolOutput {
	rid := inputValue(params["responseId"])
	var q *QueryResultData
	qIndex := -1
	if v, ok := params["query"].(string); ok {
		for i := range data.Queries {
			if data.Queries[i].Query == v {
				q, qIndex = &data.Queries[i], i
				break
			}
		}
		if q == nil {
			var avail []string
			for _, x := range data.Queries {
				avail = append(avail, `"`+x.Query+`"`)
			}
			list := strings.Join(avail, ", ")
			if list == "" {
				list = "none"
			}
			return textOutput(fmt.Sprintf("Query %s was not found for responseId %s. Received query=%s. Available queries: %s. Use one of the available queries or queryIndex.", inputValue(v), rid, inputValue(v), list),
				map[string]any{"error": "Query not found"})
		}
	} else if v, ok := number(params, "queryIndex"); ok {
		idx, isInt := isJSInteger(v)
		if isInt && idx >= 0 && idx < len(data.Queries) {
			q, qIndex = &data.Queries[idx], idx
		} else {
			var avail []string
			for i, x := range data.Queries {
				avail = append(avail, fmt.Sprintf(`%d: "%s"`, i, x.Query))
			}
			list := strings.Join(avail, ", ")
			if list == "" {
				list = "none"
			}
			return textOutput(fmt.Sprintf("Query index %s is out of range for responseId %s. Received queryIndex=%s; valid indexes are 0-%d. Available queries: %s. Use one of the available indexes.", inputValue(v), rid, inputValue(v), len(data.Queries)-1, list),
				map[string]any{"error": "Index out of range"})
		}
	} else {
		var avail []string
		for i, x := range data.Queries {
			avail = append(avail, fmt.Sprintf(`%d: "%s"`, i, x.Query))
		}
		list := strings.Join(avail, ", ")
		if list == "" {
			list = "none"
		}
		return textOutput(fmt.Sprintf("Specify query or queryIndex for responseId %s. Available queries: %s.", rid, list), map[string]any{"error": "No query specified"})
	}
	if q.Error != nil {
		return textOutput(fmt.Sprintf("Error retrieving query %s from responseId %s: %s. Check the stored search result and retry with another query or queryIndex if needed.", inputValue(q.Query), rid, *q.Error),
			map[string]any{"error": *q.Error, "query": q.Query})
	}
	full := formatFullResults(*q)
	total := jsLen(full)
	if hasFind {
		queries, err := normalizeFindQueries(findText)
		if err == nil {
			found := findContent(full, queries, mode)
			return textOutput(found.Text, findDetails(map[string]any{"responseId": params["responseId"], "query": q.Query, "resultCount": len(q.Results), "contentLength": total, "findMode": string(mode)}, found))
		}
		return textOutput(fmt.Sprintf("Unable to find %s in query %s for responseId %s: %s. Check findText and use a supported findMode.", inputValue(findText), inputValue(q.Query), rid, err),
			map[string]any{"error": err.Error(), "query": q.Query})
	}
	subject := "query " + inputValue(q.Query)
	offset, limit, bad := page(subject, total)
	if bad != nil {
		return *bad
	}
	if offset > total {
		return textOutput(fmt.Sprintf("Offset %d is out of range for query %s in responseId %s. Received offset %d; valid range is 0-%d. Use an offset within that range.", offset, inputValue(q.Query), rid, offset, total),
			map[string]any{"error": "Offset out of range", "offset": offset, "contentLength": total})
	}
	returned := limit
	if total-offset < returned {
		returned = total - offset
	}
	end := offset + returned
	continuation := ""
	for end < total {
		continuation = fmt.Sprintf("\n\n---\nShowing chars %d-%d of %d. Use %s({ responseId: \"%s\", queryIndex: %d, offset: %d, limit: %d }) for the next slice.",
			offset, end, total, r.cfg.Names.GetSearchContent, str(params["responseId"]), qIndex, end, limit)
		overflow := returned + jsLen(continuation) - maxChars
		if overflow <= 0 {
			break
		}
		returned -= overflow
		end = offset + returned
	}
	slice := jsSlice(full, offset, end)
	hasMore := end < total
	text := slice
	if hasMore {
		text += continuation
	}
	return textOutput(text, map[string]any{"responseId": params["responseId"], "query": q.Query, "resultCount": len(q.Results), "contentLength": total,
		"offset": offset, "limit": limit, "returnedChars": returned, "nextOffset": nextOffset(hasMore, end), "truncated": hasMore})
}

func str(v any) string { s, _ := v.(string); return s }

func (r *Runtime) fetchedContent(data *StoredSearchData, params map[string]any, hasFind bool, findText any, mode FindMode, page pageFunc, nextOffset func(bool, int) any) ToolOutput {
	rid := inputValue(params["responseId"])
	var u *ExtractedContent
	sel := -1
	if v, ok := params["url"].(string); ok {
		for i := range data.URLs {
			if data.URLs[i].URL == v {
				u, sel = &data.URLs[i], i
				break
			}
		}
		if u == nil {
			var avail []string
			for _, x := range data.URLs {
				avail = append(avail, x.URL)
			}
			list := "  none"
			if len(avail) > 0 {
				list = strings.Join(avail, "\n  ")
			}
			return textOutput(fmt.Sprintf("URL %s was not found for responseId %s. Received url=%s. Available URLs:\n  %s\nUse one of the available URLs or urlIndex.", inputValue(v), rid, inputValue(v), list),
				map[string]any{"error": "URL not found"})
		}
	} else if v, ok := number(params, "urlIndex"); ok {
		idx, isInt := isJSInteger(v)
		if isInt && idx >= 0 && idx < len(data.URLs) {
			u, sel = &data.URLs[idx], idx
		} else {
			var avail []string
			for i, x := range data.URLs {
				avail = append(avail, fmt.Sprintf("%d: %s", i, x.URL))
			}
			list := "  none"
			if len(avail) > 0 {
				list = strings.Join(avail, "\n  ")
			}
			return textOutput(fmt.Sprintf("URL index %s is out of range for responseId %s. Received urlIndex=%s; valid indexes are 0-%d. Available URLs:\n  %s\nUse one of the available indexes.", inputValue(v), rid, inputValue(v), len(data.URLs)-1, list),
				map[string]any{"error": "Index out of range"})
		}
	} else {
		var avail []string
		for i, x := range data.URLs {
			avail = append(avail, fmt.Sprintf("%d: %s", i, x.URL))
		}
		list := "  none"
		if len(avail) > 0 {
			list = strings.Join(avail, "\n  ")
		}
		return textOutput(fmt.Sprintf("Specify url or urlIndex for responseId %s. Available URLs:\n  %s", rid, list), map[string]any{"error": "No URL specified"})
	}
	if u.Error != nil {
		return textOutput(fmt.Sprintf("Error retrieving URL %s from responseId %s: %s. Check the stored fetch result and retry with another URL or urlIndex if needed.", inputValue(u.URL), rid, *u.Error),
			map[string]any{"error": *u.Error, "url": u.URL})
	}
	total := jsLen(u.Content)
	title := u.Title
	if title == "" {
		title = u.URL
	}
	if hasFind {
		queries, err := normalizeFindQueries(findText)
		if err == nil {
			found := findContent(u.Content, queries, mode)
			return textOutput("# "+title+"\n\n"+found.Text, findDetails(map[string]any{"url": u.URL, "title": u.Title, "contentLength": total, "findMode": string(mode)}, found))
		}
		return textOutput(fmt.Sprintf("Unable to find %s in URL %s for responseId %s: %s. Check findText and use a supported findMode.", inputValue(findText), inputValue(u.URL), rid, err),
			map[string]any{"error": err.Error(), "url": u.URL})
	}
	offset, limit, bad := page("URL "+inputValue(u.URL), total)
	if bad != nil {
		return *bad
	}
	if offset > total {
		return textOutput(fmt.Sprintf("Offset %d is out of range for URL %s in responseId %s. Received offset %d; valid range is 0-%d. Use an offset within that range.", offset, inputValue(u.URL), rid, offset, total),
			map[string]any{"error": "Offset out of range", "offset": offset, "contentLength": total})
	}
	end := offset + limit
	if total < end {
		end = total
	}
	slice := jsSlice(u.Content, offset, end)
	hasMore := end < total
	text := "# " + title + "\n\n" + slice
	if hasMore || offset > 0 {
		text += fmt.Sprintf("\n\n---\nShowing chars %d-%d of %d.", offset, end, total)
		if hasMore {
			text += fmt.Sprintf(" Use %s({ responseId: \"%s\", urlIndex: %d, offset: %d, limit: %d }) for the next slice.", r.cfg.Names.GetSearchContent, str(params["responseId"]), sel, end, limit)
		}
	}
	return textOutput(text, map[string]any{"url": u.URL, "title": u.Title, "contentLength": total, "offset": offset, "limit": limit,
		"returnedChars": jsLen(slice), "nextOffset": nextOffset(hasMore, end), "truncated": hasMore})
}

func formatFullResults(q QueryResultData) string {
	out := fmt.Sprintf("## Results for: \"%s\"\n\n", q.Query)
	providers := q.Providers
	if providers == nil && q.Provider != "" {
		providers = []string{q.Provider}
	}
	if len(providers) > 0 {
		s := ""
		if len(providers) != 1 {
			s = "s"
		}
		out += fmt.Sprintf("**Provider%s:** %s\n\n", s, strings.Join(providers, ", "))
	}
	if q.Answer != "" {
		out += q.Answer + "\n\n---\n\n"
	}
	for _, r := range q.Results {
		out += "### " + r.Title + "\n" + r.URL
		if r.Snippet != "" {
			out += "\n\n" + r.Snippet
		}
		out += "\n\n"
	}
	return out
}
