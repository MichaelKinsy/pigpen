package pi_subagents

import (
	"fmt"
	"regexp"
	"strings"
)

// Port of src/runs/shared/acceptance.ts (the validation half; see policy_acceptance_*.go for the rest).

var (
	validAcceptanceLevels   = map[string]bool{"auto": true, "none": true, "attested": true, "checked": true, "verified": true}
	acceptanceEvidenceKinds = []string{"changed-files", "tests-added", "commands-run", "validation-output", "residual-risks", "no-staged-files", "diff-summary", "review-findings", "manual-notes"}
	validAcceptanceEvidence = func() map[string]bool {
		m := map[string]bool{}
		for _, k := range acceptanceEvidenceKinds {
			m[k] = true
		}
		return m
	}()
	acceptanceEvidenceHelp   = "Supported evidence kinds: " + strings.Join(acceptanceEvidenceKinds, ", ") + `. Example: { level: "checked", evidence: ["commands-run", "changed-files"] }.`
	acceptanceObjectExample  = `Example: { level: "checked", evidence: ["commands-run", "changed-files"] }.`
	acceptanceConfigKeys     = setOf("level", "report", "preserveStagedIndex", "criteria", "evidence", "verify", "review", "stopRules", "reason")
	acceptanceGateKeys       = setOf("id", "must", "evidence", "severity")
	acceptanceVerifyKeys     = setOf("id", "command", "timeoutMs", "cwd", "env", "allowFailure", "output", "schema")
	acceptanceReviewKeys     = setOf("agent", "focus", "required")
	explicitReviewedUnavail  = "is an achieved status, not a requestable acceptance level. For a read-only reviewer call, omit acceptance. To require independent review of a writer result, use acceptance.review.required and orchestrate the reviewer separately."
	normalizedTokenSeparator = regexp.MustCompile(`[\s_]+`)
	normalizedTokenDashes    = regexp.MustCompile(`-+`)
)

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

// normalizedToken is the original's normalizedToken: trimmed, lowercased, whitespace and underscores as one dash.
func normalizedToken(value string) string {
	s := strings.ToLower(jsTrim(value))
	s = normalizedTokenSeparator.ReplaceAllString(s, "-")
	return normalizedTokenDashes.ReplaceAllString(s, "-")
}

// normalizeAcceptanceValue decodes an acceptance given as a JSON string (an object or array); an error is its message.
func normalizeAcceptanceValue(input any, pathLabel string) (value any, errMsg string) {
	s, ok := input.(string)
	if !ok {
		return input, ""
	}
	trimmed := jsTrim(s)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return input, ""
	}
	parsed, msg := jsJSONParse(trimmed)
	if msg != "" {
		return input, pathLabel + " JSON string must encode a valid acceptance object: " + msg
	}
	if _, isObj := asObject(parsed); !isObj {
		kind := "an array"
		if parsed == nil {
			kind = "null"
		} else if _, isArr := asArray(parsed); !isArr {
			kind = jsTypeof(parsed)
		}
		return input, fmt.Sprintf("%s JSON string must encode an object; got %s.", pathLabel, kind)
	}
	return parsed, ""
}

// jsTypeof is `typeof value` for a decoded value.
func jsTypeof(v any) string {
	switch v.(type) {
	case nil:
		return "object"
	case undef:
		return "undefined"
	case bool:
		return "boolean"
	case string:
		return "string"
	case *jsObject, map[string]any, []any, []string:
		return "object"
	}
	if _, ok := asNumber(v); ok {
		return "number"
	}
	return "object"
}

func unsupportedEvidenceKindMessage(pathLabel string, item any) string {
	value := ""
	if s, ok := item.(string); ok {
		value = ` "` + s + `"`
	}
	return pathLabel + value + " is not a supported evidence kind. " + acceptanceEvidenceHelp
}

func isEvidenceKind(item any) bool {
	s, ok := item.(string)
	return ok && validAcceptanceEvidence[s]
}

