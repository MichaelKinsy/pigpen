package ansi

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// Native views for file tools (`read`, `write`, `edit`, `apply_patch`), URL
// reads (`fetch`), and internal URL cards (`proc://`, `cfg://`, `agent://`).
//
// upstream:
//   packages/tui/src/tools/read.ts (describeCall, describeResult)
//   packages/tui/src/tools/write.ts (describeCall, describeResult)
//   packages/tui/src/tools/edit.ts (describeCall, describeResult)
//   packages/tui/src/tools/fetch.ts (describeReadUrlCall, describeReadUrlResult)
//   packages/tui/src/tools/proc-render.ts (describeProcRead, describeProcWrite, describeAgentWrite)
//   packages/tui/src/tools/cfg-render.ts (describeCfgRead, describeCfgWrite)

func init() {
	registerNative("read", Builder{Call: nativeReadCall, Result: nativeReadResult})
	registerNative("fetch", Builder{Call: nativeFetchCall, Result: nativeFetchResult})
	registerNative("write", Builder{Call: nativeWriteCall, Result: nativeWriteResult})
	registerNative("edit", Builder{Call: nativeEditCall, Result: nativeEditResult})
	registerNative("apply_patch", Builder{Call: nativeEditCall, Result: nativeEditResult})
}

// ── read (file, image, URL, proc://, cfg://) ─────────────────────────────────

func nvReadRawPath(args map[string]any) string {
	return nvStr(args, "file_path", "path", "url")
}

func nvIsReadMarkdown(details map[string]any, rawPath string, args map[string]any) bool {
	if nvBool(details, "raw") || nvBool(args, "raw") {
		return false
	}
	base, sel := nvSplitPathAndSel(rawPath)
	if strings.Contains(strings.ToLower(sel), "raw") {
		return false
	}
	ct := strings.ToLower(nvStr(details, "contentType"))
	if strings.Contains(ct, "markdown") {
		return true
	}
	ext := nvFileExt(base)
	return ext == "md" || ext == "markdown"
}

func nativeReadCall(_ sdk.Context, args map[string]any, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	rawPath := nvReadRawPath(args)
	scheme, rest, isURL := nvSplitURLScheme(rawPath)
	if isURL {
		switch scheme {
		case "proc":
			return nvDescribeProcRead(sc, rest, nil, nil, false), nil
		case "cfg":
			base, _ := nvSplitPathAndSel(rawPath)
			return nvDescribeCfgRead(sc, base, nil, nil, false), nil
		case "http", "https":
			return sc.container(sc.claim("omp.tool.fetch.call")), nil
		}
	}
	// upstream: tools/read.ts:790-802 — inline head only while pending.
	return sc.container(sc.claim("omp.tool.read.call")), nil
}

func nativeReadResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	rawPath := nvReadRawPath(rc.Args)
	scheme, rest, isURL := nvSplitURLScheme(rawPath)

	// Route proc:// and cfg:// reads (either by URL scheme or Details key).
	if (isURL && scheme == "proc") || nvMap(details, "proc") != nil {
		target := rest
		if !isURL {
			target = ""
		}
		target, _ = nvSplitPathAndSel(target)
		return nvDescribeProcRead(sc, target, &res, nvMap(details, "proc"), rc.IsError), nil
	}
	if (isURL && scheme == "cfg") || nvMap(details, "cfg") != nil {
		base, _ := nvSplitPathAndSel(rawPath)
		return nvDescribeCfgRead(sc, base, &res, nvMap(details, "cfg"), rc.IsError), nil
	}
	// Route http(s) URL reads.
	if nvStr(details, "kind") == "url" || nvIsHTTPURL(rawPath) {
		return nvDescribeReadURLResult(sc, res, details, rc), nil
	}

	// Error read: upstream chat/read-tool-group.ts:812-818, tools/read.ts:823-826,
	// tools/native-view.ts:108-125 (ERROR_GIST_CHARS = 120).
	// Single-line errors of <= 120 chars go into the head's `— <gist>` meta with
	// no body; longer or multi-line errors carry omp.tool.error in the body.
	if rc.IsError {
		msg := strings.TrimPrefix(strings.TrimSpace(nvResultText(res)), "Error:")
		msg = strings.TrimSpace(msg)
		if msg == "" {
			msg = "Unknown error"
		}
		full := strings.TrimSpace(nvPlainText(msg))
		if full == "" {
			full = "failed"
		}
		first, hasNewline := firstLineAndMultiline(full)
		if !hasNewline && len([]rune(first)) <= 120 {
			return sc.container(sc.claim("omp.tool.read")), nil
		}
		return sc.container(sc.claim("omp.tool.read"), nvErrorNode(sc, sc.claim("omp.tool.error"), full)), nil
	}

	// Footnote: resolvedPath, firstLineExceedsLimit, meta.truncation / artifactError.
	// upstream: tools/read.ts:830-840
	meta := nvMap(details, "meta")
	trunc := nvMap(meta, "truncation")
	fallbackTrunc := nvMap(details, "truncation")
	if trunc == nil && fallbackTrunc != nil {
		// internal/files/read.go may write top-level truncation; honor both shapes.
		trunc = fallbackTrunc
		if meta == nil {
			meta = map[string]any{"truncation": trunc}
		} else {
			metaCopy := make(map[string]any, len(meta)+1)
			for k, v := range meta {
				metaCopy[k] = v
			}
			metaCopy["truncation"] = trunc
			meta = metaCopy
		}
	}
	firstLineTooLong := trunc != nil && nvBool(fallbackTrunc, "firstLineExceedsLimit")
	var footParts []string
	if resolved := nvStr(details, "resolvedPath"); resolved != "" {
		footParts = append(footParts, "resolved "+nvShortenPath(resolved))
	}
	if firstLineTooLong {
		limitBytes := nvInt64(fallbackTrunc, "outputBytes")
		if limitBytes == 0 {
			limitBytes = nvInt64(fallbackTrunc, "totalBytes")
		}
		lineMsg := fmt.Sprintf("First line exceeds %s limit", nvFormatBytes(limitBytes))
		if artID := nvStr(trunc, "artifactId"); artID != "" {
			lineMsg += ". " + nvFormatFullOutputRef(artID, nvInt64(trunc, "artifactElidedBytes"))
		}
		footParts = append(footParts, lineMsg)
	}
	footMeta := meta
	if firstLineTooLong {
		if artErr := nvMap(meta, "artifactError"); artErr != nil {
			footMeta = map[string]any{"artifactError": artErr}
		} else {
			footMeta = nil
		}
	}
	foot := nvFootnoteNode(sc, footParts, footMeta)

	// Content text: prefer details.displayContent.text (clean, unprefixed), else result text.
	disp := nvMap(details, "displayContent")
	contentText := ""
	if disp != nil && nvHasKey(disp, "text") {
		contentText = nvStr(disp, "text")
	} else {
		contentText = nvStripOutputNotice(nvResultText(res), meta)
	}

	// Image read: upstream tools/read.ts:846-859.
	if imgs := nvExtractImages(res); len(imgs) > 0 {
		img := imgs[0]
		basePath, _ := nvSplitPathAndSel(rawPath)
		alt := nvShortenPath(basePath)
		if suffix := nvMap(details, "suffixResolution"); suffix != nil {
			if to := nvStr(suffix, "to"); to != "" {
				alt = nvShortenPath(to)
			}
		}
		if alt == "" {
			alt = "image"
		}
		imgNode := kit.NewImage(img.Data, img.MimeType)
		imgNode.ID = sc.claim("omp.tool.read.image")
		imgNode.Filename = alt
		sc.add(1)

		var children []kit.Node
		children = append(children, imgNode)
		if detailText := strings.TrimSpace(nvPlainText(contentText)); detailText != "" {
			children = append(children, sc.lines(sc.claim("omp.tool.output"), nvSanitizeLines(detailText)))
		}
		if foot != nil {
			children = append(children, foot)
		}
		return sc.container(sc.claim("omp.tool.read"), children...), nil
	}

	// Filesystem reads: upstream chat/read-tool-group.ts:803-864, 1446-1448
	// (`#shouldRenderPreview`: `this.#showContentPreview && entry.contentText !== undefined`),
	// chat/display-preferences.ts:19 (`read.toolResultPreview` defaults to false).
	// Ctrl+O (`opts.Expanded`) does NOT add a preview body when `read.toolResultPreview`
	// is false — omp's ReadToolGroupComponent keeps filesystem read rows body-less
	// even after Ctrl+O expansion.
	if readArgsCollapseIntoGroup(rawPath) && !settingBool(ctx, "read.toolResultPreview", false) {
		return sc.container(sc.claim("omp.tool.read")), nil
	}
	// Text / code / markdown read: upstream tools/read.ts:861-870.
	var children []kit.Node
	if nvIsReadMarkdown(details, rawPath, rc.Args) {
		if contentText != "" {
			children = append(children, sc.md(sc.claim("omp.tool.read.markdown"), nvSanitize(contentText)))
		}
	} else if contentText != "" {
		children = append(children, sc.lines(sc.claim("omp.tool.read.code"), nvRawLines(contentText)))
	}
	if foot != nil {
		children = append(children, foot)
	}
	return sc.container(sc.claim("omp.tool.read"), children...), nil
}

