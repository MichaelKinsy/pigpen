package pi_subagents

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// ChainStep is one `## agent` section of a .chain.md file.
type ChainStep struct {
	Agent, Task  string
	Machine      string
	Output       Tri[string]
	Phase        string
	Label        string
	As           string
	OutputSchema string
	OutputMode   string
	Reads        TriList
	Model        string
	Skills       TriList
	Progress     *bool
	ToolBudget   *jsObject
}

// Tri is a string that is unset, false or a value (the original's `string | false | undefined`).
type Tri[T any] struct {
	Set   bool
	False bool
	Value T
}

// TriList is a list that is unset, false or a list.
type TriList struct {
	Set   bool
	False bool
	Items []string
}

// ChainConfig is a chain definition.
type ChainConfig struct {
	Name, LocalName, PackageName, Description string
	Source                                    AgentSource
	FilePath                                  string
	Steps                                     []ChainStep
	ExtraFields                               *jsObject
}

// validateToolBudget is the original's validateToolBudgetConfig; it returns the message of the first problem.
func validateToolBudget(raw any, label string) string {
	v, ok := raw.(*jsObject)
	if !ok {
		return label + " must be an object with hard and optional soft/block."
	}
	isInt := func(x any, min float64) bool {
		f, ok := x.(float64)
		return ok && f == math.Trunc(f) && !math.IsInf(f, 0) && f >= min
	}
	hard, _ := v.get("hard")
	if !isInt(hard, 1) {
		return fmt.Sprintf("%s.hard must be an integer >= 1.", label)
	}
	soft, hasSoft := v.get("soft")
	if hasSoft && !isInt(soft, 1) {
		return label + ".soft must be an integer >= 1 when provided."
	}
	if hasSoft && soft.(float64) > hard.(float64) {
		return fmt.Sprintf("%s.soft must be <= %s.hard.", label, label)
	}
	if block, has := v.get("block"); has {
		if s, isStr := block.(string); !(isStr && s == "*") {
			arr, isArr := block.([]any)
			if !isArr {
				return label + `.block must be "*" or an array of tool names.`
			}
			if len(arr) == 0 {
				return label + ".block must contain at least one tool name."
			}
			for _, item := range arr {
				if s, ok := item.(string); !ok || jsTrim(s) == "" {
					return label + ".block must contain non-empty tool names."
				}
			}
		}
	}
	return ""
}

