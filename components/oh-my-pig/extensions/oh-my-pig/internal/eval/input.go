package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/eval/input.ts, packages/coding-agent/src/utils/command-args.ts

var (
	percentCmdRe = regexp.MustCompile(`^%(load|pip|bun|environment)(?:[ \t]+([\s\S]*))?$`)
	uriSchemeRe  = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)
)

// PreparedEvalSource is a cell's resolved source and percent-command metadata.
// upstream: packages/coding-agent/src/eval/input.ts:17
type PreparedEvalSource struct {
	Code        string
	Filename    string
	Packages    []string
	Environment string // "", "managed", or "project"
}

// prepareEvalSource resolves standalone percent commands (%load, %pip, %bun, %environment)
// before execution. Python only intercepts %load (for internal URLs and paths); its %pip
// runs through runner.py's own magic in the kernel's interpreter.
// upstream: packages/coding-agent/src/eval/input.ts:30
func prepareEvalSource(language, code, cwd string, ctx sdk.Context) (PreparedEvalSource, error) {
	trimmed := strings.TrimSpace(code)
	m := percentCmdRe.FindStringSubmatch(trimmed)
	if m == nil || (language == "py" && m[1] != "load") {
		return PreparedEvalSource{Code: code}, nil
	}
	cmd := m[1]
	rawArgs := m[2]
	if strings.ContainsAny(rawArgs, "\r\n") {
		return PreparedEvalSource{}, fmt.Errorf("Use %%load, %%pip, %%bun, and %%environment as standalone cells; run subsequent code in another eval call.")
	}
	args, err := parseStrictCommandArgs(rawArgs)
	if err != nil {
		return PreparedEvalSource{}, err
	}
	switch cmd {
	case "environment":
		if len(args) != 1 || (args[0] != "managed" && args[0] != "project") {
			return PreparedEvalSource{}, fmt.Errorf("Usage: %%environment managed|project. Project selection permits project dependency changes.")
		}
		return PreparedEvalSource{Code: "", Environment: args[0]}, nil
	case "pip":
		return PreparedEvalSource{}, fmt.Errorf("Use %%bun add for JavaScript dependencies.")
	case "bun":
		if len(args) < 2 || args[0] != "add" {
			return PreparedEvalSource{}, fmt.Errorf("Usage: %%bun add <package requirements...>. Quote requirements containing spaces.")
		}
		return PreparedEvalSource{Code: "", Packages: args[1:]}, nil
	case "load":
		if len(args) != 1 || args[0] == "" {
			return PreparedEvalSource{}, fmt.Errorf("Usage: %%load <script path>. Quote paths containing spaces.")
		}
		return loadEvalScript(args[0], cwd, ctx)
	}
	return PreparedEvalSource{Code: code}, nil
}

func loadEvalScript(file, cwd string, ctx sdk.Context) (PreparedEvalSource, error) {
	var filename string
	if urls.CanHandle(file) {
		uCtx := urls.Context{SDK: ctx, Cwd: cwd}
		located, ok, err := urls.Locate(uCtx, file, urls.LocateOptions{LocalOnly: true})
		if err != nil {
			return PreparedEvalSource{}, fmt.Errorf("Cannot load %s: %w", file, err)
		}
		if ok && located != "" {
			filename = located
		} else {
			// Check whether the handler backs a virtual/remote resource or can resolve text directly.
			parsed, parseErr := urls.Parse(file)
			scheme := file
			var backing urls.Backing
			if parseErr == nil {
				scheme = parsed.Scheme
				if h, found := urls.Lookup(parsed.Scheme); found {
					spec := h.Spec()
					backing = spec.Backing
					if spec.Immutable && spec.Backing != urls.BackingRemote {
						if _, resErr := urls.Resolve(uCtx, file); resErr != nil {
							return PreparedEvalSource{}, fmt.Errorf("Cannot load %s: %w", file, resErr)
						}
					}
				}
			}
			msg := fmt.Sprintf("Cannot load %s:// URL: no local file backs %s", scheme, file)
			if backing == urls.BackingRemote || backing == urls.BackingVirtual {
				return PreparedEvalSource{}, fmt.Errorf("%s. Use `read %s` to inspect it.", msg, file)
			}
			return PreparedEvalSource{}, fmt.Errorf("%s", msg)
		}
	} else {
		if uriSchemeRe.MatchString(file) && !strings.HasPrefix(strings.ToLower(file), "file://") {
			return PreparedEvalSource{}, fmt.Errorf("Eval scripts must be local. Download and inspect remote scripts before executing them.")
		}
		cleaned := file
		if strings.HasPrefix(strings.ToLower(cleaned), "file://") {
			cleaned = cleaned[len("file://"):]
		}
		cleaned = expandHome(cleaned)
		if filepath.IsAbs(cleaned) {
			filename = filepath.Clean(cleaned)
		} else {
			filename = filepath.Join(cwd, cleaned)
		}
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return PreparedEvalSource{}, fmt.Errorf("Cannot load eval script %s: %w", file, err)
	}
	return PreparedEvalSource{Code: string(data), Filename: filename}, nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// parseStrictCommandArgs splits shell-quoted arguments (upstream: command-args.ts strict: true).
func parseStrictCommandArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	var inQuote byte
	started := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\\' && inQuote != '\'' {
			if i+1 >= len(s) {
				return nil, fmt.Errorf("Incomplete command escape.")
			}
			next := s[i+1]
			if inQuote == 0 || next == '"' || next == '\\' || next == '$' || next == '`' {
				cur.WriteByte(next)
				started = true
				i++
				continue
			}
		}
		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			} else {
				cur.WriteByte(ch)
			}
		} else if ch == '"' || ch == '\'' {
			inQuote = ch
			started = true
		} else if ch == ' ' || ch == '\t' {
			if cur.Len() > 0 || started {
				args = append(args, cur.String())
			}
			cur.Reset()
			started = false
		} else {
			cur.WriteByte(ch)
			started = true
		}
	}
	if inQuote != 0 {
		return nil, fmt.Errorf("Unterminated command quote.")
	}
	if cur.Len() > 0 || started {
		args = append(args, cur.String())
	}
	return args, nil
}
