package browser

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// jsLit encodes any Go string/value as a valid JavaScript literal using JSON
// encoding (never Go's %q, which emits \a and \U0001xxxx escapes JS misreads).
func jsLit(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// pageKitJS is a self-contained helper injected into an isolated world per frame
// (`Page.createIsolatedWorld`), so page scripts cannot spoof or hijack
// `__ompBrowserKit` or `_ariaRef` expandos.
//
// Supported selector engines:
//   - CSS (default)
//   - aria/<name> (accessible name or role/name match)
//   - text/<substring> (visible text match, deepest matching element)
//   - xpath/<expr>
//   - pierce/<css> (pierces open shadow DOM roots)
//   - label/<text> (controls by <label>, aria-label, aria-labelledby)
//   - placeholder/<text>
//   - testid/<id> (exact data-testid)
//   - alt/<text>
//   - title/<text>
//   - role/<role>[name="..." [exact]]
//   - aria-ref=eN / eN / @eN (refs from last ariaSnapshot)
//
// upstream: query-handlers.ts, aria/aria-snapshot.ts, queries.ts, interactions.ts
//
//go:embed assets/pagekit.js
var pageKitJS string

// buildAriaSnapshotEvaluator builds the JS snippet that loads the vendored
// Playwright ARIA snapshot bundle in the isolated world, caches the module in
// __ompBrowserKit so tab.ref("eN") resolves against it, and returns {snapshot, hrefs}.
// upstream: aria/aria-snapshot.ts
func buildAriaSnapshotEvaluator(selector string, depth *int, boxes, urls bool) string {
	selJS := "null"
	if selector != "" {
		selJS = jsLit(selector)
	}
	depthJS := "undefined"
	if depth != nil {
		depthJS = fmt.Sprintf("%d", *depth)
	}
	return fmt.Sprintf(`(function(){
var kit = globalThis.__ompBrowserKit;
var module = { exports: {} };
%s
if (kit) kit.setAriaModule(module.exports);
var sel = %s;
var root = sel ? (kit ? kit.queryFirst(sel) : document.querySelector(sel)) : null;
if (sel && !root) throw new Error("tab.ariaSnapshot: selector " + JSON.stringify(sel) + " matched no element");
var snapshot = module.exports.ariaSnapshot(root, { depth: %s, boxes: %t });
var hrefs = {};
if (%t) {
  for (var m of snapshot.matchAll(/\[ref=(e\d+)\]/g)) {
    var ref = m[1];
    var el = module.exports.resolveAriaRef(ref);
    if (el && el.tagName === "A" && el.href) hrefs[ref] = el.href;
  }
}
return { snapshot: snapshot, hrefs: hrefs };
})()`, ariaSnapshotBundle, selJS, depthJS, boxes, urls)
}

// postProcessAriaSnapshot implements omp's interactive, compact, and urls
// filtering on raw Playwright ARIA snapshot YAML.
// upstream: postProcessAriaSnapshot in snapshot-plus.ts:152
type ariaSnapshotOptions struct {
	Depth       *int
	Boxes       bool
	Interactive bool
	Compact     bool
	URLs        bool
	Diff        bool
}

var interactiveAriaRoles = map[string]bool{
	"button": true, "link": true, "textbox": true, "combobox": true, "listbox": true,
	"checkbox": true, "radio": true, "tab": true, "menuitem": true,
	"menuitemcheckbox": true, "menuitemradio": true, "slider": true, "switch": true,
	"option": true, "spinbutton": true, "searchbox": true,
}

var emptyStructuralRoles = map[string]bool{
	"generic": true, "none": true, "group": true,
}

type snapNode struct {
	text                string
	indent              int
	role                string
	name                string
	children            []*snapNode
	interactive         bool
	containsInteractive bool
}

func postProcessAriaSnapshot(snapshot string, opts ariaSnapshotOptions, hrefs map[string]string) string {
	if !opts.Interactive && !opts.Compact && !opts.URLs {
		return snapshot
	}
	var roots []*snapNode
	var stack []*snapNode
	for _, line := range strings.Split(snapshot, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		node := parseSnapLine(line)
		for len(stack) > 0 && stack[len(stack)-1].indent >= node.indent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			p := stack[len(stack)-1]
			p.children = append(p.children, node)
		} else {
			roots = append(roots, node)
		}
		stack = append(stack, node)
	}
	if len(roots) == 0 {
		return snapshot
	}
	var mark func(*snapNode) bool
	mark = func(n *snapNode) bool {
		for _, ch := range n.children {
			if mark(ch) {
				n.containsInteractive = true
			}
		}
		return n.containsInteractive
	}
	for _, r := range roots {
		mark(r)
	}
	var render func([]*snapNode, int) []string
	render = func(nodes []*snapNode, indent int) []string {
		var out []string
		for _, n := range nodes {
			if opts.Interactive && !n.containsInteractive {
				continue
			}
			promote := opts.Compact && emptyStructuralRoles[n.role] && n.name == "" && !n.containsInteractive
			if promote {
				out = append(out, render(n.children, indent)...)
				continue
			}
			trimmed := strings.TrimLeft(n.text, " \t")
			if opts.URLs && n.role == "link" && !strings.Contains(trimmed, "[href=") {
				if ref := extractRef(trimmed); ref != "" {
					if href := hrefs[ref]; href != "" {
						suffix := fmt.Sprintf("[href=%s]", jsLit(href))
						te := strings.TrimRight(trimmed, " \t")
						if strings.HasSuffix(te, ":") {
							trimmed = te[:len(te)-1] + " " + suffix + ":"
						} else {
							trimmed = trimmed + " " + suffix
						}
					}
				}
			}
			out = append(out, strings.Repeat(" ", indent)+trimmed)
			if len(n.children) > 0 {
				step := n.children[0].indent - n.indent
				if step < 2 {
					step = 2
				}
				out = append(out, render(n.children, indent+step)...)
			}
		}
		return out
	}
	return strings.Join(render(roots, roots[0].indent), "\n")
}

