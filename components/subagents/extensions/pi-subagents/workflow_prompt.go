package pi_subagents

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of src/slash/prompt-workflows.ts: `/prompt-workflow <name> [args]` runs a prompt template (a .md file with frontmatter)
// as one child, or a declared sequence of them, through a generated workflow script.

// packagePrompts are the prompt templates the original ships in its package (its `prompts/` directory). The copy the binary
// embeds lives in `promptdata/`: PiG takes an extension directory that holds a `prompts/` directory for a resource package and
// then does not start the Go module in it (see PORT.md, PiG core findings).
//
//go:embed promptdata/*.md
var packagePrompts embed.FS

// packagePromptDir names the shipped templates in listings: the Go extension carries them inside its binary.
const packagePromptDir = "pigpen-subagents:prompts"

// PromptWorkflow is one discovered template.
type PromptWorkflow struct {
	Name        string
	Description string
	Body        string
	FilePath    string
	Agent       string
	Context     string // "", "fresh" or "fork"
	Model       string
	Skill       any // nil: unset; false; a string; or []string
	Cwd         string
	Chain       string
}

var reservedPromptCommandNames = map[string]bool{
	"chain-prompts": true, "prompt-workflow": true, "run": true, "chain": true, "parallel": true, "run-chain": true, "subagents-doctor": true, "subagents-models": true,
}

// promptDirectories are the template directories in precedence order (package, user, project).
func promptDirectories(cwd string) []string {
	return []string{packagePromptDir, filepath.Join(agentDir(), "prompts"), filepath.Join(projectConfigDir(cwd), "prompts")}
}

func readPromptFiles(cwd string) ([]string, error) {
	var files []string
	for _, dir := range promptDirectories(cwd) {
		if dir == packagePromptDir {
			entries, _ := packagePrompts.ReadDir("promptdata")
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".md") {
					files = append(files, dir+"/"+e.Name())
				}
			}
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			filePath := filepath.Join(dir, e.Name())
			isFile := e.Type().IsRegular()
			if e.Type()&fs.ModeSymlink != 0 {
				st, err := os.Stat(filePath)
				switch {
				case err == nil:
					isFile = st.Mode().IsRegular()
				case errors.Is(err, fs.ErrNotExist):
					isFile = false
				default:
					return nil, err
				}
			}
			if isFile {
				files = append(files, filePath)
			}
		}
	}
	return files, nil
}

func firstNonEmptyLine(value string) string {
	for _, line := range regexp.MustCompile(`\r?\n`).Split(value, -1) {
		if t := jsTrim(line); t != "" {
			return t
		}
	}
	return "Prompt workflow"
}

func frontmatterString(fm Frontmatter, key string) string { return jsTrim(fm.Get(key)) }

// frontmatterBool is booleanField: 0 is unset, 1 true, 2 false.
func frontmatterBool(fm Frontmatter, key string) int {
	switch strings.ToLower(jsTrim(fm.Get(key))) {
	case "true", "yes", "1":
		return 1
	case "false", "no", "0":
		return 2
	}
	return 0
}

func parsePromptSkill(value string) any {
	if value == "" {
		return nil
	}
	if value == "false" {
		return false
	}
	var parts []string
	for _, p := range strings.Split(value, ",") {
		if t := jsTrim(p); t != "" {
			parts = append(parts, t)
		}
	}
	switch {
	case len(parts) > 1:
		return parts
	case len(parts) == 1:
		return parts[0]
	}
	return nil
}

func loadPromptWorkflow(filePath string) (*PromptWorkflow, error) {
	var content []byte
	var err error
	if strings.HasPrefix(filePath, packagePromptDir+"/") {
		content, err = packagePrompts.ReadFile("promptdata/" + strings.TrimPrefix(filePath, packagePromptDir+"/"))
	} else {
		content, err = os.ReadFile(filePath)
	}
	if err != nil {
		return nil, err
	}
	fm, body := ParseFrontmatter(string(content))
	name := strings.TrimSuffix(filepath.Base(filePath), ".md")
	if name == "" || reservedPromptCommandNames[name] {
		return nil, nil
	}
	w := &PromptWorkflow{Name: name, Body: body, FilePath: filePath, Agent: "delegate", Description: frontmatterString(fm, "description"), Model: frontmatterString(fm, "model"), Skill: parsePromptSkill(frontmatterString(fm, "skill")), Cwd: frontmatterString(fm, "cwd"), Chain: frontmatterString(fm, "chain")}
	if w.Description == "" {
		w.Description = firstNonEmptyLine(body)
	}
	if subagent := frontmatterString(fm, "subagent"); subagent != "" && subagent != "true" {
		w.Agent = subagent
	}
	if frontmatterBool(fm, "inheritContext") == 1 || frontmatterBool(fm, "fork") == 1 {
		w.Context = "fork"
	}
	if frontmatterBool(fm, "fresh") == 1 {
		w.Context = "fresh"
	}
	return w, nil
}

