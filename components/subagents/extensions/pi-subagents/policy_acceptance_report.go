package pi_subagents

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"
)

// Port of src/runs/shared/acceptance.ts: reading an acceptance report out of a child's output.

const acceptanceReportNotFound = "Structured acceptance report not found."

// extractBalancedJSON is the balanced {...} object that starts at start ("" when it never closes).
func extractBalancedJSON(text string, start int) (string, bool) {
	u := []rune(text)
	depth, inString, escaped := 0, false, false
	for i := start; i < len(u); i++ {
		c := u[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return string(u[start : i+1]), true
			}
		}
	}
	return "", false
}

var (
	reportWrappers = setOf("acceptance", "acceptance-report", "acceptance_report", "acceptanceReport")
	reportFields   = map[string]string{
		"criteriaSatisfied": "criteriaSatisfied", "criteria_satisfied": "criteriaSatisfied",
		"changedFiles": "changedFiles", "changed_files": "changedFiles",
		"testsAddedOrUpdated": "testsAddedOrUpdated", "tests_added_or_updated": "testsAddedOrUpdated",
		"commandsRun": "commandsRun", "commands_run": "commandsRun",
		"validationOutput": "validationOutput", "validation_output": "validationOutput",
		"residualRisks": "residualRisks", "residual_risks": "residualRisks",
		"noStagedFiles": "noStagedFiles", "no_staged_files": "noStagedFiles",
		"diffSummary": "diffSummary", "diff_summary": "diffSummary",
		"reviewFindings": "reviewFindings", "review_findings": "reviewFindings",
		"manualNotes": "manualNotes", "manual_notes": "manualNotes", "notes": "notes",
	}
	criterionReportFields = setOf("id", "status", "evidence")
	commandReportFields   = setOf("command", "result", "summary")
)

func normalizeCriterionStatus(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	switch tok := normalizedToken(s); {
	case contains([]string{"satisfied", "met", "complete", "completed", "done", "pass", "passed", "success", "succeeded"}, tok):
		return "satisfied"
	case contains([]string{"not-satisfied", "not-met", "unmet", "incomplete", "fail", "failed"}, tok):
		return "not-satisfied"
	case contains([]string{"not-applicable", "n-a", "na", "skip", "skipped"}, tok):
		return "not-applicable"
	}
	return v
}

func normalizeCommandResult(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	switch tok := normalizedToken(s); {
	case contains([]string{"passed", "pass", "success", "successful", "succeeded", "ok"}, tok):
		return "passed"
	case contains([]string{"failed", "fail", "failure", "error"}, tok):
		return "failed"
	case contains([]string{"not-run", "not-executed", "skip", "skipped"}, tok):
		return "not-run"
	}
	return v
}

func normalizeCriterionReport(v any, pathLabel string, errs *[]string) any {
	o, ok := asObject(v)
	if !ok {
		return v
	}
	out := newObject()
	for _, k := range o.order() {
		fv := o.vals[k]
		if !criterionReportFields[k] {
			*errs = append(*errs, pathLabel+"."+k+": unsupported acceptance criterion field")
			continue
		}
		switch {
		case k == "id":
			if s, ok := fv.(string); ok {
				fv = normalizedToken(s)
			}
		case k == "status":
			fv = normalizeCriterionStatus(fv)
		}
		out.set(k, fv)
	}
	return out
}

func normalizeCommandReport(v any, pathLabel string, errs *[]string) any {
	o, ok := asObject(v)
	if !ok {
		return v
	}
	out := newObject()
	for _, k := range o.order() {
		fv := o.vals[k]
		if !commandReportFields[k] {
			*errs = append(*errs, pathLabel+"."+k+": unsupported acceptance command field")
			continue
		}
		if k == "result" {
			fv = normalizeCommandResult(fv)
		}
		out.set(k, fv)
	}
	return out
}

