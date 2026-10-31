package pi_subagents

import (
	"fmt"
	"regexp"
	"strings"
)

// The original's dynamic fan-out and chain output validators
// (src/runs/shared/dynamic-fanout.ts, chain-outputs.ts): a chain step that
// expands a prior structured output into parallel tasks, and the {outputs.x}
// bindings between steps. The dynamic expansion itself needs a workflow
// runtime; its shape is validated here so a definition is refused before any
// child starts, as the original does.

var (
	safeOutputNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	dynamicStepKeys        = setOf("expand", "parallel", "collect", "concurrency", "failFast", "phase", "label", "acceptance", "agentContract", "gateOn")
	dynamicExpandKeys      = setOf("from", "item", "key", "maxItems", "onEmpty")
	dynamicExpandFromKeys  = setOf("output", "path")
	dynamicParallelKeys    = setOf("agent", "task", "phase", "label", "outputSchema", "cwd", "machine", "output", "outputMode", "reads", "progress", "skill", "model", "fast", "toolBudget", "acceptance", "agentContract", "gateOn")
	dynamicCollectKeys     = setOf("as", "outputSchema")
	reservedTemplateNames  = setOf("task", "previous", "chain_dir", "outputs")
)

// dynamicFanoutError is the original's DynamicFanoutError.
type dynamicFanoutError struct{ msg string }

func (e *dynamicFanoutError) Error() string { return e.msg }

// chainOutputError is the original's ChainOutputValidationError.
type chainOutputError struct{ msg string }

func (e *chainOutputError) Error() string { return e.msg }

// hasDynamicFanoutFields is the original's: a step declares expand or collect.
func hasDynamicFanoutFields(step *jsObject) bool {
	_, hasExpand := step.get("expand")
	_, hasCollect := step.get("collect")
	return hasExpand || hasCollect
}

// isDynamicStep reports whether a JSON step is the original's DynamicParallelStep.
func isDynamicStep(step *jsObject) bool {
	parallel, has := step.get("parallel")
	if !has {
		return false
	}
	_, isArr := parallel.([]any)
	_, hasExpand := step.get("expand")
	_, hasCollect := step.get("collect")
	return hasExpand && hasCollect && !isArr
}

// isParallelStep reports whether a JSON step is the original's ParallelStep.
func isParallelStep(step *jsObject) bool {
	parallel, has := step.get("parallel")
	if !has {
		return false
	}
	_, isArr := parallel.([]any)
	return isArr
}

func assertOnlyKeys(value *jsObject, allowed map[string]bool, label string) error {
	if value == nil {
		return &dynamicFanoutError{label + " must be an object."}
	}
	for _, key := range value.order() {
		if !allowed[key] {
			return &dynamicFanoutError{fmt.Sprintf("%s does not support field '%s'.", label, key)}
		}
	}
	return nil
}

// assertJsonPointer is the original's: a JSON Pointer starts with '/' and
// carries only valid ~ escapes.
func assertJsonPointer(pointer, label string) error {
	if pointer == "" {
		return nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return &dynamicFanoutError{label + " must be a JSON Pointer starting with '/'."}
	}
	for _, segment := range strings.Split(pointer[1:], "/") {
		if jsonPointerEscapeInvalid(segment) {
			return &dynamicFanoutError{label + " contains invalid JSON Pointer escape."}
		}
	}
	return nil
}

// jsonPointerEscapeInvalid reports whether segment has a '~' not followed by 0 or 1.
func jsonPointerEscapeInvalid(segment string) bool {
	for i := 0; i < len(segment); i++ {
		if segment[i] != '~' {
			continue
		}
		if i+1 >= len(segment) || (segment[i+1] != '0' && segment[i+1] != '1') {
			return true
		}
	}
	return false
}