// discoverPromptWorkflows is discoverPromptWorkflows: a template of the project beats one of the user, which beats the package's.
func discoverPromptWorkflows(cwd string) ([]PromptWorkflow, error) {
	files, err := readPromptFiles(cwd)
	if err != nil {
		return nil, err
	}
	byName := map[string]PromptWorkflow{}
	for _, file := range files {
		w, err := loadPromptWorkflow(file)
		if err != nil {
			return nil, err
		}
		if w != nil {
			byName[w.Name] = *w
		}
	}
	out := make([]PromptWorkflow, 0, len(byName))
	for _, w := range byName {
		out = append(out, w)
	}
	sort.SliceStable(out, func(i, j int) bool { return localeLess(out[i].Name, out[j].Name) })
	return out, nil
}

func shellWords(input string) []string {
	var words []string
	var current strings.Builder
	var quote rune
	escaped, has := false, false
	for _, ch := range input {
		switch {
		case escaped:
			current.WriteRune(ch)
			has, escaped = true, false
		case ch == '\\':
			escaped = true
		case quote != 0:
			if ch == quote {
				quote = 0
			} else {
				current.WriteRune(ch)
				has = true
			}
		case ch == '\'' || ch == '"':
			quote = ch
		case isJSSpace(ch):
			if has && current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
				has = false
			}
		default:
			current.WriteRune(ch)
			has = true
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

// replaceLiteral is template.replace(/needle/g, replacement) with a string replacement: `$$`, `$&`, a backtick and `$'` are
// expanded as JavaScript does.
func replaceLiteral(template, needle, replacement string) string {
	var b strings.Builder
	rest := 0
	for {
		i := strings.Index(template[rest:], needle)
		if i < 0 {
			break
		}
		i += rest
		b.WriteString(template[rest:i])
		for j := 0; j < len(replacement); j++ {
			if replacement[j] != '$' || j+1 >= len(replacement) {
				b.WriteByte(replacement[j])
				continue
			}
			switch replacement[j+1] {
			case '$':
				b.WriteByte('$')
				j++
			case '&':
				b.WriteString(needle)
				j++
			case '`':
				b.WriteString(template[:i])
				j++
			case '\'':
				b.WriteString(template[i+len(needle):])
				j++
			default:
				b.WriteByte('$')
			}
		}
		rest = i + len(needle)
	}
	b.WriteString(template[rest:])
	return b.String()
}

var (
	fallbackArgPattern = regexp.MustCompile(`\$\{(\d+):-([^}]*)\}`)
	numberedArgPattern = regexp.MustCompile(`\$(\d+)`)
)

func argAt(args []string, index string) (string, bool) {
	n, err := strconv.Atoi(index)
	if err != nil || n-1 < 0 || n-1 >= len(args) {
		return "", false
	}
	return args[n-1], true
}

func substituteArgs(template string, args []string) string {
	all := strings.Join(args, " ")
	out := replaceLiteral(template, "$ARGUMENTS", all)
	out = replaceLiteral(out, "$@", all)
	out = fallbackArgPattern.ReplaceAllStringFunc(out, func(m string) string {
		sub := fallbackArgPattern.FindStringSubmatch(m)
		if v, ok := argAt(args, sub[1]); ok && v != "" {
			return v
		}
		return sub[2]
	})
	return numberedArgPattern.ReplaceAllStringFunc(out, func(m string) string {
		v, _ := argAt(args, m[1:])
		return v
	})
}

type promptRuntime struct {
	args          []string
	agentOverride string
	fork, fresh   bool
	bg            bool
}

func parsePromptRuntimeOptions(words []string) promptRuntime {
	var r promptRuntime
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "--fork":
			r.fork = true
		case w == "--fresh":
			r.fresh = true
		case w == "--bg" || w == "--async":
			r.bg = true
		case w == "--subagent":
			i++
			r.agentOverride = ""
			if i < len(words) {
				r.agentOverride = words[i]
			}
		case (strings.HasPrefix(w, "--subagent=") || strings.HasPrefix(w, "--subagent:")) && len(w) > len("--subagent=") && !strings.ContainsAny(w[len("--subagent="):], "\n\r\u2028\u2029"):
			r.agentOverride = w[len("--subagent="):]
		default:
			r.args = append(r.args, w)
		}
	}
	return r
}

