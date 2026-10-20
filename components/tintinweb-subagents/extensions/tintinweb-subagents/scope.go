package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Extension and tool scope of an agent: which extensions its child loads and which tools it can call.
// upstream: src/agent-runner.ts (extensionCanonicalName, extensionCanonicalNames, parseExtensionsSpec, parseExtSelectors,
// the loader and tool-scope part of runAgent, installExtensionToolScope).

// subagentToolNames are the tools this extension registers; a subagent never inherits them.
// upstream: agent-runner.ts SUBAGENT_TOOL_NAMES, EXCLUDED_TOOL_NAMES.
var subagentToolNames = []string{"Agent", "SubagentWorkflow", "get_subagent_result", "steer_subagent"}

var jsExt = regexp.MustCompile(`\.(ts|js)$`)

// extensionCanonicalName is the lowercased name an extension answers to in `extensions: [...]`: a directory extension
// (`foo/index.ts`) is its parent directory's name, a single file its basename without .ts or .js. An extension that
// PiG ships (`builtin:<name>`) is its name. upstream: agent-runner.ts extensionCanonicalName.
func extensionCanonicalName(extPath string) string {
	if name, ok := strings.CutPrefix(extPath, "builtin:"); ok {
		return strings.ToLower(name)
	}
	base := filepath.Base(extPath)
	var name string
	if base == "index.ts" || base == "index.js" {
		name = filepath.Base(filepath.Dir(extPath))
	} else {
		name = jsExt.ReplaceAllString(base, "")
	}
	return strings.ToLower(name)
}

// extensionPackageName is the unscoped, lowercased npm short name of the pi package that declares extPath as an
// extension entry, or "". The search stops at the first package.json (the package root) and at a node_modules
// directory. upstream: agent-runner.ts extensionPackageName.
func extensionPackageName(extPath string) string {
	entry, err := filepath.Abs(extPath)
	if err != nil {
		entry = filepath.Clean(extPath)
	}
	dir := filepath.Dir(extPath)
	for {
		if filepath.Base(dir) == "node_modules" {
			return ""
		}
		data, err := os.ReadFile(filepath.Join(dir, "package.json"))
		var pkg any
		if err == nil {
			err = json.Unmarshal(data, &pkg)
		}
		if err != nil { // no package.json, or not JSON: keep climbing
			parent := filepath.Dir(dir)
			if parent == dir {
				return ""
			}
			dir = parent
			continue
		}
		// The first package.json that is JSON is the package root, whatever shape it has.
		manifest, _ := pkg.(map[string]any)
		name, _ := manifest["name"].(string)
		pi, _ := manifest["pi"].(map[string]any)
		entries, _ := pi["extensions"].([]any)
		for _, e := range entries {
			s, ok := e.(string)
			if !ok {
				continue
			}
			p := s
			if !isRooted(p) {
				p = filepath.Join(dir, p)
			}
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			if filepath.Clean(p) == entry && name != "" {
				short := name
				if strings.HasPrefix(name, "@") {
					if i := strings.Index(name, "/"); i >= 0 {
						short = name[i+1:]
					}
				}
				return strings.ToLower(short)
			}
		}
		return "" // the first package.json is the package root: decide here
	}
}

// extensionCanonicalNames is every name an extension answers to: its path-derived name and, when a pi package
// manifest declares the entry, that package's short name. upstream: agent-runner.ts extensionCanonicalNames (#143).
func extensionCanonicalNames(extPath string) []string {
	canonical := extensionCanonicalName(extPath)
	if strings.HasPrefix(extPath, "builtin:") {
		return []string{canonical}
	}
	if pkg := extensionPackageName(extPath); pkg != "" && pkg != canonical {
		return []string{canonical, pkg}
	}
	return []string{canonical}
}

// extensionsSpec is a parsed `extensions: [...]` list.
type extensionsSpec struct {
	Names    map[string]bool
	Paths    []string
	Wildcard bool
}

