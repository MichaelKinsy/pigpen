package warden

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The tool_result output pipeline (extension.ts 1795-2095, less the stuck and waste parts): credential masking, the
// output verdicts (injection, exfiltration, retention, format), the credential notices, the duplicate note, the
// compression (excerpt, parser excerpt, context filter), the repeated-run cut and the banner placement.
// It reads the original result and returns the content the model should read, or nothing to change.

// outputPart is one block of a tool result's content: the decoded JSON object, so an image keeps every field.
type outputPart = map[string]any

func isTextPart(p outputPart) bool { return p["type"] == "text" }

func partText(p outputPart) string {
	s, _ := p["text"].(string)
	return s
}

func withText(p outputPart, text string) outputPart {
	out := make(outputPart, len(p))
	for k, v := range p {
		out[k] = v
	}
	out["text"] = text
	return out
}

// outputPartsOf decodes a tool result's content array.
func outputPartsOf(v any) []outputPart {
	var parts []outputPart
	switch c := v.(type) {
	case []any:
		for _, p := range c {
			if m, ok := p.(map[string]any); ok {
				parts = append(parts, m)
			}
		}
	case []map[string]any:
		parts = append(parts, c...)
	case string:
		parts = append(parts, outputPart{"type": "text", "text": c})
	}
	return parts
}

// toolOutputResult is what the pipeline decided.
type toolOutputResult struct {
	// content is the final content; changed says it differs from the event's (a patch is due).
	content []outputPart
	changed bool
	// text is the joined text of the masked text blocks: what the done-check and the call-waste window read.
	text string
	// aborted: the request was cancelled before the pipeline finished; nothing is patched.
	aborted bool
}

// patch is the tool_result handler's return value.
func (r toolOutputResult) patch() any {
	if !r.changed || r.aborted {
		return nil
	}
	content := make([]any, len(r.content))
	for i, p := range r.content {
		content[i] = p
	}
	return map[string]any{"content": content}
}

// abortContext is the Go context of a handler's request: it ends when the host cancels the request (upstream's
// `ctx.signal`).
func abortContext(ctx sdk.Context) (context.Context, context.CancelFunc) {
	goCtx, cancel := context.WithCancel(context.Background())
	if done := ctx.Done(); done != nil {
		go func() {
			select {
			case <-done:
				cancel()
			case <-goCtx.Done():
			}
		}()
	}
	return goCtx, cancel
}

func tokenSet(pairs ...string) Tokens {
	t := Tokens{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			t[pairs[i]] = pairs[i+1]
		}
	}
	return t
}

func fixed2(p *float64) string {
	if p == nil {
		return ""
	}
	return toFixed(*p, 2)
}

func fixed2or(p *float64, none string) string {
	if p == nil {
		return none
	}
	return toFixed(*p, 2)
}

// confFormat is "confidence C; format F (P)" of a retention verdict, the part every trace line of a judged output shares.
func confFormat(v OutputVerdict) string {
	format := string(v.Format)
	if format == "" {
		format = "generic"
	}
	fc := ""
	if v.FormatConfidence != nil {
		fc = " (" + toFixed(*v.FormatConfidence, 2) + ")"
	}
	return fmt.Sprintf("confidence %s; format %s%s", fixed2or(v.Confidence, "undefined"), format, fc)
}

// recordKeptWhole puts a judged output the saver left whole into the trace with its verdict, so the confidence gate
// can be calibrated. Trace only: nothing happened to the output, so the status line keeps the last real context event.
func (w *warden) recordKeptWhole(cfg Config, tool string, v OutputVerdict, prefix string) {
	w.trace.Push(&TraceEntry{At: w.opts.Now(), Guard: GuardContext, Line: RenderTemplate(cfg.Widget.Context, tokenSet("tool", tool, "retention", "kept whole")),
		Details: []string{fmt.Sprintf("%skept whole: retention %s; %s; %s; %d ms", prefix, v.Retention, confFormat(v), v.Model, v.ElapsedMs)}})
}