// itemNamePattern is the original's ITEM_NAME_PATTERN.
var itemNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateDynamicStepShape is the original's validateDynamicStepShape: the
// whole shape of a dynamic fan-out step.
func ValidateDynamicStepShape(step *jsObject, stepIndex int, maxItems *int) error {
	prefix := fmt.Sprintf("Dynamic chain step %d", stepIndex+1)
	if err := assertOnlyKeys(step, dynamicStepKeys, prefix); err != nil {
		return err
	}
	expand := step.objAt("expand")
	from := expand.objAt("from")
	if expand == nil || from == nil {
		return &dynamicFanoutError{prefix + " requires expand.from."}
	}
	if err := assertOnlyKeys(expand, dynamicExpandKeys, prefix+" expand"); err != nil {
		return err
	}
	if err := assertOnlyKeys(from, dynamicExpandFromKeys, prefix+" expand.from"); err != nil {
		return err
	}
	output, _ := from.str("output")
	if !safeOutputNamePattern.MatchString(output) {
		return &dynamicFanoutError{fmt.Sprintf("%s has invalid expand.from.output '%s'.", prefix, output)}
	}
	path, _ := from.str("path")
	if err := assertJsonPointer(path, prefix+" expand.from.path"); err != nil {
		return err
	}
	if key, present := expand.get("key"); present {
		keyStr, _ := key.(string)
		if err := assertJsonPointer(keyStr, prefix+" expand.key"); err != nil {
			return err
		}
	}
	itemName, _ := expand.str("item")
	if itemName == "" {
		itemName = "item"
	}
	if !itemNamePattern.MatchString(itemName) {
		return &dynamicFanoutError{fmt.Sprintf("%s has invalid expand.item '%s'.", prefix, itemName)}
	}
	expandMax, hasExpandMax := expand.get("maxItems")
	if !hasExpandMax && maxItems == nil {
		return &dynamicFanoutError{prefix + " requires expand.maxItems or config.chain.dynamicFanout.maxItems."}
	}
	if hasExpandMax {
		if n, isNum := expandMax.(float64); !isNum || n != float64(int(n)) || n < 0 {
			return &dynamicFanoutError{prefix + " expand.maxItems must be an integer >= 0."}
		}
	}
	if maxItems != nil && *maxItems < 0 {
		return &dynamicFanoutError{"config.chain.dynamicFanout.maxItems must be an integer >= 0."}
	}
	parallel := step.objAt("parallel")
	parallelIsArr := step.arrAt("parallel") != nil
	if parallel == nil || parallelIsArr {
		return &dynamicFanoutError{prefix + " requires a single parallel template object and cannot mix dynamic expand/collect with static parallel arrays."}
	}
	if err := assertOnlyKeys(parallel, dynamicParallelKeys, prefix+" parallel"); err != nil {
		return err
	}
	if _, nested := parallel.get("expand"); nested {
		return &dynamicFanoutError{prefix + " does not support nested dynamic fanout."}
	}
	if agent, _ := parallel.str("agent"); agent == "" {
		return &dynamicFanoutError{prefix + " parallel.agent is required."}
	}
	collect := step.objAt("collect")
	collectAs := collect.str2("as")
	if collect == nil || !safeOutputNamePattern.MatchString(collectAs) {
		return &dynamicFanoutError{prefix + " requires collect.as with a safe output name."}
	}
	if err := assertOnlyKeys(collect, dynamicCollectKeys, prefix+" collect"); err != nil {
		return err
	}
	for _, entry := range []struct{ label, template string }{
		{"parallel.task", parallel.str2("task")},
		{"parallel.label", parallel.str2("label")},
	} {
		if entry.template != "" {
			if err := assertNoUnresolvedItemReferences(entry.template, itemName, prefix+" "+entry.label); err != nil {
				return err
			}
		}
	}
	return nil
}

// str2 is a value read that returns "" when absent or not a string.
func (o *jsObject) str2(key string) string {
	if o == nil {
		return ""
	}
	s, _ := o.str(key)
	return s
}

// assertNoUnresolvedItemReferences is the original's: a template may reference
// the item (with a valid path) or the reserved names, nothing else.
func assertNoUnresolvedItemReferences(template, itemName, label string) error {
	itemRef := regexp.MustCompile(`^\{([A-Za-z_][A-Za-z0-9_]*)`)
	for _, raw := range braceRefs(template) {
		reference := raw[1 : len(raw)-1]
		if reference == itemName || strings.HasPrefix(reference, itemName+".") {
			if strings.HasSuffix(reference, ".") || strings.Contains(reference, "..") || !itemRef.MatchString(raw) {
				return &dynamicFanoutError{fmt.Sprintf("Invalid item reference '%s' in %s.", raw, label)}
			}
			continue
		}
		name := ""
		for i, r := range reference {
			c := byte(r)
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
				continue
			}
			name = reference[:i]
			break
		}
		if name == "" && len(reference) > 0 {
			name = reference
		}
		if name == itemName {
			return &dynamicFanoutError{fmt.Sprintf("Invalid item reference '%s' in %s.", raw, label)}
		}
		if reservedTemplateNames[name] {
			continue
		}
		if name != "" {
			return &dynamicFanoutError{fmt.Sprintf("Unsupported template reference '%s' in %s.", raw, label)}
		}
	}
	if strings.Contains(template, "{"+itemName+".}") {
		return &dynamicFanoutError{fmt.Sprintf("Invalid item reference in %s.", label)}
	}
	return nil
}

// braceRefs lists every {..} reference in a template.
func braceRefs(template string) []string {
	var out []string
	for {
		open := strings.IndexByte(template, '}')
		_ = open
		i := strings.IndexByte(template, '{')
		if i == -1 {
			return out
		}
		j := strings.IndexByte(template[i:], '}')
		if j == -1 {
			return out
		}
		j += i
		out = append(out, template[i:j+1])
		template = template[j+1:]
	}
}

