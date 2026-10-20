package pi_subagents

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Port of src/runs/shared/dynamic-fanout.ts and src/runs/shared/chain-outputs.ts: a chain step that expands one named
// structured output into a parallel group, and the checks that a chain's named outputs are bound before they are used. Steps
// are ordered JSON (*jsObject), the shape the original reads.

// DynamicFanoutError is a malformed or unsatisfiable dynamic step.
type DynamicFanoutError struct{ Message string }

func (e *DynamicFanoutError) Error() string { return e.Message }

func dynErr(format string, a ...any) error { return &DynamicFanoutError{fmt.Sprintf(format, a...)} }

// DynamicFanoutConfig is the chain's dynamicFanout config.
type DynamicFanoutConfig struct {
	MaxItems          *float64
	AllowRunnerFields bool
}

// DynamicMaterializedItem is one expanded item.
type DynamicMaterializedItem struct {
	Index int
	Key   string
	IDKey string
	Item  any
}

// DynamicMaterializedGroup is a step expanded into its parallel tasks.
type DynamicMaterializedGroup struct {
	Items    []DynamicMaterializedItem
	Parallel []*jsObject
}

// DynamicCollectedResult is one child's record in the collected output.
type DynamicCollectedResult struct {
	Key           string
	Index         int
	Item          any
	Agent         string
	ExitCode      *int
	Text          string
	Structured    any
	HasStructured bool
	Error         string
	TimedOut      bool
	Stopped       bool
	OutputPath    string
	ArtifactPaths *jsObject
}

// DynamicChildResult is the part of a child's result a collection reads. Text is its output: the result's `output` when it
// has one, else getSingleResultOutput (the final output or the last assistant message), which the caller computes.
type DynamicChildResult struct {
	Agent           string
	ExitCode        *int
	Text            string
	Error           string
	TimedOut        bool
	Stopped         bool
	Structured      any
	HasStructured   bool
	SavedOutputPath string
	ArtifactPaths   *jsObject
}

// ChainOutputEntry is one named output of a chain (types.ts ChainOutputMapEntry).
type ChainOutputEntry struct {
	Text          string
	Structured    any
	HasStructured bool
	Agent         string
	StepIndex     int
}

