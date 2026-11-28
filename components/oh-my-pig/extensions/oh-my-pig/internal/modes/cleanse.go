package modes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:466-477
// upstream: packages/coding-agent/src/modes/controllers/cleanse-command-controller.ts
// upstream: packages/coding-agent/src/cleanse/index.ts
// upstream: packages/coding-agent/src/cleanse/checkers.ts

const (
	cleanseUsage         = "Usage: /cleanse [request] [--all] [--tests] [-n <agents>] [-m <model>]"
	cleanseDefaultAgents = 32
	cleanseMaxAgents     = 64
	cleanseDefaultModel  = "@smol"
)

// CleanseOptions holds parsed `/cleanse` command arguments.
// upstream: packages/coding-agent/src/modes/controllers/cleanse-command-controller.ts:20-27
type CleanseOptions struct {
	Request      string
	All          bool
	IncludeTests bool
	MaxAgents    int
	Model        string
	Cancel       bool
}

// CleanseChecker describes one discovered project diagnostic checker.
type CleanseChecker struct {
	ID       string
	Label    string
	Language string
	Command  string
	IsTest   bool
}

// CleanseRequest is passed to SpawnCleanse when an integrator supplies a custom
// parallel cleanse dispatcher; otherwise `/cleanse` sends a structured parallel
// subagent prompt via `SendUserMessage`.
type CleanseRequest struct {
	Options  CleanseOptions
	Checkers []CleanseChecker
	Prompt   string
}

// SpawnCleanse optionally handles `/cleanse` runs via a custom parallel worker
// orchestrator. When nil, `/cleanse` dispatches via `sendPromptOrSteer`.
var SpawnCleanse func(ctx sdk.Context, req CleanseRequest) (cancel func(), err error)

// ParseCleanseArgs parses `/cleanse` CLI flags matching upstream
// `cleanse-command-controller.ts:154-179`.
// Defaults: MaxAgents=32 (clamped 1..64), Model="@smol".
func ParseCleanseArgs(args string) (CleanseOptions, error) {
	opts := CleanseOptions{
		MaxAgents: cleanseDefaultAgents,
		Model:     cleanseDefaultModel,
	}
	tokens := strings.Fields(args)
	var reqParts []string

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch tok {
		case "--all", "-a":
			opts.All = true
		case "--tests", "-t":
			opts.IncludeTests = true
		case "--cancel", "cancel":
			if len(tokens) == 1 {
				opts.Cancel = true
				return opts, nil
			}
			reqParts = append(reqParts, tok)
		case "--agents", "-n":
			if i+1 >= len(tokens) {
				return opts, fmt.Errorf("%s", cleanseUsage)
			}
			i++
			v, err := strconv.Atoi(tokens[i])
			if err != nil || v <= 0 {
				return opts, fmt.Errorf("%s", cleanseUsage)
			}
			if v > cleanseMaxAgents {
				v = cleanseMaxAgents
			}
			opts.MaxAgents = v
		case "--model", "-m":
			if i+1 >= len(tokens) {
				return opts, fmt.Errorf("%s", cleanseUsage)
			}
			i++
			val := strings.TrimSpace(tokens[i])
			if val == "" {
				return opts, fmt.Errorf("%s", cleanseUsage)
			}
			opts.Model = val
		default:
			if strings.HasPrefix(tok, "-") && len(tok) > 1 && !isNegativeNumberToken(tok) {
				return opts, fmt.Errorf("%s", cleanseUsage)
			}
			reqParts = append(reqParts, tok)
		}
	}
	opts.Request = strings.TrimSpace(strings.Join(reqParts, " "))
	return opts, nil
}

func isNegativeNumberToken(tok string) bool {
	if len(tok) < 2 || tok[0] != '-' {
		return false
	}
	return tok[1] >= '0' && tok[1] <= '9'
}

