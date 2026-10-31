package pi_subagents

import (
	"strings"
)

// The original's validateAcceptanceInput (src/runs/shared/acceptance.ts): the
// acceptance policy of a chain step, a task or a run, checked before anything
// launches. It returns the messages of every problem, in the original's order.

var (
	acceptanceLevels = map[string]bool{"auto": true, "none": true, "attested": true, "checked": true, "verified": true}
	acceptanceEvidenceKinds = []string{
		"changed-files", "tests-added", "commands-run", "validation-output",
		"residual-risks", "no-staged-files", "diff-summary", "review-findings", "manual-notes",
	}
	acceptanceEvidenceSet = func() map[string]bool {
		m := map[string]bool{}
		for _, k := range acceptanceEvidenceKinds {
			m[k] = true
		}
		return m
	}()
	acceptanceConfigKeys = setOf("level", "report", "preserveStagedIndex", "criteria", "evidence", "verify", "review", "stopRules", "reason")
	acceptanceGateKeys   = setOf("id", "must", "evidence", "severity")
	acceptanceVerifyKeys = setOf("id", "command", "timeoutMs", "cwd", "env", "allowFailure", "output", "schema")
	acceptanceReviewKeys = setOf("agent", "focus", "required")
)

const (
	acceptanceObjectExample     = `Example: { level: "checked", evidence: ["commands-run", "changed-files"] }.`
	explicitReviewedUnavailable = "is an achieved status, not a requestable acceptance level. For a read-only reviewer call, omit acceptance. To require independent review of a writer result, use acceptance.review.required and orchestrate the reviewer separately."
)

func setOf(keys ...string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// objAt is a field read that returns nil when absent or not an object.
func (o *jsObject) objAt(key string) *jsObject {
	if o == nil {
		return nil
	}
	v, _ := o.get(key)
	x, _ := v.(*jsObject)
	return x
}

// arrAt is a field read that returns nil when absent or not an array.
func (o *jsObject) arrAt(key string) []any {
	if o == nil {
		return nil
	}
	v, _ := o.get(key)
	x, _ := v.([]any)
	return x
}

func acceptanceEvidenceHelpText() string {
	return "Supported evidence kinds: " + strings.Join(acceptanceEvidenceKinds, ", ") + `. Example: { level: "checked", evidence: ["commands-run", "changed-files"] }.`
}

// normalizedToken is the original's: trimmed, lower case, white space and
// underscores to dashes, runs collapsed.
func normalizedToken(value string) string {
	var b strings.Builder
	inSep := false
	for _, r := range strings.ToLower(jsTrim(value)) {
		if r == ' ' || r == '\t' || r == '_' || isJSSpace(r) {
			inSep = true
			continue
		}
		if inSep && b.Len() > 0 {
			b.WriteByte('-')
		}
		inSep = false
		b.WriteRune(r)
	}
	s := b.String()
	s = dashRun.ReplaceAllString(s, "-")
	return s
}

// normalizeAcceptanceJSON parses a `{`-prefixed acceptance string as JSON, as
// the original's normalizeAcceptanceValue does.
func normalizeAcceptanceJSON(input any, pathLabel string) (value any, errText string) {
	s, isString := input.(string)
	if !isString {
		return input, ""
	}
	trimmed := jsTrim(s)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return input, ""
	}
	parsed, err := parseJSON([]byte(trimmed))
	if err != nil {
		return input, pathLabel + " JSON string must encode a valid acceptance object: " + err.Error()
	}
	if parsed == nil {
		return input, pathLabel + " JSON string must encode an object; got null."
	}
	switch parsed.(type) {
	case *jsObject:
		return parsed, ""
	case []any:
		return input, pathLabel + " JSON string must encode an object; got an array."
	}
	return input, pathLabel + " JSON string must encode an object; got " + typeofJSON(parsed) + "."
}

func typeofJSON(v any) string {
	switch v.(type) {
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	}
	return "unknown"
}