// toolOutput runs the pipeline for one tool result. `data` is the tool_result event.
func (w *warden) toolOutput(ctx sdk.Context, cfg Config, data map[string]any) toolOutputResult {
	tool, _ := data["toolName"].(string)
	input, _ := data["input"].(map[string]any)
	if input == nil {
		input = map[string]any{}
	}
	original := outputPartsOf(data["content"])
	unchanged := toolOutputResult{content: original}

	// High-confidence credential values are masked before any other rewrite, so neither the model nor a stored copy
	// sees them. Detection below still reads the original text, so the banner names what was masked. Masking is local
	// and sends nothing, so it runs with the security guard off; only the user's `security.maskOutput` stops it.
	var rawTexts []string
	for _, p := range original {
		if isTextPart(p) {
			rawTexts = append(rawTexts, partText(p))
		}
	}
	maskOutput := cfg.Security.MaskOutput
	var maskedTexts []string
	var maskedCounts []int
	maskedCount := 0
	if maskOutput {
		for _, raw := range rawTexts {
			text, n := MaskSecrets(raw)
			maskedTexts = append(maskedTexts, text)
			maskedCounts = append(maskedCounts, n)
			maskedCount += n
		}
	}
	maskedContent := original
	changed := false
	if maskedCount > 0 {
		maskedContent = make([]outputPart, len(original))
		i := 0
		for k, p := range original {
			if isTextPart(p) {
				maskedContent[k] = withText(p, maskedTexts[i])
				i++
			} else {
				maskedContent[k] = p
			}
		}
		changed = true
	}
	var textBlocks []outputPart
	var blockTexts []string
	for _, p := range maskedContent {
		if isTextPart(p) {
			textBlocks = append(textBlocks, p)
			blockTexts = append(blockTexts, partText(p))
		}
	}
	text := strings.Join(blockTexts, "\n")
	unchanged.text = text

	st := &w.context
	gctx, cancel := abortContext(ctx)
	defer cancel()
	aborted := func() bool { return ctx.Err() != nil }

	// A recall brings stored text back on purpose: it is not judged, compressed, or dropped again.
	_, recallRead := st.ledger.StoredPathIn(jsStringify(input))
	// Duplicate detection is code only: an identical result adds nothing, whatever the judge would say about it.
	key := ""
	if cfg.Context.Enabled && !recallRead && len(textBlocks) == 1 && utf16Len(text) >= cfg.Context.DuplicateMinChars {
		key = OutputKey(text)
	}
	var earlierTool, earlierPath string
	hasEarlier := false
	if key != "" {
		earlierTool, earlierPath, hasEarlier = st.ledger.DuplicateOf(key)
	}
	// A multi-block result is judged per text block: each block earns its own retention and banner, and the merged
	// verdict feeds the session bookkeeping below. A block below both thresholds spends no request; its credential
	// scan still runs offline.
	multiBlock := len(textBlocks) > 1 && !hasEarlier && !recallRead
	var blockVerdicts []OutputVerdict
	var output OutputVerdict
	judge := w.judgeFor(ctx, cfg)
	task, _ := w.latestUserPrompt(ctx)
	opts := OutputOptions{Security: cfg.Security, Context: cfg.Context, Judge: judge, Timeout: w.timeout(cfg), TaskContext: w.outputTaskContext(ctx)}
	switch {
	case hasEarlier || recallRead:
		output = OutputVerdict{Retention: RetentionAll}
	case multiBlock:
		for _, raw := range rawTexts {
			if aborted() {
				break
			}
			blockVerdicts = append(blockVerdicts, EvaluateOutput(gctx, tool, raw, task, opts, st.learner))
		}
		output = MergeOutput(blockVerdicts)
	default:
		output = EvaluateOutput(gctx, tool, strings.Join(rawTexts, "\n"), task, opts, st.learner)
	}
	if aborted() {
		return toolOutputResult{content: original, aborted: true}
	}
	if output.Error != "" {
		w.noteError(ctx, output.Error, output.ErrorCode)
	}
	content := maskedContent

	// A credential-shaped value the agent has already been warned about this session is traced, not announced again.
	// Per value, not per set: masking one value or a changed subset must not re-announce the rest.
	valuesOf := func(v OutputVerdict) []string {
		if v.SecretIDs != nil {
			return v.SecretIDs
		}
		if v.Secret && v.SecretID != "" {
			return []string{v.SecretID}
		}
		return nil
	}
	secretValues := valuesOf(output)
	var unseenSecrets []string
	st.mu.Lock()
	for _, id := range secretValues {
		if !st.secretsSeen[id] {
			unseenSecrets = append(unseenSecrets, id)
		}
	}
	secretRepeat := output.Secret && len(secretValues) > 0 && len(unseenSecrets) == 0
	// Per-block banners are computed before secretsSeen is updated, so a block whose values were all announced
	// earlier stays quiet while a block with a new value earns the banner.
	// With masking on, a new credential-shaped value that was not masked earns a trace line, not a banner.
	unmaskedSecret := false
	var blockNotices []string
	if multiBlock {
		for index, verdict := range blockVerdicts {
			values := valuesOf(verdict)
			repeat := verdict.Secret && len(values) > 0
			for _, id := range values {
				if !st.secretsSeen[id] {
					repeat = false
				}
			}
			masked := 0
			if maskOutput && index < len(maskedCounts) {
				masked = maskedCounts[index]
			}
			if maskOutput && verdict.Secret && !repeat && masked == 0 {
				unmaskedSecret = true
			}
			v := verdict
			if repeat {
				v.Secret = false
			}
			blockNotices = append(blockNotices, SecurityNotice(v, masked, maskOutput))
		}
	}
	if maskOutput && !multiBlock && output.Secret && !secretRepeat && maskedCount == 0 {
		unmaskedSecret = true
	}
	for _, id := range unseenSecrets {
		st.secretsSeen[id] = true
	}
	var unseenSynthetic []string
	for _, id := range output.SyntheticIDs {
		if !st.secretsSeen[id] {
			unseenSynthetic = append(unseenSynthetic, id)
		}
	}
	for _, id := range unseenSynthetic {
		st.secretsSeen[id] = true
	}
	st.mu.Unlock()
	notice := ""
	if multiBlock {
		for _, n := range blockNotices {
			if n != "" {
				notice = n
				break
			}
		}
	} else {
		v := output
		if secretRepeat {
			v.Secret = false
		}
		notice = SecurityNotice(v, maskedCount, maskOutput)
	}
	secTokens := func(status string) Tokens {
		return tokenSet("tool", tool, "injection", fixed2(output.Injection), "exfiltration", fixed2(output.Exfiltration), "status", status)
	}
	if unmaskedSecret {
		w.record(ctx, cfg, GuardSecurity, RenderTemplate(cfg.Widget.Security, secTokens("possible credentials, none masked (traced)")), []string{
			"credential-shaped values in this output were detected but none was masked; traced, not announced to the agent"}, nil)
	}
	// Fixture and documentation stand-ins (`devtok_`, `sk-synthetic-`, an alphabet run) earn one trace line and nothing
	// else: no banner in the result and no steer. Most credential steers in the benchmark were these values read from
	// a test file.
	if len(unseenSynthetic) > 0 {
		w.record(ctx, cfg, GuardSecurity, RenderTemplate(cfg.Widget.Security, secTokens("credential-shaped stand-in (traced)")), []string{
			fmt.Sprintf("%d credential-shaped value%s in this output match a test fixture or a documented example; traced once, never announced", len(unseenSynthetic), pluralS(len(unseenSynthetic)))}, nil)
	}
	if secretRepeat && !output.Suspicious {
		id := ""
		if output.SecretID != "" {
			id = " (" + output.SecretID + ")"
		}
		w.record(ctx, cfg, GuardSecurity, RenderTemplate(cfg.Widget.Security, secTokens("possible credentials (seen before)")), []string{
			"the same credential-shaped value" + id + " was already announced this session; no banner or steer this time"}, nil)
	}

	recallTool := w.searchToolFor(cfg)
	storedPath := ""
	ctxTokens := func(retention string, bytesSaved int) Tokens {
		saved := ""
		if bytesSaved >= 0 {
			saved = fmt.Sprint(bytesSaved)
		}
		return tokenSet("tool", tool, "retention", retention, "bytesSaved", saved)
	}
	if hasEarlier {
		path := earlierPath
		var err error
		if path == "" {
			path, err = SaveOutput(text)
		}
		if err != nil {
			w.noteError(ctx, "Could not store full output; keeping it unchanged.", "")
		} else {
			storedPath = path
			replacement := DuplicateNote(text, earlierTool) + "\n\n" + recallInstruction(recallTool, path)
			bytesSaved := len(text) - len(replacement)
			if bytesSaved > 0 {
				content = mapTextParts(content, func(string) string { return replacement })
				changed = true
				st.ledger.Duplicate(bytesSaved)
				st.ledger.Remember(key, earlierTool, path)
				st.setSaving(w.record(ctx, cfg, GuardContext, RenderTemplate(cfg.Widget.Context, ctxTokens("duplicate", bytesSaved)), []string{
					fmt.Sprintf("identical to an earlier %s result; saved %d bytes; full output: %s", earlierTool, bytesSaved, path),
					FormatLedger(st.ledger.Snapshot()),
				}, nil))
			}
		}
	}
	if cfg.Context.Enabled && !hasEarlier && !recallRead && len(textBlocks) == 1 && utf16Len(text) >= cfg.Context.TailMinChars {
		st.ledger.Candidate()
	}
	// Captured before the loop so the notice block below knows whether each block already carries its banner.
	bannersPlacedWithBlocks := multiBlock && !aborted()
	if bannersPlacedWithBlocks {
		// Retention and banners per block; block order and non-text parts are never touched.
		content = append([]outputPart(nil), content...)
		changed = true
		textIndex := 0
		for index, part := range content {
			if !isTextPart(part) {
				continue
			}
			var verdict *OutputVerdict
			blockNotice := ""
			if textIndex < len(blockVerdicts) {
				verdict = &blockVerdicts[textIndex]
				blockNotice = blockNotices[textIndex]
			}
			blockText := partText(part)
			textIndex++
			if verdict == nil {
				continue
			}
			replacement, compressed := "", false
			if excerpt, ok := CompressOutput(blockText, verdict.Retention, verdict.Format); ok {
				path, err := SaveOutput(blockText)
				if err != nil {
					w.noteError(ctx, "Could not store full output; keeping the block unchanged.", "")
				} else {
					lead := ""
					if blockNotice != "" {
						lead = blockNotice + "\n\n"
					}
					body := lead + excerpt + "\n\n" + recallInstruction(recallTool, path)
					bytesSaved := len(blockText) - len(body)
					if bytesSaved > 0 {
						replacement, compressed = body, true
						kind := KindParser
						if IsGenericExcerpt(excerpt) {
							kind = KindExcerpt
						}
						st.ledger.Record(path, bytesSaved, &StoredSource{Tool: tool, Bytes: len(blockText), Kind: kind})
						storedPath = path
						st.learner.Record(tool, verdict.Retention, verdict.Format, false)
						st.setSaving(w.record(ctx, cfg, GuardContext, RenderTemplate(cfg.Widget.Context, ctxTokens(string(verdict.Retention), bytesSaved)), []string{
							fmt.Sprintf("text block %d of %d: retention %s; %s; saved %d bytes; full output: %s", textIndex, len(blockVerdicts), verdict.Retention, confFormat(*verdict), bytesSaved, path),
							FormatLedger(st.ledger.Snapshot()),
						}, nil))
					}
				}
			}
			if !compressed && verdict.Confidence != nil {
				w.recordKeptWhole(cfg, tool, *verdict, fmt.Sprintf("text block %d of %d: ", textIndex, len(blockVerdicts)))
			}
			if replacement == "" && blockNotice != "" {
				replacement = blockNotice + "\n\n" + blockText + "\n\n" + blockNotice
			}
			if replacement != "" {
				content[index] = withText(part, replacement)
			}
		}
	} else {
		excerpt, haveExcerpt := "", false
		if !hasEarlier {
			excerpt, haveExcerpt = CompressOutput(text, output.Retention, output.Format)
		}
		generic := haveExcerpt && IsGenericExcerpt(excerpt)
		compressed := false
		// Only the generic excerpt is filtered; parser excerpts, `all`, duplicates, and repeats never reach this.
		var filtered *FilterResult
		if haveExcerpt && generic && cfg.Context.Filter.Enabled && !aborted() {
			filtered = w.runFilter(ctx, gctx, cfg, tool, input, text, excerpt)
		}
		if haveExcerpt && !aborted() {
			path, err := SaveOutput(text)
			if err != nil {
				w.noteError(ctx, "Could not store full output; keeping it unchanged.", "")
			} else {
				body := excerpt
				if filtered != nil && filtered.OK {
					body = filtered.Text
				}
				replacement := body + "\n\n" + recallInstruction(recallTool, path)
				noticeBytes := 0
				if notice != "" {
					noticeBytes = len(notice)*2 + 4
				}
				bytesSaved := len(text) - len(replacement) - noticeBytes
				if bytesSaved > 0 {
					content = mapTextParts(content, func(string) string { return replacement })
					changed, compressed = true, true
					kind := KindParser
					switch {
					case filtered != nil && filtered.OK:
						kind = KindFiltered
					case generic:
						kind = KindExcerpt
					}
					src := &StoredSource{Tool: tool, Bytes: len(text), Kind: kind}
					if filtered != nil && filtered.OK {
						src.KeptChars = filtered.KeptChars
					}
					st.ledger.Record(path, bytesSaved, src)
					storedPath = path
					st.learner.Record(tool, output.Retention, output.Format, false)
					retention := string(output.Retention)
					if filtered != nil && filtered.OK {
						retention = "filtered"
					}
					details := []string{fmt.Sprintf("retention: %s; %s; %s; %d ms", output.Retention, confFormat(output), output.Model, output.ElapsedMs)}
					if filtered != nil {
						if filtered.OK {
							details = append(details, fmt.Sprintf("context filter: kept %d of %d chunks, %d of %d characters; %d %s; %d ms", filtered.Kept, filtered.Chunks, filtered.KeptChars, utf16Len(text), filtered.Requests, pluralWord(filtered.Requests, "request"), filtered.ElapsedMs))
						} else {
							details = append(details, fmt.Sprintf("context filter fell back to the excerpt: %s; %d chunks; %d %s; %d ms", filtered.Reason, filtered.Chunks, filtered.Requests, pluralWord(filtered.Requests, "request"), filtered.ElapsedMs))
						}
					}
					details = append(details, fmt.Sprintf("saved %d bytes; full output: %s", bytesSaved, path), FormatLedger(st.ledger.Snapshot()))
					st.setSaving(w.record(ctx, cfg, GuardContext, RenderTemplate(cfg.Widget.Context, ctxTokens(retention, bytesSaved)), details, nil))
				}
			}
		}
		if !compressed && !aborted() && output.Confidence != nil {
			w.recordKeptWhole(cfg, tool, output, "")
		}
	}
	// A recall brings the stored text back on purpose, and a duplicate note has nothing left to cut.
	if cfg.Context.Enabled && cfg.Context.DedupeRuns && !hasEarlier && !recallRead && !aborted() {
		w.syncSeen(ctx)
		if deduped, ok := w.dedupeContent(ctx, cfg, content, tool); ok {
			content, changed = deduped, true
		}
	}
	if key != "" && !hasEarlier {
		st.ledger.Remember(key, tool, storedPath)
	}
	// The banner in the result already tells the agent, so the notice rides the result content alone and is not also
	// steered. A steer sent here would join the request that carries this result and cost no extra turn
	// (tests/steer-delivery.test.ts); it would only repeat the banner.
	if notice != "" && len(textBlocks) > 0 {
		if !bannersPlacedWithBlocks {
			index := 0
			content = mapTextParts(content, func(s string) string {
				index++
				out := s
				if index == 1 {
					out = notice + "\n\n" + out
				}
				if index == len(textBlocks) {
					out += "\n\n" + notice
				}
				return out
			})
			changed = true
		}
		status := []string{}
		if output.Suspicious {
			status = append(status, "untrusted instructions")
		}
		if output.Secret {
			status = append(status, "possible credentials")
		}
		w.record(ctx, cfg, GuardSecurity, RenderTemplate(cfg.Widget.Security, secTokens(strings.Join(status, ", "))), []string{
			fmt.Sprintf("jev: injection %s; exfiltration %s", fixed2or(output.Injection, "not judged"), fixed2or(output.Exfiltration, "not judged")),
			"output sample: " + utf16Slice(Redact(text), 0, 300),
			"agent told by the banner in the tool result: " + notice,
		}, nil)
	}
	return toolOutputResult{content: content, changed: changed, text: unchanged.text}
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// mapTextParts rewrites every text block; other blocks keep their place and bytes.
func mapTextParts(content []outputPart, f func(string) string) []outputPart {
	out := make([]outputPart, len(content))
	for i, p := range content {
		if isTextPart(p) {
			out[i] = withText(p, f(partText(p)))
		} else {
			out[i] = p
		}
	}
	return out
}

// outputTaskContext is the scope context of the output check: up to eight earlier user and assistant messages,
// newest last, the latest user prompt left out (extension.ts recentTaskContext).
func (w *warden) outputTaskContext(ctx sdk.Context) []TaskMessage {
	branch := w.branchOf(ctx)
	var messages []TaskMessage
	skippedLatestUser := false
	for i := len(branch) - 1; i >= 0 && len(messages) < 8; i-- {
		e := branch[i]
		if e.Type != "message" || (e.Role != "user" && e.Role != "assistant") {
			continue
		}
		if e.Role == "user" && !skippedLatestUser {
			skippedLatestUser = true
			continue
		}
		if strings.TrimSpace(e.Content) != "" {
			messages = append([]TaskMessage{{Role: e.Role, Text: utf16Slice(Redact(e.Content), 0, 750)}}, messages...)
		}
	}
	return messages
}

// filterOffReason is the reason no judge is available for the filter: judgments are off for this backend or the
// request budget is spent; otherwise the judge is cooling down after errors.
func (w *warden) filterOffReason(cfg Config) FilterFallback {
	if cfg.Backend == BackendNone || cfg.Consent != cfg.Backend {
		return "no_consent"
	}
	if cfg.MaxRequests > 0 && w.budget.Used() >= cfg.MaxRequests {
		return FallbackBudget
	}
	return FallbackCooldown
}

// runFilter is the context filter (beta) for one output that would get the generic excerpt. It never panics; a
// fallback keeps the excerpt. Its cost and fallback reason go to the ledger either way.
func (w *warden) runFilter(ctx sdk.Context, gctx context.Context, cfg Config, tool string, input map[string]any, text, excerpt string) *FilterResult {
	st := &w.context
	judge := w.judgeFor(ctx, cfg)
	if judge == nil {
		reason := w.filterOffReason(cfg)
		st.ledger.FilterSpent(0, 0, string(reason))
		return &FilterResult{Reason: reason}
	}
	command := ""
	if c, ok := input["command"].(string); ok {
		command = c
	} else {
		command = jsStringify(input)
	}
	task, _ := w.latestUserPrompt(ctx)
	result := FilterOutput(gctx, text, FilterInput{Tool: tool, Command: command, Task: task, Spine: w.outputSpine(ctx), Plan: w.outputPlan(ctx)}, FilterOptions{Config: cfg.Context.Filter, Judge: judge})
	// Kept text is bounded by maxKeptChars; the gap markers are not, so the bound is checked on the result.
	if result.OK && utf16Len(result.Text) > utf16Len(excerpt)+cfg.Context.Filter.MaxKeptChars {
		result = FilterResult{Reason: FallbackTooLong, Chunks: result.Chunks, Requests: result.Requests, ElapsedMs: result.ElapsedMs}
	}
	reason := ""
	if !result.OK {
		reason = string(result.Reason)
	}
	st.ledger.FilterSpent(result.Requests, result.ElapsedMs, reason)
	if !result.OK && result.Reason == FallbackBudget {
		w.noteError(ctx, "TypeSafe request budget reached; the context filter kept the excerpt.", "budget")
	}
	return &result
}

// outputSpine is the task spine of the branch (extension.ts taskSpine(getBranch())).
func (w *warden) outputSpine(ctx sdk.Context) *TaskSpine {
	var msgs []BranchMessage
	for _, e := range w.branchOf(ctx) {
		if e.Type == "message" {
			msgs = append(msgs, BranchMessage{Type: "message", Role: e.Role, Text: e.Content})
		}
	}
	task, _ := w.latestUserPrompt(ctx)
	return TaskSpineOf(msgs, task)
}

// outputPlan is the agent's own words for the call that just finished: the text of the assistant message that
// carried it, or of the one right before it with no tool call in between (extension.ts planForCall).
func (w *warden) outputPlan(ctx sdk.Context) string {
	branch := w.branchOf(ctx)
	carrierSeen := false
	for i := len(branch) - 1; i >= 0; i-- {
		e := branch[i]
		if e.Type != "message" {
			continue
		}
		switch e.Role {
		case "user":
			return ""
		case "toolResult":
			// A result after the carrier belongs to a sibling call; one before it means an earlier tool call ran.
			if carrierSeen {
				return ""
			}
			continue
		case "assistant":
		default:
			continue
		}
		text := strings.TrimSpace(e.Content)
		if !carrierSeen {
			if text != "" {
				return text
			}
			carrierSeen = true
			continue
		}
		if len(e.ToolCalls) > 0 {
			return ""
		}
		return text
	}
	return ""
}