var (
	safeOutputNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	itemNamePattern       = safeOutputNamePattern
	itemRefPattern        = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(?:\.([^{}]+))?\}`)
	braceRefPattern       = regexp.MustCompile(`\{([^{}]*)\}`)
	leadingNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
	badPointerEscape      = regexp.MustCompile(`~([^01]|$)`)
	arrayIndexPattern     = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	unsafeKeyPattern      = regexp.MustCompile(`[\x00-\x1f\x7f]`)
)

var reservedTemplateNames = map[string]bool{"task": true, "previous": true, "chain_dir": true, "outputs": true}

func keySet(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func unionKeys(base map[string]bool, more ...string) map[string]bool {
	m := map[string]bool{}
	for k := range base {
		m[k] = true
	}
	for _, k := range more {
		m[k] = true
	}
	return m
}

var (
	dynamicStepKeys           = keySet("expand", "parallel", "collect", "concurrency", "failFast", "phase", "label", "acceptance", "agentContract", "gateOn")
	runnerDynamicStepKeys     = unionKeys(dynamicStepKeys, "effectiveAcceptance", "acceptanceInput", "acceptanceRole", "sessionFiles", "thinkingOverrides")
	dynamicExpandKeys         = keySet("from", "item", "key", "maxItems", "onEmpty")
	dynamicExpandFromKeys     = keySet("output", "path")
	dynamicParallelKeys       = keySet("agent", "task", "phase", "label", "outputSchema", "cwd", "machine", "output", "outputMode", "reads", "progress", "skill", "model", "fast", "toolBudget", "acceptance", "agentContract", "gateOn")
	runnerDynamicParallelKeys = unionKeys(dynamicParallelKeys, "outputName", "structured", "inheritProjectContext", "inheritGlobalContext", "inheritSkills", "skills", "outputPath", "namespaceOutputPath", "maxSubagentDepth", "timeoutMs", "waitToolEnabled", "waitToolDefaultTimeoutMs",
		"structuredOutput", "structuredOutputSchema", "tools", "excludeTools", "allowNestedSubagents", "extensions", "subagentOnlyExtensions", "mcpDirectTools", "mutationTools", "capabilityCeiling", "systemPrompt",
		"systemPromptMode", "thinking", "requestedModel", "sessionFile", "effectiveAcceptance", "acceptanceInput", "acceptanceRole", "parentSessionId", "requiredExtensions", "launchResolvedExtensions", "requestedCwd", "machineEnv")
	dynamicCollectKeys = keySet("as", "outputSchema")
)

// jsCoerce is String(value) for the places the original feeds a regex a value that may not be a string.
func jsCoerce(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case undef:
		return "undefined"
	case string:
		return x
	case float64:
		return jsNumber(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return "[object Object]"
}

func isSafeOutputName(v any) bool { return safeOutputNamePattern.MatchString(jsCoerce(v)) }

func assertJSONPointer(pointer any, label string) error {
	p, ok := pointer.(string)
	if !ok {
		return errors.New("Cannot read properties of undefined (reading 'startsWith')")
	}
	if p == "" {
		return nil
	}
	if !strings.HasPrefix(p, "/") {
		return dynErr("%s must be a JSON Pointer starting with '/'.", label)
	}
	for _, seg := range strings.Split(p[1:], "/") {
		if badPointerEscape.MatchString(seg) {
			return dynErr("%s contains invalid JSON Pointer escape.", label)
		}
	}
	return nil
}

func decodePointerSegment(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
}

// resolveJSONPointer is resolveJsonPointer.
func resolveJSONPointer(value any, pointer, label string) (any, error) {
	if err := assertJSONPointer(pointer, label); err != nil {
		return nil, err
	}
	if pointer == "" {
		return value, nil
	}
	current := value
	for _, raw := range strings.Split(pointer[1:], "/") {
		seg := decodePointerSegment(raw)
		if list, isList := current.([]any); isList {
			if !arrayIndexPattern.MatchString(seg) {
				return nil, dynErr("%s segment '%s' does not address an array index.", label, seg)
			}
			var idx int
			if _, err := fmt.Sscanf(seg, "%d", &idx); err != nil || idx >= len(list) {
				return nil, dynErr("%s does not exist.", label)
			}
			current = list[idx]
			continue
		}
		o, ok := current.(*jsObject)
		if !ok {
			return nil, dynErr("%s does not exist.", label)
		}
		next, has := o.vals[seg]
		if !has {
			return nil, dynErr("%s does not exist.", label)
		}
		current = next
	}
	return current, nil
}

func scalarToKey(value any, label string) (string, error) {
	var key string
	switch x := value.(type) {
	case string:
		key = x
	case float64:
		key = jsNumber(x)
	case bool:
		key = jsCoerce(x)
	default:
		return "", dynErr("%s must resolve to a string, number, or boolean.", label)
	}
	if jsTrim(key) == "" {
		return "", dynErr("%s resolved to an empty key.", label)
	}
	if unsafeKeyPattern.MatchString(key) {
		return "", dynErr("%s resolved to an unsafe key.", label)
	}
	if jsLength(key) > 200 {
		return "", dynErr("%s resolved to a key longer than 200 characters.", label)
	}
	return key, nil
}

var nonIDChars = regexp.MustCompile(`[^a-z0-9]+`)

// normalizeItemKeyForID is normalizeItemKeyForId.
func normalizeItemKeyForID(key string) string {
	n := strings.Trim(nonIDChars.ReplaceAllString(strings.ToLower(key), "-"), "-")
	if len(n) > 80 {
		n = n[:80]
	}
	if n == "" {
		return "item"
	}
	return n
}

func valueToTemplateText(value any, reference string) (string, error) {
	switch x := value.(type) {
	case undef:
		return "", dynErr("Unresolved item reference '%s'.", reference)
	case string:
		return x, nil
	case float64, bool, nil:
		return jsCoerce(x), nil
	}
	return marshalJSON(value, ""), nil
}

// resolveItemTemplate is resolveItemTemplate: {item} and {item.path.to.field} replaced by the item's text.
func resolveItemTemplate(template, itemName string, item any) (string, error) {
	var firstErr error
	out := itemRefPattern.ReplaceAllStringFunc(template, func(raw string) string {
		if firstErr != nil {
			return raw
		}
		m := itemRefPattern.FindStringSubmatch(raw)
		name, pathText := m[1], m[2]
		if name != itemName {
			return raw
		}
		hasPath := pathText != "" // the group needs one character at least: "" is an absent path
		if hasPath && (jsTrim(pathText) == "" || strings.Contains(pathText, "..")) {
			firstErr = dynErr("Invalid item reference '%s'.", raw)
			return raw
		}
		resolved := item
		if hasPath {
			segs := strings.Split(pathText, ".")
			for i, s := range segs {
				segs[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
			}
			var err error
			if resolved, err = resolveJSONPointer(item, "/"+strings.Join(segs, "/"), raw); err != nil {
				firstErr = err
				return raw
			}
		}
		text, err := valueToTemplateText(resolved, raw)
		if err != nil {
			firstErr = err
			return raw
		}
		return text
	})
	return out, firstErr
}

func assertOnlyKeys(value any, allowed map[string]bool, label string) error {
	o, ok := value.(*jsObject)
	if !ok || o == nil {
		return dynErr("%s must be an object.", label)
	}
	for _, k := range o.order() {
		if !allowed[k] {
			return dynErr("%s does not support field '%s'.", label, k)
		}
	}
	return nil
}

// assertNoUnresolvedItemReferences is assertNoUnresolvedItemReferences.
func assertNoUnresolvedItemReferences(template, itemName, label string) error {
	for _, m := range braceRefPattern.FindAllStringSubmatch(template, -1) {
		raw, reference := m[0], m[1]
		if reference == itemName || strings.HasPrefix(reference, itemName+".") {
			if !itemRefPattern.MatchString(raw) || reference == itemName+"." || strings.Contains(reference, "..") {
				return dynErr("Invalid item reference '%s' in %s.", raw, label)
			}
			continue
		}
		name := leadingNamePattern.FindString(reference)
		if name == itemName {
			return dynErr("Invalid item reference '%s' in %s.", raw, label)
		}
		if name != "" && reservedTemplateNames[name] {
			continue
		}
		if name != "" {
			return dynErr("Unsupported template reference '%s' in %s.", raw, label)
		}
	}
	if strings.Contains(template, "{"+itemName+".}") || regexp.MustCompile(`\{`+regexp.QuoteMeta(itemName)+`(?:\.|$)[^}]*$`).MatchString(template) {
		return dynErr("Invalid item reference in %s.", label)
	}
	return nil
}

// hasDynamicFanoutFields is hasDynamicFanoutFields: a step that carries expand or collect.
func hasDynamicFanoutFields(step any) bool {
	o, ok := step.(*jsObject)
	if !ok || o == nil {
		return false
	}
	_, e := o.vals["expand"]
	_, c := o.vals["collect"]
	return e || c
}

// validateDynamicStepShape is validateDynamicStepShape.
func validateDynamicStepShape(step *jsObject, stepIndex int, config DynamicFanoutConfig) error {
	prefix := fmt.Sprintf("Dynamic chain step %d", stepIndex+1)
	allowedStep, allowedParallel := dynamicStepKeys, dynamicParallelKeys
	if config.AllowRunnerFields {
		allowedStep, allowedParallel = runnerDynamicStepKeys, runnerDynamicParallelKeys
	}
	if err := assertOnlyKeys(step, allowedStep, prefix); err != nil {
		return err
	}
	expand := step.obj("expand")
	var from *jsObject
	if expand != nil {
		from = expand.obj("from")
	}
	if from == nil || !jsTruthy(expand.vals["from"]) {
		return dynErr("%s requires expand.from.", prefix)
	}
	if err := assertOnlyKeys(expand, dynamicExpandKeys, prefix+" expand"); err != nil {
		return err
	}
	if err := assertOnlyKeys(from, dynamicExpandFromKeys, prefix+" expand.from"); err != nil {
		return err
	}
	if !isSafeOutputName(anyOrUndef(from, "output")) {
		return dynErr("%s has invalid expand.from.output '%s'.", prefix, jsCoerce(anyOrUndef(from, "output")))
	}
	if err := assertJSONPointer(anyOrUndef(from, "path"), prefix+" expand.from.path"); err != nil {
		return err
	}
	if key, has := lpGet(expand, "key"); has {
		if err := assertJSONPointer(key, prefix+" expand.key"); err != nil {
			return err
		}
	}
	itemName := "item"
	if i, has := lpGet(expand, "item"); has && i != nil {
		itemName = jsCoerce(i)
	}
	if !itemNamePattern.MatchString(itemName) {
		return dynErr("%s has invalid expand.item '%s'.", prefix, itemName)
	}
	maxItems, hasMax := lpGet(expand, "maxItems")
	if !hasMax && config.MaxItems == nil {
		return dynErr("%s requires expand.maxItems or config.chain.dynamicFanout.maxItems.", prefix)
	}
	if hasMax {
		f, isNum := maxItems.(float64)
		if !isNum || f != float64(int64(f)) || f < 0 {
			return dynErr("%s expand.maxItems must be an integer >= 0.", prefix)
		}
	}
	if config.MaxItems != nil && (*config.MaxItems != float64(int64(*config.MaxItems)) || *config.MaxItems < 0) {
		return dynErr("config.chain.dynamicFanout.maxItems must be an integer >= 0.")
	}
	parallel, isObject := step.vals["parallel"].(*jsObject)
	if !isObject || parallel == nil {
		return dynErr("%s requires a single parallel template object and cannot mix dynamic expand/collect with static parallel arrays.", prefix)
	}
	if err := assertOnlyKeys(parallel, allowedParallel, prefix+" parallel"); err != nil {
		return err
	}
	if _, nested := parallel.vals["expand"]; nested {
		return dynErr("%s does not support nested dynamic fanout.", prefix)
	}
	if !jsTruthy(parallel.vals["agent"]) {
		return dynErr("%s parallel.agent is required.", prefix)
	}
	collect := step.obj("collect")
	if collect == nil || !jsTruthy(collect.vals["as"]) || !isSafeOutputName(collect.vals["as"]) {
		return dynErr("%s requires collect.as with a safe output name.", prefix)
	}
	if err := assertOnlyKeys(collect, dynamicCollectKeys, prefix+" collect"); err != nil {
		return err
	}
	for _, t := range []struct{ label, key string }{{"parallel.task", "task"}, {"parallel.label", "label"}} {
		v := parallel.vals[t.key]
		if !jsTruthy(v) {
			continue
		}
		template, ok := v.(string)
		if !ok {
			return errors.New("template.matchAll is not a function")
		}
		if err := assertNoUnresolvedItemReferences(template, itemName, prefix+" "+t.label); err != nil {
			return err
		}
	}
	return nil
}

// resolveDynamicFanoutItems is resolveDynamicFanoutItems.
func resolveDynamicFanoutItems(step *jsObject, outputs map[string]ChainOutputEntry, stepIndex int, config DynamicFanoutConfig) ([]DynamicMaterializedItem, error) {
	if err := validateDynamicStepShape(step, stepIndex, config); err != nil {
		return nil, err
	}
	n := stepIndex + 1
	expand := step.obj("expand")
	from := expand.obj("from")
	sourceName, _ := from.vals["output"].(string)
	source, ok := outputs[sourceName]
	if !ok {
		return nil, dynErr("Dynamic chain step %d references unknown output '%s'.", n, sourceName)
	}
	if !source.HasStructured {
		return nil, dynErr("Dynamic chain step %d requires structured output '%s'.", n, sourceName)
	}
	pointer, _ := from.vals["path"].(string)
	value, err := resolveJSONPointer(source.Structured, pointer, fmt.Sprintf("Dynamic chain step %d expand.from.path", n))
	if err != nil {
		return nil, err
	}
	list, isList := value.([]any)
	if !isList {
		return nil, dynErr("Dynamic chain step %d expand.from.path must resolve to an array.", n)
	}
	var maxItems float64
	if m, has := lpGet(expand, "maxItems"); has {
		maxItems = m.(float64)
	} else if config.MaxItems != nil {
		maxItems = *config.MaxItems
	} else {
		return nil, dynErr("Dynamic chain step %d requires an effective maxItems.", n)
	}
	if float64(len(list)) > maxItems {
		return nil, dynErr("Dynamic chain step %d resolved %d items, exceeding maxItems %s.", n, len(list), jsNumber(maxItems))
	}
	seen, seenIDs := map[string]bool{}, map[string]bool{}
	out := make([]DynamicMaterializedItem, len(list))
	keyPointer, hasKey := lpGet(expand, "key")
	for index, item := range list {
		key := itoa(index)
		if hasKey {
			p, _ := keyPointer.(string)
			label := fmt.Sprintf("Dynamic chain step %d expand.key", n)
			resolved, err := resolveJSONPointer(item, p, label)
			if err != nil {
				return nil, err
			}
			if key, err = scalarToKey(resolved, label); err != nil {
				return nil, err
			}
		}
		if seen[key] {
			return nil, dynErr("Dynamic chain step %d produced duplicate item key '%s'.", n, key)
		}
		seen[key] = true
		id := normalizeItemKeyForID(key)
		if seenIDs[id] {
			return nil, dynErr("Dynamic chain step %d produced colliding item id '%s'.", n, id)
		}
		seenIDs[id] = true
		out[index] = DynamicMaterializedItem{Index: index, Key: key, IDKey: id, Item: item}
	}
	return out, nil
}

// materializeDynamicParallelStep is materializeDynamicParallelStep.
func materializeDynamicParallelStep(step *jsObject, outputs map[string]ChainOutputEntry, stepIndex int, config DynamicFanoutConfig) (DynamicMaterializedGroup, error) {
	items, err := resolveDynamicFanoutItems(step, outputs, stepIndex, config)
	if err != nil {
		return DynamicMaterializedGroup{}, err
	}
	expand := step.obj("expand")
	if len(items) == 0 {
		onEmpty := "skip"
		if v, has := lpGet(expand, "onEmpty"); has && v != nil {
			onEmpty = jsCoerce(v)
		}
		if onEmpty == "fail" {
			return DynamicMaterializedGroup{}, dynErr("Dynamic chain step %d source array is empty.", stepIndex+1)
		}
		return DynamicMaterializedGroup{Items: items, Parallel: []*jsObject{}}, nil
	}
	itemName := "item"
	if i, has := lpGet(expand, "item"); has && i != nil {
		itemName = jsCoerce(i)
	}
	template := step.obj("parallel")
	taskTemplate := "{previous}"
	if t, ok := lpGet(template, "task"); ok && t != nil {
		taskTemplate = jsCoerce(t)
	}
	parallel := make([]*jsObject, len(items))
	for i, entry := range items {
		task, err := resolveItemTemplate(taskTemplate, itemName, entry.Item)
		if err != nil {
			return DynamicMaterializedGroup{}, err
		}
		child := template.clone()
		child.set("task", task)
		if l, ok := template.vals["label"].(string); ok && l != "" {
			label, err := resolveItemTemplate(l, itemName, entry.Item)
			if err != nil {
				return DynamicMaterializedGroup{}, err
			}
			child.set("label", label)
		}
		parallel[i] = child
	}
	return DynamicMaterializedGroup{Items: items, Parallel: parallel}, nil
}

// collectDynamicResults is collectDynamicResults: the ordered child records of a fan-out, one per item.
func collectDynamicResults(step *jsObject, items []DynamicMaterializedItem, results []*DynamicChildResult) []DynamicCollectedResult {
	templateAgent, _ := step.obj("parallel").vals["agent"].(string)
	out := make([]DynamicCollectedResult, len(items))
	for i, entry := range items {
		var r *DynamicChildResult
		if i < len(results) {
			r = results[i]
		}
		c := DynamicCollectedResult{Key: entry.Key, Index: entry.Index, Item: entry.Item, Agent: templateAgent}
		if r != nil {
			c.Agent, c.ExitCode, c.Text = r.Agent, r.ExitCode, r.Text
			c.Structured, c.HasStructured, c.Error, c.TimedOut, c.Stopped = r.Structured, r.HasStructured, r.Error, r.TimedOut, r.Stopped
			c.OutputPath, c.ArtifactPaths = r.SavedOutputPath, r.ArtifactPaths
		}
		out[i] = c
	}
	return out
}

// object is the JSON shape of a collected record, in the original's field order (undefined fields absent).
func (c DynamicCollectedResult) object() *jsObject {
	o := jo("key", c.Key, "index", float64(c.Index), "item", c.Item, "agent", c.Agent)
	if c.ExitCode == nil {
		o.set("exitCode", nil)
	} else {
		o.set("exitCode", float64(*c.ExitCode))
	}
	o.set("text", c.Text)
	if c.HasStructured {
		o.set("structured", c.Structured)
	}
	if c.Error != "" {
		o.set("error", c.Error)
	}
	if c.TimedOut {
		o.set("timedOut", true)
	}
	if c.Stopped {
		o.set("stopped", true)
	}
	if c.OutputPath != "" {
		o.set("outputPath", c.OutputPath)
	}
	if c.ArtifactPaths != nil {
		o.set("artifactPaths", c.ArtifactPaths)
	}
	return o
}

// validateDynamicCollection is validateDynamicCollection: the collected records against the step's collect.outputSchema.
func validateDynamicCollection(schema any, collected []DynamicCollectedResult) error {
	if !jsTruthy(schema) {
		return nil
	}
	value := make([]any, len(collected))
	for i, c := range collected {
		value[i] = c.object()
	}
	if violations := schemaViolations(schema, value); len(violations) > 0 {
		if len(violations) > 8 {
			violations = violations[:8]
		}
		return dynErr("Collected output validation failed: %s", strings.Join(violations, "; "))
	}
	return nil
}

// ---- chain-outputs.ts ----

// ChainOutputValidationError is a chain whose named outputs are not bound.
type ChainOutputValidationError struct{ Message string }

func (e *ChainOutputValidationError) Error() string { return e.Message }

func chainErr(format string, a ...any) error {
	return &ChainOutputValidationError{fmt.Sprintf(format, a...)}
}

var outputRefPattern = regexp.MustCompile(`\{outputs\.([^}]*)\}`)

// ChainOutputValidationContext is the part of a chain that precedes the steps validated.
type ChainOutputValidationContext struct {
	PriorOutputNames []string
	StartStepIndex   int
}

func chainOutputNamesForStep(step *jsObject) []string {
	if list, isList := step.vals["parallel"].([]any); isList {
		var names []string
		for _, t := range list {
			if to, ok := t.(*jsObject); ok {
				if as, ok := to.vals["as"].(string); ok && as != "" {
					names = append(names, as)
				}
			}
		}
		return names
	}
	if isDynamicRunnerGroup(step) {
		return []string{jsCoerce(step.obj("collect").vals["as"])}
	}
	if as, ok := step.vals["as"].(string); ok && as != "" {
		return []string{as}
	}
	return nil
}

func chainTaskTemplatesForStep(step *jsObject) []string {
	if list, isList := step.vals["parallel"].([]any); isList {
		var out []string
		for _, t := range list {
			task := "{previous}"
			if to, ok := t.(*jsObject); ok {
				if s, ok := to.vals["task"].(string); ok {
					task = s
				}
			}
			out = append(out, task)
		}
		return out
	}
	if isDynamicRunnerGroup(step) {
		p := step.obj("parallel")
		task := "{previous}"
		if s, ok := p.vals["task"].(string); ok {
			task = s
		}
		out := []string{task}
		if l, ok := p.vals["label"].(string); ok && l != "" {
			out = append(out, l)
		}
		return out
	}
	if s, ok := step.vals["task"].(string); ok {
		return []string{s}
	}
	return []string{"{previous}"}
}

// validateChainOutputBindings is validateChainOutputBindings.
func validateChainOutputBindings(steps []*jsObject, config DynamicFanoutConfig) error {
	return validateChainOutputBindingsWithContext(steps, config, ChainOutputValidationContext{})
}

// validateChainOutputBindingsWithContext is validateChainOutputBindingsWithContext.
func validateChainOutputBindingsWithContext(steps []*jsObject, config DynamicFanoutConfig, ctx ChainOutputValidationContext) error {
	available, seen := map[string]bool{}, map[string]bool{}
	for _, n := range ctx.PriorOutputNames {
		available[n], seen[n] = true, true
	}
	for stepIndex, step := range steps {
		display := ctx.StartStepIndex + stepIndex + 1
		if hasDynamicFanoutFields(step) {
			if !isDynamicRunnerGroup(step) {
				return chainErr("Dynamic chain step %d requires expand, a single parallel template object, and collect; dynamic expand/collect cannot be mixed with static parallel arrays.", display)
			}
			if err := validateDynamicStepShape(step, display-1, config); err != nil {
				var dfe *DynamicFanoutError
				if errors.As(err, &dfe) {
					return &ChainOutputValidationError{dfe.Message}
				}
				return err
			}
			output := jsCoerce(step.obj("expand").obj("from").vals["output"])
			if !available[output] {
				return chainErr("Dynamic chain step %d references unknown output '%s'. Named outputs are only available after producing step/group completes.", display, output)
			}
		}
		for _, name := range chainOutputNamesForStep(step) {
			if !safeOutputNamePattern.MatchString(name) {
				return chainErr("Invalid chain output name '%s' at step %d. Use /^[A-Za-z_][A-Za-z0-9_]*$/.", name, display)
			}
			if seen[name] {
				return chainErr("Duplicate chain output name '%s'. Each as name must be unique.", name)
			}
			seen[name] = true
		}
		for _, template := range chainTaskTemplatesForStep(step) {
			for _, m := range outputRefPattern.FindAllStringSubmatch(template, -1) {
				raw, name := m[0], m[1]
				if !safeOutputNamePattern.MatchString(name) {
					return chainErr("Invalid chain output reference '%s' at step %d. Use {outputs.name} with /^[A-Za-z_][A-Za-z0-9_]*$/ names.", raw, display)
				}
				if !available[name] {
					return chainErr("Unknown chain output reference '%s' at step %d. Named outputs are only available after producing step/group completes.", raw, display)
				}
			}
		}
		for _, name := range chainOutputNamesForStep(step) {
			available[name] = true
		}
	}
	return nil
}

// resolveOutputReferences is resolveOutputReferences: {outputs.name} replaced by the named output's text.
func resolveOutputReferences(template string, outputs map[string]ChainOutputEntry) (string, error) {
	var firstErr error
	out := outputRefPattern.ReplaceAllStringFunc(template, func(raw string) string {
		if firstErr != nil {
			return raw
		}
		name := outputRefPattern.FindStringSubmatch(raw)[1]
		if !safeOutputNamePattern.MatchString(name) {
			firstErr = chainErr("Invalid chain output reference '%s'. Use {outputs.name} with /^[A-Za-z_][A-Za-z0-9_]*$/ names.", raw)
			return raw
		}
		entry, ok := outputs[name]
		if !ok {
			firstErr = chainErr("Unknown chain output reference '%s'.", raw)
			return raw
		}
		return entry.Text
	})
	return out, firstErr
}

// chainOutputEntryFromResult is outputEntryFromResult/outputEntryFromAsyncResult: a structured output is compact JSON text.
func chainOutputEntryFromResult(agent, output string, structured any, hasStructured bool, stepIndex int) ChainOutputEntry {
	e := ChainOutputEntry{Text: output, Agent: agent, StepIndex: stepIndex}
	if hasStructured {
		e.Text, e.Structured, e.HasStructured = marshalJSON(structured, ""), structured, true
	}
	return e
}
