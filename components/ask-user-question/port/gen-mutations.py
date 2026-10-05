#!/usr/bin/env python3
"""Generates port/mutations.json: one deliberate defect per contract row. Each `find` must occur exactly
once in its file (checked here), so a mutation never silently applies to the wrong place."""
import json, os, sys

root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "rpiv-ask-user-question")
M = []


def m(name, file, find, replace):
    M.append({"name": name, "file": file, "find": find, "replace": replace})


# logic.go
m("lone-cr-kept", "logic.go", 'strings.ReplaceAll(strings.ReplaceAll(s, "\\r\\n", "\\n"), "\\r", "")', 'strings.ReplaceAll(s, "\\r\\n", "\\n")')
m("preview-not-normalized", "logic.go", 'Preview:     normalizeLineTerminators(o.Preview),', 'Preview:     o.Preview,')
m("header-not-normalized", "logic.go", 'Header:      normalizeLineTerminators(q.Header),', 'Header:      q.Header,')
m("multiselect-lost-on-normalize", "logic.go", 'MultiSelect: q.MultiSelect,\n\t\t}\n\t\tfor _, o := range q.Options {', '}\n\t\tfor _, o := range q.Options {')
m("no-questions-allowed", "logic.go", 'if len(p.Questions) == 0 {', 'if false {')
m("five-questions-allowed", "logic.go", 'if len(p.Questions) > maxQuestions {', 'if len(p.Questions) > maxQuestions+1 {')
m("duplicate-question-allowed", "logic.go", 'if seenQuestions[q.Question] {', 'if false {')
m("one-option-allowed", "logic.go", 'if len(q.Options) < minOptions {', 'if len(q.Options) < 1 {')
m("reserved-label-allowed", "logic.go", 'if o.Label == reserved {', 'if o.Label == reserved && false {')
m("duplicate-label-allowed", "logic.go", 'if seen[o.Label] {', 'if false {')
m("reserved-next-lost", "logic.go", 'var reservedLabels = []string{"Other", labelOther, labelNext}', 'var reservedLabels = []string{"Other", labelOther}')
m("reserved-other-lost", "logic.go", 'var reservedLabels = []string{"Other", labelOther, labelNext}', 'var reservedLabels = []string{labelOther, labelNext}')
m("multi-has-no-next", "logic.go", 'if q.MultiSelect {\n\t\titems = append(items, selectItem{Kind: "next"', 'if false {\n\t\titems = append(items, selectItem{Kind: "next"')
m("option-keeps-preview-row", "logic.go", 'items = append(items, selectItem{Kind: "option", Label: o.Label, Description: o.Description})', 'items = append(items, selectItem{Kind: "option", Label: o.Label, Description: o.Preview})')
m("multi-empty-not-placeholder", "logic.go", 'return noInput\n\tcase "custom":', 'return ""\n\tcase "custom":')
m("custom-empty-not-placeholder", "logic.go", 'if a.Answer != nil && *a.Answer != "" {', 'if a.Answer != nil {')
m("option-nil-not-placeholder", "logic.go", 'default: // "option"\n\t\tif a.Answer != nil {\n\t\t\treturn *a.Answer\n\t\t}\n\t\treturn noInput', 'default: // "option"\n\t\treturn *a.Answer')
m("segment-order", "logic.go", 'if a.Preview != "" {\n\t\tparts = append(parts, "selected preview: "+a.Preview)\n\t}\n\tif a.Notes != "" {\n\t\tparts = append(parts, "user notes: "+a.Notes)\n\t}', 'if a.Notes != "" {\n\t\tparts = append(parts, "user notes: "+a.Notes)\n\t}\n\tif a.Preview != "" {\n\t\tparts = append(parts, "selected preview: "+a.Preview)\n\t}')
m("decline-drops-answers", "logic.go", 'd.Answers, d.GlobalNote = r.Answers, r.GlobalNote', 'd.GlobalNote = r.GlobalNote')
m("decline-drops-note", "logic.go", 'd.Answers, d.GlobalNote = r.Answers, r.GlobalNote', 'd.Answers = r.Answers')
m("answers-not-matched-by-index", "logic.go", 'if a.QuestionIndex == i {', 'if a.QuestionIndex >= -i {')
m("global-note-segment-lost", "logic.go", 'if r.GlobalNote != "" {\n\t\tsegments', 'if false {\n\t\tsegments')
m("envelope-without-suffix", "logic.go", 'envelopePrefix+" "+strings.Join(segments, " ")+" "+envelopeSuffix', 'envelopePrefix+" "+strings.Join(segments, " ")')
m("decline-text", "logic.go", 'declineMessage = "User declined to answer questions"', 'declineMessage = "User declined to answer the questions"')
m("no-segments-not-decline", "logic.go", 'if len(segments) == 0 {', 'if false {')
# params.go
m("selected-presence-lost", "params.go", 'a.HasSelected = true', 'a.HasSelected = len(sel) > 0')
m("result-error-dropped", "params.go", 'r.Error, _ = m["error"].(string)', '')
m("decode-ignores-cancelled-type", "params.go", 'cancelled, ok := m["cancelled"].(bool)\n\tif !ok {\n\t\treturn nil, false\n\t}', 'cancelled, _ := m["cancelled"].(bool)')
m("non-object-question-kept", "params.go", 'm, ok := raw.(map[string]any)\n\t\tif !ok {\n\t\t\tcontinue\n\t\t}\n\t\tq := question{}', 'm, _ := raw.(map[string]any)\n\t\tq := question{}')
# rpc.go
m("preview-not-cut", "rpc.go", 'utf16Prefix(o.Preview, maxPreviewChars)', 'o.Preview')
m("preview-cut-at-runes", "rpc.go", 'if units+w > n {', 'if units+1 > n {')
m("preview-limit", "rpc.go", 'maxPreviewChars         = 600', 'maxPreviewChars         = 500')
m("header-prefix-always", "rpc.go", 'if q.Header != "" {\n\t\t\theader =', 'if true {\n\t\t\theader =')
m("option-line-dash", "rpc.go", 'o.Label + " — " + o.Description', 'o.Label + " - " + o.Description')
m("other-row-number", "rpc.go", 'strconv.Itoa(len(q.Options)+1)+". "+labelOther', 'strconv.Itoa(len(q.Options))+". "+labelOther')
m("parse-index-zero-based", "rpc.go", 'i := n - 1', 'i := n')
m("parse-index-upper-bound", "rpc.go", 'i >= count {', 'i > count {')
m("parse-int-sign-lost", "rpc.go", 'neg = s[0] == \'-\'', 'neg = false')
m("parse-int-space-kept", "rpc.go", 's = strings.TrimLeftFunc(s, isJSSpace)', '')
m("custom-input-placeholder", "rpc.go", 'typed, ok, err := ui.Input(header+q.Question+"\\n\\n"+customAnswerTitle, "")', 'typed, ok, err := ui.Input(header+q.Question+"\\n\\n"+customAnswerTitle, "x")')
m("custom-answer-trimmed", "rpc.go", 'Kind: "custom", Answer: &typed}, nil', 'Kind: "custom", Answer: func() *string { t := jsTrim(typed); return &t }()}, nil')
m("custom-answer-dismissed-not-cancel", "rpc.go", 'typed, ok, err := ui.Input(header+q.Question+"\\n\\n"+customAnswerTitle, "")\n\tif err != nil || !ok {\n\t\treturn nil, err\n\t}', 'typed, _, err := ui.Input(header+q.Question+"\\n\\n"+customAnswerTitle, "")\n\tif err != nil {\n\t\treturn nil, err\n\t}')
m("multi-placeholder", "rpc.go", 'multiSelectPlaceholder  = "1,3"', 'multiSelectPlaceholder  = "1, 3"')
m("multi-instructions", "rpc.go", 'comma-separated (e.g. "1,3")', 'comma separated (e.g. "1,3")')
m("multi-trim-lost", "rpc.go", 'trimmed := jsTrim(value)', 'trimmed := value')
m("multi-split-space-lost", "rpc.go", 'return r == \',\' || isJSSpace(r)', 'return r == \',\'')
m("multi-dedup-lost", "rpc.go", 'if !hasString(selected, label) {', 'if true {')
m("multi-bad-token-ignored", "rpc.go", 'if !ok {\n\t\t\tvalid = false\n\t\t\tbreak\n\t\t}', 'if !ok {\n\t\t\tcontinue\n\t\t}')
m("multi-token-trailing-dot", "rpc.go", 'tok = strings.TrimSuffix(tok, ".")', '')
m("multi-token-nondigit", "rpc.go", 'if r < \'0\' || r > \'9\' {', 'if r < \'0\' {')
m("multi-empty-is-cancel", "rpc.go", 'return &questionAnswer{QuestionIndex: qi, Question: q.Question, Kind: "multi", HasSelected: true}, nil', 'return nil, nil')
m("single-answer-preview-lost", "rpc.go", 'Answer: &o.Label, Preview: o.Preview}', 'Answer: &o.Label}')
m("single-unparsable-continues", "rpc.go", 'idx, ok := parseIndex(chosen, len(options))\n\tif !ok {\n\t\treturn nil, nil\n\t}', 'idx, _ := parseIndex(chosen, len(options))')
m("cancel-keeps-no-answers", "rpc.go", 'return questionnaireResult{Answers: answers, Cancelled: true}, nil', 'return questionnaireResult{Cancelled: true}, nil')
# schema.go
m("max-label", "schema.go", 'map[string]any{"maxLength": maxLabelLength}', 'map[string]any{"maxLength": maxLabelLength + 1}')
m("max-header", "schema.go", 'map[string]any{"maxLength": maxHeaderLength}', 'map[string]any{"maxLength": maxHeaderLength + 1}')
m("min-options", "schema.go", '"minItems":    minOptions,', '"minItems":    1,')
m("max-options", "schema.go", '"maxItems":    maxOptions,', '"maxItems":    maxOptions + 1,')
m("min-questions", "schema.go", '"minItems":    1,\n\t\t\t\t"maxItems":    maxQuestions,', '"minItems":    0,\n\t\t\t\t"maxItems":    maxQuestions,')
m("question-required", "schema.go", '"required": []string{"question", "header", "options"},', '"required": []string{"question", "options"},')
m("option-description-required", "schema.go", '"required": []string{"label", "description"},', '"required": []string{"label"},')
m("questions-required", "schema.go", '"required": []string{"questions"},', '"required": []string{},')
m("multi-default", "schema.go", '"default":     false,', '"default":     true,')
m("limits-text", "schema.go", '"Questions to ask the user (1-4 questions)"', '"Questions to ask the user (1-5 questions)"')
# copy.go
m("snippet-text", "copy.go", 'when requirements are ambiguous"', 'when requirement are ambiguous"')
m("guideline-lost", "copy.go", '"Do not stack multiple ask_user_question calls back-to-back — group all clarifying questions into one invocation.",', '')
# extension.go
m("no-ui-text", "extension.go", 'errNoUIText = "Error: UI not available (running in non-interactive mode)"', 'errNoUIText = "Error: UI not available"')
m("no-ui-code", "extension.go", 'questionnaireResult{Cancelled: true, Error: errNoUI}', 'questionnaireResult{Cancelled: true}')
m("validation-error-code-lost", "extension.go", 'questionnaireResult{Cancelled: true, Error: v.Error}', 'questionnaireResult{Cancelled: true}')
m("no-ui-after-validation", "extension.go", 'if !ctx.HasUI() {\n\t\treturn toResult(buildToolResult(errNoUIText', 'if false {\n\t\treturn toResult(buildToolResult(errNoUIText')
m("event-before-validation", "extension.go", 'if v := validateQuestionnaire(typed); !v.OK {', 'emit(ctx, promptEvent, promptEventPayload(typed))\n\tif v := validateQuestionnaire(typed); !v.OK {')
m("prompt-event-lost", "extension.go", 'emit(ctx, promptEvent, promptEventPayload(typed))\n\treturn a.runDialogs', 'return a.runDialogs')
m("blocked-start-lost", "extension.go", 'emit(ctx, blockedEvent, map[string]any{"active": true})\n', '')
m("blocked-not-cleared", "extension.go", 'defer emit(ctx, blockedEvent, map[string]any{"active": false})', '')
m("has-preview-always", "extension.go", '"hasPreview": o.Preview != ""', '"hasPreview": true')
m("multi-select-in-event", "extension.go", '"multiSelect": q.MultiSelect,', '"multiSelect": false,')
m("event-header-lost", "extension.go", '"header": q.Header, "multiSelect"', '"multiSelect"')
m("dialog-error-swallowed", "extension.go", 'if err != nil {\n\t\treturn nil, err\n\t}\n\treturn toResult(buildQuestionnaireResponse(&result, typed))', 'if err != nil {\n\t\tresult = questionnaireResult{Cancelled: true}\n\t}\n\treturn toResult(buildQuestionnaireResponse(&result, typed))')
m("label-lost", "extension.go", 'Label:            toolLabel,', '')
m("reconcile-strip-lost", "extension.go", 'case !ctx.HasUI() && has:', 'case false:')
m("reconcile-restore-lost", "extension.go", 'case ctx.HasUI() && !has:', 'case false:')
m("reconcile-restore-prepends", "extension.go", 'append(append([]string{}, active...), toolName)', 'append([]string{toolName}, active...)')
m("reconcile-strips-siblings", "extension.go", 'if n != toolName {\n\t\t\t\tkept = append(kept, n)', 'if false {\n\t\t\t\tkept = append(kept, n)')
m("reconcile-not-registered", "extension.go", 'e.OnEvent(sdk.EventBeforeAgentStart, reconcile)', '_ = reconcile')
m("guidance-description-ignored", "extension.go", 'if g.Description != "" {', 'if false {')
m("guidance-guidelines-ignored", "extension.go", 'if g.PromptGuidelines != nil {', 'if false {')
m("guidance-snippet-ignored", "extension.go", 'if g.PromptSnippet != "" {', 'if false {')

bad = 0
for x in M:
    src = open(os.path.join(root, x["file"])).read()
    n = src.count(x["find"])
    if n != 1:
        print(f'{x["name"]}: find occurs {n} times in {x["file"]}', file=sys.stderr)
        bad += 1
if bad:
    sys.exit(1)
json.dump(M, open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations.json"), "w"), indent=1, ensure_ascii=False)
open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations.json"), "a").write("\n")
print(len(M), "mutations")