func firstLineAndMultiline(s string) (string, bool) {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx]), true
	}
	return s, false
}

// ── fetch / URL reads (tools/fetch.ts) ───────────────────────────────────────

func nativeFetchCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	return sc.container(sc.claim("omp.tool.fetch.call")), nil
}

func nativeFetchResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	return nvDescribeReadURLResult(sc, res, details, rc), nil
}

// nvDescribeReadURLResult ports describeReadUrlResult in tools/fetch.ts:158-218.
func nvDescribeReadURLResult(sc *nvScope, res sdk.ToolRenderResult, details map[string]any, rc sdk.ToolRenderContext) kit.Node {
	if rc.IsError || details == nil {
		msg := strings.TrimPrefix(strings.TrimSpace(nvResultText(res)), "Error:")
		msg = strings.TrimSpace(msg)
		if msg == "" {
			msg = "No response data"
		}
		return sc.container(sc.claim("omp.tool.fetch"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg))
	}

	contentText := nvResultText(res)
	contentBody := contentText
	if idx := strings.Index(contentText, "---\n\n"); idx >= 0 {
		contentBody = contentText[idx+len("---\n\n"):]
	}

	var nonEmpty []string
	for line := range strings.SplitSeq(contentBody, "\n") {
		if strings.TrimSpace(line) != "" {
			nonEmpty = append(nonEmpty, strings.TrimRight(nvPlainText(line), " \t"))
		}
	}
	shown := nonEmpty
	if len(shown) > nvExpandedPreviewLines {
		shown = shown[:nvExpandedPreviewLines]
	}
	hidden := len(nonEmpty) - len(shown)

	var children []kit.Node
	if len(shown) > 0 {
		children = append(children, sc.lines(sc.claim("omp.tool.fetch.content"), shown))
	} else {
		children = append(children, sc.text(sc.claim("omp.tool.fetch.content"), "(no content)"))
	}

	ct := nvStr(details, "contentType")
	if ct == "" {
		ct = "unknown"
	}
	var statParts []string
	statParts = append(statParts, ct)
	if method := nvStr(details, "method"); method != "" {
		statParts = append(statParts, method)
	}
	statParts = append(statParts, nvFormatCount("line", len(nonEmpty)))
	statParts = append(statParts, fmt.Sprintf("%d chars", len(strings.TrimSpace(contentBody))))
	if hidden > 0 {
		statParts = append(statParts, fmt.Sprintf("… %s", nvFormatCount("more line", hidden)))
	}
	for _, note := range nvStrSlice(details, "notes") {
		if t := strings.TrimSpace(note); t != "" {
			statParts = append(statParts, t)
		}
	}
	if stats := nvStatsNode(sc, statParts); stats != nil {
		children = append(children, stats)
	}

	meta := nvMap(details, "meta")
	trunc := nvMap(meta, "truncation")
	truncated := nvBool(details, "truncated") || trunc != nil
	if truncated {
		noticeMsg := "Output truncated"
		if artID := nvStr(trunc, "artifactId"); artID != "" {
			noticeMsg = fmt.Sprintf("Output truncated · %s", nvFormatFullOutputRef(artID, nvInt64(trunc, "artifactElidedBytes")))
		}
		children = append(children, nvNoticeNode(sc, noticeMsg))
	}

	return sc.container(sc.claim("omp.tool.fetch"), children...)
}

// ── write (file, figure, agent://, proc://, cfg://) ──────────────────────────

// nvFigureFences mirrors FIGURE_FENCES in packages/tui/src/tools/write.ts:90-103.
var nvFigureFences = map[string]string{
	"svg":     "svg",
	"mmd":     "mermaid",
	"mermaid": "mermaid",
	"obj":     "obj",
	"ply":     "ply",
	"wrl":     "wrl",
	"vrml":    "vrml",
	"x3dv":    "x3dv",
	"stl":     "stl",
	"gltf":    "gltf",
	"usda":    "usda",
	"usd":     "usd",
}

func nvWriteFigure(sc *nvScope, rawPath, content string, isError, closed bool) kit.Node {
	if isError || strings.TrimSpace(content) == "" {
		return nil
	}
	lang, ok := nvFigureFences[nvFileExt(rawPath)]
	if !ok {
		return nil
	}
	// upstream: tools/write.ts:143 — mermaid waits until closed.
	if lang == "mermaid" && !closed {
		return nil
	}
	clean := strings.ReplaceAll(content, "\r", "")
	fence := "```" + lang + "\n" + clean
	if !strings.HasSuffix(fence, "\n") {
		fence += "\n"
	}
	if closed {
		fence += "```"
	}
	return sc.md(sc.claim("omp.tool.write.figure"), fence)
}