// ValidateChainOutputBindings is the original's validateChainOutputBindings
// for the JSON chain steps this port parses: every as name is safe and unique,
// every {outputs.x} reference names a produced output, and a dynamic step's
// expand.from names one too.
func ValidateChainOutputBindings(steps []*jsObject, dynamicMaxItems *int) error {
	available := map[string]bool{}
	seen := map[string]bool{}
	for i, step := range steps {
		display := i + 1
		if hasDynamicFanoutFields(step) {
			if !isDynamicStep(step) {
				return &chainOutputError{fmt.Sprintf("Dynamic chain step %d requires expand, a single parallel template object, and collect; dynamic expand/collect cannot be mixed with static parallel arrays.", display)}
			}
			if err := ValidateDynamicStepShape(step, i, dynamicMaxItems); err != nil {
				if _, isDynamic := err.(*dynamicFanoutError); isDynamic {
					return &chainOutputError{err.Error()}
				}
				return err
			}
			expand := step.objAt("expand")
			from := expand.objAt("from")
			output, _ := from.str("output")
			if !available[output] {
				return &chainOutputError{fmt.Sprintf("Dynamic chain step %d references unknown output '%s'. Named outputs are only available after producing step/group completes.", display, output)}
			}
		}
		for _, name := range outputNamesForStep(step) {
			if !safeOutputNamePattern.MatchString(name) {
				return &chainOutputError{fmt.Sprintf("Invalid chain output name '%s' at step %d. Use /^[A-Za-z_][A-Za-z0-9_]*$/.", name, display)}
			}
			if seen[name] {
				return &chainOutputError{fmt.Sprintf("Duplicate chain output name '%s'. Each as name must be unique.", name)}
			}
			seen[name] = true
		}
		for _, template := range taskTemplatesForStep(step) {
			for _, raw := range outputRefs(template) {
				name := raw[len("{outputs.") : len(raw)-1]
				if !safeOutputNamePattern.MatchString(name) {
					return &chainOutputError{fmt.Sprintf("Invalid chain output reference '%s' at step %d. Use {outputs.name} with /^[A-Za-z_][A-Za-z0-9_]*$ names.", raw, display)}
				}
				if !available[name] {
					return &chainOutputError{fmt.Sprintf("Unknown chain output reference '%s' at step %d. Named outputs are only available after producing step/group completes.", raw, display)}
				}
			}
		}
		for _, name := range outputNamesForStep(step) {
			available[name] = true
		}
	}
	return nil
}

// outputRefs lists every {outputs.x} reference in a template.
func outputRefs(template string) []string {
	var out []string
	rest := template
	for {
		i := strings.Index(rest, "{outputs.")
		if i == -1 {
			return out
		}
		j := strings.IndexByte(rest[i:], '}')
		if j == -1 {
			return out
		}
		j += i
		out = append(out, rest[i:j+1])
		rest = rest[j+1:]
	}
}

// outputNamesForStep is the original's: the as names a step produces.
func outputNamesForStep(step *jsObject) []string {
	if isParallelStep(step) {
		parallel := step.arrAt("parallel")
		var names []string
		for _, task := range parallel {
			if taskObj, isObj := task.(*jsObject); isObj {
				if as, _ := taskObj.str("as"); as != "" {
					names = append(names, as)
				}
			}
		}
		return names
	}
	if isDynamicStep(step) {
		collect := step.objAt("collect")
		if as, _ := collect.str("as"); as != "" {
			return []string{as}
		}
		return nil
	}
	if as, _ := step.str("as"); as != "" {
		return []string{as}
	}
	return nil
}

// taskTemplatesForStep is the original's: the templates a step interpolates.
func taskTemplatesForStep(step *jsObject) []string {
	if isParallelStep(step) {
		parallel := step.arrAt("parallel")
		var templates []string
		for _, task := range parallel {
			if taskObj, isObj := task.(*jsObject); isObj {
				t, _ := taskObj.str("task")
				if t == "" {
					t = "{previous}"
				}
				templates = append(templates, t)
			}
		}
		return templates
	}
	if isDynamicStep(step) {
		parallel := step.objAt("parallel")
		collect := step.objAt("collect")
		var templates []string
		if t, _ := parallel.str("task"); t == "" {
			templates = append(templates, "{previous}")
		} else {
			templates = append(templates, t)
		}
		if l, _ := collect.str("label"); l != "" {
			templates = append(templates, l)
		}
		return templates
	}
	if t, _ := step.str("task"); t == "" {
		return []string{"{previous}"}
	} else {
		return []string{t}
	}
}