func parseSnapLine(line string) *snapNode {
	indent := 0
	for _, ch := range line {
		if ch == ' ' {
			indent++
		} else if ch == '\t' {
			indent += 2
		} else {
			break
		}
	}
	body := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(body, "-") {
		body = strings.TrimLeft(body[1:], " \t")
	}
	roleEnd := 0
	for roleEnd < len(body) {
		c := body[roleEnd]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			roleEnd++
		} else {
			break
		}
	}
	role := strings.ToLower(body[:roleEnd])
	rest := body[roleEnd:]
	for {
		a := strings.IndexByte(rest, '[')
		if a < 0 {
			break
		}
		b := strings.IndexByte(rest[a:], ']')
		if b < 0 {
			break
		}
		rest = rest[:a] + rest[a+b+1:]
	}
	name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), ":"))
	if name == `""` || name == `''` {
		name = ""
	}
	inter := interactiveAriaRoles[role]
	return &snapNode{
		text:                line,
		indent:              indent,
		role:                role,
		name:                name,
		interactive:         inter,
		containsInteractive: inter,
	}
}

func extractRef(line string) string {
	idx := strings.Index(line, "[ref=")
	if idx < 0 {
		return ""
	}
	rest := line[idx+5:]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// ariaSnapshotBaseline holds the last snapshot for diff mode.
// upstream: AriaSnapshotBaseline in snapshot-plus.ts:46
type ariaSnapshotBaseline struct {
	url      string
	revision int
	snapshot string
}

// diffAriaSnapshot returns full, unchanged, or unified delta against baseline.
// upstream: diffAriaSnapshot in snapshot-plus.ts
func diffAriaSnapshot(baselines map[string]*ariaSnapshotBaseline, key, pageURL, snapshot string) map[string]any {
	prev := baselines[key]
	if prev == nil || prev.url != pageURL {
		rev := 1
		if prev != nil {
			rev = prev.revision + 1
		}
		baselines[key] = &ariaSnapshotBaseline{url: pageURL, revision: rev, snapshot: snapshot}
		return map[string]any{"status": "full", "revision": rev, "snapshot": snapshot}
	}
	if prev.snapshot == snapshot {
		return map[string]any{"status": "unchanged", "revision": prev.revision}
	}
	baseRev := prev.revision
	rev := baseRev + 1
	delta := unifiedLineDiff(prev.snapshot, snapshot, 3)
	baselines[key] = &ariaSnapshotBaseline{url: pageURL, revision: rev, snapshot: snapshot}
	return map[string]any{
		"status":       "delta",
		"revision":     rev,
		"baseRevision": baseRev,
		"delta":        delta,
	}
}

func unifiedLineDiff(oldText, newText string, contextLines int) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	start := prefix - contextLines
	if start < 0 {
		start = 0
	}
	oldEnd := len(oldLines) - suffix
	newEnd := len(newLines) - suffix
	tailEnd := oldEnd + contextLines
	if tailEnd > len(oldLines) {
		tailEnd = len(oldLines)
	}
	var b strings.Builder
	for i := start; i < prefix; i++ {
		b.WriteString("  " + oldLines[i] + "\n")
	}
	for i := prefix; i < oldEnd; i++ {
		b.WriteString("- " + oldLines[i] + "\n")
	}
	for i := prefix; i < newEnd; i++ {
		b.WriteString("+ " + newLines[i] + "\n")
	}
	for i := oldEnd; i < tailEnd; i++ {
		b.WriteString("  " + oldLines[i] + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