// validateAcceptanceInput is the original's validateAcceptanceInput: every problem with an acceptance value, in the
// original's order; no problems is an empty list.
func validateAcceptanceInput(input any, pathLabel string) []string {
	errors := []string{}
	if isUndefined(input) {
		return errors
	}
	if b, ok := input.(bool); ok && !b {
		return errors
	}
	input, msg := normalizeAcceptanceValue(input, pathLabel)
	if msg != "" {
		return []string{msg}
	}
	if s, ok := input.(string); ok {
		switch {
		case s == "reviewed":
			errors = append(errors, pathLabel+" "+explicitReviewedUnavail)
		case !validAcceptanceLevels[s]:
			errors = append(errors, fmt.Sprintf("%s has invalid level '%s'.", pathLabel, s))
		case s == "none":
			errors = append(errors, pathLabel+` level "none" requires a reason; use { level: "none", reason: "..." }.`)
		case s == "verified":
			errors = append(errors, pathLabel+` level "verified" requires object form with at least one runtime verify command. Use level "checked" or provide a non-empty acceptance.verify array.`)
		}
		return errors
	}
	value, ok := asObject(input)
	if !ok {
		return append(errors, pathLabel+" must be a string level, false, or an object. "+acceptanceObjectExample)
	}
	add := func(format string, a ...any) { errors = append(errors, fmt.Sprintf(format, a...)) }
	for _, key := range value.order() {
		if !acceptanceConfigKeys[key] {
			add("%s.%s is not supported.", pathLabel, key)
		}
	}
	level, hasLevel := value.get("level")
	hasLevel = hasLevel && !isUndefined(level)
	levelStr, levelIsStr := level.(string)
	if levelIsStr && levelStr == "reviewed" {
		add("%s.level %s", pathLabel, explicitReviewedUnavail)
	} else if hasLevel && !(levelIsStr && validAcceptanceLevels[levelStr]) {
		add("%s.level must be one of auto, none, attested, checked, verified.", pathLabel)
	}
	get := func(k string) (any, bool) {
		v, ok := value.get(k)
		return v, ok && !isUndefined(v)
	}
	if v, ok := get("report"); ok && v != "on" && v != "off" {
		add("%s.report must be on or off.", pathLabel)
	}
	if v, ok := get("preserveStagedIndex"); ok && v != true {
		add("%s.preserveStagedIndex must be true when provided.", pathLabel)
	}
	if v, _ := get("preserveStagedIndex"); v == true && levelStr != "checked" && levelStr != "verified" {
		add("%s.preserveStagedIndex requires level checked or verified.", pathLabel)
	}
	reason, hasReason := get("reason")
	reasonStr, reasonIsStr := reason.(string)
	if levelStr == "none" && levelIsStr && !(reasonIsStr && jsTrim(reasonStr) != "") {
		add("%s.reason is required when level is none.", pathLabel)
	}
	if hasReason && !reasonIsStr {
		add("%s.reason must be a string.", pathLabel)
	}
	criteria, hasCriteria := get("criteria")
	criteriaArr, criteriaIsArr := asArray(criteria)
	if hasCriteria && !criteriaIsArr {
		add("%s.criteria must be an array.", pathLabel)
	}
	if criteriaIsArr {
		ids := map[string]bool{}
		for index, criterion := range criteriaArr {
			if _, isStr := criterion.(string); isStr {
				continue
			}
			cp := fmt.Sprintf("%s.criteria[%d]", pathLabel, index)
			gate, ok := asObject(criterion)
			if !ok {
				add("%s must be a string or an object.", cp)
				continue
			}
			for _, key := range gate.order() {
				if !acceptanceGateKeys[key] {
					add("%s.%s is not supported.", cp, key)
				}
			}
			id, _ := gate.get("id")
			if idStr, ok := id.(string); !ok || jsTrim(idStr) == "" {
				add("%s.id is required.", cp)
			} else {
				n := normalizedToken(idStr)
				if ids[n] {
					add("%s.id duplicates normalized criterion id '%s'.", cp, n)
				}
				ids[n] = true
			}
			if must, _ := gate.get("must"); !isNonEmptyString(must) {
				add("%s.must is required.", cp)
			}
			ev, hasEv := gate.get("evidence")
			hasEv = hasEv && !isUndefined(ev)
			evArr, evIsArr := asArray(ev)
			if hasEv && !evIsArr {
				add("%s.evidence must be an array. %s", cp, acceptanceEvidenceHelp)
			}
			if evIsArr {
				for i, item := range evArr {
					if !isEvidenceKind(item) {
						errors = append(errors, unsupportedEvidenceKindMessage(fmt.Sprintf("%s.evidence[%d]", cp, i), item))
					}
				}
			}
			if sev, ok := gate.get("severity"); ok && !isUndefined(sev) && sev != "required" && sev != "recommended" {
				add("%s.severity must be required or recommended.", cp)
			}
		}
	}
	evidence, hasEvidence := get("evidence")
	if evArr, isArr := asArray(evidence); isArr {
		for i, item := range evArr {
			if !isEvidenceKind(item) {
				errors = append(errors, unsupportedEvidenceKindMessage(fmt.Sprintf("%s.evidence[%d]", pathLabel, i), item))
			}
		}
	} else if hasEvidence {
		add("%s.evidence must be an array. %s", pathLabel, acceptanceEvidenceHelp)
	}
	verify, hasVerify := get("verify")
	verifyArr, verifyIsArr := asArray(verify)
	if levelStr == "verified" && (!verifyIsArr || len(verifyArr) == 0) {
		add(`%s.verify must contain at least one runtime command when level is verified. Use level "checked" or provide a non-empty acceptance.verify array.`, pathLabel)
	} else if hasVerify && !verifyIsArr {
		add("%s.verify must be an array.", pathLabel)
	}
	if verifyIsArr {
		for index, command := range verifyArr {
			vp := fmt.Sprintf("%s.verify[%d]", pathLabel, index)
			cmd, ok := asObject(command)
			if !ok {
				add("%s must be an object.", vp)
				continue
			}
			for _, key := range cmd.order() {
				if !acceptanceVerifyKeys[key] {
					add("%s.%s is not supported.", vp, key)
				}
			}
			cget := func(k string) (any, bool) {
				v, ok := cmd.get(k)
				return v, ok && !isUndefined(v)
			}
			if id, _ := cget("id"); !isNonEmptyString(id) {
				add("%s.id is required.", vp)
			}
			if c, _ := cget("command"); !isNonEmptyString(c) {
				add("%s.command is required.", vp)
			}
			if t, ok := cget("timeoutMs"); ok {
				if f, isNum := asNumber(t); !isNum || !isIntegerValue(t) || f < 1 {
					add("%s.timeoutMs must be an integer >= 1.", vp)
				}
			}
			if c, ok := cget("cwd"); ok {
				if _, isStr := c.(string); !isStr {
					add("%s.cwd must be a string.", vp)
				}
			}
			if env, ok := cget("env"); ok {
				envObj, isObj := asObject(env)
				if !isObj {
					add("%s.env must be an object.", vp)
				} else {
					for _, k := range envObj.order() {
						if _, isStr := envObj.vals[k].(string); !isStr {
							add("%s.env.%s must be a string.", vp, k)
						}
					}
				}
			}
			if a, ok := cget("allowFailure"); ok {
				if _, isBool := a.(bool); !isBool {
					add("%s.allowFailure must be a boolean.", vp)
				}
			}
			output, hasOutput := cget("output")
			if hasOutput && output != "json" {
				add(`%s.output must be "json" when present.`, vp)
			}
			if schema, ok := cget("schema"); ok {
				if output != "json" {
					add(`%s.schema requires output: "json".`, vp)
				}
				if _, isObj := asObject(schema); !isObj {
					add("%s.schema must be a JSON Schema object.", vp)
				}
			}
		}
	}
	if review, ok := get("review"); ok && review != false {
		reviewObj, isObj := asObject(review)
		if !isObj {
			add("%s.review must be false or an object.", pathLabel)
		} else {
			for _, key := range reviewObj.order() {
				if !acceptanceReviewKeys[key] {
					add("%s.review.%s is not supported.", pathLabel, key)
				}
			}
			rget := func(k string) (any, bool) {
				v, ok := reviewObj.get(k)
				return v, ok && !isUndefined(v)
			}
			if v, ok := rget("agent"); ok {
				if _, isStr := v.(string); !isStr {
					add("%s.review.agent must be a string.", pathLabel)
				}
			}
			if v, ok := rget("focus"); ok {
				if _, isStr := v.(string); !isStr {
					add("%s.review.focus must be a string.", pathLabel)
				}
			}
			if v, ok := rget("required"); ok {
				if _, isBool := v.(bool); !isBool {
					add("%s.review.required must be a boolean.", pathLabel)
				}
			}
		}
	}
	stopRules, hasStop := get("stopRules")
	stopArr, stopIsArr := asArray(stopRules)
	if hasStop && !stopIsArr {
		add("%s.stopRules must be an array.", pathLabel)
	}
	for i, item := range stopArr {
		if _, isStr := item.(string); !isStr {
			add("%s.stopRules[%d] must be a string.", pathLabel, i)
		}
	}
	return errors
}

