package archive

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
)

// upstream: packages/coding-agent/src/archive/prelude-definition.ts,
//           packages/coding-agent/src/archive/prelude.js,
//           packages/coding-agent/src/archive/prelude.py,
//           packages/coding-agent/src/prompts/tools/archive.md

//go:embed assets/archive.md
var archiveDocumentation string

//go:embed assets/prelude.js
var archiveJavascript string

//go:embed assets/prelude.py
var archivePython string

// SettingArchiveEnabled matches omp's cfgArchiveEnabled ("archive.enabled", default true).
// upstream: packages/coding-agent/src/tools/settings.ts
const SettingArchiveEnabled = "archive.enabled"

const (
	defaultLimit = 20
	minLimit     = 1
	maxLimit     = 500
)

// Setting reads a setting key for ctx (wired by wiring.go -> settings.Any).
// Default is enabled (`true`) when nil or unset.
var Setting func(ctx sdk.Context, key string) any

// Register installs the `archive` eval prelude.
// Safe to call multiple times across sessions in one process; does no I/O.
func Register(_ *sdk.Extension) {
	eval.RegisterPrelude(Prelude())
}

// Prelude constructs the eval.Prelude definition for `archive`.
//
// upstream: packages/coding-agent/src/archive/prelude-definition.ts:85-103
func Prelude() eval.Prelude {
	return eval.Prelude{
		Name: "archive",
		Documentation: func() string {
			return strings.TrimSpace(archiveDocumentation)
		},
		JavaScript: archiveJavascript,
		Python:     archivePython,
		Exports:    []string{"archive"},
		Approval:   "read",
		Enabled:    archiveEnabled,
		Invoke: func(call eval.PreludeCall, rawParams json.RawMessage) (*eval.PreludeResult, error) {
			return invokeArchivePrelude(call, rawParams)
		},
		Status: func(parameters json.RawMessage, _ *eval.PreludeResult) string {
			return describeArchiveCall(parameters)
		},
	}
}

func archiveEnabled(ctx sdk.Context) bool {
	if Setting != nil {
		if b, ok := Setting(ctx, SettingArchiveEnabled).(bool); ok {
			return b
		}
	}
	var zero sdk.Context
	if ctx != zero {
		var enabled = true
		func() {
			defer func() { _ = recover() }()
			if cfg, err := ctx.GetSettings(); err == nil && cfg != nil {
				if v, ok := lookupBool(cfg, SettingArchiveEnabled); ok {
					enabled = v
				}
			}
		}()
		return enabled
	}
	return true
}

func lookupBool(cfg map[string]any, dotted string) (bool, bool) {
	if v, ok := cfg[dotted]; ok {
		b, ok := v.(bool)
		return b, ok
	}
	parts := strings.Split(dotted, ".")
	var cur any = cfg
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return false, false
		}
		cur, ok = m[p]
		if !ok {
			return false, false
		}
	}
	b, ok := cur.(bool)
	return b, ok
}

type parsedArchiveParams struct {
	action     string
	limit      int
	hasProject bool
	project    string
	id         string
	hasQuery   bool
	query      string
}

// parseArchiveParams strictly validates prelude arguments matching upstream's
// omptype union with `"+": "reject"`.
//
// upstream: packages/coding-agent/src/archive/prelude-definition.ts:22-34, 95-97
func parseArchiveParams(raw json.RawMessage) (*parsedArchiveParams, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("archive received invalid arguments: expected an object")
	}

	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil || obj == nil {
		return nil, fmt.Errorf("archive received invalid arguments: malformed JSON object")
	}

	rawAction, ok := obj["action"]
	if !ok {
		return nil, fmt.Errorf("archive received invalid arguments: action is required")
	}
	action, ok := rawAction.(string)
	if !ok {
		return nil, fmt.Errorf("archive received invalid arguments: action must be a string")
	}

	var allowed map[string]bool
	switch action {
	case "projects":
		allowed = map[string]bool{"action": true, "limit": true}
	case "sessions", "recaps":
		allowed = map[string]bool{"action": true, "project": true, "limit": true}
	case "session":
		allowed = map[string]bool{"action": true, "id": true, "limit": true}
	case "prompts":
		allowed = map[string]bool{"action": true, "query": true, "project": true, "limit": true}
	case "search":
		allowed = map[string]bool{"action": true, "query": true, "project": true, "limit": true}
	default:
		return nil, fmt.Errorf("archive received invalid arguments: unknown action %q", action)
	}

	var unknown []string
	for k := range obj {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("archive received invalid arguments: unexpected key(s) %s for action %q", strings.Join(unknown, ", "), action)
	}

	p := &parsedArchiveParams{
		action: action,
		limit:  defaultLimit,
	}

	if rawLimit, exists := obj["limit"]; exists {
		n, err := parseStrictLimit(rawLimit)
		if err != nil {
			return nil, fmt.Errorf("archive received invalid arguments: %s", err.Error())
		}
		p.limit = n
	}

	if rawProj, exists := obj["project"]; exists {
		s, ok := rawProj.(string)
		if !ok || len(s) == 0 {
			return nil, fmt.Errorf("archive received invalid arguments: project must be a non-empty string")
		}
		p.hasProject = true
		p.project = s
	}

	if action == "session" {
		rawID, exists := obj["id"]
		if !exists {
			return nil, fmt.Errorf("archive received invalid arguments: id is required for action \"session\"")
		}
		s, ok := rawID.(string)
		if !ok || len(s) == 0 {
			return nil, fmt.Errorf("archive received invalid arguments: id must be a non-empty string")
		}
		p.id = s
	}

	if action == "search" {
		rawQuery, exists := obj["query"]
		if !exists {
			return nil, fmt.Errorf("archive received invalid arguments: query is required for action \"search\"")
		}
		s, ok := rawQuery.(string)
		if !ok || len(s) == 0 {
			return nil, fmt.Errorf("archive received invalid arguments: query must be a non-empty string")
		}
		p.hasQuery = true
		p.query = s
	} else if action == "prompts" {
		if rawQuery, exists := obj["query"]; exists {
			s, ok := rawQuery.(string)
			if !ok || len(s) == 0 {
				return nil, fmt.Errorf("archive received invalid arguments: query must be a non-empty string")
			}
			p.hasQuery = true
			p.query = s
		}
	}

	return p, nil
}

