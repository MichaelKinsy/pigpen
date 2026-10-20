package pi_subagents

import (
	"fmt"
	"regexp"
	"strings"
)

// The .chain.json format and its validators (src/agents/chain-serializer.ts parseJsonChain/serializeJsonChain,
// src/runs/shared/chain-outputs.ts validateChainOutputBindings, src/runs/shared/dynamic-fanout.ts validateDynamicStepShape).

var (
	safeOutputNameRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	outputRefRe       = regexp.MustCompile(`\{outputs\.([^}]*)\}`)
	itemRefRe         = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(?:\.([^{}]+))?\}`)
	braceRefRe        = regexp.MustCompile(`\{([^{}]*)\}`)
	leadingIdentRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
	badPointerEscape  = regexp.MustCompile(`~[^01]|~$`)
	reservedTemplates = map[string]bool{"task": true, "previous": true, "chain_dir": true, "outputs": true}

	dynamicStepKeys     = keySet("expand", "parallel", "collect", "concurrency", "failFast", "phase", "label", "acceptance", "agentContract", "gateOn")
	dynamicExpandKeys   = keySet("from", "item", "key", "maxItems", "onEmpty")
	dynamicExpandFrom   = keySet("output", "path")
	dynamicParallelKeys = keySet("agent", "task", "phase", "label", "outputSchema", "cwd", "machine", "output", "outputMode", "reads", "progress", "skill", "model", "fast", "toolBudget", "acceptance", "agentContract", "gateOn")
	dynamicCollectKeys  = keySet("as", "outputSchema")
)

func keySet(k ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range k {
		m[x] = true
	}
	return m
}

// dynamicFanoutError is dynamic-fanout.ts DynamicFanoutError / chain-outputs.ts ChainOutputValidationError: a validation message.
type dynamicFanoutError struct{ msg string }

func (e dynamicFanoutError) Error() string { return e.msg }

func dfErr(format string, a ...any) error { return dynamicFanoutError{fmt.Sprintf(format, a...)} }

func assertOnlyKeys(v any, allowed map[string]bool, label string) error {
	o, ok := v.(*jsObject)
	if !ok {
		return dfErr("%s must be an object.", label)
	}
	for _, k := range o.order() {
		if !allowed[k] {
			return dfErr("%s does not support field '%s'.", label, k)
		}
	}
	return nil
}

func assertJSONPointer(pointer, label string) error {
	if pointer == "" {
		return nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return dfErr("%s must be a JSON Pointer starting with '/'.", label)
	}
	for _, seg := range strings.Split(pointer[1:], "/") {
		if badPointerEscape.MatchString(seg) {
			return dfErr("%s contains invalid JSON Pointer escape.", label)
		}
	}
	return nil
}

func assertNoUnresolvedItemReferences(template, itemName, label string) error {
	for _, m := range braceRefRe.FindAllStringSubmatch(template, -1) {
		raw, reference := m[0], m[1]
		if reference == itemName || strings.HasPrefix(reference, itemName+".") {
			if !itemRefRe.MatchString(raw) || reference == itemName+"." || strings.Contains(reference, "..") {
				return dfErr("Invalid item reference '%s' in %s.", raw, label)
			}
			continue
		}
		name := leadingIdentRe.FindString(reference)
		if name == itemName && name != "" {
			return dfErr("Invalid item reference '%s' in %s.", raw, label)
		}
		if name != "" && reservedTemplates[name] {
			continue
		}
		if name != "" {
			return dfErr("Unsupported template reference '%s' in %s.", raw, label)
		}
	}
	if strings.Contains(template, "{"+itemName+".}") || regexp.MustCompile(`\{`+regexp.QuoteMeta(itemName)+`(?:\.|$)[^}]*$`).MatchString(template) {
		return dfErr("Invalid item reference in %s.", label)
	}
	return nil
}

// validateDynamicStepShape is dynamic-fanout.ts:183 for a step decoded from JSON; configMaxItems is config.maxItems (nil: unset).
func validateDynamicStepShape(step *jsObject, stepIndex int, configMaxItems *float64) error {
	prefix := fmt.Sprintf("Dynamic chain step %d", stepIndex+1)
	if err := assertOnlyKeys(step, dynamicStepKeys, prefix); err != nil {
		return err
	}
	expand := step.obj("expand")
	var from *jsObject
	if expand != nil {
		from = expand.obj("from")
	}
	if expand == nil || from == nil {
		return dfErr("%s requires expand.from.", prefix)
	}
	if err := assertOnlyKeys(expand, dynamicExpandKeys, prefix+" expand"); err != nil {
		return err
	}
	if err := assertOnlyKeys(from, dynamicExpandFrom, prefix+" expand.from"); err != nil {
		return err
	}
	output, _ := from.vals["output"].(string)
	if !safeOutputNameRe.MatchString(output) {
		return dfErr("%s has invalid expand.from.output '%s'.", prefix, jsString(from.vals["output"]))
	}
	path, _ := from.vals["path"].(string)
	if err := assertJSONPointer(path, prefix+" expand.from.path"); err != nil {
		return err
	}
	if k, ok := expand.vals["key"]; ok && !isUndefined(k) {
		ks, _ := k.(string)
		if err := assertJSONPointer(ks, prefix+" expand.key"); err != nil {
			return err
		}
	}
	itemName := "item"
	if v, ok := expand.vals["item"]; ok && v != nil && !isUndefined(v) {
		itemName, _ = v.(string)
	}
	if !safeOutputNameRe.MatchString(itemName) {
		return dfErr("%s has invalid expand.item '%s'.", prefix, itemName)
	}
	maxItems, hasMax := expand.vals["maxItems"]
	hasMax = hasMax && !isUndefined(maxItems)
	if !hasMax && configMaxItems == nil {
		return dfErr("%s requires expand.maxItems or config.chain.dynamicFanout.maxItems.", prefix)
	}
	if hasMax {
		if n, ok := maxItems.(float64); !ok || !isIntegerNum(n) || n < 0 {
			return dfErr("%s expand.maxItems must be an integer >= 0.", prefix)
		}
	}
	if configMaxItems != nil && (!isIntegerNum(*configMaxItems) || *configMaxItems < 0) {
		return dfErr("config.chain.dynamicFanout.maxItems must be an integer >= 0.")
	}
	par, isObj := step.vals["parallel"].(*jsObject)
	if !isObj {
		return dfErr("%s requires a single parallel template object and cannot mix dynamic expand/collect with static parallel arrays.", prefix)
	}
	if err := assertOnlyKeys(par, dynamicParallelKeys, prefix+" parallel"); err != nil {
		return err
	}
	if par.has("expand") {
		return dfErr("%s does not support nested dynamic fanout.", prefix)
	}
	if ag, _ := par.vals["agent"].(string); ag == "" {
		return dfErr("%s parallel.agent is required.", prefix)
	}
	collect := step.obj("collect")
	as := ""
	if collect != nil {
		as, _ = collect.vals["as"].(string)
	}
	if as == "" || !safeOutputNameRe.MatchString(as) {
		return dfErr("%s requires collect.as with a safe output name.", prefix)
	}
	if err := assertOnlyKeys(collect, dynamicCollectKeys, prefix+" collect"); err != nil {
		return err
	}
	for _, t := range []struct{ label, key string }{{"parallel.task", "task"}, {"parallel.label", "label"}} {
		if s, _ := par.vals[t.key].(string); s != "" {
			if err := assertNoUnresolvedItemReferences(s, itemName, prefix+" "+t.label); err != nil {
				return err
			}
		}
	}
	return nil
}

func jsString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "null"
	case bool:
		return fmt.Sprint(x)
	case float64:
		return jsNumber(x)
	}
	if isUndefined(v) {
		return "undefined"
	}
	return fmt.Sprint(v)
}

func hasDynamicFanoutFields(step *jsObject) bool {
	_, e := step.vals["expand"]
	_, c := step.vals["collect"]
	return e || c
}

func isParallelChainStep(step *jsObject) bool {
	_, ok := step.vals["parallel"].([]any)
	return ok
}

func isDynamicParallelChainStep(step *jsObject) bool {
	_, isArr := step.vals["parallel"].([]any)
	_, e := step.vals["expand"]
	_, c := step.vals["collect"]
	_, p := step.vals["parallel"]
	return e && c && p && !isArr
}

func chainStepOutputNames(step *jsObject) []string {
	var names []string
	switch {
	case isParallelChainStep(step):
		for _, t := range step.vals["parallel"].([]any) {
			if o, ok := t.(*jsObject); ok {
				if as, _ := o.vals["as"].(string); as != "" {
					names = append(names, as)
				}
			}
		}
	case isDynamicParallelChainStep(step):
		if c := step.obj("collect"); c != nil {
			names = append(names, jsString(c.vals["as"]))
		}
	default:
		if as, _ := step.vals["as"].(string); as != "" {
			names = append(names, as)
		}
	}
	return names
}

func chainStepTemplates(step *jsObject) []string {
	taskOrPrev := func(o *jsObject) string {
		if t, ok := o.vals["task"].(string); ok {
			return t
		}
		return "{previous}"
	}
	switch {
	case isParallelChainStep(step):
		var out []string
		for _, t := range step.vals["parallel"].([]any) {
			o, _ := t.(*jsObject)
			if o == nil {
				o = newObject()
			}
			out = append(out, taskOrPrev(o))
		}
		return out
	case isDynamicParallelChainStep(step):
		p := step.obj("parallel")
		out := []string{taskOrPrev(p)}
		if l, _ := p.vals["label"].(string); l != "" {
			out = append(out, l)
		}
		return out
	}
	return []string{taskOrPrev(step)}
}

// ChainOutputContext is chain-outputs.ts ChainOutputValidationContext.
type ChainOutputContext struct {
	PriorOutputNames []string
	StartStepIndex   int
}

// validateChainOutputBindings is chain-outputs.ts:validateChainOutputBindings over a JSON chain's step objects; maxItems is
// the dynamic fan-out config (nil: unset).
func validateChainOutputBindings(steps []any, maxItems *float64, ctx ChainOutputContext) error {
	available, seen := map[string]bool{}, map[string]bool{}
	for _, n := range ctx.PriorOutputNames {
		available[n], seen[n] = true, true
	}
	for i, sv := range steps {
		display := ctx.StartStepIndex + i + 1
		step, _ := sv.(*jsObject)
		if step == nil {
			step = newObject()
		}
		if hasDynamicFanoutFields(step) {
			if !isDynamicParallelChainStep(step) {
				return dfErr("Dynamic chain step %d requires expand, a single parallel template object, and collect; dynamic expand/collect cannot be mixed with static parallel arrays.", display)
			}
			if err := validateDynamicStepShape(step, display-1, maxItems); err != nil {
				return err
			}
			out := jsString(step.obj("expand").obj("from").vals["output"])
			if !available[out] {
				return dfErr("Dynamic chain step %d references unknown output '%s'. Named outputs are only available after producing step/group completes.", display, out)
			}
		}
		for _, name := range chainStepOutputNames(step) {
			if !safeOutputNameRe.MatchString(name) {
				return dfErr("Invalid chain output name '%s' at step %d. Use /^[A-Za-z_][A-Za-z0-9_]*$/.", name, display)
			}
			if seen[name] {
				return dfErr("Duplicate chain output name '%s'. Each as name must be unique.", name)
			}
			seen[name] = true
		}
		for _, tpl := range chainStepTemplates(step) {
			for _, m := range outputRefRe.FindAllStringSubmatch(tpl, -1) {
				if !safeOutputNameRe.MatchString(m[1]) {
					return dfErr("Invalid chain output reference '%s' at step %d. Use {outputs.name} with /^[A-Za-z_][A-Za-z0-9_]*$/ names.", m[0], display)
				}
				if !available[m[1]] {
					return dfErr("Unknown chain output reference '%s' at step %d. Named outputs are only available after producing step/group completes.", m[0], display)
				}
			}
		}
		for _, name := range chainStepOutputNames(step) {
			available[name] = true
		}
	}
	return nil
}

func validateJSONChainToolBudget(v any, label string) error {
	if msg := validateToolBudget(v, label); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func acceptanceErrorOf(filePath string, errs []string) error {
	return fmt.Errorf("Invalid JSON chain '%s': %s", filePath, strings.Join(errs, " "))
}

// parseJSONChain is chain-serializer.ts:parseJsonChain.
func parseJSONChain(content string, source AgentSource, filePath string) (*ChainConfig, error) {
	parsed, msg := jsJSONParse(content)
	if msg != "" {
		return nil, fmt.Errorf("Invalid JSON chain '%s': %s", filePath, msg)
	}
	in, ok := parsed.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("JSON chain '%s' must contain an object root.", filePath)
	}
	name, _ := in.vals["name"].(string)
	if jsTrim(name) == "" {
		return nil, fmt.Errorf("JSON chain '%s' must include string name.", filePath)
	}
	desc, _ := in.vals["description"].(string)
	if jsTrim(desc) == "" {
		return nil, fmt.Errorf("JSON chain '%s' must include string description.", filePath)
	}
	chain, isArr := in.vals["chain"].([]any)
	if !isArr {
		return nil, fmt.Errorf("JSON chain '%s' must include array chain.", filePath)
	}
	for i, sv := range chain {
		step, ok := sv.(*jsObject)
		if !ok {
			return nil, fmt.Errorf("JSON chain '%s' step %d must be an object.", filePath, i+1)
		}
		if v, has := step.vals["toolBudget"]; has && !isUndefined(v) {
			if err := validateJSONChainToolBudget(v, fmt.Sprintf("step %d toolBudget", i+1)); err != nil {
				return nil, err
			}
		}
		if errs := validateAcceptanceInput(step.vals["acceptance"], fmt.Sprintf("step %d acceptance", i+1)); len(errs) > 0 {
			return nil, acceptanceErrorOf(filePath, errs)
		}
		switch par := step.vals["parallel"].(type) {
		case []any:
			for ti, tv := range par {
				task, ok := tv.(*jsObject)
				if !ok {
					continue
				}
				if v, has := task.vals["toolBudget"]; has && !isUndefined(v) {
					if err := validateJSONChainToolBudget(v, fmt.Sprintf("step %d parallel task %d toolBudget", i+1, ti+1)); err != nil {
						return nil, err
					}
				}
				if errs := validateAcceptanceInput(task.vals["acceptance"], fmt.Sprintf("step %d parallel task %d acceptance", i+1, ti+1)); len(errs) > 0 {
					return nil, acceptanceErrorOf(filePath, errs)
				}
			}
		case *jsObject:
			if v, has := par.vals["toolBudget"]; has && !isUndefined(v) {
				if err := validateJSONChainToolBudget(v, fmt.Sprintf("step %d dynamic template toolBudget", i+1)); err != nil {
					return nil, err
				}
			}
			if errs := validateAcceptanceInput(par.vals["acceptance"], fmt.Sprintf("step %d dynamic template acceptance", i+1)); len(errs) > 0 {
				return nil, acceptanceErrorOf(filePath, errs)
			}
		}
	}
	maxSafe := float64(9007199254740991)
	if err := validateChainOutputBindings(chain, &maxSafe, ChainOutputContext{}); err != nil {
		if _, ok := err.(dynamicFanoutError); ok {
			return nil, fmt.Errorf("Invalid JSON chain '%s': %s", filePath, err.Error())
		}
		return nil, err
	}
	pkgRaw, hasPkg := in.vals["package"].(string)
	pkg, perr := parsePackageName(pkgRaw, hasPkg, fmt.Sprintf("Chain '%s' package", name))
	if perr != "" {
		return nil, fmt.Errorf("%s", perr)
	}
	extra := newObject()
	for _, k := range in.order() {
		if k == "name" || k == "package" || k == "description" || k == "chain" {
			continue
		}
		if s, ok := in.vals[k].(string); ok {
			extra.set(k, s)
		}
	}
	c := &ChainConfig{Name: buildRuntimeName(jsTrim(name), pkg), LocalName: jsTrim(name), PackageName: pkg, Description: jsTrim(desc), Source: source, FilePath: filePath, JSONSteps: chain}
	if len(extra.keys) > 0 {
		c.ExtraFields = extra
	}
	return c, nil
}

// serializeJSONChain is chain-serializer.ts:serializeJsonChain.
func serializeJSONChain(c *ChainConfig) string {
	root := newObject()
	root.set("name", frontmatterName(c.Name, c.LocalName, c.PackageName))
	root.set("description", c.Description)
	steps := c.JSONSteps
	if steps == nil {
		steps = []any{}
	}
	root.set("chain", steps)
	if c.PackageName != "" {
		root.set("package", c.PackageName)
	}
	if c.ExtraFields != nil {
		for _, k := range c.ExtraFields.order() {
			if k != "name" && k != "description" && k != "package" && k != "chain" {
				root.set(k, c.ExtraFields.vals[k])
			}
		}
	}
	return marshalJSON(root, "  ") + "\n"
}