func nvParseProcWriteTarget(rest string) (id, action string) {
	action = "stdin"
	id = rest
	if cut, ok := strings.CutSuffix(id, "/kill"); ok {
		id = cut
		action = "kill"
	} else if cut, ok := strings.CutSuffix(id, "/mode"); ok {
		id = cut
		action = "mode"
	}
	return strings.Trim(id, "/"), action
}

func nativeWriteCall(_ sdk.Context, args map[string]any, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	rawPath := nvStr(args, "file_path", "path")
	content := nvStr(args, "content")

	if scheme, rest, isURL := nvSplitURLScheme(rawPath); isURL {
		switch scheme {
		case "agent":
			return nvDescribeAgentWrite(sc, rest, content, nil, nil, false), nil
		case "proc":
			id, action := nvParseProcWriteTarget(rest)
			return nvDescribeProcWrite(sc, id, action, content, nil, nil, false), nil
		case "cfg":
			return nvDescribeCfgWrite(sc, rawPath, content, nil, nil, false), nil
		}
	}

	var children []kit.Node
	if fig := nvWriteFigure(sc, rawPath, content, false, rc.ArgsComplete); fig != nil {
		children = append(children, fig)
	}
	if content != "" {
		children = append(children, sc.lines(sc.claim("omp.tool.write.source"), nvRawLines(content)))
	}
	return sc.container(sc.claim("omp.tool.write.call"), children...), nil
}

func nativeWriteResult(_ sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)
	rawPath := nvStr(rc.Args, "file_path", "path")
	content := nvStr(rc.Args, "content")
	scheme, rest, isURL := nvSplitURLScheme(rawPath)

	if (isURL && scheme == "agent") || nvMap(details, "message") != nil {
		return nvDescribeAgentWrite(sc, rest, content, &res, nvMap(details, "message"), rc.IsError), nil
	}
	if (isURL && scheme == "proc") || nvMap(details, "proc") != nil {
		id, action := nvParseProcWriteTarget(rest)
		return nvDescribeProcWrite(sc, id, action, content, &res, nvMap(details, "proc"), rc.IsError), nil
	}
	if (isURL && scheme == "cfg") || nvMap(details, "cfg") != nil {
		return nvDescribeCfgWrite(sc, rawPath, content, &res, nvMap(details, "cfg"), rc.IsError), nil
	}

	if rc.IsError {
		return sc.container(sc.claim("omp.tool.write"), nvErrorNode(sc, sc.claim("omp.tool.error"), nvResultText(res))), nil
	}

	var children []kit.Node
	if opts.IsPartial {
		if prog := strings.TrimSpace(nvPlainText(nvResultText(res))); prog != "" {
			children = append(children, sc.text(sc.claim("omp.tool.stats"), prog))
		}
	}
	if fig := nvWriteFigure(sc, rawPath, content, false, true); fig != nil {
		children = append(children, fig)
	}
	if content != "" {
		children = append(children, sc.lines(sc.claim("omp.tool.write.source"), nvRawLines(content)))
	}
	if !opts.IsPartial {
		if diag := nvDiagnosticsSection(sc, nvMap(details, "diagnostics"), false, rc.Cwd, -1); diag != nil {
			children = append(children, diag)
		}
	}
	return sc.container(sc.claim("omp.tool.write"), children...), nil
}

// ── edit / apply_patch (tools/edit.ts) ───────────────────────────────────────

// nvEditHeaderRE matches hashline edit section headers: `[path#TAG]` or `[path]`.
var nvEditHeaderRE = regexp.MustCompile(`^\[([^\]#]+?)(?:#[0-9A-Fa-f]{4})?\]\s*$`)

type nvEditSection struct {
	path   string
	change string
}

func nvParseEditSections(input string) []nvEditSection {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	var out []nvEditSection
	var cur *nvEditSection
	var body []string
	flush := func() {
		if cur == nil {
			return
		}
		cur.change = strings.TrimSpace(strings.Join(body, "\n"))
		out = append(out, *cur)
		cur = nil
		body = nil
	}
	for line := range strings.SplitSeq(strings.ReplaceAll(input, "\r\n", "\n"), "\n") {
		if m := nvEditHeaderRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			flush()
			cur = &nvEditSection{path: strings.TrimSpace(m[1])}
			continue
		}
		if cur != nil {
			body = append(body, line)
		}
	}
	flush()
	return out
}