// DiscoverCleanseCheckers inspects cwd for common language project manifests
// (Go, JS/TS, Rust, Python) and returns runnable diagnostic checkers.
// upstream: packages/coding-agent/src/cleanse/checkers.ts
func DiscoverCleanseCheckers(cwd string, includeTests bool) []CleanseChecker {
	var out []CleanseChecker
	if cwd == "" {
		return out
	}

	// Go (`go.mod`)
	if fileExists(filepath.Join(cwd, "go.mod")) {
		out = append(out, CleanseChecker{
			ID:       "go-vet",
			Label:    "Go vet",
			Language: "go",
			Command:  "go vet ./...",
		})
		if includeTests {
			out = append(out, CleanseChecker{
				ID:       "go-test",
				Label:    "Go test",
				Language: "go",
				Command:  "go test ./...",
				IsTest:   true,
			})
		}
	}

	// JavaScript / TypeScript (`package.json`)
	if raw, err := os.ReadFile(filepath.Join(cwd, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(raw, &pkg) == nil && len(pkg.Scripts) > 0 {
			runner := detectJSRunner(cwd)
			for _, name := range []string{"typecheck", "check", "lint"} {
				if _, ok := pkg.Scripts[name]; ok {
					out = append(out, CleanseChecker{
						ID:       "js-" + name,
						Label:    fmt.Sprintf("JS %s", name),
						Language: "typescript",
						Command:  fmt.Sprintf("%s run %s", runner, name),
					})
				}
			}
			if includeTests {
				if _, ok := pkg.Scripts["test"]; ok {
					out = append(out, CleanseChecker{
						ID:       "js-test",
						Label:    "JS test",
						Language: "typescript",
						Command:  fmt.Sprintf("%s test", runner),
						IsTest:   true,
					})
				}
			}
		} else if fileExists(filepath.Join(cwd, "tsconfig.json")) {
			out = append(out, CleanseChecker{
				ID:       "tsc",
				Label:    "TypeScript check",
				Language: "typescript",
				Command:  "tsc --noEmit",
			})
		}
	} else if fileExists(filepath.Join(cwd, "tsconfig.json")) {
		out = append(out, CleanseChecker{
			ID:       "tsc",
			Label:    "TypeScript check",
			Language: "typescript",
			Command:  "tsc --noEmit",
		})
	}

	// Rust (`Cargo.toml`)
	if fileExists(filepath.Join(cwd, "Cargo.toml")) {
		out = append(out, CleanseChecker{
			ID:       "cargo-check",
			Label:    "Cargo check",
			Language: "rust",
			Command:  "cargo check --all-targets",
		})
		out = append(out, CleanseChecker{
			ID:       "cargo-clippy",
			Label:    "Cargo clippy",
			Language: "rust",
			Command:  "cargo clippy --all-targets",
		})
		if includeTests {
			out = append(out, CleanseChecker{
				ID:       "cargo-test",
				Label:    "Cargo test",
				Language: "rust",
				Command:  "cargo test",
				IsTest:   true,
			})
		}
	}

	// Python (`pyproject.toml`)
	if raw, err := os.ReadFile(filepath.Join(cwd, "pyproject.toml")); err == nil {
		txt := string(raw)
		if strings.Contains(txt, "ruff") {
			out = append(out, CleanseChecker{
				ID:       "ruff",
				Label:    "Ruff check",
				Language: "python",
				Command:  "ruff check .",
			})
		}
		if strings.Contains(txt, "mypy") {
			out = append(out, CleanseChecker{
				ID:       "mypy",
				Label:    "Mypy",
				Language: "python",
				Command:  "mypy .",
			})
		}
		if includeTests || strings.Contains(txt, "pytest") {
			if includeTests {
				out = append(out, CleanseChecker{
					ID:       "pytest",
					Label:    "Pytest",
					Language: "python",
					Command:  "pytest",
					IsTest:   true,
				})
			}
		}
		// Fallback checker when pyproject.toml has none of the explicit tool sections.
		if !hasLanguage(out, "python") {
			out = append(out, CleanseChecker{
				ID:       "ruff",
				Label:    "Ruff check",
				Language: "python",
				Command:  "ruff check .",
			})
		}
	}

	return out
}

func detectJSRunner(cwd string) string {
	switch {
	case fileExists(filepath.Join(cwd, "bun.lock")), fileExists(filepath.Join(cwd, "bun.lockb")):
		return "bun"
	case fileExists(filepath.Join(cwd, "pnpm-lock.yaml")):
		return "pnpm"
	case fileExists(filepath.Join(cwd, "yarn.lock")):
		return "yarn"
	default:
		return "npm"
	}
}

func hasLanguage(list []CleanseChecker, lang string) bool {
	for _, c := range list {
		if c.Language == lang {
			return true
		}
	}
	return false
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// BuildCleansePrompt renders the structured parallel diagnostic & repair prompt
// dispatched by `/cleanse`.
func BuildCleansePrompt(opts CleanseOptions, checkers []CleanseChecker) string {
	var b strings.Builder
	b.WriteString("<cleanse-run>\n")
	b.WriteString("Detect and fix project diagnostics cleanly using parallel subagents partitioned by disjoint file ownership.\n\n")
	fmt.Fprintf(&b, "- Max parallel subagents: %d\n", opts.MaxAgents)
	fmt.Fprintf(&b, "- Preferred worker model: %s\n", opts.Model)
	if opts.IncludeTests {
		b.WriteString("- Include test suites: yes\n")
	}
	if opts.Request != "" {
		fmt.Fprintf(&b, "- Target request: %s\n", opts.Request)
	}
	if len(checkers) > 0 {
		b.WriteString("\nDiscovered checkers:\n")
		for _, c := range checkers {
			fmt.Fprintf(&b, "- %s (%s): `%s`\n", c.Label, c.Language, c.Command)
		}
	}
	b.WriteString("\nWorkflow:\n")
	b.WriteString("1. Run the discovered/requested diagnostic checkers to collect all current errors and warnings.\n")
	b.WriteString("2. Group diagnostics by file/package into disjoint ownership slices (up to max agents) and dispatch parallel subagents to fix them without overlapping edits.\n")
	b.WriteString("3. Re-run the checkers once after all workers finish to verify zero remaining diagnostics, and report the summary.\n")
	b.WriteString("</cleanse-run>")
	return b.String()
}

// handleCleanseCommand implements `/cleanse [request] [--all|-a] [--tests|-t] [-n|--agents <N>] [-m|--model <model>]`.
// upstream: packages/coding-agent/src/modes/controllers/cleanse-command-controller.ts:60-179
func (m *manager) handleCleanseCommand(ctx sdk.Context, args string) error {
	sid := strictSessionID(ctx)
	opts, err := ParseCleanseArgs(args)
	if err != nil {
		notifyInfo(ctx, err.Error())
		return nil
	}

	if opts.Cancel {
		return m.cancelCleanse(ctx, sid)
	}

	// Acquire the single-active-run slot atomically before any interactive UI
	// prompt (`ctx.Select`) or spawn call, rolling it back on any early exit or
	// error so concurrent `/cleanse` invocations cannot race.
	committed := false
	if sid != "" {
		m.mu.Lock()
		st := m.stateLocked(sid)
		if st.cleanseActive {
			m.mu.Unlock()
			notifyWarn(ctx, "A /cleanse run is already active — run /cleanse cancel or press Escape to cancel it.")
			return nil
		}
		st.cleanseActive = true
		st.cleanseCancel = nil
		m.mu.Unlock()
		defer func() {
			if !committed {
				m.mu.Lock()
				if st := m.sessions[sid]; st != nil {
					st.cleanseActive = false
					st.cleanseCancel = nil
				}
				m.mu.Unlock()
			}
		}()
	}

	var checkers []CleanseChecker
	if opts.Request == "" {
		checkers = DiscoverCleanseCheckers(ctx.Cwd(), opts.IncludeTests)
		if len(checkers) == 0 {
			notifyWarn(ctx, "No supported project checker found (checked go.mod, package.json, Cargo.toml, pyproject.toml). Pass a request, e.g. /cleanse \"fix lint errors\".")
			return nil
		}
		if !opts.All && ctx.HasUI() && len(checkers) > 1 {
			allOpt := fmt.Sprintf("Run all %d discovered checkers", len(checkers))
			options := make([]string, 0, len(checkers)+1)
			options = append(options, allOpt)
			for _, c := range checkers {
				options = append(options, fmt.Sprintf("%s — %s", c.Label, c.Command))
			}
			pickedVal, ok, selErr := ctx.Select("Select what to cleanse", options)
			if selErr != nil || !ok {
				notifyInfo(ctx, "Cleanse cancelled.")
				return selErr
			}
			picked, _ := pickedVal.(string)
			if picked != allOpt {
				for i, c := range checkers {
					if options[i+1] == picked {
						checkers = []CleanseChecker{c}
						break
					}
				}
			}
		}
	}

	promptText := BuildCleansePrompt(opts, checkers)

	if SpawnCleanse != nil {
		cancelFn, spawnErr := SpawnCleanse(ctx, CleanseRequest{
			Options:  opts,
			Checkers: checkers,
			Prompt:   promptText,
		})
		if spawnErr != nil {
			notifyError(ctx, fmt.Sprintf("Failed to start /cleanse: %v", spawnErr))
			return nil
		}
		if sid != "" {
			m.mu.Lock()
			if st := m.sessions[sid]; st != nil {
				if cancelFn != nil {
					st.cleanseCancel = cancelFn
					committed = true
				} else {
					// Synchronous/self-completing dispatcher returned nil cancelFn:
					// release the active slot on return.
					st.cleanseActive = false
					st.cleanseCancel = nil
				}
			}
			m.mu.Unlock()
		}
		notifyInfo(ctx, formatCleanseStartNotice(opts, checkers))
		return nil
	}

	notifyInfo(ctx, formatCleanseStartNotice(opts, checkers))
	if err := sendPromptOrSteer(ctx, promptText); err != nil {
		notifyError(ctx, fmt.Sprintf("Failed to start /cleanse: %v", err))
		return nil
	}
	committed = true
	return nil
}

func (m *manager) cancelCleanse(ctx sdk.Context, sid string) error {
	if sid == "" {
		notifyInfo(ctx, "No active /cleanse run.")
		return nil
	}
	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil || !st.cleanseActive {
		m.mu.Unlock()
		notifyInfo(ctx, "No active /cleanse run.")
		return nil
	}
	cancelFn := st.cleanseCancel
	st.cleanseActive = false
	st.cleanseCancel = nil
	m.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	} else {
		ctx.Abort()
	}
	notifyInfo(ctx, "Cleanse cancelled.")
	return nil
}

func formatCleanseStartNotice(opts CleanseOptions, checkers []CleanseChecker) string {
	if opts.Request != "" {
		return fmt.Sprintf("Starting /cleanse for %q (max %d agents, model %s).", opts.Request, opts.MaxAgents, opts.Model)
	}
	return fmt.Sprintf("Starting /cleanse with %d checker(s) (max %d agents, model %s).", len(checkers), opts.MaxAgents, opts.Model)
}
