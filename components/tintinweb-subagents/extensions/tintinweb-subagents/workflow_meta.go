package tintinweb_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// Extracting and validating a workflow script's `meta` block. The block has to be readable before execution (the
// declared phases seed the progress groups) and has to be a pure literal: it is evaluated alone, in an empty
// JavaScript realm, so anything that is not a literal throws or is rejected. upstream: src/workflow/meta.ts.

// workflowPhaseMeta is a phase declared up front, so the UI can show it before any agent runs.
// Detail and Model are pointers because the original tells an absent field from an empty one (`detail: ""` is kept).
type workflowPhaseMeta struct {
	Title  string  `json:"title"`
	Detail *string `json:"detail,omitempty"`
	// Model is display-only: the runtime does not read it.
	Model *string `json:"model,omitempty"`
}

// workflowMeta: WhenToUse is a pointer and Phases is nil-or-not for the same reason (`phases: []` is kept, so the
// worker's `meta` global holds what the author wrote). Phases marshals through MarshalJSON, since omitempty drops an
// empty non-nil slice.
type workflowMeta struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	WhenToUse   *string             `json:"whenToUse,omitempty"`
	Phases      []workflowPhaseMeta `json:"phases,omitempty"`
}

func (m workflowMeta) MarshalJSON() ([]byte, error) {
	type plain workflowMeta
	if m.Phases == nil {
		return json.Marshal(plain(m))
	}
	return json.Marshal(struct {
		plain
		Phases []workflowPhaseMeta `json:"phases"`
	}{plain(m), m.Phases})
}

// workflowMetaError is the author-facing rejection of a meta block; its message is shown verbatim.
type workflowMetaError struct{ msg string }

func (e *workflowMetaError) Error() string { return e.msg }

func workflowMetaFail(message string) error { return &workflowMetaError{message} }

// workflowMetaEvalTimeout bounds evaluating the meta fragment: an IIFE needs no globals, so a non-terminating one is evaluable
// and would wedge the host. upstream: META_EVAL_TIMEOUT_MS.
const workflowMetaEvalTimeout = 100 * time.Millisecond

const workflowPureLiteralHint = "The `meta` object must be a PURE LITERAL — no variables, function calls, spreads, or template interpolation."

var workflowMetaDeclaration = regexp.MustCompile(`(^|[\r\n])[ \t]*export[ \t\r\n]+const[ \t\r\n]+meta[ \t\r\n]*=`)

// hasMetaDeclaration reports whether source even claims to be a workflow script: the cheap half of extractMeta, so a
// directory of .js files can be told apart from a directory of workflows without evaluating anything.
func hasMetaDeclaration(source string) bool { return workflowMetaDeclaration.MatchString(source) }

// scanMetaObjectLiteral finds the index just past the object literal that starts at open, tracking string, template,
// comment and regex context so braces inside them do not move the depth counter. end is -1 when braces never
// balance; sawInterpolation is true when a `${` opened inside a template literal.
func scanMetaObjectLiteral(source string, open int) (end int, sawInterpolation bool) {
	depth := 0
	i := open
	const (
		code = iota
		lineComment
		blockComment
		single
		double
		template
		regex
	)
	mode := code
	var templateStack []int
	for i < len(source) {
		c := source[i]
		var next byte
		if i+1 < len(source) {
			next = source[i+1]
		}
		switch mode {
		case lineComment:
			if c == '\n' {
				mode = code
			}
			i++
			continue
		case blockComment:
			if c == '*' && next == '/' {
				mode = code
				i += 2
				continue
			}
			i++
			continue
		case single, double, regex:
			if c == '\\' {
				i += 2
				continue
			}
			if mode == single && c == '\'' {
				mode = code
			} else if mode == double && c == '"' {
				mode = code
			} else if mode == regex && c == '/' {
				mode = code
			} else if c == '\n' && mode != double {
				// An unterminated regex/string cannot run past a newline; a misdetected regex must not swallow the rest.
				mode = code
			}
			i++
			continue
		case template:
			if c == '\\' {
				i += 2
				continue
			}
			if c == '`' {
				mode = code
				i++
				continue
			}
			if c == '$' && next == '{' {
				sawInterpolation = true
				templateStack = append(templateStack, depth)
				depth++
				mode = code
				i += 2
				continue
			}
			i++
			continue
		}
		// mode == code
		switch {
		case c == '/' && next == '/':
			mode = lineComment
			i += 2
		case c == '/' && next == '*':
			mode = blockComment
			i += 2
		case c == '\'':
			mode = single
			i++
		case c == '"':
			mode = double
			i++
		case c == '`':
			mode = template
			i++
		case c == '/' && isMetaRegexPosition(source, i):
			mode = regex
			i++
		case c == '{':
			depth++
			i++
		case c == '}':
			depth--
			i++
			if len(templateStack) > 0 && depth == templateStack[len(templateStack)-1] {
				templateStack = templateStack[:len(templateStack)-1]
				mode = template
				continue
			}
			if depth == 0 {
				return i, sawInterpolation
			}
		default:
			i++
		}
	}
	return -1, sawInterpolation
}