func nativeEditCall(_ sdk.Context, args map[string]any, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	// Hashline / magic-section `input` string: per-file sections with omp.tool.edit.change.
	// upstream: tools/edit.ts:271-281, 1263-1324
	if input := nvStr(args, "input"); input != "" {
		sections := nvParseEditSections(input)
		if len(sections) == 1 {
			var children []kit.Node
			if ch := nvPlainText(sections[0].change); ch != "" {
				children = append(children, sc.md(sc.claim("omp.tool.edit.change"), ch))
			}
			return sc.container(sc.claim("omp.tool.edit.call"), children...), nil
		}
		if len(sections) > 1 {
			var children []kit.Node
			for i, sec := range sections {
				if !sc.canAdd(3) {
					break
				}
				var secBody []kit.Node
				if ch := nvPlainText(sec.change); ch != "" {
					secBody = append(secBody, sc.md(sc.indexed("omp.tool.edit.change", i), ch))
				}
				children = append(children, nvFileDiffSection(sc, "omp.tool.edit.file", i, sec.path, "", 0, 0, rc.Cwd, secBody...))
			}
			return sc.container(sc.claim("omp.tool.edit.call"), children...), nil
		}
	}

	rawPath := nvStr(args, "file_path", "path")
	if previewDiff := nvStr(args, "previewDiff"); previewDiff != "" {
		return sc.container(sc.claim("omp.tool.edit.call"), sc.diff(sc.claim("omp.tool.edit.diff"), nvSanitize(previewDiff), rawPath)), nil
	}
	if diffArg := nvStr(args, "diff"); diffArg != "" && nvStr(args, "op") != "" {
		return sc.container(sc.claim("omp.tool.edit.call"), sc.diff(sc.claim("omp.tool.edit.diff"), nvSanitize(diffArg), rawPath)), nil
	}
	// Non-diff replacement text / raw patch uses code payload (omp.tool.write.source),
	// not omp.tool.edit.diff (upstream: tools/edit.ts:1300-1301).
	if patch := nvStr(args, "diff", "newText", "new_string", "patch"); patch != "" {
		return sc.container(sc.claim("omp.tool.edit.call"), sc.lines(sc.claim("omp.tool.write.source"), nvRawLines(patch))), nil
	}
	return sc.container(sc.claim("omp.tool.edit.call")), nil
}

func nativeEditResult(_ sdk.Context, res sdk.ToolRenderResult, _ sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, _ int) (kit.Node, error) {
	sc := newNVScope()
	details := nvToMap(res.Details)

	// Multi-file result: upstream tools/edit.ts:1332-1376.
	perFile := nvSlice(details, "perFileResults")
	if len(perFile) > 1 {
		var children []kit.Node
		for i, raw := range perFile {
			if !sc.canAdd(6) {
				children = append(children, nvStatsNode(sc, []string{fmt.Sprintf("+%d more files", len(perFile)-i)}))
				break
			}
			entry := nvToMap(raw)
			if entry == nil {
				continue
			}
			filePath := nvStr(entry, "sourcePath", "path")
			rename := nvStr(entry, "move", "rename")
			if rename == filePath {
				rename = ""
			}
			isErr := nvBool(entry, "isError")
			diffText := nvStr(entry, "diff")
			op := nvStr(entry, "op")
			var added, removed int
			if !isErr && diffText != "" {
				added, removed = nvCountDiffStats(diffText)
			}
			var secBody []kit.Node
			if isErr {
				errMsg := nvStr(entry, "displayErrorText", "errorText")
				if errMsg == "" {
					errMsg = "Edit failed"
				}
				secBody = append(secBody, nvErrorNode(sc, sc.indexed("omp.tool.error", i), errMsg))
			} else if diffText != "" {
				secBody = append(secBody, sc.diff(sc.indexed("omp.tool.edit.diff", i), nvSanitize(diffText), nvStr(entry, "path", "sourcePath")))
			} else if op != "delete" && op != "create" && rename == "" {
				shown := nvDisplayPath(filePath, rc.Cwd)
				msg := "No changes were made."
				if shown != "" {
					msg = fmt.Sprintf("No changes were made to %s.", shown)
				}
				secBody = append(secBody, sc.text(sc.indexed("omp.tool.notice", i), msg))
			}
			if diag := nvDiagnosticsSection(sc, nvMap(entry, "diagnostics"), false, rc.Cwd, i); diag != nil {
				secBody = append(secBody, diag)
			}
			children = append(children, nvFileDiffSection(sc, "omp.tool.edit.file", i, filePath, rename, added, removed, rc.Cwd, secBody...))
		}
		return sc.container(sc.claim("omp.tool.edit"), children...), nil
	}

	// Single-file result: upstream tools/edit.ts:1377-1382, 1465-1533.
	entry := details
	if len(perFile) == 1 {
		if m := nvToMap(perFile[0]); m != nil {
			entry = m
		}
	}
	isErr := rc.IsError || nvBool(entry, "isError")
	filePath := nvStr(entry, "sourcePath", "path")
	if filePath == "" {
		filePath = nvStr(rc.Args, "file_path", "path")
	}
	op := nvStr(entry, "op")
	if op == "" {
		op = nvStr(rc.Args, "op")
	}
	rename := nvStr(entry, "move", "rename")
	if rename == "" {
		rename = nvStr(rc.Args, "rename")
	}
	var children []kit.Node
	if isErr {
		errMsg := nvStr(entry, "displayErrorText", "errorText")
		if errMsg == "" {
			errMsg = nvResultText(res)
		}
		if errMsg != "" {
			children = append(children, nvErrorNode(sc, sc.claim("omp.tool.error"), errMsg))
		}
	} else if diffText := nvStr(entry, "diff"); diffText != "" {
		children = append(children, sc.diff(sc.claim("omp.tool.edit.diff"), nvSanitize(diffText), nvStr(entry, "path", "sourcePath")))
	} else if entry != nil && op != "delete" && op != "create" && rename == "" {
		shown := nvDisplayPath(filePath, rc.Cwd)
		msg := "No changes were made."
		if shown != "" {
			msg = fmt.Sprintf("No changes were made to %s.", shown)
		}
		children = append(children, sc.text(sc.claim("omp.tool.notice"), msg))
	}
	if diag := nvDiagnosticsSection(sc, nvMap(entry, "diagnostics"), false, rc.Cwd, -1); diag != nil {
		children = append(children, diag)
	}
	return sc.container(sc.claim("omp.tool.edit"), children...), nil
}