func parseStrictLimit(raw any) (int, error) {
	switch v := raw.(type) {
	case json.Number:
		s := v.String()
		if strings.ContainsAny(s, ".eE") {
			f, err := v.Float64()
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
				return 0, fmt.Errorf("limit must be an integer between %d and %d", minLimit, maxLimit)
			}
			n := int(f)
			if n < minLimit || n > maxLimit {
				return 0, fmt.Errorf("limit must be an integer between %d and %d", minLimit, maxLimit)
			}
			return n, nil
		}
		n64, err := v.Int64()
		if err != nil || n64 < minLimit || n64 > maxLimit {
			return 0, fmt.Errorf("limit must be an integer between %d and %d", minLimit, maxLimit)
		}
		return int(n64), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v {
			return 0, fmt.Errorf("limit must be an integer between %d and %d", minLimit, maxLimit)
		}
		n := int(v)
		if n < minLimit || n > maxLimit {
			return 0, fmt.Errorf("limit must be an integer between %d and %d", minLimit, maxLimit)
		}
		return n, nil
	case int:
		if v < minLimit || v > maxLimit {
			return 0, fmt.Errorf("limit must be an integer between %d and %d", minLimit, maxLimit)
		}
		return v, nil
	default:
		return 0, fmt.Errorf("limit must be an integer between %d and %d", minLimit, maxLimit)
	}
}

// resolveProject maps a `project` argument to a target project directory,
// or "" to span every project ("*").
//
// upstream: packages/coding-agent/src/archive/prelude-definition.ts:41-44
func resolveProject(hasProject bool, project, cwd string) string {
	if hasProject {
		if project == "*" {
			return ""
		}
		return resolveToCwd(project, cwd)
	}
	return cwd
}

// describeArchiveCall produces the status-tree line for a completed archive call.
// upstream: packages/coding-agent/src/archive/prelude-definition.ts:65-82
func describeArchiveCall(raw json.RawMessage) string {
	p, err := parseArchiveParams(raw)
	if err != nil {
		return ""
	}
	switch p.action {
	case "projects":
		return "archive.projects()"
	case "session":
		return fmt.Sprintf("archive.session(%s)", p.id)
	case "search":
		return fmt.Sprintf("archive.search(%s)", jsonQuote(p.query))
	case "prompts":
		if p.hasQuery && p.query != "" {
			return fmt.Sprintf("archive.prompts(%s)", jsonQuote(p.query))
		}
		if p.hasProject {
			return fmt.Sprintf("archive.prompts(%s)", shortenPath(p.project))
		}
		return "archive.prompts()"
	case "sessions", "recaps":
		if p.hasProject {
			return fmt.Sprintf("archive.%s(%s)", p.action, shortenPath(p.project))
		}
		return fmt.Sprintf("archive.%s()", p.action)
	}
	return ""
}

func invokeArchivePrelude(call eval.PreludeCall, rawParams json.RawMessage) (*eval.PreludeResult, error) {
	p, err := parseArchiveParams(rawParams)
	if err != nil {
		return nil, err
	}

	var cwd string
	var done <-chan struct{}
	var zero sdk.Context
	if call.SDK != zero {
		func() {
			defer func() { _ = recover() }()
			cwd = call.SDK.Cwd()
			done = call.SDK.Done()
		}()
	}

	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if done != nil {
		go func() {
			select {
			case <-done:
				cancel()
			case <-reqCtx.Done():
			}
		}()
	}

	var view ArchiveView
	switch p.action {
	case "projects":
		view, err = ArchiveProjects(reqCtx, p.limit)
	case "sessions":
		view, err = ArchiveSessions(reqCtx, resolveProject(p.hasProject, p.project, cwd), p.limit)
	case "session":
		target := p.id
		if strings.HasSuffix(target, ".jsonl") {
			target = resolveToCwd(target, cwd)
		}
		view, err = ArchiveSessionDetailByID(reqCtx, target, p.limit)
	case "prompts":
		view, err = ArchivePrompts(reqCtx, p.query, p.hasQuery, resolveProject(p.hasProject, p.project, cwd), p.limit)
	case "search":
		view, err = ArchiveSearch(reqCtx, p.query, resolveProject(p.hasProject, p.project, cwd), p.limit)
	case "recaps":
		view, err = ArchiveRecaps(reqCtx, resolveProject(p.hasProject, p.project, cwd), p.limit)
	default:
		return nil, fmt.Errorf("archive received invalid arguments: unknown action %q", p.action)
	}
	if err != nil {
		return nil, err
	}

	// CRITICAL bridge invariant (internal/eval/bridge.go:397-416):
	// Details MUST ALWAYS be non-nil so bridge.go returns `{text, details}` rather than
	// a bare string, which prelude.py (`isinstance(response, dict)`) and prelude.js require.
	if view.Records == nil {
		view.Records = []any{}
	}
	return &eval.PreludeResult{
		Text:    view.Text,
		Details: view.Records,
	}, nil
}