// isMetaRegexPosition decides whether the `/` at i opens a regex literal rather than a division: a regex can only follow
// an operator or opener, never a value.
func isMetaRegexPosition(source string, i int) bool {
	j := i - 1
	for j >= 0 && strings.ContainsRune(" \t\r\n\v\f", rune(source[j])) {
		j--
	}
	if j < 0 {
		return true
	}
	prev := source[j]
	isWord := prev == '_' || prev == '$' || prev >= '0' && prev <= '9' || prev >= 'a' && prev <= 'z' || prev >= 'A' && prev <= 'Z'
	return !(isWord || prev == ')' || prev == ']')
}

// assertWorkflowPhases checks meta.phases. nulls names the `null` values of the literal (Export cannot tell null from
// undefined, and the original rejects null where it accepts absence): "phases", "phases.N.detail", "phases.N.model".
func assertWorkflowPhases(value any, nulls map[string]bool) ([]workflowPhaseMeta, error) {
	if value == nil && !nulls["phases"] {
		return nil, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, workflowMetaFail("`meta.phases` must be an array of { title, detail?, model? } objects.")
	}
	out := make([]workflowPhaseMeta, 0, len(list))
	for index, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, workflowMetaFail(fmt.Sprintf("`meta.phases[%d]` must be an object with a `title`.", index))
		}
		title, isString := m["title"].(string)
		if !isString || strings.TrimSpace(title) == "" {
			return nil, workflowMetaFail(fmt.Sprintf("`meta.phases[%d].title` must be a non-empty string.", index))
		}
		phase := workflowPhaseMeta{Title: title}
		if d, present := m["detail"]; present && (d != nil || nulls[fmt.Sprintf("phases.%d.detail", index)]) {
			s, isString := d.(string)
			if !isString {
				return nil, workflowMetaFail(fmt.Sprintf("`meta.phases[%d].detail` must be a string.", index))
			}
			phase.Detail = &s
		}
		if d, present := m["model"]; present && (d != nil || nulls[fmt.Sprintf("phases.%d.model", index)]) {
			s, isString := d.(string)
			if !isString {
				return nil, workflowMetaFail(fmt.Sprintf("`meta.phases[%d].model` must be a string.", index))
			}
			phase.Model = &s
		}
		out = append(out, phase)
	}
	return out, nil
}

// evalWorkflowMetaFragment evaluates `(fragment)` in an empty realm, bounded by metaEvalTimeout.
// nulls lists the literal's `null` values that matter to the checks (see assertWorkflowPhases, plus "whenToUse").
func evalWorkflowMetaFragment(fragment string) (any, map[string]bool, error) {
	vm := goja.New()
	timer := time.AfterFunc(workflowMetaEvalTimeout, func() { vm.Interrupt("timeout") })
	defer timer.Stop()
	program, err := goja.Compile("workflow-meta.js", "("+fragment+")", false)
	if err != nil {
		return nil, nil, err
	}
	v, err := vm.RunProgram(program)
	if err != nil {
		return nil, nil, err
	}
	nulls := map[string]bool{}
	if obj, ok := v.(*goja.Object); ok {
		isNull := func(o *goja.Object, key string) bool { return goja.IsNull(o.Get(key)) }
		nulls["whenToUse"] = isNull(obj, "whenToUse")
		nulls["phases"] = isNull(obj, "phases")
		if phases, ok := obj.Get("phases").(*goja.Object); ok {
			for i := 0; ; i++ {
				entry, ok := phases.Get(strconv.Itoa(i)).(*goja.Object)
				if !ok {
					if phases.Get(strconv.Itoa(i)) == nil {
						break
					}
					continue
				}
				nulls[fmt.Sprintf("phases.%d.detail", i)] = isNull(entry, "detail")
				nulls[fmt.Sprintf("phases.%d.model", i)] = isNull(entry, "model")
			}
		}
	}
	return v.Export(), nulls, nil
}

// wfMetaEvalDetail is String(error), which is what the original prints: the error comes out of a different V8 context, so
// `error instanceof Error` is false there and the name stays (`ReferenceError: computeName is not defined`).
func wfMetaEvalDetail(err error) string {
	var ex *goja.Exception
	if errors.As(err, &ex) {
		return ex.Value().String()
	}
	return err.Error()
}