// parseExtensionsSpec classifies the entries of an extensions list: "*" keeps every default-discovered extension, an
// entry with a path separator or a leading ~ is a path (resolved against cwd, its canonical name added to the names),
// anything else a lowercased name. upstream: agent-runner.ts parseExtensionsSpec.
func parseExtensionsSpec(entries []string, cwd string) extensionsSpec {
	spec := extensionsSpec{Names: map[string]bool{}}
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		if entry == "*" {
			spec.Wildcard = true
			continue
		}
		if !strings.ContainsAny(entry, `/\`) && !strings.HasPrefix(entry, "~") {
			spec.Names[strings.ToLower(entry)] = true
			continue
		}
		p := entry
		if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
			home, _ := os.UserHomeDir()
			p = home + p[1:]
		}
		abs := p
		if !isRooted(p) {
			abs = filepath.Join(cwd, p)
		}
		spec.Paths = append(spec.Paths, abs)
		spec.Names[extensionCanonicalName(abs)] = true
	}
	return spec
}

// extSelectors are the parsed `ext:` entries of the tools list.
type extSelectors struct {
	ExtNames  map[string]bool
	Narrowing map[string]map[string]bool
}

// parseExtSelectors turns `ext:foo` and `ext:foo/bar` entries into the extension names to keep and a per-extension
// narrowing of its tools. A name lands in the narrowing only when a `/tool` form is seen, so a bare `ext:foo` beside
// `ext:foo/bar` leaves the narrowing in effect. The split is on the first `/`; the extension name is lowercased and the
// tool name keeps its case. upstream: agent-runner.ts parseExtSelectors.
func parseExtSelectors(entries []string) extSelectors {
	out := extSelectors{ExtNames: map[string]bool{}, Narrowing: map[string]map[string]bool{}}
	for _, raw := range entries {
		if raw == "" {
			continue
		}
		body := ""
		if len(raw) > len("ext:") {
			body = raw[len("ext:"):]
		}
		slash := strings.Index(body, "/")
		nameRaw := body
		if slash >= 0 {
			nameRaw = body[:slash]
		}
		name := strings.ToLower(strings.TrimSpace(nameRaw))
		if name == "" {
			continue
		}
		out.ExtNames[name] = true
		if slash < 0 {
			continue
		}
		tool := strings.TrimSpace(body[slash+1:])
		if tool == "" {
			continue
		}
		if out.Narrowing[name] == nil {
			out.Narrowing[name] = map[string]bool{}
		}
		out.Narrowing[name][tool] = true
	}
	return out
}

// loadedExtension is an extension a child loads: its path and the tools it registers.
type loadedExtension struct {
	Path  string
	Tools []string
}

// scopeInput is what the scope of one child is derived from.
type scopeInput struct {
	Type      string       // the agent type, for the warnings
	Config    *agentConfig // the agent's file or defaults
	Isolated  bool         // no extensions, skills or ext: selectors whatever the file says
	ConfigCwd string       // where relative extension paths and project config resolve
	ToolNames []string     // the built-in tools the agent asks for
	// Discovered are the extensions the child's loader finds by default, with their tools. A path entry of an
	// extensions list that is not among them is loaded fresh: Loadable names those a path resolves to.
	Discovered []loadedExtension
	// Loadable maps an extensions-list path to the extension it loads (a path that fails to load is absent).
	Loadable map[string]loadedExtension
	// Readmit are injected tools that stay active whatever the built-in list says (nested delegation, structured output).
	Readmit []string
}

// scopePlan is the extension and tool scope of one child.
type scopePlan struct {
	NoExtensions    bool              // the child loads no extension at all
	Tools           []string          // static allowlist (NoExtensions only); nil: unset
	ExcludeTools    []string          // names denied at the registry (extensions loaded)
	Survivors       []loadedExtension // the extensions the child loads
	LoadAll         bool              // all default-discovered extensions, unfiltered
	loadAllExt      bool              // extensions: true or a "*" entry: the filter keeps what it does not exclude
	AdditionalPaths []string          // extension paths loaded besides the defaults
	Warnings        []string          // misconfigurations, each reported like a tool activity of the run
	KeepNames       map[string]bool
	ExcludeNames    map[string]bool

	toolNames  []string
	disallowed map[string]bool
	extNames   map[string]bool
	narrowing  map[string]map[string]bool
	readmit    []string
}

// planScope derives the scope of a child. upstream: agent-runner.ts runAgent (the extension loading, the `ext:`
// selectors, the static allowlist and the excludeTools).
func planScope(in scopeInput) scopePlan {
	cfg := in.Config
	if cfg == nil {
		cfg = &agentConfig{Extensions: true}
	}
	var p scopePlan
	extensions := cfg.Extensions
	excludeExtensions := cfg.ExcludeExtensions
	if in.Isolated {
		extensions, excludeExtensions = false, nil
	}
	selectors := cfg.ExtSelectors
	if in.Isolated {
		selectors = nil
	}
	sel := parseExtSelectors(selectors)
	p.NoExtensions = extensions == false
	list, isList := extensions.([]string)
	var spec extensionsSpec
	if isList {
		spec = parseExtensionsSpec(list, in.ConfigCwd)
	} else {
		spec.Names = map[string]bool{}
	}
	p.KeepNames = spec.Names
	p.ExcludeNames = map[string]bool{}
	for _, n := range excludeExtensions {
		p.ExcludeNames[strings.ToLower(n)] = true
	}
	hasExcludes := len(p.ExcludeNames) > 0
	loadAll := extensions == true || (isList && spec.Wildcard)
	p.LoadAll = loadAll && !hasExcludes
	p.loadAllExt = loadAll
	p.AdditionalPaths = spec.Paths
	p.extNames, p.narrowing = sel.ExtNames, sel.Narrowing
	p.toolNames = in.ToolNames
	p.readmit = in.Readmit
	if len(cfg.DisallowedTools) > 0 {
		p.disallowed = map[string]bool{}
		for _, t := range cfg.DisallowedTools {
			p.disallowed[t] = true
		}
	}

	// The loader's filter: the default-discovered extensions plus the loaded paths, minus the excluded ones.
	discoveredNames := map[string]bool{}
	if !p.NoExtensions {
		all := slices.Clone(in.Discovered)
		for _, path := range spec.Paths {
			e, ok := in.Loadable[path]
			if !ok || slices.ContainsFunc(all, func(o loadedExtension) bool { return filepath.Clean(o.Path) == filepath.Clean(path) }) {
				continue
			}
			all = append(all, e)
		}
		if p.LoadAll {
			p.Survivors = all
		} else {
			// The loader's base set holds the extensions loaded from paths too, which an exclude may name.
			for _, e := range all {
				for _, n := range extensionCanonicalNames(e.Path) {
					discoveredNames[n] = true
				}
			}
			for _, e := range all {
				canons := extensionCanonicalNames(e.Path)
				if slices.ContainsFunc(canons, func(n string) bool { return p.ExcludeNames[n] }) {
					continue // exclude wins
				}
				if loadAll || slices.ContainsFunc(canons, func(n string) bool { return spec.Names[n] }) {
					p.Survivors = append(p.Survivors, e)
				}
			}
		}
	}

	// A plain entry of `tools:` is expected to be a built-in name: an unknown one is a typo.
	for _, name := range cfg.BuiltinToolNames {
		if !slices.Contains(builtinToolNames, name) {
			p.Warnings = append(p.Warnings, `tools-error:tool "`+name+`" requested by agent "`+in.Type+`" is not a known built-in`)
		}
	}
	warn := func(s string) { p.Warnings = append(p.Warnings, "extension-error:"+s) }
	if hasExcludes && p.NoExtensions {
		warn(`exclude_extensions has no effect for agent "` + in.Type + `" — extensions: false loads nothing`)
	}
	if hasExcludes && !p.NoExtensions && !p.LoadAll {
		for _, name := range sortedKeys(p.ExcludeNames) {
			if !discoveredNames[name] {
				warn(`exclude_extensions: "` + name + `" for agent "` + in.Type + `" did not match any discovered extension`)
			}
		}
	}
	if len(spec.Names) > 0 || len(sel.ExtNames) > 0 {
		surviving := map[string]bool{}
		for _, e := range p.Survivors {
			for _, n := range extensionCanonicalNames(e.Path) {
				surviving[n] = true
			}
		}
		for _, name := range sortedKeys(spec.Names) {
			if !surviving[name] {
				if p.ExcludeNames[name] {
					warn(`extension "` + name + `" is in both extensions: and exclude_extensions: for agent "` + in.Type + `" — exclude wins`)
				} else {
					warn(`extension "` + name + `" requested by agent "` + in.Type + `" was not loaded`)
				}
			}
		}
		for _, name := range sortedKeys(sel.ExtNames) {
			if !surviving[name] {
				warn("ext:" + name + ` referenced by agent "` + in.Type + `" but extension "` + name + `" is not loaded (check extensions:/exclude_extensions:)`)
			}
		}
	}

	builtinSet := map[string]bool{}
	for _, t := range in.ToolNames {
		builtinSet[t] = true
	}
	if p.NoExtensions {
		// A strict allowlist: nothing can register later.
		p.Tools = []string{}
		for _, t := range in.ToolNames {
			if !slices.Contains(subagentToolNames, t) && !p.disallowed[t] {
				p.Tools = append(p.Tools, t)
			}
		}
		// The injected tools were vetted against disallowed_tools by the caller.
		for _, t := range in.Readmit {
			if !slices.Contains(p.Tools, t) {
				p.Tools = append(p.Tools, t)
			}
		}
	} else {
		deny := map[string]bool{}
		var order []string
		add := func(n string) {
			if !deny[n] {
				deny[n] = true
				order = append(order, n)
			}
		}
		for _, n := range subagentToolNames {
			if !slices.Contains(in.Readmit, n) {
				add(n)
			}
		}
		for _, n := range builtinToolNames {
			if !builtinSet[n] {
				add(n)
			}
		}
		for _, n := range cfg.DisallowedTools {
			if !slices.Contains(in.Readmit, n) {
				add(n)
			}
		}
		p.ExcludeTools = order
	}
	return p
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// inScope is the set of tool names the child may call right now: the built-ins it asked for and the tools of the
// loaded extensions. Once any `ext:` selector is present extension tools become an explicit allowlist: an extension
// no selector names contributes nothing, and `ext:foo/bar` narrows foo to bar. Our own orchestration tools never
// pass; injected tools do. upstream: agent-runner.ts installExtensionToolScope (inScope).
func (p *scopePlan) inScope() map[string]bool {
	keep := map[string]bool{}
	for _, t := range p.toolNames {
		if !p.disallowed[t] {
			keep[t] = true
		}
	}
	optInActive := len(p.extNames) > 0
	for _, e := range p.Survivors {
		canons := extensionCanonicalNames(e.Path)
		if optInActive && !slices.ContainsFunc(canons, func(c string) bool { return p.extNames[c] }) {
			continue
		}
		// The first alias that carries a narrowing set.
		var narrowed map[string]bool
		for _, c := range canons {
			if n := p.narrowing[c]; n != nil {
				narrowed = n
				break
			}
		}
		for _, name := range e.Tools {
			if narrowed != nil && !narrowed[name] {
				continue
			}
			if p.disallowed[name] {
				continue
			}
			keep[name] = true
		}
	}
	for _, name := range subagentToolNames {
		delete(keep, name)
	}
	for _, name := range p.readmit {
		keep[name] = true
	}
	return keep
}

// Callable is the tools the child can call, in the order of its registry: the built-ins, then each loaded
// extension's tools. Under a static allowlist it is that list.
func (p *scopePlan) Callable() []string {
	if p.Tools != nil {
		return slices.Clone(p.Tools)
	}
	allowed := p.inScope()
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if allowed[n] && !seen[n] && !slices.Contains(p.ExcludeTools, n) {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, n := range builtinToolNames {
		add(n)
	}
	for _, e := range p.Survivors {
		for _, n := range e.Tools {
			add(n)
		}
	}
	for _, n := range p.readmit {
		add(n)
	}
	return out
}
