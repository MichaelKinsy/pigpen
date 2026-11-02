package pi_subagents

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// The `.chain.json` format (the original's parseJsonChain/serializeJsonChain in
// chain-serializer.ts): a JSON root with name, description and a chain array,
// validated the same way as a .chain.md before anything runs.

// jsonChain parses a .chain.json file into a ChainConfig.
func jsonChain(content, source, filePath string) (*ChainConfig, error) {
	invalid := func(format string, args ...any) (*ChainConfig, error) {
		return nil, fmt.Errorf("Invalid JSON chain '"+filePath+"': "+format, args...)
	}
	var parsed any
	parsed, err := parseJSON([]byte(content))
	if err != nil {
		return invalid("%s", err.Error())
	}
	obj, isObj := parsed.(*jsObject)
	if !isObj || obj == nil {
		return nil, fmt.Errorf("JSON chain '%s' must contain an object root.", filePath)
	}
	name, _ := obj.str("name")
	if jsTrim(name) == "" {
		return nil, fmt.Errorf("JSON chain '%s' must include string name.", filePath)
	}
	description, _ := obj.str("description")
	if jsTrim(description) == "" {
		return nil, fmt.Errorf("JSON chain '%s' must include string description.", filePath)
	}
	rawChain, hasChain := obj.get("chain")
	if !hasChain {
		return nil, fmt.Errorf("JSON chain '%s' must include array chain.", filePath)
	}
	arr, isArr := rawChain.([]any)
	if !isArr {
		return nil, fmt.Errorf("JSON chain '%s' must include array chain.", filePath)
	}
	steps := make([]*jsObject, 0, len(arr))
	for i, step := range arr {
		stepObj, isStep := step.(*jsObject)
		if !isStep || stepObj == nil {
			return nil, fmt.Errorf("JSON chain '%s' step %d must be an object.", filePath, i+1)
		}
		if budget, present := stepObj.get("toolBudget"); present {
			if msg := validateToolBudget(budget, fmt.Sprintf("step %d toolBudget", i+1)); msg != "" {
				return nil, errors.New(msg)
			}
		}
		if acceptance, present := stepObj.get("acceptance"); present {
			if errs := ValidateAcceptanceInput(acceptance, fmt.Sprintf("step %d acceptance", i+1)); len(errs) > 0 {
				return invalid("%s", strings.Join(errs, " "))
			}
		}
		parallel, hasParallel := stepObj.get("parallel")
		if hasParallel {
			if parallelArr, isParallelArr := parallel.([]any); isParallelArr {
				for j, task := range parallelArr {
					taskObj, isTask := task.(*jsObject)
					if !isTask || taskObj == nil {
						continue
					}
					if budget, present := taskObj.get("toolBudget"); present {
						if msg := validateToolBudget(budget, fmt.Sprintf("step %d parallel task %d toolBudget", i+1, j+1)); msg != "" {
							return nil, errors.New(msg)
						}
					}
					if acceptance, present := taskObj.get("acceptance"); present {
						if errs := ValidateAcceptanceInput(acceptance, fmt.Sprintf("step %d parallel task %d acceptance", i+1, j+1)); len(errs) > 0 {
							return invalid("%s", strings.Join(errs, " "))
						}
					}
				}
			} else if parallelObj, isParallelObj := parallel.(*jsObject); isParallelObj {
				if budget, present := parallelObj.get("toolBudget"); present {
					if msg := validateToolBudget(budget, fmt.Sprintf("step %d dynamic template toolBudget", i+1)); msg != "" {
						return nil, errors.New(msg)
					}
				}
				if acceptance, present := parallelObj.get("acceptance"); present {
					if errs := ValidateAcceptanceInput(acceptance, fmt.Sprintf("step %d dynamic template acceptance", i+1)); len(errs) > 0 {
						return invalid("%s", strings.Join(errs, " "))
					}
				}
			}
		}
		steps = append(steps, stepObj)
	}
	// The chain output bindings and dynamic fan-out shape.
	if err := ValidateChainOutputBindings(steps, nil); err != nil {
		return invalid("%s", err.Error())
	}
	pkg, perr := parsePackageName(obj.strOr("package"), obj.has("package"), fmt.Sprintf("Chain '%s' package", jsTrim(name)))
	if perr != "" {
		return nil, errors.New(perr)
	}
	extra := newObject()
	for _, key := range obj.order() {
		if key == "name" || key == "package" || key == "description" || key == "chain" {
			continue
		}
		v, _ := obj.get(key)
		if s, isString := v.(string); isString {
			extra.set(key, s)
		}
	}
	c := &ChainConfig{
		Name: buildRuntimeName(jsTrim(name), pkg), LocalName: jsTrim(name), PackageName: pkg,
		Description: jsTrim(description), Source: AgentSource(source), FilePath: filePath,
		Steps: parseJSONChainSteps(steps),
	}
	for _, s := range steps {
		c.rawJSON = append(c.rawJSON, s)
	}
	if len(extra.keys) > 0 {
		c.ExtraFields = extra
	}
	return c, nil
}

// strOr reads a string field, "" when absent.
func (o *jsObject) strOr(key string) string {
	s, _ := o.str(key)
	return s
}