func splitPromptChain(input string) []string {
	var out []string
	for _, p := range strings.Split(input, " -> ") {
		if t := jsTrim(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func promptWorkflowScript(workflows []PromptWorkflow, args []string, runtime promptRuntime) string {
	launches := make([]string, len(workflows))
	for i, w := range workflows {
		task := jsTrim(substituteArgs(w.Body, args))
		context := w.Context
		if runtime.fork {
			context = "fork"
		} else if runtime.fresh {
			context = "fresh"
		}
		agent := w.Agent
		if runtime.agentOverride != "" {
			agent = runtime.agentOverride
		}
		child := newObject()
		child.set("agent", agent)
		child.set("task", task)
		if w.Model != "" {
			child.set("model", w.Model)
		}
		switch s := w.Skill.(type) {
		case string:
			child.set("skill", s)
		case bool:
			child.set("skill", s)
		case []string:
			child.set("skill", anyStrings(s))
		}
		if w.Cwd != "" {
			child.set("cwd", w.Cwd)
		}
		if context != "" {
			child.set("context", context)
		}
		key := marshalJSON(fmt.Sprintf("prompt-%d-%s", i+1, w.Name), "")
		launches[i] = fmt.Sprintf("const step%d = await runs.run(%s, { ...%s, task: %s.replaceAll(\"{previous}\", previous) });\nprevious = step%d.output;", i, key, marshalJSON(child, ""), marshalJSON(task, ""), i)
	}
	return "let previous = \"\";\n" + strings.Join(launches, "\n") + "\nreturn previous;"
}

func promptWorkflowExecutionParams(workflows []PromptWorkflow, runtime promptRuntime) map[string]any {
	return map[string]any{"workflowScript": promptWorkflowScript(workflows, runtime.args, runtime), "agentScope": "both", "async": runtime.bg}
}

func formatPromptWorkflowList(workflows []PromptWorkflow) string {
	if len(workflows) == 0 {
		return "No prompt workflows found in package, user, or project prompts."
	}
	lines := []string{"Prompt workflows:"}
	for _, w := range workflows {
		lines = append(lines, fmt.Sprintf("- %s: %s (%s)", w.Name, w.Description, w.FilePath))
	}
	return strings.Join(lines, "\n")
}

func findPromptWorkflow(workflows []PromptWorkflow, name string) *PromptWorkflow {
	for i := range workflows {
		if workflows[i].Name == name {
			return &workflows[i]
		}
	}
	return nil
}

// promptCommandEnv is what the command needs of the host.
type promptCommandEnv struct {
	Cwd    string
	Notify func(message, level string)
	Send   func(content string)
	Run    func(params map[string]any) error
}

// runPromptWorkflowCommand is the handler of `/prompt-workflow`.
func runPromptWorkflowCommand(env promptCommandEnv, rawArgs string) error {
	words := shellWords(rawArgs)
	name := ""
	if len(words) > 0 {
		name, words = words[0], words[1:]
	}
	workflows, err := discoverPromptWorkflows(env.Cwd)
	if err != nil {
		return err
	}
	if name == "" || name == "list" {
		env.Send(formatPromptWorkflowList(workflows))
		return nil
	}
	workflow := findPromptWorkflow(workflows, name)
	if workflow == nil {
		env.Notify("Unknown prompt workflow: "+name, "error")
		return nil
	}
	runtime := parsePromptRuntimeOptions(words)
	selected := []PromptWorkflow{*workflow}
	if workflow.Chain != "" {
		selected = selected[:0]
		for _, stepName := range splitPromptChain(workflow.Chain) {
			step := findPromptWorkflow(workflows, stepName)
			if step == nil {
				env.Notify(fmt.Sprintf("Unknown prompt workflow in chain '%s': %s", workflow.Name, stepName), "error")
				return nil
			}
			selected = append(selected, *step)
		}
	}
	if err := env.Run(promptWorkflowExecutionParams(selected, runtime)); err != nil {
		env.Notify(err.Error(), "error")
	}
	return nil
}

// promptWorkflowRunner executes the generated workflow call. The workflow runtime registers itself here when a launcher is
// wired; until then the call is refused with the port's standard message, as the tool refuses `workflowScript`.
var promptWorkflowRunner = func(sdk.Context, map[string]any) error {
	return errors.New(refusal("workflow runs (`workflowScript`)"))
}

func init() {
	extensionHooks = append(extensionHooks, func(e *sdk.Extension) {
		e.Command("prompt-workflow", "Run a prompt template through native pi-subagents: /prompt-workflow <name> [args]", func(ctx sdk.Context, args string) error {
			return runPromptWorkflowCommand(promptCommandEnv{
				Cwd:    ctx.Cwd(),
				Notify: ctx.Notify,
				Send:   func(content string) { _ = ctx.SendMessage("prompt-workflow", content, true, sdk.SendMessageOptions{}) },
				Run:    func(params map[string]any) error { return promptWorkflowRunner(ctx, params) },
			}, args)
		})
	})
}