// ValidateAcceptanceInput is the original's validateAcceptanceInput.
func ValidateAcceptanceInput(input any, pathLabel string) []string {
	if pathLabel == "" {
		pathLabel = "acceptance"
	}
	if input == nil {
		return nil
	}
	if b, isBool := input.(bool); isBool && !b {
		return nil // false disables acceptance
	}
	normalized, errText := normalizeAcceptanceJSON(input, pathLabel)
	if errText != "" {
		return []string{errText}
	}
	input = normalized
	// A host call carries decoded Go maps; the validator reads jsObjects.
	if _, isMap := input.(map[string]any); isMap {
		if round, err := parseJSON([]byte(marshalJSON(input, ""))); err == nil {
			input = round
		}
	}
	if s, isString := input.(string); isString {
		switch {
		case s == "reviewed":
			return []string{pathLabel + " " + explicitReviewedUnavailable}
		case !acceptanceLevels[s]:
			return []string{pathLabel + " has invalid level '" + s + "'."}
		case s == "none":
			return []string{pathLabel + ` level "none" requires a reason; use { level: "none", reason: "..." }.`}
		case s == "verified":
			return []string{pathLabel + ` level "verified" requires object form with at least one runtime verify command. Use level "checked" or provide a non-empty acceptance.verify array.`}
		}
		return nil
	}
	obj, isObj := input.(*jsObject)
	if !isObj {
		return []string{pathLabel + " must be a string level, false, or an object. " + acceptanceObjectExample}
	}
	var errors []string
	push := func(msg string) { errors = append(errors, msg) }
	keys := obj.order()
	for _, key := range keys {
		if !acceptanceConfigKeys[key] {
			push(pathLabel + "." + key + " is not supported.")
		}
	}
	level, _ := obj.str("level")
	if level == "reviewed" {
		push(pathLabel + ".level " + explicitReviewedUnavailable)
	} else if _, present := obj.get("level"); present && !acceptanceLevels[level] {
		push(pathLabel + ".level must be one of auto, none, attested, checked, verified.")
	}
	if report, present := obj.get("report"); present {
		if s, isString := report.(string); !isString || (s != "on" && s != "off") {
			push(pathLabel + ".report must be on or off.")
		}
	}
	if psi, present := obj.get("preserveStagedIndex"); present {
		if b, isBool := psi.(bool); !isBool || !b {
			push(pathLabel + ".preserveStagedIndex must be true when provided.")
		} else if level != "checked" && level != "verified" {
			push(pathLabel + ".preserveStagedIndex requires level checked or verified.")
		}
	}
	if level == "none" {
		reason, _ := obj.str("reason")
		if jsTrim(reason) == "" {
			push(pathLabel + ".reason is required when level is none.")
		}
	}
	if reason, present := obj.get("reason"); present {
		if _, isString := reason.(string); !isString {
			push(pathLabel + ".reason must be a string.")
		}
	}
	criteria, criteriaPresent := obj.get("criteria")
	if criteriaPresent {
		if _, isArr := criteria.([]any); !isArr {
			push(pathLabel + ".criteria must be an array.")
		}
	}
	if arr, isArr := criteria.([]any); isArr {
		seen := map[string]bool{}
		for i, criterion := range arr {
			if s, isString := criterion.(string); isString {
				_ = s
				continue
			}
			criterionPath := pathLabel + ".criteria[" + formatInt(int64(i)) + "]"
			gate, isObj := criterion.(*jsObject)
			if !isObj {
				push(criterionPath + " must be a string or an object.")
				continue
			}
			for _, key := range gate.order() {
				if !acceptanceGateKeys[key] {
					push(criterionPath + "." + key + " is not supported.")
				}
			}
			id, _ := gate.str("id")
			if jsTrim(id) == "" {
				push(criterionPath + ".id is required.")
			} else {
				norm := normalizedToken(id)
				if seen[norm] {
					push(criterionPath + ".id duplicates normalized criterion id '" + norm + "'.")
				}
				seen[norm] = true
			}
			must, _ := gate.str("must")
			if jsTrim(must) == "" {
				push(criterionPath + ".must is required.")
			}
			if ev, present := gate.get("evidence"); present {
				arr, isArr := ev.([]any)
				if !isArr {
					push(criterionPath + ".evidence must be an array. " + acceptanceEvidenceHelpText())
				} else {
					for j, item := range arr {
						s, isString := item.(string)
						if !isString || !acceptanceEvidenceSet[s] {
							push(unsupportedEvidenceKindMessage(criterionPath+".evidence["+formatInt(int64(j))+"]", item))
						}
					}
				}
			}
			if sev, present := gate.get("severity"); present {
				if s, isString := sev.(string); !isString || (s != "required" && s != "recommended") {
					push(criterionPath + ".severity must be required or recommended.")
				}
			}
		}
	}
	if ev, present := obj.get("evidence"); present {
		if arr, isArr := ev.([]any); isArr {
			for i, item := range arr {
				s, isString := item.(string)
				if !isString || !acceptanceEvidenceSet[s] {
					push(unsupportedEvidenceKindMessage(pathLabel+".evidence["+formatInt(int64(i))+"]", item))
				}
			}
		} else {
			push(pathLabel + ".evidence must be an array. " + acceptanceEvidenceHelpText())
		}
	}
	verify, verifyPresent := obj.get("verify")
	if level == "verified" {
		arr, isArr := verify.([]any)
		if !isArr || len(arr) == 0 {
			push(pathLabel + `.verify must contain at least one runtime command when level is verified. Use level "checked" or provide a non-empty acceptance.verify array.`)
		}
	} else if verifyPresent {
		if _, isArr := verify.([]any); !isArr {
			push(pathLabel + ".verify must be an array.")
		}
	}
	if arr, isArr := verify.([]any); isArr {
		for i, command := range arr {
			cmd, isObj := command.(*jsObject)
			index := pathLabel + ".verify[" + formatInt(int64(i)) + "]"
			if !isObj || command == nil {
				push(index + " must be an object.")
				continue
			}
			for _, key := range cmd.order() {
				if !acceptanceVerifyKeys[key] {
					push(index + "." + key + " is not supported.")
				}
			}
			if id, _ := cmd.str("id"); jsTrim(id) == "" {
				push(index + ".id is required.")
			}
			if c, _ := cmd.str("command"); jsTrim(c) == "" {
				push(index + ".command is required.")
			}
			if t, present := cmd.get("timeoutMs"); present {
				if n, isNum := t.(float64); !isNum || n != float64(int(n)) || n < 1 {
					push(index + ".timeoutMs must be an integer >= 1.")
				}
			}
			if cwdValue, present := cmd.get("cwd"); present {
				if _, isString := cwdValue.(string); !isString {
					push(index + ".cwd must be a string.")
				}
			}
			if env, present := cmd.get("env"); present {
				envObj, isObj := env.(*jsObject)
				if !isObj {
					push(index + ".env must be an object.")
				} else {
					for _, envKey := range envObj.order() {
						v, _ := envObj.get(envKey)
						if _, isString := v.(string); !isString {
							push(index + ".env." + envKey + " must be a string.")
						}
					}
				}
			}
			if af, present := cmd.get("allowFailure"); present {
				if _, isBool := af.(bool); !isBool {
					push(index + ".allowFailure must be a boolean.")
				}
			}
			if out, present := cmd.get("output"); present {
				if s, isString := out.(string); !isString || s != "json" {
					push(index + `.output must be "json" when present.`)
				}
			}
			if schema, present := cmd.get("schema"); present {
				if out, _ := cmd.str("output"); out != "json" {
					push(index + `.schema requires output: "json".`)
				}
				if _, isObj := schema.(*jsObject); !isObj {
					push(index + ".schema must be a JSON Schema object.")
				}
			}
		}
	}
	if review, present := obj.get("review"); present && review != nil {
		if b, isBool := review.(bool); isBool && !b {
			// false disables review
		} else {
			reviewObj, isObj := review.(*jsObject)
			if !isObj {
				push(pathLabel + ".review must be false or an object.")
			} else {
				for _, key := range reviewObj.order() {
					if !acceptanceReviewKeys[key] {
						push(pathLabel + ".review." + key + " is not supported.")
					}
				}
				if a, present := reviewObj.get("agent"); present {
					if _, isString := a.(string); !isString {
						push(pathLabel + ".review.agent must be a string.")
					}
				}
				if f, present := reviewObj.get("focus"); present {
					if _, isString := f.(string); !isString {
						push(pathLabel + ".review.focus must be a string.")
					}
				}
				if r, present := reviewObj.get("required"); present {
					if _, isBool := r.(bool); !isBool {
						push(pathLabel + ".review.required must be a boolean.")
					}
				}
			}
		}
	}
	if sr, present := obj.get("stopRules"); present {
		arr, isArr := sr.([]any)
		if !isArr {
			push(pathLabel + ".stopRules must be an array.")
		} else {
			for i, item := range arr {
				if _, isString := item.(string); !isString {
					push(pathLabel + ".stopRules[" + formatInt(int64(i)) + "] must be a string.")
				}
			}
		}
	}
	return errors
}

func unsupportedEvidenceKindMessage(pathLabel string, item any) string {
	value := ""
	if s, isString := item.(string); isString {
		value = ` "` + s + `"`
	}
	return pathLabel + value + " is not a supported evidence kind. " + acceptanceEvidenceHelpText()
}