// parseJSONChainSteps keeps the raw JSON of each step so serializeJsonChain
// writes the definition back unchanged (the original stores the parsed steps).
func parseJSONChainSteps(steps []*jsObject) []ChainStep {
	out := make([]ChainStep, 0, len(steps))
	for _, step := range steps {
		agent, _ := step.str("agent")
		task, _ := step.str("task")
		cs := ChainStep{Agent: agent, Task: task}
		// The run path reads the model and tool budget; other fields are kept
		// in the raw JSON for the writer.
		if model, _ := step.str("model"); model != "" {
			cs.Model = model
		}
		if budget, present := step.get("toolBudget"); present {
			if obj, isObj := budget.(*jsObject); isObj {
				cs.ToolBudget = obj
			}
		}
		if machine, _ := step.str("machine"); machine != "" {
			cs.Machine = machine
		}
		if output, present := step.get("output"); present {
			if s, isString := output.(string); isString {
				cs.Output = Tri[string]{Set: true, Value: s}
			} else if b, isBool := output.(bool); isBool && !b {
				cs.Output = Tri[string]{Set: true, False: true}
			}
		}
		if phase, _ := step.str("phase"); phase != "" {
			cs.Phase = phase
		}
		if label, _ := step.str("label"); label != "" {
			cs.Label = label
		}
		if as, _ := step.str("as"); as != "" {
			cs.As = as
		}
		if schema, present := step.get("outputSchema"); present {
			if s, isString := schema.(string); isString {
				cs.OutputSchema = s
			}
		}
		if mode, _ := step.str("outputMode"); mode == "inline" || mode == "file-only" {
			cs.OutputMode = mode
		}
		if reads, present := step.get("reads"); present {
			if b, isBool := reads.(bool); isBool && !b {
				cs.Reads = TriList{Set: true, False: true}
			} else if arr, isArr := reads.([]any); isArr {
				items := make([]string, 0, len(arr))
				for _, r := range arr {
					if s, isString := r.(string); isString && jsTrim(s) != "" {
						items = append(items, jsTrim(s))
					}
				}
				if len(items) > 0 {
					cs.Reads = TriList{Set: true, Items: items}
				} else {
					cs.Reads = TriList{Set: true, False: true}
				}
			}
		}
		if skills, present := step.get("skills"); present {
			if b, isBool := skills.(bool); isBool && !b {
				cs.Skills = TriList{Set: true, False: true}
			} else if arr, isArr := skills.([]any); isArr {
				items := make([]string, 0, len(arr))
				for _, s := range arr {
					if str, isString := s.(string); isString && jsTrim(str) != "" {
						items = append(items, jsTrim(str))
					}
				}
				if len(items) > 0 {
					cs.Skills = TriList{Set: true, Items: items}
				} else {
					cs.Skills = TriList{Set: true, False: true}
				}
			}
		}
		if progress, present := step.get("progress"); present {
			if b, isBool := progress.(bool); isBool {
				cs.Progress = &b
			}
		}
		out = append(out, cs)
	}
	return out
}

// serializeJsonChain writes a chain back as .chain.json (the original's): the
// root in the original's key order, the raw steps when the chain came from JSON.
func serializeJsonChain(c *ChainConfig) string {
	root := newObject()
	root.set("name", frontmatterName(c.Name, c.LocalName, c.PackageName))
	root.set("description", c.Description)
	if len(c.rawJSON) > 0 {
		root.set("chain", append([]any{}, c.rawJSON...))
	} else {
		steps := []any{}
		for _, s := range c.Steps {
			steps = append(steps, jsonChainStepMap(s))
		}
		root.set("chain", steps)
	}
	if c.PackageName != "" {
		root.set("package", c.PackageName)
	}
	if c.ExtraFields != nil {
		for _, k := range c.ExtraFields.order() {
			if k == "name" || k == "description" || k == "package" || k == "chain" {
				continue
			}
			v, _ := c.ExtraFields.str(k)
			root.set(k, v)
		}
	}
	return marshalJSON(root, "  ") + "\n"
}

// jsonChainStepMap renders a step in the original's JSON key names.
func jsonChainStepMap(s ChainStep) map[string]any {
	m := map[string]any{"agent": s.Agent}
	if s.Task != "" {
		m["task"] = s.Task
	}
	if s.Machine != "" {
		m["machine"] = s.Machine
	}
	if s.Output.False {
		m["output"] = false
	} else if s.Output.Value != "" {
		m["output"] = s.Output.Value
	}
	if s.Phase != "" {
		m["phase"] = s.Phase
	}
	if s.Label != "" {
		m["label"] = s.Label
	}
	if s.As != "" {
		m["as"] = s.As
	}
	if s.OutputSchema != "" {
		m["outputSchema"] = s.OutputSchema
	}
	if s.OutputMode != "" {
		m["outputMode"] = s.OutputMode
	}
	if s.Reads.False {
		m["reads"] = false
	} else if len(s.Reads.Items) > 0 {
		m["reads"] = append([]any{}, toAnySlice(s.Reads.Items)...)
	}
	if s.Model != "" {
		m["model"] = s.Model
	}
	if s.Skills.False {
		m["skills"] = false
	} else if len(s.Skills.Items) > 0 {
		m["skills"] = append([]any{}, toAnySlice(s.Skills.Items)...)
	}
	if s.Progress != nil {
		m["progress"] = *s.Progress
	}
	if s.ToolBudget != nil {
		m["toolBudget"] = s.ToolBudget
	}
	return m
}

func toAnySlice(items []string) []any {
	out := make([]any, 0, len(items))
	for _, s := range items {
		out = append(out, s)
	}
	return out
}

// chainPathIsJSON reports whether a chain file is the JSON format.
func chainPathIsJSON(path string) bool {
	return strings.HasSuffix(path, ".chain.json")
}

// loadJSONChain reads one .chain.json file; a failure becomes a diagnostic.
func loadJSONChain(path string, source AgentSource) (ChainConfig, Diagnostic) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ChainConfig{}, Diagnostic{Source: source, FilePath: path, Error: err.Error()}
	}
	c, err := jsonChain(string(b), string(source), path)
	if err != nil {
		return ChainConfig{}, Diagnostic{Source: source, FilePath: path, Error: err.Error()}
	}
	return *c, Diagnostic{}
}