func splitList(raw string) []string {
	var out []string
	for _, v := range strings.Split(raw, ",") {
		if v = jsTrim(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func parseStepBody(agent, section string) (ChainStep, error) {
	lines := strings.Split(section, "\n")
	blank := -1
	for i, l := range lines {
		if jsTrim(l) == "" {
			blank = i
			break
		}
	}
	config, task := lines, ""
	if blank != -1 {
		config = lines[:blank]
		task = jsTrim(strings.Join(lines[blank+1:], "\n"))
	}
	step := ChainStep{Agent: agent, Task: task}
	for _, line := range config {
		k, rawValue, ok := matchKeyLine(line)
		if !ok {
			continue
		}
		key := strings.ToLower(jsTrim(k))
		raw := jsTrim(rawValue)
		switch key {
		case "machine":
			if raw != "" {
				step.Machine = raw
			}
		case "output":
			if raw == "false" {
				step.Output = Tri[string]{Set: true, False: true}
			} else if raw != "" {
				step.Output = Tri[string]{Set: true, Value: raw}
			}
		case "phase":
			if raw != "" {
				step.Phase = raw
			}
		case "label":
			if raw != "" {
				step.Label = raw
			}
		case "as":
			if raw != "" {
				step.As = raw
			}
		case "outputschema":
			if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
				return step, errors.New("Inline outputSchema values are not supported in .chain.md files; use a schema file path.")
			}
			if raw != "" {
				step.OutputSchema = raw
			}
		case "outputmode":
			if raw == "inline" || raw == "file-only" {
				step.OutputMode = raw
			}
		case "reads":
			if raw == "false" {
				step.Reads = TriList{Set: true, False: true}
			} else if items := splitList(raw); len(items) > 0 {
				step.Reads = TriList{Set: true, Items: items}
			} else {
				step.Reads = TriList{Set: true, False: true}
			}
		case "model":
			if raw != "" {
				step.Model = raw
			}
		case "skills":
			if raw == "false" {
				step.Skills = TriList{Set: true, False: true}
			} else if items := splitList(raw); len(items) > 0 {
				step.Skills = TriList{Set: true, Items: items}
			} else {
				step.Skills = TriList{Set: true, False: true}
			}
		case "progress":
			t, f := true, false
			if raw == "true" {
				step.Progress = &t
			} else if raw == "false" {
				step.Progress = &f
			}
		case "toolbudget":
			parsed, err := parseJSON([]byte(raw))
			if err != nil {
				return step, fmt.Errorf("Invalid toolBudget in .chain.md step '%s': %s", agent, err.Error())
			}
			if msg := validateToolBudget(parsed, fmt.Sprintf("toolBudget for step '%s'", agent)); msg != "" {
				return step, errors.New(msg)
			}
			step.ToolBudget = parsed.(*jsObject)
		}
	}
	return step, nil
}

// headingAt is one match of /^##\s+(.+)[^\S\n]*$/m starting at line start st: the heading text and the match end.
func headingAt(body string, st int) (agent string, end int, ok bool) {
	if !strings.HasPrefix(body[st:], "##") {
		return "", 0, false
	}
	a := st + 2
	b := a
	for b < len(body) {
		r, n := utf8.DecodeRuneInString(body[b:])
		if !isJSSpace(r) {
			break
		}
		b += n
	}
	// \s+ takes at least one character; `.+` needs one that is not a line terminator, so \s+ gives back what it must
	e := b
	for ; e > a; e -= lastRuneLen(body[a:e]) {
		if e < len(body) {
			if r, _ := utf8.DecodeRuneInString(body[e:]); !isLineTerminator(r) {
				break
			}
		}
	}
	if e <= a {
		return "", 0, false
	}
	eol := e
	for eol < len(body) {
		r, n := utf8.DecodeRuneInString(body[eol:])
		if isLineTerminator(r) {
			break
		}
		eol += n
	}
	// [^\S\n]* takes the white space after `.+` that is not "\n"; `$` needs a line terminator or the end of the input
	kmax := eol
	for kmax < len(body) {
		r, n := utf8.DecodeRuneInString(body[kmax:])
		if r == '\n' || !isJSSpace(r) {
			break
		}
		kmax += n
	}
	for k := kmax; k >= eol; k -= lastRuneLen(body[eol:k]) {
		if k == len(body) {
			return jsTrim(body[e:eol]), k, true
		}
		if r, _ := utf8.DecodeRuneInString(body[k:]); isLineTerminator(r) {
			return jsTrim(body[e:eol]), k, true
		}
		if k == eol {
			break
		}
	}
	return "", 0, false
}

func lastRuneLen(s string) int {
	_, n := utf8.DecodeLastRuneInString(s)
	if n == 0 {
		return 1
	}
	return n
}

// ParseChain parses a .chain.md file.
func ParseChain(content string, source AgentSource, filePath string) (*ChainConfig, error) {
	fm, body := ParseFrontmatter(content)
	if fm.Get("name") == "" || fm.Get("description") == "" {
		return nil, errors.New("Chain frontmatter must include name and description")
	}
	type heading struct {
		agent      string
		start, end int
	}
	var hs []heading
	last := 0
	for _, st := range lineStarts(body) {
		if st < last {
			continue
		}
		if agent, end, ok := headingAt(body, st); ok {
			hs = append(hs, heading{agent, st, end})
			last = end
		}
	}
	steps := []ChainStep{}
	for i, h := range hs {
		start := h.end
		if start < len(body) && body[start] == '\n' {
			start++
		}
		stop := len(body)
		if i+1 < len(hs) {
			stop = hs[i+1].start
		}
		section := ""
		if start < stop {
			section = strings.TrimRightFunc(body[start:stop], isJSSpace)
		}
		step, err := parseStepBody(h.agent, section)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	local := fm.Get("name")
	pkg, perr := parsePackageName(fm.Get("package"), fm.Has("package"), fmt.Sprintf("Chain '%s' package", local))
	if perr != "" {
		return nil, errors.New(perr)
	}
	extra := newObject()
	for _, k := range fm.Keys() {
		if k == "name" || k == "package" || k == "description" {
			continue
		}
		extra.set(k, fm.Get(k))
	}
	c := &ChainConfig{Name: buildRuntimeName(local, pkg), LocalName: local, PackageName: pkg, Description: fm.Get("description"), Source: source, FilePath: filePath, Steps: steps}
	if len(extra.keys) > 0 {
		c.ExtraFields = extra
	}
	return c, nil
}

// SerializeChain writes a chain back as a .chain.md file.
func SerializeChain(c *ChainConfig) string {
	var lines []string
	lines = append(lines, "---", "name: "+frontmatterName(c.Name, c.LocalName, c.PackageName))
	if c.PackageName != "" {
		lines = append(lines, "package: "+c.PackageName)
	}
	lines = append(lines, "description: "+c.Description)
	if c.ExtraFields != nil {
		for _, k := range c.ExtraFields.order() {
			v, _ := c.ExtraFields.str(k)
			lines = append(lines, k+": "+v)
		}
	}
	lines = append(lines, "---", "")
	for i, s := range c.Steps {
		lines = append(lines, "## "+s.Agent)
		if s.Machine != "" {
			lines = append(lines, "machine: "+s.Machine)
		}
		if s.Output.False {
			lines = append(lines, "output: false")
		} else if s.Output.Value != "" {
			lines = append(lines, "output: "+s.Output.Value)
		}
		if s.Phase != "" {
			lines = append(lines, "phase: "+s.Phase)
		}
		if s.Label != "" {
			lines = append(lines, "label: "+s.Label)
		}
		if s.As != "" {
			lines = append(lines, "as: "+s.As)
		}
		if s.OutputSchema != "" {
			lines = append(lines, "outputSchema: "+s.OutputSchema)
		}
		if s.OutputMode != "" {
			lines = append(lines, "outputMode: "+s.OutputMode)
		}
		if s.Reads.False {
			lines = append(lines, "reads: false")
		} else if len(s.Reads.Items) > 0 {
			lines = append(lines, "reads: "+strings.Join(s.Reads.Items, ", "))
		}
		if s.Model != "" {
			lines = append(lines, "model: "+s.Model)
		}
		if s.Skills.False {
			lines = append(lines, "skills: false")
		} else if len(s.Skills.Items) > 0 {
			lines = append(lines, "skills: "+strings.Join(s.Skills.Items, ", "))
		}
		if s.Progress != nil {
			lines = append(lines, "progress: "+map[bool]string{true: "true", false: "false"}[*s.Progress])
		}
		if s.ToolBudget != nil {
			lines = append(lines, "toolBudget: "+marshalJSON(s.ToolBudget, ""))
		}
		lines = append(lines, "", s.Task)
		if i < len(c.Steps)-1 {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
