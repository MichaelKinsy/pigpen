package websearch

import (
	"context"
	"fmt"
	"strings"
)

// fetch_content (index.ts:2525-2790).

func initialContentSlice(content string, maxChars int) (text string, endOffset, totalBytes, totalLines, shownBytes, shownLines int) {
	total := jsLen(content)
	endOffset = total
	if maxChars < endOffset {
		endOffset = maxChars
	}
	if endOffset < total {
		if lb := jsLastIndexOf(content, "\n", endOffset); lb >= maxChars*8/10 {
			endOffset = lb + 1
		}
	}
	text = jsSlice(content, 0, endOffset)
	lines := func(s string) int {
		if s == "" {
			return 0
		}
		return strings.Count(s, "\n") + 1
	}
	return text, endOffset, len(content), lines(content), len(text), lines(text)
}

func (r *Runtime) fetchContent(ctx context.Context, params map[string]any, update func(ToolOutput)) (ToolOutput, error) {
	normalized, err := NormalizeFetchContentParams(params)
	if err != nil {
		return errorOutput(err.Error(), err.Error(), nil), nil
	}
	urlList, opts := normalized.URLList, normalized.Options
	mode := opts.Mode
	if mode == "" {
		mode = r.cfg.DefaultMode
	}
	if !sliceHas(r.cfg.AllowedModes, mode) {
		msg := fmt.Sprintf(`Fetch mode "%s" is disabled by fetch.allowedModes.`, mode)
		return errorOutput(msg, msg, nil), nil
	}
	ctx, err = ScopeProxy(ctx, opts.Proxy)
	if err != nil {
		return ToolOutput{}, err
	}
	switch {
	case mode == "answer" && opts.Prompt == "":
		return errorOutput("mode answer requires prompt.", "mode answer requires prompt", nil), nil
	case mode == "raw" && (opts.ForceClone != nil && *opts.ForceClone || opts.Timestamp != "" || opts.Frames != 0 || opts.Prompt != "" || opts.Model != "" || opts.AnswerModel != ""):
		return errorOutput("mode raw cannot be combined with forceClone, prompt, timestamp, frames, model, or answerModel.", "Incompatible raw mode options", nil), nil
	case mode != "answer" && opts.AnswerModel != "":
		return errorOutput("answerModel requires mode answer.", "answerModel requires mode answer", nil), nil
	case mode == "answer" && opts.Model != "":
		return errorOutput("use answerModel, not model, with mode answer.", "model is incompatible with mode answer", nil), nil
	case mode == "answer" && opts.Auth != nil:
		return errorOutput("auth cannot be combined with mode answer.", "auth cannot be combined with mode answer", nil), nil
	}
	if opts.Auth != nil {
		msg := "authFetch profiles (local browser-cookie fetching) are not available in this Go port; no browser cookies are read"
		return errorOutput(msg, msg, nil), nil
	}
	if len(urlList) == 0 {
		return errorOutput("No URL provided.", "No URL provided", nil), nil
	}
	if update != nil {
		update(textOutput(fmt.Sprintf("Fetching %d URL(s)...", len(urlList)), map[string]any{"phase": "fetch", "progress": 0}))
	}

	ex := ExtractOptions{Mode: mode, Frames: opts.Frames, Timestamp: opts.Timestamp, ForceClone: opts.ForceClone != nil && *opts.ForceClone, Model: opts.Model, ToolNames: r.registeredNames()}
	if mode != "answer" {
		ex.Prompt = opts.Prompt
	}
	fetched := FetchAllContent(ctx, urlList, ex)
	if ctx.Err() != nil {
		return ToolOutput{}, abortError(ctx.Err())
	}

	presented := fetched
	if mode == "answer" {
		presented = make([]ExtractedContent, len(fetched))
		for i, res := range fetched {
			switch {
			case res.Error != nil:
			case res.Thumbnail != nil || strings.HasPrefix(res.MimeType, "image/"):
				res.Error = errp("Page answer requires textual fetched content")
			default:
				res.Error = errp("Page answer failed: answering from a page with a model is not available in this Go port yet (planned for a later slice)")
			}
			presented[i] = res
		}
	}
	successful, totalChars := 0, 0
	for _, p := range presented {
		if p.Error == nil {
			successful++
		}
		totalChars += jsLen(p.Content)
	}

	responseID := GenerateID()
	data := &StoredSearchData{ID: responseID, Type: "fetch", Timestamp: nowMs(), URLs: stripThumbnails(fetched)}
	r.host.AppendEntry("web-search-results", StoreFetchedContentResult(responseID, data))

	optional := func(d map[string]any) map[string]any {
		if opts.Prompt != "" {
			d["prompt"] = opts.Prompt
		}
		if opts.Timestamp != "" {
			d["timestamp"] = opts.Timestamp
		}
		if opts.Frames != 0 {
			d["frames"] = opts.Frames
		}
		return d
	}

	if len(urlList) == 1 {
		res := presented[0]
		if res.Error != nil {
			return textOutput("Error: "+*res.Error, optional(map[string]any{"urls": urlList, "urlCount": 1, "successful": 0, "error": *res.Error, "responseId": responseID})), nil
		}
		full := jsLen(res.Content)
		slice, end, totalBytes, totalLines, shownBytes, shownLines := initialContentSlice(res.Content, MaxInlineContentChars(currentRoot()))
		truncated := end < full
		output := slice
		if truncated {
			output += fmt.Sprintf("\n\n---\nShowing %d of %d chars, %d of %d bytes, and %d of %d lines. ", end, full, shownBytes, totalBytes, shownLines, totalLines)
			if r.cfg.GetSearchContent {
				output += fmt.Sprintf(`Use %s({ responseId: "%s", urlIndex: 0, offset: %d }) for the next slice.`, r.cfg.Names.GetSearchContent, responseID, end)
			} else {
				output += "Content retrieval is not registered."
			}
		}
		var content []ContentBlock
		if res.Thumbnail != nil {
			content = append(content, ContentBlock{Type: "image", Data: res.Thumbnail.Data, MimeType: res.Thumbnail.MimeType})
		}
		content = append(content, ContentBlock{Type: "text", Text: output})
		imageCount := 0
		if res.Thumbnail != nil {
			imageCount = 1
		}
		details := optional(map[string]any{"urls": urlList, "urlCount": 1, "successful": 1, "totalChars": full, "title": res.Title, "responseId": responseID,
			"truncated": truncated, "hasImage": imageCount > 0, "imageCount": imageCount, "mode": mode,
			"totalBytes": totalBytes, "totalLines": totalLines, "shownBytes": shownBytes, "shownLines": shownLines})
		if res.MimeType != "" {
			details["mimeType"] = res.MimeType
		}
		if res.Status != nil {
			details["status"] = *res.Status
		}
		if res.Duration != nil {
			details["duration"] = *res.Duration
		}
		return ToolOutput{Content: content, Details: details}, nil
	}

	var b strings.Builder
	b.WriteString("## Fetched URLs\n\n")
	for _, p := range presented {
		if p.Error != nil {
			fmt.Fprintf(&b, "- %s: Error - %s\n", p.URL, *p.Error)
		} else {
			title := p.Title
			if title == "" {
				title = p.URL
			}
			fmt.Fprintf(&b, "- %s (%d chars)\n", title, jsLen(p.Content))
		}
	}
	if r.cfg.GetSearchContent {
		fmt.Fprintf(&b, "\n---\nUse %s({ responseId: \"%s\", urlIndex: 0 }) to retrieve bounded content slices.", r.cfg.Names.GetSearchContent, responseID)
	} else {
		b.WriteString("\n---\nContent retrieval is not registered.")
	}
	return textOutput(b.String(), map[string]any{"urls": urlList, "urlCount": len(urlList), "successful": successful, "totalChars": totalChars, "responseId": responseID}), nil
}

// currentRoot re-reads web-search.json (fetch_content applies maxInlineContentChars at call time,
// get_search_content at registration time, as the original does); an unreadable config gives
// the defaults.
func currentRoot() map[string]any {
	root, err := ReadConfigRoot()
	if err != nil || root == nil {
		return map[string]any{}
	}
	return root
}