// field is object[key] where an absent key is undefined (reported as "missing"), not null.
func field(o *jsObject, key string) any {
	if v, ok := o.vals[key]; ok {
		return v
	}
	return undef{}
}

func pathFor(base, segment string) string {
	if base != "" {
		return base + "." + segment
	}
	return segment
}

// listOrSingle is `Array.isArray(v) ? v : object ? [v] : v`.
func listOrSingle(v any) any {
	if _, ok := asArray(v); ok {
		return v
	}
	if _, ok := asObject(v); ok {
		return []any{v}
	}
	return v
}

func normalizeAcceptanceReportValue(value any, pathLabel string) (any, string, []string) {
	errs := []string{}
	reportValue, reportPath := value, pathLabel
	if rec, ok := asObject(reportValue); ok {
		var wrappers []string
		for _, k := range rec.order() {
			if reportWrappers[k] {
				wrappers = append(wrappers, k)
			}
		}
		if len(wrappers) > 0 {
			w := wrappers[0]
			if len(wrappers) > 1 {
				label := pathLabel
				if label == "" {
					label = "acceptance-report"
				}
				errs = append(errs, label+": multiple acceptance report wrappers are ambiguous")
			}
			for _, k := range rec.order() {
				if k != w {
					errs = append(errs, pathFor(pathLabel, k)+": unsupported alongside acceptance report wrapper '"+w+"'")
				}
			}
			reportValue = rec.vals[w]
			reportPath = pathFor(pathLabel, w)
		}
	}
	rv, ok := asObject(reportValue)
	if !ok {
		return reportValue, reportPath, errs
	}
	out := newObject()
	for _, k := range rv.order() {
		fv := rv.vals[k]
		canonical, known := reportFields[k]
		if !known {
			errs = append(errs, pathFor(reportPath, k)+": unsupported acceptance report field")
			continue
		}
		if out.hasOwn(canonical) {
			errs = append(errs, pathFor(reportPath, k)+": duplicates normalized field '"+canonical+"'")
			continue
		}
		fieldPath := pathFor(reportPath, canonical)
		switch canonical {
		case "criteriaSatisfied", "commandsRun":
			items := listOrSingle(fv)
			if arr, ok := asArray(items); ok {
				norm := make([]any, len(arr))
				for i, it := range arr {
					if canonical == "criteriaSatisfied" {
						norm[i] = normalizeCriterionReport(it, fmt.Sprintf("%s[%d]", fieldPath, i), &errs)
					} else {
						norm[i] = normalizeCommandReport(it, fmt.Sprintf("%s[%d]", fieldPath, i), &errs)
					}
				}
				out.set(canonical, norm)
			} else {
				out.set(canonical, items)
			}
		case "changedFiles", "testsAddedOrUpdated", "validationOutput", "residualRisks", "reviewFindings":
			items := fv
			if s, ok := fv.(string); ok {
				items = []any{s}
			}
			if arr, ok := asArray(items); ok {
				kept := []any{}
				for _, it := range arr {
					if s, isStr := it.(string); !isStr || jsTrim(s) != "" {
						kept = append(kept, it)
					}
				}
				out.set(canonical, kept)
			} else {
				out.set(canonical, items)
			}
		case "noStagedFiles":
			if s, ok := fv.(string); ok {
				switch jsTrim(strings.ToLower(s)) {
				case "true":
					fv = true
				case "false":
					fv = false
				}
			}
			out.set(canonical, fv)
		default:
			out.set(canonical, fv)
		}
	}
	return out, reportPath, errs
}

func describeValidationValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case undef:
		return "missing"
	case string:
		short := x
		if len(utf16.Encode([]rune(x))) > 80 {
			short = string(utf16.Decode(utf16.Encode([]rune(x))[:77])) + "..."
		}
		return marshalJSON(short, "")
	case bool:
		return "boolean " + jsToString(x)
	}
	if _, ok := asArray(v); ok {
		return "array"
	}
	if _, ok := asObject(v); ok {
		return "object"
	}
	if f, ok := asNumber(v); ok {
		return "number " + jsNumber(f)
	}
	return "missing"
}