func isNonEmptyString(v any) bool {
	s, ok := v.(string)
	return ok && jsTrim(s) != ""
}

// normalizeAcceptanceInput is the original's normalizeAcceptanceInput: the acceptance config (an object) of any accepted
// shape: undefined or "auto" is {level: "auto"}, false is level none with a reason, a string is that level, an object (or a JSON
// string encoding one) is itself.
func normalizeAcceptanceInput(input any) (*jsObject, error) {
	v, msg := normalizeAcceptanceValue(input, "acceptance")
	if msg != "" {
		return nil, fmt.Errorf("%s", msg)
	}
	out := newObject()
	switch x := v.(type) {
	case string:
		if x == "auto" {
			out.set("level", "auto")
		} else {
			out.set("level", x)
		}
		return out, nil
	case bool:
		if !x {
			out.set("level", "none")
			out.set("reason", "disabled by deprecated false shorthand")
			return out, nil
		}
	}
	if isUndefined(v) {
		out.set("level", "auto")
		return out, nil
	}
	if obj, ok := asObject(v); ok {
		return obj.clone(), nil
	}
	return out, nil
}

// resolveAcceptanceReportMode is "off", "optional" or "required".
func resolveAcceptanceReportMode(input any) (string, error) {
	value, msg := normalizeAcceptanceValue(input, "acceptance")
	if msg != "" {
		return "", fmt.Errorf("%s", msg)
	}
	report := any(nil)
	if obj, ok := asObject(value); ok {
		report = obj.vals["report"]
	}
	switch {
	case value == false || report == "off":
		return "off", nil
	case report == "on":
		return "required", nil
	}
	return "optional", nil
}