// ── proc://, agent://, cfg:// internal URL cards ─────────────────────────────

func nvJobListItem(job map[string]any, badge string) kit.ListItem {
	id := nvPlainLine(nvStr(job, "id"))
	typ := nvPlainLine(nvStr(job, "type"))
	label := id
	if typ != "" {
		label = fmt.Sprintf("[%s] %s", typ, id)
	}
	detail := nvPlainLine(nvStr(job, "intent"))
	if detail == "" {
		detail = nvPlainLine(nvStr(job, "label"))
	}
	val := badge
	if val == "" {
		if dur, ok := nvFloat(job, "durationMs"); ok {
			val = nvFormatDuration(int64(dur))
		} else {
			val = nvPlainLine(nvStr(job, "status"))
		}
	}
	return kit.ListItem{
		Label:   label,
		Detail:  detail,
		Columns: []string{val},
	}
}

func nvDaemonMetaText(daemon map[string]any) string {
	var parts []string
	if state := nvStr(daemon, "state"); state != "" {
		parts = append(parts, state)
	}
	if pid := nvInt(daemon, "pid"); pid > 0 {
		parts = append(parts, fmt.Sprintf("pid %d", pid))
	}
	if mode := nvStr(daemon, "mode"); mode != "" && mode != "session" {
		parts = append(parts, mode)
	}
	return strings.Join(parts, " · ")
}

