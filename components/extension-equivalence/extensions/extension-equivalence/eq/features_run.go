package eq

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// scenarioModels is the models.json of the scripted provider: the primary model first, then Models. The
// default model is byte for byte the one every scenario had before ModelSpec existed.
func scenarioModels(sc *Scenario, baseURL string) map[string]any {
	var models []any
	for _, m := range sc.allModels() {
		models = append(models, map[string]any{
			"id": m.ID, "name": m.ID, "reasoning": m.Reasoning, "input": m.Input,
			"contextWindow": m.ContextWindow, "maxTokens": m.MaxTokens,
			"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
		})
	}
	return map[string]any{"providers": map[string]any{"eq-llm": map[string]any{
		"baseUrl": baseURL, "api": "openai-completions", "apiKey": "eq-key", "models": models,
	}}}
}

// writeSettings writes the scenario's Settings as the agent directory's settings.json.
func writeSettings(agent string, settings map[string]any) error {
	if settings == nil {
		return nil
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	return os.WriteFile(filepath.Join(agent, "settings.json"), b, 0o600)
}

// writeHelperExtensions writes the scenario's helper extensions under dir and returns their paths, in scenario
// order, split by whether they load before or after the extension under test.
func writeHelperExtensions(dir string, helpers []HelperExtension) (before, after []string, err error) {
	if len(helpers) == 0 {
		return nil, nil, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	for _, h := range helpers {
		p := filepath.Join(dir, h.Name)
		if err := os.WriteFile(p, []byte(h.Source), 0o644); err != nil {
			return nil, nil, err
		}
		if h.Position == "before" {
			before = append(before, p)
		} else {
			after = append(after, p)
		}
	}
	return before, after, nil
}

// noteResponse keeps a command's response for a step that saves values from it and wakes the trigger waiting for it.
// It runs on the goroutine that reads the host's output.
func (d *driver) noteResponse(id string, rec map[string]any) {
	d.mu.Lock()
	if d.want[id] {
		d.resp[id] = rec
	}
	done, ok := d.awaiting[id]
	delete(d.awaiting, id)
	d.mu.Unlock()
	if ok {
		close(done)
	}
}

// captureFiles records the state of the files the captures select, one files event per capture.
func (d *driver) captureFiles(caps []FileCapture) error {
	for _, c := range caps {
		data, err := d.rec.norm.captureFiles(d.roots[c.root()], c)
		if err != nil {
			return fmt.Errorf("captureFiles %s: %w", c.label(), err)
		}
		d.rec.addRaw(ChFiles, canonical(data))
	}
	return nil
}

// turnHasVars reports whether a turn refers to a saved variable.
func turnHasVars(t Turn) bool {
	found := false
	check := func(s string) {
		if varRefRE.MatchString(s) || builtinVarRE.MatchString(s) {
			found = true
		}
	}
	check(t.Text)
	check(t.Thinking)
	for _, c := range t.ToolCalls {
		walkStrings(c.Arguments, check)
	}
	if t.Error != nil {
		check(t.Error.Message)
		check(t.Error.Body)
	}
	return found
}

// expandTurnVars returns the turn with its saved variables replaced; the scripted turn itself is not changed.
func expandTurnVars(t Turn, lv *laneVars) (Turn, error) {
	var err error
	str := func(s string) string {
		if err != nil {
			return s
		}
		v, e := lv.expand(s)
		if e != nil {
			err = e
			return s
		}
		return v.(string)
	}
	out := t
	out.Text, out.Thinking = str(t.Text), str(t.Thinking)
	out.ToolCalls = nil
	for _, c := range t.ToolCalls {
		v, e := lv.expand(c.Arguments)
		if e != nil && err == nil {
			err = e
		}
		args, _ := v.(map[string]any)
		out.ToolCalls = append(out.ToolCalls, ToolCall{Name: c.Name, Arguments: args})
	}
	if t.Error != nil {
		e := *t.Error
		e.Message, e.Body = str(e.Message), str(e.Body)
		out.Error = &e
	}
	return out, err
}

// turnStrings is every string a turn can carry a variable in, as one decoded JSON value for checkVars.
func turnStrings(t Turn) any {
	out := []any{t.Text, t.Thinking}
	for _, c := range t.ToolCalls {
		out = append(out, c.Arguments)
	}
	if t.Error != nil {
		out = append(out, t.Error.Message, t.Error.Body)
	}
	return out
}
