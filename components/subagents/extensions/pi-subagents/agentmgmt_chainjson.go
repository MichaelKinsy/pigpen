package pi_subagents

import (
	"errors"
	"fmt"
	"strings"
)


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
	cfg := make([]*jsObject, 0, len(chain))
	for _, sv := range chain {
		cfg = append(cfg, sv.(*jsObject))
	}
	if err := validateChainOutputBindings(cfg, DynamicFanoutConfig{MaxItems: &maxSafe}); err != nil {
		var ce *ChainOutputValidationError
		if errors.As(err, &ce) {
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