func pushTypeError(errs *[]string, pathLabel, expected string, v any) {
	*errs = append(*errs, fmt.Sprintf("%s: expected %s; got %s", pathLabel, expected, describeValidationValue(v)))
}

func validateStringArrayField(errs *[]string, v any, pathLabel string) {
	arr, ok := asArray(v)
	if !ok {
		pushTypeError(errs, pathLabel, "string[]", v)
		return
	}
	for i, item := range arr {
		if s, isStr := item.(string); !isStr || jsTrim(s) == "" {
			pushTypeError(errs, fmt.Sprintf("%s[%d]", pathLabel, i), "non-empty string", item)
		}
	}
}

func isStringArray(v any) bool {
	arr, ok := asArray(v)
	if !ok {
		return false
	}
	for _, x := range arr {
		if _, isStr := x.(string); !isStr {
			return false
		}
	}
	return true
}

// validateAcceptanceReport normalizes and validates a report value: the canonical report (nil with errors when invalid).
func validateAcceptanceReport(value any, pathLabel string) (*jsObject, []string) {
	nv, np, errs := normalizeAcceptanceReportValue(value, pathLabel)
	pathLabel = np
	report, ok := asObject(nv)
	if !ok {
		label := pathLabel
		if label == "" {
			label = "acceptance-report"
		}
		pushTypeError(&errs, label, "object", nv)
		return nil, errs
	}
	get := func(k string) (any, bool) { v, ok := report.vals[k]; return v, ok && !isUndefined(v) }
	if cs, ok := get("criteriaSatisfied"); ok {
		arr, isArr := asArray(cs)
		if !isArr {
			pushTypeError(&errs, pathFor(pathLabel, "criteriaSatisfied"), "array", cs)
		} else {
			ids := map[string]bool{}
			for i, item := range arr {
				ip := fmt.Sprintf("%s[%d]", pathFor(pathLabel, "criteriaSatisfied"), i)
				c, ok := asObject(item)
				if !ok {
					pushTypeError(&errs, ip, "object", item)
					continue
				}
				id := field(c, "id")
				if !isUndefined(id) {
					if _, isStr := id.(string); !isStr {
						pushTypeError(&errs, ip+".id", "string", id)
					} else if idS := id.(string); idS != "" {
						if ids[idS] {
							errs = append(errs, ip+".id: duplicate normalized criterion id '"+idS+"'")
						}
						ids[idS] = true
					}
				}
				if st := field(c, "status"); st != "satisfied" && st != "not-satisfied" && st != "not-applicable" {
					pushTypeError(&errs, ip+".status", `one of "satisfied", "not-satisfied", "not-applicable"`, st)
				}
				if e, isStr := field(c, "evidence").(string); !isStr || jsTrim(e) == "" {
					pushTypeError(&errs, ip+".evidence", "non-empty string", field(c, "evidence"))
				}
			}
		}
	}
	for _, k := range []string{"changedFiles", "testsAddedOrUpdated"} {
		if v, ok := get(k); ok {
			validateStringArrayField(&errs, v, pathFor(pathLabel, k))
		}
	}
	if cr, ok := get("commandsRun"); ok {
		arr, isArr := asArray(cr)
		if !isArr {
			pushTypeError(&errs, pathFor(pathLabel, "commandsRun"), "array", cr)
		} else {
			for i, item := range arr {
				ip := fmt.Sprintf("%s[%d]", pathFor(pathLabel, "commandsRun"), i)
				c, ok := asObject(item)
				if !ok {
					pushTypeError(&errs, ip, "object", item)
					continue
				}
				if s, isStr := field(c, "command").(string); !isStr || jsTrim(s) == "" {
					pushTypeError(&errs, ip+".command", "non-empty string", field(c, "command"))
				}
				if r := field(c, "result"); r != "passed" && r != "failed" && r != "not-run" {
					pushTypeError(&errs, ip+".result", `one of "passed", "failed", "not-run"`, r)
				}
				if s, isStr := field(c, "summary").(string); !isStr || jsTrim(s) == "" {
					pushTypeError(&errs, ip+".summary", "non-empty string", field(c, "summary"))
				}
			}
		}
	}
	for _, k := range []string{"validationOutput", "residualRisks"} {
		if v, ok := get(k); ok {
			validateStringArrayField(&errs, v, pathFor(pathLabel, k))
		}
	}
	if v, ok := get("noStagedFiles"); ok {
		if _, isBool := v.(bool); !isBool {
			pushTypeError(&errs, pathFor(pathLabel, "noStagedFiles"), "boolean", v)
		}
	}
	if v, ok := get("diffSummary"); ok {
		if s, isStr := v.(string); !isStr || jsTrim(s) == "" {
			pushTypeError(&errs, pathFor(pathLabel, "diffSummary"), "non-empty string", v)
		}
	}
	if v, ok := get("reviewFindings"); ok {
		validateStringArrayField(&errs, v, pathFor(pathLabel, "reviewFindings"))
	}
	for _, k := range []string{"manualNotes", "notes"} {
		if v, ok := get(k); ok {
			if _, isStr := v.(string); !isStr {
				pushTypeError(&errs, pathFor(pathLabel, k), "string", v)
			}
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	for _, k := range []string{"criteriaSatisfied", "changedFiles", "testsAddedOrUpdated", "commandsRun", "validationOutput", "residualRisks", "noStagedFiles", "diffSummary", "manualNotes", "notes", "reviewFindings"} {
		if _, ok := get(k); ok {
			return report, errs
		}
	}
	label := pathLabel
	if label == "" {
		label = "acceptance-report"
	}
	return nil, []string{label + ": expected at least one acceptance report field"}
}

func hasGenericAcceptanceReportSignal(v any) bool {
	rec, ok := asObject(v)
	if !ok || !rec.hasOwn("criteriaSatisfied") {
		return false
	}
	for _, k := range []string{"changedFiles", "testsAddedOrUpdated", "commandsRun", "validationOutput", "residualRisks", "noStagedFiles", "diffSummary", "reviewFindings", "manualNotes"} {
		if rec.hasOwn(k) {
			return true
		}
	}
	return false
}

// parseReportJSON is JSON.parse of the body, falling back to its first balanced object; the error is the V8 message.
func parseReportJSON(body string) (any, string) {
	trimmed := jsTrim(body)
	v, msg := jsJSONParse(trimmed)
	if msg == "" {
		return v, ""
	}
	if start := strings.Index(trimmed, "{"); start > 0 {
		startRune := len([]rune(trimmed[:start]))
		if j, ok := extractBalancedJSON(trimmed, startRune); ok {
			v2, msg2 := jsJSONParse(j)
			return v2, msg2
		}
	}
	return nil, msg
}

func fencedBlocks(output, tag string) []string {
	re := regexp.MustCompile("(?is)```" + tag + `\s*\n(.*?)` + "```")
	var out []string
	for _, m := range re.FindAllStringSubmatch(output, -1) {
		if b := jsTrim(m[1]); b != "" {
			out = append(out, b)
		}
	}
	return out
}

// acceptanceReportParse is the original's AcceptanceReportParseResult.
type acceptanceReportParse struct {
	Report     *jsObject
	Error      string
	Malformed  bool
	SourcePath string
}

func parseAcceptanceReportBody(body string) (*jsObject, []string, string) {
	v, msg := parseReportJSON(body)
	if msg != "" {
		return nil, nil, msg
	}
	r, errs := validateAcceptanceReport(v, "")
	return r, errs, ""
}

var unterminatedFence = regexp.MustCompile("(?i)```acceptance[-_]report\\b[^\n]*\n")

func parseUnterminatedAcceptanceReportFence(output string) acceptanceReportParse {
	loc := unterminatedFence.FindStringIndex(output)
	if loc == nil {
		return acceptanceReportParse{}
	}
	if strings.Contains(output[loc[1]:], "```") {
		return acceptanceReportParse{}
	}
	v, msg := jsJSONParse(jsTrim(output[loc[1]:]))
	if msg != "" {
		return acceptanceReportParse{Error: "Failed to parse acceptance-report: " + msg, Malformed: true}
	}
	if r, errs := validateAcceptanceReport(v, ""); r != nil {
		return acceptanceReportParse{Report: r}
	} else {
		return acceptanceReportParse{Error: "Failed to parse acceptance-report: Invalid acceptance-report: " + strings.Join(errs, "; "), Malformed: true}
	}
}

func parseGenericJSONAcceptanceReportBody(body string) (acceptanceReportParse, string) {
	parsed, msg := parseReportJSON(body)
	if msg != "" {
		return acceptanceReportParse{}, msg
	}
	nv, _, nerrs := normalizeAcceptanceReportValue(parsed, "")
	hasCriteriaMarker := false
	if o, ok := asObject(nv); ok && o.hasOwn("criteriaSatisfied") {
		hasCriteriaMarker = true
	}
	if !hasGenericAcceptanceReportSignal(nv) && !(hasCriteriaMarker && len(nerrs) > 0) {
		return acceptanceReportParse{}, ""
	}
	if r, errs := validateAcceptanceReport(parsed, ""); r != nil {
		return acceptanceReportParse{Report: r}, ""
	} else {
		return acceptanceReportParse{Error: "Invalid acceptance-report: " + strings.Join(errs, "; "), Malformed: true}, ""
	}
}

var (
	explicitFenceRe = regexp.MustCompile("(?i)```acceptance[-_]report\\b")
	markerRe        = regexp.MustCompile(`(?i)ACCEPTANCE_REPORT\s*:`)
)

// parseAcceptanceReport finds the report in an output: an acceptance-report fence, a json-family fence that has the report's
// shape, or an ACCEPTANCE_REPORT: marker.
func parseAcceptanceReport(output string) acceptanceReportParse {
	explicit := explicitFenceRe.MatchString(output)
	var parseErrors []string
	for _, body := range fencedBlocks(output, "acceptance[-_]report") {
		r, errs, msg := parseAcceptanceReportBody(body)
		switch {
		case msg != "":
			parseErrors = append(parseErrors, msg)
		case r != nil:
			return acceptanceReportParse{Report: r}
		default:
			parseErrors = append(parseErrors, "Invalid acceptance-report: "+strings.Join(errs, "; "))
		}
	}
	if len(parseErrors) > 0 {
		return acceptanceReportParse{Error: "Failed to parse acceptance-report: " + strings.Join(parseErrors, "; "), Malformed: true}
	}
	if explicit {
		if rec := parseUnterminatedAcceptanceReportFence(output); rec.Report != nil || rec.Error != "" {
			return rec
		}
		return acceptanceReportParse{Error: "Failed to parse acceptance-report: Empty or unterminated acceptance-report fence.", Malformed: true}
	}
	for _, body := range fencedBlocks(output, "(?:json|jsonc|json5)") {
		parsed, msg := parseGenericJSONAcceptanceReportBody(body)
		if msg != "" {
			continue // unrelated malformed generic JSON is not an acceptance report
		}
		if parsed.Report != nil {
			return acceptanceReportParse{Report: parsed.Report}
		}
		if parsed.Error != "" {
			return acceptanceReportParse{Error: "Failed to parse acceptance-report: " + parsed.Error, Malformed: parsed.Malformed}
		}
	}
	if loc := markerRe.FindStringIndex(output); loc != nil {
		jsonStart := strings.Index(output[loc[0]:], "{")
		if jsonStart == -1 {
			return acceptanceReportParse{Error: "Failed to parse acceptance-report: Expected a JSON object after ACCEPTANCE_REPORT:.", Malformed: true}
		}
		jsonStart += loc[0]
		j, ok := extractBalancedJSON(output, len([]rune(output[:jsonStart])))
		if !ok {
			return acceptanceReportParse{Error: "Failed to parse acceptance-report: Unterminated JSON object after ACCEPTANCE_REPORT:.", Malformed: true}
		}
		v, msg := jsJSONParse(j)
		if msg != "" {
			return acceptanceReportParse{Error: "Failed to parse acceptance-report: " + msg, Malformed: true}
		}
		if r, errs := validateAcceptanceReport(v, ""); r != nil {
			return acceptanceReportParse{Report: r}
		} else {
			return acceptanceReportParse{Error: "Failed to parse acceptance-report: Invalid acceptance-report: " + strings.Join(errs, "; "), Malformed: true}
		}
	}
	return acceptanceReportParse{Error: acceptanceReportNotFound}
}

// acceptanceFileOutput is the child-written configured output searched for a report.
type acceptanceFileOutput struct {
	Content       string
	Path          string
	Authoritative bool
	Durable       bool
}

func parseAcceptanceReportSources(output string, file *acceptanceFileOutput) acceptanceReportParse {
	fromText := func() acceptanceReportParse { return parseAcceptanceReport(output) }
	fromFile := func() acceptanceReportParse {
		if file == nil {
			return acceptanceReportParse{Error: acceptanceReportNotFound}
		}
		p := parseAcceptanceReport(file.Content)
		if p.Report != nil || p.Error == acceptanceReportNotFound {
			return p
		}
		p.Error += " (in configured output " + file.Path + ")"
		p.SourcePath = file.Path
		return p
	}
	primary, secondary := fromText, fromFile
	if file != nil && file.Authoritative {
		primary, secondary = fromFile, fromText
	}
	first := primary()
	// A malformed report in the primary source is a defect to surface, not a miss to paper over with the secondary source.
	if first.Report != nil || first.Error != acceptanceReportNotFound {
		return first
	}
	return secondary()
}

var (
	trailingFence   = regexp.MustCompile("(?is)\n?```(acceptance[-_]report|json|jsonc|json5)\\s*\n(.*?)```\\s*")
	stripFenceTail  = regexp.MustCompile("(?is)\n?```acceptance[-_]report\\s*\n.*?```\\s*$")
	stripMarkerTail = regexp.MustCompile(`(?is)\n?ACCEPTANCE_REPORT\s*:\s*\{.*\}\s*$`)
)

// stripAcceptanceReport removes a trailing report from an output (an unrelated or invalid json fence stays).
func stripAcceptanceReport(output string) string {
	var found struct {
		index int
		tag   string
		body  string
		ok    bool
	}
	for _, m := range trailingFence.FindAllStringSubmatchIndex(output, -1) {
		if jsTrim(output[m[1]:]) == "" && m[3] > m[2] && m[5] > m[4] {
			found.index, found.tag, found.body, found.ok = m[0], strings.ToLower(output[m[2]:m[3]]), output[m[4]:m[5]], true
		}
	}
	if found.ok {
		if found.tag == "acceptance-report" || found.tag == "acceptance_report" {
			return strings.TrimRight(output[:found.index], jsSpaceChars)
		}
		if p, msg := parseGenericJSONAcceptanceReportBody(found.body); msg == "" && p.Report != nil {
			return strings.TrimRight(output[:found.index], jsSpaceChars)
		}
	}
	out := stripFenceTail.ReplaceAllString(output, "")
	out = stripMarkerTail.ReplaceAllString(out, "")
	return strings.TrimRight(out, jsSpaceChars)
}

const jsSpaceChars = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"