// executionAcceptanceInput is the original's ExecutionAcceptanceInput: the acceptance of a call and of its nested tasks and
// chain steps (the shapes the public tool no longer takes, which the original still validates).
type executionAcceptanceInput struct {
	Acceptance   any
	OutputSchema any
	Tasks        []executionAcceptanceInput
	Chain        []executionAcceptanceStep
}

type executionAcceptanceStep struct {
	executionAcceptanceInput
	Parallel    []executionAcceptanceInput
	ParallelOne *executionAcceptanceInput
}

// validateExecutionAcceptancePolicy is the original's validateExecutionAcceptancePolicy.
func validateExecutionAcceptancePolicy(in executionAcceptanceInput) []string {
	errors := validateAcceptanceInput(in.Acceptance, "acceptance")
	for i, t := range in.Tasks {
		errors = append(errors, validateAcceptanceInput(t.Acceptance, fmt.Sprintf("tasks[%d].acceptance", i))...)
	}
	for i, step := range in.Chain {
		errors = append(errors, validateAcceptanceInput(step.Acceptance, fmt.Sprintf("chain[%d].acceptance", i))...)
		for j, t := range step.Parallel {
			errors = append(errors, validateAcceptanceInput(t.Acceptance, fmt.Sprintf("chain[%d].parallel[%d].acceptance", i, j))...)
		}
		if step.ParallelOne != nil {
			errors = append(errors, validateAcceptanceInput(step.ParallelOne.Acceptance, fmt.Sprintf("chain[%d].parallel.acceptance", i))...)
		}
	}
	return errors
}

// validateExecutionAcceptance adds the report-mode check (acceptance.report needs an outputSchema).
func validateExecutionAcceptance(in executionAcceptanceInput) []string {
	errors := validateExecutionAcceptancePolicy(in)
	errors = append(errors, validateAcceptanceReportMode(in.Acceptance, in.OutputSchema, "acceptance")...)
	for i, t := range in.Tasks {
		errors = append(errors, validateAcceptanceReportMode(t.Acceptance, t.OutputSchema, fmt.Sprintf("tasks[%d].acceptance", i))...)
	}
	for i, step := range in.Chain {
		errors = append(errors, validateAcceptanceReportMode(step.Acceptance, step.OutputSchema, fmt.Sprintf("chain[%d].acceptance", i))...)
		for j, t := range step.Parallel {
			errors = append(errors, validateAcceptanceReportMode(t.Acceptance, t.OutputSchema, fmt.Sprintf("chain[%d].parallel[%d].acceptance", i, j))...)
		}
		if step.ParallelOne != nil {
			errors = append(errors, validateAcceptanceReportMode(step.ParallelOne.Acceptance, step.ParallelOne.OutputSchema, fmt.Sprintf("chain[%d].parallel.acceptance", i))...)
		}
	}
	return errors
}

func validateAcceptanceReportMode(acceptance, outputSchema any, pathLabel string) []string {
	value, msg := normalizeAcceptanceValue(acceptance, pathLabel)
	if msg != "" {
		return nil
	}
	obj, ok := asObject(value)
	if !ok || !obj.hasOwn("report") {
		return nil
	}
	if isUndefined(outputSchema) || outputSchema == false {
		return []string{pathLabel + ".report requires outputSchema."}
	}
	return nil
}
