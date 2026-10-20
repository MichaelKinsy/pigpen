package tintinweb_subagents

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// What the extensions of a child are, from where this extension stands: the parent's own tool registry says which
// tools each extension registers, and `pig status --json` lists the extensions pig discovers by default (the SDK has
// no listing of extensions). upstream: agent-runner.ts reads the child's loader (loader.getExtensions()) instead.

// builtinExtensionNames are the extensions PiG ships inside pig, loaded as `builtin:<name>`: llama.cpp, codemode,
// tool-search and mcp (PiG v0.5.0 coding/cli/builtin_extensions.go). `--no-extensions` turns them off with the rest.
var builtinExtensionNames = []string{"llama.cpp", "codemode", "tool-search", "mcp"}

// toolOrigin is where a tool of the parent's registry comes from.
type toolOrigin struct {
	Name string
	Path string // the extension that registered it; "" for a built-in tool
}

// registryOrigins groups the parent's registered tools by the extension that registered them.
func registryOrigins(tools []sdk.ToolInfo) []toolOrigin {
	out := make([]toolOrigin, 0, len(tools))
	for _, t := range tools {
		path := t.SourceInfo.Path
		if slices.Contains(builtinToolNames, t.Name) && (t.SourceInfo.Source == "builtin" || t.Source == "builtin" || path == "" || strings.HasPrefix(path, "<")) {
			path = ""
		} else if path == "" {
			path = "builtin:" + t.Source
		}
		out = append(out, toolOrigin{Name: t.Name, Path: path})
	}
	return out
}

// extensionsOf turns a registry listing and a list of discovered extension paths into the extensions a child would
// load, each with the tools the registry knows for it. A listed path the registry does not know has no tools there.
func extensionsOf(origins []toolOrigin, listed []string) []loadedExtension {
	var out []loadedExtension
	index := map[string]int{}
	add := func(path string) int {
		key := filepath.Clean(path)
		if i, ok := index[key]; ok {
			return i
		}
		out = append(out, loadedExtension{Path: path})
		index[key] = len(out) - 1
		return len(out) - 1
	}
	for _, p := range listed {
		add(p)
	}
	for _, o := range origins {
		if o.Path == "" {
			continue
		}
		i := add(o.Path)
		out[i].Tools = append(out[i].Tools, o.Name)
	}
	return out
}