// nvDescribeProcRead ports describeProcRead in packages/tui/src/tools/proc-render.ts:453-526.
func nvDescribeProcRead(sc *nvScope, id string, res *sdk.ToolRenderResult, details map[string]any, isError bool) kit.Node {
	if isError {
		msg := "Process read failed."
		if res != nil {
			if t := strings.TrimSpace(nvResultText(*res)); t != "" {
				msg = t
			}
		}
		return sc.container(sc.claim("omp.tool.proc"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg))
	}
	if res == nil {
		return sc.container(sc.claim("omp.tool.proc.call"))
	}

	// Single job read: list item + optional omp.tool.proc.log.
	if job := nvMap(details, "job"); job != nil {
		var children []kit.Node
		children = append(children, sc.listLines(sc.claim("omp.tool.proc.table"), []kit.ListItem{nvJobListItem(job, "")}))
		logText := nvStr(details, "log")
		if logText == "" {
			logText = nvStr(job, "errorText", "resultText")
		}
		if strings.TrimSpace(logText) != "" {
			children = append(children, sc.lines(sc.claim("omp.tool.proc.log"), nvSanitizeLines(logText)))
		}
		return sc.container(sc.claim("omp.tool.proc"), children...)
	}

	// Single service/daemon read: omp.tool.proc.log.
	if daemon := nvMap(details, "daemon"); daemon != nil {
		termRows := nvStrSlice(details, "terminalRows")
		var logLines []string
		if len(termRows) > 0 {
			logLines = nvSanitizeLines(strings.Join(termRows, "\n"))
		} else {
			logLines = nvSanitizeLines(nvStr(details, "log"))
		}
		return sc.container(sc.claim("omp.tool.proc"), sc.lines(sc.claim("omp.tool.proc.log"), logLines))
	}

	// Targeted proc://<id>/streams or evicted job preview without table lists.
	jobs := nvSlice(details, "jobs")
	daemons := nvSlice(details, "daemons")
	agents := nvSlice(details, "agents")
	if id != "" && details == nil {
		var children []kit.Node
		if body := strings.TrimSpace(nvPlainText(nvResultText(*res))); body != "" {
			children = append(children, sc.lines(sc.claim("omp.tool.proc.preview"), nvSanitizeLines(body)))
		}
		return sc.container(sc.claim("omp.tool.proc"), children...)
	}
	if id != "" && jobs == nil && daemons == nil && agents == nil {
		var children []kit.Node
		if body := strings.TrimSpace(nvPlainText(nvResultText(*res))); body != "" {
			children = append(children, sc.lines(sc.claim("omp.tool.proc.preview"), nvSanitizeLines(body)))
		}
		return sc.container(sc.claim("omp.tool.proc"), children...)
	}

	// Root proc:// table: jobs + services + agents as kit.List items.
	var items []kit.ListItem
	for _, raw := range jobs {
		if jm := nvToMap(raw); jm != nil {
			items = append(items, nvJobListItem(jm, ""))
		}
	}
	for _, raw := range daemons {
		if dm := nvToMap(raw); dm != nil {
			name := nvPlainLine(nvStr(dm, "name", "id"))
			items = append(items, kit.ListItem{
				Label:  "service " + name,
				Detail: nvDaemonMetaText(dm),
			})
		}
	}
	for _, raw := range agents {
		if am := nvToMap(raw); am != nil {
			aid := nvPlainLine(nvStr(am, "id"))
			age := ""
			if ms, ok := nvFloat(am, "ageMs"); ok {
				age = nvFormatDuration(int64(ms))
			}
			items = append(items, kit.ListItem{
				Label:   "agent " + aid,
				Columns: []string{age},
			})
		}
	}
	return sc.container(sc.claim("omp.tool.proc"), sc.listLines(sc.claim("omp.tool.proc.table"), items))
}

// nvDescribeProcWrite ports describeProcWrite in packages/tui/src/tools/proc-render.ts:408-450.
func nvDescribeProcWrite(sc *nvScope, id, action, content string, res *sdk.ToolRenderResult, details map[string]any, isError bool) kit.Node {
	rootRole := "omp.tool.proc"
	if res == nil {
		rootRole = "omp.tool.proc.call"
	}
	if isError {
		msg := "Process operation failed."
		if res != nil {
			if t := strings.TrimSpace(nvResultText(*res)); t != "" {
				msg = t
			}
		}
		return sc.container(sc.claim(rootRole), nvErrorNode(sc, sc.claim("omp.tool.error"), msg))
	}
	var children []kit.Node
	if action == "stdin" && strings.TrimSpace(content) != "" {
		children = append(children, sc.lines(sc.claim("omp.tool.proc.preview"), nvSanitizeLines(content)))
	}
	if nvStr(details, "op") == "cancel" {
		jobsByID := map[string]map[string]any{}
		for _, raw := range nvSlice(details, "jobs") {
			if jm := nvToMap(raw); jm != nil {
				jobsByID[nvStr(jm, "id")] = jm
			}
		}
		var items []kit.ListItem
		for _, raw := range nvSlice(details, "cancelled") {
			cm := nvToMap(raw)
			if cm == nil {
				continue
			}
			cid := nvStr(cm, "id")
			status := nvStr(cm, "status")
			if jm, ok := jobsByID[cid]; ok {
				items = append(items, nvJobListItem(jm, status))
			} else {
				items = append(items, kit.ListItem{
					Label:   nvPlainLine(cid),
					Columns: []string{nvPlainLine(status)},
				})
			}
		}
		children = append(children, sc.listLines(sc.claim("omp.tool.proc.cancelled"), items))
	}
	return sc.container(sc.claim(rootRole), children...)
}