// wfErrorDetail is an error's message as JavaScript would report it (without goja's name prefix on thrown errors).
func wfErrorDetail(err error) string {
	var overflow *goja.StackOverflowError
	if errors.As(err, &overflow) {
		return "Maximum call stack size exceeded"
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		if o, ok := ex.Value().(*goja.Object); ok {
			if m := o.Get("message"); m != nil && !goja.IsUndefined(m) {
				return m.String()
			}
		}
		return ex.Value().String()
	}
	return err.Error()
}

// extractMeta pulls meta off the front of a workflow script and hands back the runnable body (the script with the
// leading `export ` replaced by spaces, so every later offset, line and column stays what the author wrote). It
// returns a *workflowMetaError with author-facing guidance for every rejection. upstream: extractMeta.
func extractMeta(source string) (workflowMeta, string, error) {
	loc := workflowMetaDeclaration.FindStringIndex(source)
	if loc == nil {
		return workflowMeta{}, "", workflowMetaFail("A workflow script must begin with `export const meta = { name, description }`.\n" + workflowPureLiteralHint)
	}
	open := strings.Index(source[loc[1]:], "{")
	if open == -1 {
		return workflowMeta{}, "", workflowMetaFail("`export const meta` must be assigned an object literal.\n" + workflowPureLiteralHint)
	}
	open += loc[1]
	closeAt, sawInterpolation := scanMetaObjectLiteral(source, open)
	if closeAt == -1 {
		return workflowMeta{}, "", workflowMetaFail("`meta` object literal is never closed — check for an unbalanced `{`.")
	}
	// Caught here rather than by evaluation: a self-contained substitution resolves without touching a global.
	if sawInterpolation {
		return workflowMeta{}, "", workflowMetaFail("`meta` must not use template interpolation (`${...}`).\n" + workflowPureLiteralHint)
	}
	value, nulls, err := evalWorkflowMetaFragment(source[open:closeAt])
	if err != nil {
		var interrupted *goja.InterruptedError
		if errors.As(err, &interrupted) {
			return workflowMeta{}, "", workflowMetaFail(fmt.Sprintf("`meta` did not finish evaluating within %dms — it must be a literal, not a computation.\n%s", workflowMetaEvalTimeout.Milliseconds(), workflowPureLiteralHint))
		}
		return workflowMeta{}, "", workflowMetaFail("`meta` could not be evaluated: " + wfMetaEvalDetail(err) + "\n" + workflowPureLiteralHint)
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return workflowMeta{}, "", workflowMetaFail("`meta` must be an object literal.\n" + workflowPureLiteralHint)
	}
	name, isString := raw["name"].(string)
	if !isString || strings.TrimSpace(name) == "" {
		return workflowMeta{}, "", workflowMetaFail("`meta.name` is required and must be a non-empty string.")
	}
	description, isString := raw["description"].(string)
	if !isString || strings.TrimSpace(description) == "" {
		return workflowMeta{}, "", workflowMetaFail("`meta.description` is required and must be a non-empty string.")
	}
	meta := workflowMeta{Name: name, Description: description}
	if w, present := raw["whenToUse"]; present && (w != nil || nulls["whenToUse"]) {
		s, isString := w.(string)
		if !isString {
			return workflowMeta{}, "", workflowMetaFail("`meta.whenToUse` must be a string.")
		}
		meta.WhenToUse = &s
	}
	phases, err := assertWorkflowPhases(raw["phases"], nulls)
	if err != nil {
		return workflowMeta{}, "", err
	}
	meta.Phases = phases
	exportAt := loc[0] + strings.Index(source[loc[0]:], "export")
	body := source[:exportAt] + strings.Repeat(" ", 6) + source[exportAt+6:]
	return meta, body, nil
}

var (
	workflowNamesMu sync.Mutex
	workflowNames   = map[string]string{}
)

// workflowCallName is the label a SubagentWorkflow call renders under, from whichever field it carries; extraction is
// cached by exact source. upstream: workflowCallName.
func workflowCallName(script, scriptPath, name string) string {
	if script == "" {
		if scriptPath != "" {
			parts := regexp.MustCompile(`[/\\]`).Split(scriptPath, -1)
			return parts[len(parts)-1]
		}
		if name != "" {
			return name
		}
		return "workflow"
	}
	workflowNamesMu.Lock()
	defer workflowNamesMu.Unlock()
	if cached, ok := workflowNames[script]; ok {
		return cached
	}
	result := "workflow"
	if meta, _, err := extractMeta(script); err == nil {
		result = meta.Name
	}
	workflowNames[script] = result
	return result
}