// statusExtensions lists the enabled extensions `pig status --json` reports for a workspace: the ones a child
// discovers by default. The listing is cached by the caller; it costs a process start.
func statusExtensions(ctx context.Context, cwd string) ([]string, error) {
	bin, err := pigBinary()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "status", "--json")
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		// `pig status` exits non-zero when it reports an unhealthy installation, with the JSON still on stdout.
		if out.Len() == 0 {
			return nil, err
		}
	}
	var st struct {
		Resources struct {
			Items []struct {
				Kind    string `json:"kind"`
				Path    string `json:"path"`
				Enabled bool   `json:"enabled"`
			} `json:"items"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		return nil, err
	}
	var paths []string
	for _, it := range st.Resources.Items {
		if it.Kind == "extensions" && it.Enabled {
			paths = append(paths, it.Path)
		}
	}
	return paths, nil
}

// childScope is the extension and tool scope of one child, as pig flags. It derives the plan from the agent's file
// and the parent's registry, and turns it into the spec fields.
func (a *app) childScope(ctx sdk.Context, typ string, cfg *agentConfig, isolated bool, toolNames []string, configCwd string, readmit []string) (scopePlan, scopeSpec, error) {
	origins := []toolOrigin(nil)
	if tools, err := ctx.GetAllTools(); err == nil {
		origins = registryOrigins(tools)
	}
	in := scopeInput{Type: typ, Config: cfg, Isolated: isolated, ConfigCwd: configCwd, ToolNames: toolNames, Readmit: readmit}
	// Only a filtered load needs the list of what pig discovers; loading everything is pig's default.
	filtered := false
	if list, ok := cfg.Extensions.([]string); ok && !isolated {
		spec := parseExtensionsSpec(list, configCwd)
		filtered = !(spec.Wildcard && len(cfg.ExcludeExtensions) == 0)
	}
	if len(cfg.ExcludeExtensions) > 0 && !isolated && cfg.Extensions != false {
		filtered = true
	}
	var listed []string
	if filtered {
		var err error
		if listed, err = a.discoveredPaths(configCwd); err != nil {
			return scopePlan{}, scopeSpec{}, err
		}
	}
	in.Discovered = extensionsOf(origins, listed)
	if list, ok := cfg.Extensions.([]string); ok && !isolated {
		in.Loadable = map[string]loadedExtension{}
		for _, p := range parseExtensionsSpec(list, configCwd).Paths {
			if _, err := os.Stat(p); err == nil {
				in.Loadable[p] = loadedExtension{Path: p}
			}
		}
	}
	plan := planScope(in)
	return plan, plan.spec(in.Discovered), nil
}

// scopeSpec is the part of a childSpec a scopePlan decides.
type scopeSpec struct {
	NoExtensions   bool
	ExtensionPaths []string
	AllowTools     []string
	ExcludeTools   []string
}

// spec turns the plan into pig flags: a static allowlist without extensions; otherwise a denylist of every known tool
// that is out of scope, and, when the extensions are filtered, an explicit list of the ones to load.
func (p *scopePlan) spec(discovered []loadedExtension) scopeSpec {
	if p.NoExtensions {
		return scopeSpec{NoExtensions: true, AllowTools: p.Tools}
	}
	var s scopeSpec
	allowed := p.inScope()
	exclude := slices.Clone(p.ExcludeTools)
	for _, e := range discovered {
		for _, t := range e.Tools {
			if !allowed[t] && !slices.Contains(exclude, t) {
				exclude = append(exclude, t)
			}
		}
	}
	s.ExcludeTools = exclude
	if !p.LoadAll {
		s.NoExtensions = true
		for _, e := range p.Survivors {
			s.ExtensionPaths = append(s.ExtensionPaths, e.Path)
		}
		for _, name := range builtinExtensionNames {
			if p.loadsBuiltin(name) {
				s.ExtensionPaths = append(s.ExtensionPaths, "builtin:"+name)
			}
		}
		return s
	}
	// Everything pig discovers loads, and so do the paths the agent names: the original's loader is always given
	// additionalExtensionPaths (agent-runner.ts:727,751), and pig, like Pi, loads a path that is also discovered once.
	// A path that does not load is no survivor and is left out.
	for _, path := range p.AdditionalPaths {
		inSurvivors := slices.ContainsFunc(p.Survivors, func(e loadedExtension) bool { return filepath.Clean(e.Path) == filepath.Clean(path) })
		if inSurvivors && !slices.Contains(s.ExtensionPaths, path) {
			s.ExtensionPaths = append(s.ExtensionPaths, path)
		}
	}
	return s
}

// loadsBuiltin reports whether a filtered load keeps a built-in extension: by name, as for any other extension.
func (p *scopePlan) loadsBuiltin(name string) bool {
	if p.ExcludeNames[name] {
		return false
	}
	return p.KeepNames[name] || p.loadAllExt
}

// discoverCache remembers what `pig status --json` listed for a workspace until the next session start.
type discoverCache struct {
	mu    sync.Mutex
	cwd   string
	paths []string
	ok    bool
}

// discoveredPaths is the list of default-discovered extension paths for a workspace, asked of pig once per session.
func (a *app) discoveredPaths(cwd string) ([]string, error) {
	a.disc.mu.Lock()
	defer a.disc.mu.Unlock()
	if a.disc.ok && a.disc.cwd == cwd {
		return a.disc.paths, nil
	}
	paths, err := a.listExtensions(context.Background(), cwd)
	if err != nil {
		return nil, err
	}
	a.disc.cwd, a.disc.paths, a.disc.ok = cwd, paths, true
	return paths, nil
}

func (d *discoverCache) reset() {
	d.mu.Lock()
	d.ok = false
	d.mu.Unlock()
}