// nvDescribeAgentWrite ports describeAgentWrite in packages/tui/src/tools/proc-render.ts:360-405.
func nvDescribeAgentWrite(sc *nvScope, to, body string, res *sdk.ToolRenderResult, details map[string]any, isError bool) kit.Node {
	rootRole := "omp.tool.irc"
	if res == nil {
		rootRole = "omp.tool.irc.call"
	}
	receipts := nvSlice(details, "receipts")
	if isError && len(receipts) == 0 {
		msg := "Message delivery failed."
		if res != nil {
			if t := strings.TrimSpace(nvResultText(*res)); t != "" {
				msg = t
			}
		}
		return sc.container(sc.claim(rootRole), nvErrorNode(sc, sc.claim("omp.tool.error"), msg))
	}
	var children []kit.Node
	if strings.TrimSpace(body) != "" {
		children = append(children, sc.lines(sc.claim("omp.tool.proc.preview"), nvSanitizeLines(body)))
	}
	if res != nil && len(receipts) == 0 {
		note := strings.TrimSpace(nvResultText(*res))
		if note == "" {
			note = "No live peers to broadcast to."
		}
		children = append(children, sc.text(sc.claim("omp.tool.notice"), nvPlainText(note)))
	}
	failed := 0
	var items []kit.ListItem
	for _, raw := range receipts {
		rm := nvToMap(raw)
		if rm == nil {
			continue
		}
		outcome := nvStr(rm, "outcome")
		if outcome == "failed" {
			failed++
		}
		items = append(items, kit.ListItem{
			Label:   nvPlainLine(nvStr(rm, "to")),
			Detail:  nvPlainLine(nvStr(rm, "error")),
			Columns: []string{nvPlainLine(outcome)},
		})
	}
	if len(items) > 1 || failed > 0 {
		children = append(children, sc.listLines(sc.claim("omp.tool.irc.receipts"), items))
	}
	return sc.container(sc.claim(rootRole), children...)
}

// nvDescribeCfgRead ports describeCfgRead in packages/tui/src/tools/cfg-render.ts:84-104.
func nvDescribeCfgRead(sc *nvScope, rawURL string, res *sdk.ToolRenderResult, details map[string]any, isError bool) kit.Node {
	if isError {
		msg := "Settings read failed."
		if res != nil {
			if t := strings.TrimSpace(nvResultText(*res)); t != "" {
				msg = t
			}
		}
		return sc.container(sc.claim("omp.tool.cfg"), nvErrorNode(sc, sc.claim("omp.tool.error"), msg))
	}
	if res == nil {
		return sc.container(sc.claim("omp.tool.cfg.call"))
	}
	listing := strings.TrimRight(nvPlainText(nvResultText(*res)), " \t\n")
	if listing == "" {
		return sc.container(sc.claim("omp.tool.cfg"))
	}
	return sc.container(sc.claim("omp.tool.cfg"), sc.lines(sc.claim("omp.tool.cfg.listing"), nvSanitizeLines(listing)))
}

// nvDescribeCfgWrite ports describeCfgWrite in packages/tui/src/tools/cfg-render.ts:107-143.
func nvDescribeCfgWrite(sc *nvScope, rawURL, content string, res *sdk.ToolRenderResult, details map[string]any, isError bool) kit.Node {
	rootRole := "omp.tool.cfg"
	if res == nil {
		rootRole = "omp.tool.cfg.call"
	}
	if isError {
		msg := "Settings write failed."
		if res != nil {
			if t := strings.TrimSpace(nvResultText(*res)); t != "" {
				msg = t
			}
		}
		return sc.container(sc.claim(rootRole), nvErrorNode(sc, sc.claim("omp.tool.error"), msg))
	}
	nextVal := nvStr(details, "value")
	if nextVal == "" {
		nextVal = strings.TrimSpace(content)
	}
	if nextVal == "" {
		nextVal = "…"
	}
	changeText := "→ " + nvPlainText(nextVal)
	if details != nil {
		prev := nvPlainText(nvStr(details, "previous"))
		changeText = prev + " → " + nvPlainText(nextVal)
	}
	var children []kit.Node
	children = append(children, sc.text(sc.claim("omp.tool.cfg.change"), changeText))
	if eff := nvStr(details, "effective"); eff != "" {
		children = append(children, sc.text(
			sc.claim("omp.tool.notice"),
			fmt.Sprintf("still %s: a higher-precedence layer overrides the saved value", nvPlainText(eff)),
		))
	}
	return sc.container(sc.claim(rootRole), children...)
}

// nvReadDisplayFilePath is unused externally; keep filepath import used on all platforms.
var _ = filepath.ToSlash
