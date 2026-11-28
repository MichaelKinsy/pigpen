package surface

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/pig-hermes/sessions"
)

// ── Shared config.yaml / .env / formatting helpers ───────────────────────────

var (
	validMCPNameRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	upperSnakeKeyRe  = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)
	dangerousEnvKeys = map[string]bool{
		"PATH":             true,
		"PYTHONPATH":       true,
		"PYTHONSTARTUP":    true,
		"HERMES_HOME":      true,
		"HERMES_YOLO_MODE": true,
		"HOME":             true,
		"SHELL":            true,
		"USER":             true,
		"IFS":              true,
		"ENV":              true,
		"BASH_ENV":         true,
		"ZDOTDIR":          true,
		"NODE_OPTIONS":     true,
		"PERL5OPT":         true,
		"RUBYOPT":          true,
		"CLASSPATH":        true,
		"GOPATH":           true,
		"GOROOT":           true,
	}
	dangerousEnvPrefixes = []string{"LD_", "DYLD_", "GIT_CONFIG_"}
	secretKeyHints       = []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "AUTH", "PRIVATE"}
	validEffortLevels    = map[string]bool{
		"minimal": true,
		"low":     true,
		"medium":  true,
		"high":    true,
		"xhigh":   true,
		"max":     true,
		"ultra":   true,
		"none":    true,
		"auto":    true,
	}
)

type modelPreset struct {
	Provider     string
	DefaultModel string
	Description  string
}

var commonModelPresets = []modelPreset{
	{Provider: "github-copilot", DefaultModel: "claude-sonnet-4", Description: "GitHub Copilot subscription"},
	{Provider: "anthropic", DefaultModel: "claude-sonnet-4-5", Description: "Anthropic Messages API"},
	{Provider: "openai", DefaultModel: "gpt-4o", Description: "OpenAI API"},
	{Provider: "openrouter", DefaultModel: "anthropic/claude-sonnet-4", Description: "OpenRouter multi-provider gateway"},
	{Provider: "google", DefaultModel: "gemini-2.5-pro", Description: "Google Gemini API"},
	{Provider: "custom", DefaultModel: "local-model", Description: "Custom OpenAI-compatible endpoint"},
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".hermes-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func isUpperSnakeKey(k string) bool {
	return upperSnakeKeyRe.MatchString(k) && !strings.Contains(k, ".")
}

func isDangerousEnvKey(k string) bool {
	u := strings.ToUpper(strings.TrimSpace(k))
	if dangerousEnvKeys[u] {
		return true
	}
	for _, p := range dangerousEnvPrefixes {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

func isSecretKey(k string) bool {
	u := strings.ToUpper(k)
	for _, h := range secretKeyHints {
		if strings.Contains(u, h) {
			return true
		}
	}
	return false
}

func redactSecret(val string) string {
	if val == "" {
		return ""
	}
	if len(val) <= 8 {
		return "****"
	}
	return val[:3] + "..." + val[len(val)-3:]
}

func humanBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024.0)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024.0*1024.0))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(1024.0*1024.0*1024.0))
	}
}

// ── Ordered YAML tree (pure stdlib, preserves unrelated keys) ────────────────

type yamlKind int

const (
	yamlScalar yamlKind = iota
	yamlMap
	yamlList
)

type yamlEntry struct {
	Key  string
	Node *yamlNode
}

type yamlNode struct {
	Kind     yamlKind
	Scalar   string
	Quoted   bool
	EmptyMap bool
	Items    []string
	Entries  []*yamlEntry
}

func newYAMLMap() *yamlNode {
	return &yamlNode{Kind: yamlMap}
}

func (n *yamlNode) getChild(key string) *yamlNode {
	if n == nil || n.Kind != yamlMap {
		return nil
	}
	for _, e := range n.Entries {
		if e.Key == key {
			return e.Node
		}
	}
	return nil
}

func (n *yamlNode) setChild(key string, child *yamlNode) {
	if n.Kind != yamlMap {
		n.Kind = yamlMap
		n.Scalar = ""
		n.Items = nil
	}
	for _, e := range n.Entries {
		if e.Key == key {
			e.Node = child
			return
		}
	}
	n.Entries = append(n.Entries, &yamlEntry{Key: key, Node: child})
}

func (n *yamlNode) deleteChild(key string) bool {
	if n == nil || n.Kind != yamlMap {
		return false
	}
	for i, e := range n.Entries {
		if e.Key == key {
			n.Entries = append(n.Entries[:i], n.Entries[i+1:]...)
			return true
		}
	}
	return false
}

func (n *yamlNode) getPath(dotted string) (*yamlNode, bool) {
	parts := strings.Split(dotted, ".")
	cur := n
	for _, p := range parts {
		if cur == nil || cur.Kind != yamlMap {
			return nil, false
		}
		cur = cur.getChild(p)
	}
	if cur == nil {
		return nil, false
	}
	return cur, true
}

func (n *yamlNode) setScalarPath(dotted, val string) {
	parts := strings.Split(dotted, ".")
	cur := n
	for i := 0; i < len(parts)-1; i++ {
		next := cur.getChild(parts[i])
		if next == nil || next.Kind != yamlMap {
			next = newYAMLMap()
			cur.setChild(parts[i], next)
		}
		cur = next
	}
	cur.setChild(parts[len(parts)-1], &yamlNode{Kind: yamlScalar, Scalar: val})
}

func (n *yamlNode) setListPath(dotted string, items []string) {
	parts := strings.Split(dotted, ".")
	cur := n
	for i := 0; i < len(parts)-1; i++ {
		next := cur.getChild(parts[i])
		if next == nil || next.Kind != yamlMap {
			next = newYAMLMap()
			cur.setChild(parts[i], next)
		}
		cur = next
	}
	cp := make([]string, len(items))
	copy(cp, items)
	cur.setChild(parts[len(parts)-1], &yamlNode{Kind: yamlList, Items: cp})
}

func (n *yamlNode) deletePath(dotted string) bool {
	parts := strings.Split(dotted, ".")
	return deletePathRec(n, parts)
}

func deletePathRec(cur *yamlNode, parts []string) bool {
	if cur == nil || cur.Kind != yamlMap || len(parts) == 0 {
		return false
	}
	if len(parts) == 1 {
		return cur.deleteChild(parts[0])
	}
	child := cur.getChild(parts[0])
	if child == nil || child.Kind != yamlMap {
		return false
	}
	removed := deletePathRec(child, parts[1:])
	if removed && len(child.Entries) == 0 {
		cur.deleteChild(parts[0])
	}
	return removed
}

func loadYAMLDoc(path string) (*yamlNode, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newYAMLMap(), nil
		}
		return nil, err
	}
	return parseYAMLDoc(string(raw)), nil
}

func preserveFileMode(path string, defaultPerm os.FileMode) os.FileMode {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return info.Mode().Perm()
	}
	return defaultPerm
}

func saveYAMLDoc(path string, root *yamlNode) error {
	var b strings.Builder
	renderYAMLNode(&b, root, 0)
	return atomicWriteFile(path, []byte(b.String()), preserveFileMode(path, 0o600))
}

func parseYAMLDoc(content string) *yamlNode {
	root := newYAMLMap()
	type frame struct {
		indent int
		node   *yamlNode
	}
	stack := []frame{{indent: -1, node: root}}

	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		raw := sc.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if strings.HasPrefix(trimmed, "- ") {
			item := unquoteSimpleYAML(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			// Allow indentless YAML sequences (- x at the parent key's own indent).
			for len(stack) > 1 && stack[len(stack)-1].indent > indent {
				stack = stack[:len(stack)-1]
			}
			parent := stack[len(stack)-1].node
			if parent == root {
				continue
			}
			if parent.Kind != yamlList {
				parent.Kind = yamlList
				parent.Entries = nil
				parent.Scalar = ""
				parent.EmptyMap = false
			}
			parent.Items = append(parent.Items, item)
			continue
		}

		k, v, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		for len(stack) > 1 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1].node
		if v == "" {
			child := parent.getChild(k)
			if child == nil || child.Kind == yamlScalar {
				child = newYAMLMap()
				parent.setChild(k, child)
			}
			stack = append(stack, frame{indent: indent, node: child})
			continue
		}

		v = stripSimpleYAMLComment(v)
		if v == "{}" {
			parent.setChild(k, &yamlNode{Kind: yamlMap, EmptyMap: true})
			continue
		}
		if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
			items := parseYAMLFlowList(v)
			parent.setChild(k, &yamlNode{Kind: yamlList, Items: items})
			continue
		}
		wasQuoted := isQuotedSimpleYAML(v)
		parent.setChild(k, &yamlNode{Kind: yamlScalar, Scalar: unquoteSimpleYAML(v), Quoted: wasQuoted})
	}
	return root
}

func isQuotedSimpleYAML(v string) bool {
	return len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\''))
}

func stripSimpleYAMLComment(v string) string {
	if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
		return v
	}
	if idx := strings.Index(v, " #"); idx >= 0 {
		return strings.TrimSpace(v[:idx])
	}
	return v
}

func unquoteSimpleYAML(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		if unq, err := strconv.Unquote(v); err == nil {
			return unq
		}
		return v[1 : len(v)-1]
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}

func parseYAMLFlowList(v string) []string {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "["), "]"))
	if inner == "" {
		return nil
	}
	parts := strings.Split(inner, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if item := unquoteSimpleYAML(strings.TrimSpace(p)); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func isYAMLBoolNullOrNumeric(v string) bool {
	switch strings.ToLower(v) {
	case "true", "false", "yes", "no", "on", "off", "null", "~", ".inf", "-.inf", "+.inf", ".nan":
		return true
	}
	if _, err := strconv.ParseInt(v, 0, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return true
	}
	return false
}

func formatYAMLScalar(v string, forceQuote bool) string {
	if v == "" {
		return `""`
	}
	if forceQuote && isYAMLBoolNullOrNumeric(v) {
		return strconv.Quote(v)
	}
	if strings.ContainsAny(v, "\n\r\t") || strings.Contains(v, " #") || strings.Contains(v, ": ") ||
		strings.HasSuffix(v, ":") ||
		strings.HasPrefix(v, " ") || strings.HasSuffix(v, " ") ||
		strings.ContainsRune("@*&!|>%#`?-[{,\"'", rune(v[0])) {
		return strconv.Quote(v)
	}
	return v
}

func renderYAMLNode(b *strings.Builder, n *yamlNode, indent int) {
	if n == nil || n.Kind != yamlMap {
		return
	}
	pad := strings.Repeat(" ", indent)
	for _, e := range n.Entries {
		switch e.Node.Kind {
		case yamlMap:
			if len(e.Node.Entries) == 0 {
				if e.Node.EmptyMap {
					fmt.Fprintf(b, "%s%s: {}\n", pad, e.Key)
				}
				continue
			}
			fmt.Fprintf(b, "%s%s:\n", pad, e.Key)
			renderYAMLNode(b, e.Node, indent+2)
		case yamlList:
			if len(e.Node.Items) == 0 {
				fmt.Fprintf(b, "%s%s: []\n", pad, e.Key)
				continue
			}
			fmt.Fprintf(b, "%s%s:\n", pad, e.Key)
			for _, item := range e.Node.Items {
				fmt.Fprintf(b, "%s  - %s\n", pad, formatYAMLScalar(item, false))
			}
		case yamlScalar:
			fmt.Fprintf(b, "%s%s: %s\n", pad, e.Key, formatYAMLScalar(e.Node.Scalar, e.Node.Quoted))
		}
	}
}

func yamlNodeToAny(n *yamlNode) any {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yamlScalar:
		return n.Scalar
	case yamlList:
		out := make([]string, len(n.Items))
		copy(out, n.Items)
		return out
	case yamlMap:
		m := make(map[string]any, len(n.Entries))
		for _, e := range n.Entries {
			m[e.Key] = yamlNodeToAny(e.Node)
		}
		return m
	default:
		return nil
	}
}

// ── .env ordered read/write helpers (0600 perms) ─────────────────────────────

type dotEnvLine struct {
	raw      string
	key      string
	val      string
	isKV     bool
	modified bool
}

func readDotEnvLines(path string) ([]dotEnvLine, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var lines []dotEnvLine
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		text := sc.Text()
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			lines = append(lines, dotEnvLine{raw: text})
			continue
		}
		body := strings.TrimPrefix(trimmed, "export ")
		k, v, ok := strings.Cut(body, "=")
		if !ok {
			lines = append(lines, dotEnvLine{raw: text})
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = unquoteSimpleYAML(v)
		lines = append(lines, dotEnvLine{raw: text, key: k, val: v, isKV: true})
	}
	return lines, nil
}

func formatDotEnvLine(key, val string) string {
	if strings.ContainsAny(val, " \t#\"'\n\r") {
		return fmt.Sprintf("%s=%s", key, strconv.Quote(val))
	}
	return fmt.Sprintf("%s=%s", key, val)
}

func writeDotEnvLines(path string, lines []dotEnvLine) error {
	var b strings.Builder
	for _, l := range lines {
		if l.isKV && l.modified {
			b.WriteString(formatDotEnvLine(l.key, l.val))
		} else {
			b.WriteString(l.raw)
		}
		b.WriteByte('\n')
	}
	return atomicWriteFile(path, []byte(b.String()), 0o600)
}

func setDotEnvKey(path, key, value string) error {
	lines, err := readDotEnvLines(path)
	if err != nil {
		return err
	}
	updated := false
	for i := range lines {
		if lines[i].isKV && lines[i].key == key {
			lines[i].val = value
			lines[i].modified = true
			updated = true
			break
		}
	}
	if !updated {
		lines = append(lines, dotEnvLine{key: key, val: value, isKV: true, modified: true})
	}
	return writeDotEnvLines(path, lines)
}

func unsetDotEnvKey(path, key string) (bool, error) {
	lines, err := readDotEnvLines(path)
	if err != nil {
		return false, err
	}
	out := make([]dotEnvLine, 0, len(lines))
	removed := false
	for _, l := range lines {
		if l.isKV && l.key == key {
			removed = true
			continue
		}
		out = append(out, l)
	}
	if !removed {
		return false, nil
	}
	if err := writeDotEnvLines(path, out); err != nil {
		return false, err
	}
	return true, nil
}

func ensureDotEnvFile(path string) error {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		_ = os.Chmod(path, 0o600)
		return nil
	}
	return atomicWriteFile(path, []byte("# Hermes environment variables\n"), 0o600)
}

// ── 1. setup subcommand ──────────────────────────────────────────────────────

func runSetupSubcommand(env clihook.Env, args []string) int {
	out := envOut(env)
	errOut := envErr(env)
	p := paths.ResolveProfilePaths("")

	var (
		section        string
		nonInteractive bool
		quick          bool
		reset          bool
		flagProvider   string
		flagModel      string
		flagSkin       string
		flagTelemetry  string
	)

	validSections := map[string]bool{
		"model": true, "tts": true, "terminal": true,
		"tools": true, "telemetry": true, "agent": true,
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--non-interactive":
			nonInteractive = true
		case "--quick":
			quick = true
		case "--reset":
			reset = true
		case "--provider":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "setup: --provider requires a value")
				return 1
			}
			i++
			flagProvider = strings.TrimSpace(args[i])
		case "--model":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "setup: --model requires a value")
				return 1
			}
			i++
			flagModel = strings.TrimSpace(args[i])
		case "--skin":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "setup: --skin requires a value")
				return 1
			}
			i++
			flagSkin = strings.TrimSpace(args[i])
		case "--telemetry":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "setup: --telemetry requires on|off")
				return 1
			}
			i++
			flagTelemetry = strings.ToLower(strings.TrimSpace(args[i]))
			if flagTelemetry != "on" && flagTelemetry != "off" {
				fmt.Fprintf(errOut, "setup: --telemetry must be 'on' or 'off', got %q\n", args[i])
				return 1
			}
		case "-h", "--help", "help":
			fmt.Fprintln(out, "Usage: setup [model|tts|terminal|tools|telemetry|agent] [--non-interactive] [--quick] [--reset] [--provider <p>] [--model <m>] [--skin <s>] [--telemetry <on|off>]")
			return 0
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(errOut, "setup: unknown flag %q\n", a)
				return 1
			}
			if section != "" {
				fmt.Fprintf(errOut, "setup: unexpected argument %q\n", a)
				return 1
			}
			if !validSections[a] {
				fmt.Fprintf(errOut, "setup: unknown section %q (valid: model, tts, terminal, tools, telemetry, agent)\n", a)
				return 1
			}
			section = a
		}
	}

	if err := os.MkdirAll(p.Home, 0o755); err != nil {
		fmt.Fprintf(errOut, "setup: failed to create %s: %v\n", p.Home, err)
		return 1
	}

	// Backup existing config.yaml before any modifications or --reset.
	var backupPath string
	if raw, err := os.ReadFile(p.ConfigFile); err == nil {
		ts := time.Now().UTC().Format("20060102-150405")
		backupPath = filepath.Join(p.Home, "backups", "config.yaml.pre-setup."+ts)
		if err := atomicWriteFile(backupPath, raw, preserveFileMode(p.ConfigFile, 0o600)); err != nil {
			fmt.Fprintf(errOut, "setup: failed to create backup %s: %v\n", backupPath, err)
			return 1
		}
	}

	var doc *yamlNode
	if reset {
		doc = newYAMLMap()
	} else {
		var err error
		doc, err = loadYAMLDoc(p.ConfigFile)
		if err != nil {
			fmt.Fprintf(errOut, "setup: failed to read %s: %v\n", p.ConfigFile, err)
			return 1
		}
	}

	getCur := func(dotted, fallback string) string {
		if n, ok := doc.getPath(dotted); ok && n.Kind == yamlScalar && strings.TrimSpace(n.Scalar) != "" {
			return n.Scalar
		}
		return fallback
	}

	var reader *bufio.Reader
	if !nonInteractive && env.Stdin != nil {
		reader = bufio.NewReader(env.Stdin)
	}
	prompt := func(label, defVal string) string {
		if nonInteractive || reader == nil {
			return defVal
		}
		fmt.Fprintf(out, "%s [%s]: ", label, defVal)
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return defVal
		}
		ans := strings.TrimSpace(line)
		if ans == "" {
			return defVal
		}
		return ans
	}

	wantSection := func(name string) bool {
		if section != "" {
			return section == name
		}
		if quick {
			return name == "model" || name == "telemetry" || name == "display"
		}
		return true
	}

	// Model section
	if wantSection("model") {
		curProvider := getCur("model.provider", "github-copilot")
		curModel := getCur("model.default", "claude-sonnet-4")
		prov := curProvider
		if flagProvider != "" {
			prov = flagProvider
		} else {
			prov = prompt("Model provider", curProvider)
		}
		mod := curModel
		if flagModel != "" {
			mod = flagModel
		} else {
			mod = prompt("Default model", curModel)
		}
		doc.setScalarPath("model.provider", prov)
		doc.setScalarPath("model.default", mod)
	}

	// Display / skin
	if section == "" || flagSkin != "" {
		curSkin := getCur("display.skin", "default")
		skin := curSkin
		if flagSkin != "" {
			skin = flagSkin
		} else if !quick || section == "" {
			skin = prompt("Display skin", curSkin)
		}
		doc.setScalarPath("display.skin", skin)
	}

	// Terminal section
	if wantSection("terminal") {
		curBackend := getCur("terminal.backend", "local")
		backend := prompt("Terminal backend (local|docker|ssh)", curBackend)
		doc.setScalarPath("terminal.backend", backend)
	}

	// Agent section
	if wantSection("agent") {
		curTurns := getCur("agent.max_turns", "90")
		turns := prompt("Agent max turns", curTurns)
		if n, err := strconv.Atoi(turns); err == nil && n > 0 {
			doc.setScalarPath("agent.max_turns", strconv.Itoa(n))
		} else {
			doc.setScalarPath("agent.max_turns", curTurns)
		}
		curComp := getCur("compression.enabled", "true")
		comp := strings.ToLower(prompt("Enable context compression (true|false)", curComp))
		if comp == "false" || comp == "no" || comp == "off" || comp == "0" {
			doc.setScalarPath("compression.enabled", "false")
		} else {
			doc.setScalarPath("compression.enabled", "true")
		}
	}

	// TTS section (explicit section only)
	if section == "tts" {
		curTTS := getCur("tts.provider", "edge")
		ttsProv := prompt("TTS provider (edge|openai|elevenlabs)", curTTS)
		doc.setScalarPath("tts.provider", ttsProv)
		if ttsProv == "edge" {
			curVoice := getCur("tts.edge.voice", "en-US-AriaNeural")
			doc.setScalarPath("tts.edge.voice", prompt("Edge TTS voice", curVoice))
		}
	}

	// Tools section (explicit section only)
	if section == "tools" {
		curToolset := getCur("toolset", "hermes-cli")
		doc.setScalarPath("toolset", prompt("Default toolset", curToolset))
	}

	// Telemetry section
	if wantSection("telemetry") || flagTelemetry != "" {
		curTel := getCur("telemetry.shared_metrics.enabled", "false")
		telOn := curTel == "true"
		if flagTelemetry != "" {
			telOn = flagTelemetry == "on"
		} else {
			defStr := "off"
			if telOn {
				defStr = "on"
			}
			ans := strings.ToLower(prompt("Shared telemetry metrics (on|off)", defStr))
			telOn = ans == "on" || ans == "true" || ans == "yes" || ans == "1"
		}
		val := "false"
		if telOn {
			val = "true"
		}
		doc.setScalarPath("telemetry.shared_metrics.enabled", val)
		doc.setScalarPath("telemetry.shared_metrics.send", val)
	}

	if err := saveYAMLDoc(p.ConfigFile, doc); err != nil {
		fmt.Fprintf(errOut, "setup: failed to write %s: %v\n", p.ConfigFile, err)
		return 1
	}
	if err := ensureDotEnvFile(p.EnvFile); err != nil {
		fmt.Fprintf(errOut, "setup: failed to initialize %s: %v\n", p.EnvFile, err)
		return 1
	}

	fmt.Fprintf(out, "Hermes setup complete (profile: %s)\n", p.ProfileName)
	fmt.Fprintf(out, "  Config:    %s\n", p.ConfigFile)
	fmt.Fprintf(out, "  Env:       %s\n", p.EnvFile)
	fmt.Fprintf(out, "  Model:     %s / %s\n", getCur("model.provider", "(default)"), getCur("model.default", "(default)"))
	fmt.Fprintf(out, "  Skin:      %s\n", getCur("display.skin", "default"))
	fmt.Fprintf(out, "  Terminal:  %s\n", getCur("terminal.backend", "local"))
	fmt.Fprintf(out, "  Telemetry: %s\n", getCur("telemetry.shared_metrics.enabled", "false"))
	if backupPath != "" {
		fmt.Fprintf(out, "  Backup:    %s\n", backupPath)
		fmt.Fprintf(out, "  Restore:   cp %q %q\n", backupPath, p.ConfigFile)
	}
	return 0
}

// ── 2. config subcommand ─────────────────────────────────────────────────────

func runConfigSubcommand(env clihook.Env, args []string) int {
	out := envOut(env)
	errOut := envErr(env)
	p := paths.ResolveProfilePaths("")

	sub := "show"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		rest = args[1:]
	}

	switch sub {
	case "show":
		return runConfigShow(p, out, errOut)
	case "get":
		return runConfigGet(p, rest, out, errOut)
	case "set":
		return runConfigSet(p, rest, out, errOut)
	case "unset":
		return runConfigUnset(p, rest, out, errOut)
	case "path":
		fmt.Fprintln(out, p.ConfigFile)
		return 0
	case "env-path":
		fmt.Fprintln(out, p.EnvFile)
		return 0
	case "check":
		return runConfigCheck(p, out, errOut)
	default:
		fmt.Fprintf(errOut, "config: unknown subcommand %q (valid: show, get, set, unset, path, env-path, check)\n", sub)
		return 1
	}
}

func runConfigShow(p paths.ProfilePaths, out, errOut io.Writer) int {
	cfg, err := config.Load(p.ConfigFile, p.EnvFile)
	if err != nil {
		fmt.Fprintf(errOut, "config show: %v\n", err)
		return 1
	}
	doc, _ := loadYAMLDoc(p.ConfigFile)
	envLines, _ := readDotEnvLines(p.EnvFile)

	getStr := func(dotted, fallback string) string {
		if doc != nil {
			if n, ok := doc.getPath(dotted); ok && n.Kind == yamlScalar && n.Scalar != "" {
				return n.Scalar
			}
		}
		return fallback
	}

	fmt.Fprintf(out, "Hermes Configuration (profile: %s)\n", p.ProfileName)
	fmt.Fprintf(out, "\nModel\n")
	fmt.Fprintf(out, "  provider: %s\n", emptyDash(cfg.Provider))
	fmt.Fprintf(out, "  default:  %s\n", emptyDash(cfg.Model))
	if cfg.BaseURL != "" {
		fmt.Fprintf(out, "  base_url: %s\n", cfg.BaseURL)
	}
	if eff := getStr("agent.reasoning_effort", ""); eff != "" {
		fmt.Fprintf(out, "  effort:   %s\n", eff)
	}

	fmt.Fprintf(out, "\nDisplay\n")
	fmt.Fprintf(out, "  skin:     %s\n", cfg.Skin)

	fmt.Fprintf(out, "\nTerminal\n")
	fmt.Fprintf(out, "  backend:  %s\n", getStr("terminal.backend", "local"))

	fmt.Fprintf(out, "\nMemory\n")
	fmt.Fprintf(out, "  memory_char_limit: %d\n", cfg.MemoryLimit)
	fmt.Fprintf(out, "  user_char_limit:   %d\n", cfg.UserLimit)

	fmt.Fprintf(out, "\nMCP Servers\n")
	if len(cfg.MCPServers) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else {
		names := make([]string, 0, len(cfg.MCPServers))
		for name := range cfg.MCPServers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			srv := cfg.MCPServers[name]
			target := srv.URL
			if target == "" {
				target = srv.Command
			}
			status := "enabled"
			if !srv.Enabled {
				status = "disabled"
			}
			fmt.Fprintf(out, "  %s: %s (%s)\n", name, target, status)
		}
	}

	fmt.Fprintf(out, "\nEnv Keys (%s)\n", p.EnvFile)
	envCount := 0
	for _, l := range envLines {
		if !l.isKV {
			continue
		}
		envCount++
		val := l.val
		if isSecretKey(l.key) {
			val = redactSecret(val)
		}
		fmt.Fprintf(out, "  %s=%s\n", l.key, val)
	}
	if envCount == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	return 0
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unset)"
	}
	return s
}

func runConfigGet(p paths.ProfilePaths, args []string, out, errOut io.Writer) int {
	var (
		key    string
		asJSON bool
		rawOut bool
	)
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "--raw":
			rawOut = true
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(errOut, "config get: unknown flag %q\n", a)
				return 1
			}
			if key == "" {
				key = a
			}
		}
	}
	if key == "" {
		fmt.Fprintln(errOut, "usage: config get <key> [--json] [--raw]")
		return 1
	}

	if isUpperSnakeKey(key) {
		lines, err := readDotEnvLines(p.EnvFile)
		if err != nil {
			fmt.Fprintf(errOut, "config get: %v\n", err)
			return 1
		}
		for _, l := range lines {
			if l.isKV && l.key == key {
				val := l.val
				if !rawOut && isSecretKey(key) {
					val = redactSecret(val)
				}
				if asJSON {
					b, _ := json.Marshal(val)
					fmt.Fprintln(out, string(b))
				} else {
					fmt.Fprintln(out, val)
				}
				return 0
			}
		}
		fmt.Fprintf(errOut, "config get: %s is not set\n", key)
		return 1
	}

	doc, err := loadYAMLDoc(p.ConfigFile)
	if err != nil {
		fmt.Fprintf(errOut, "config get: %v\n", err)
		return 1
	}
	node, ok := doc.getPath(key)
	if !ok {
		fmt.Fprintf(errOut, "config get: %s is not set\n", key)
		return 1
	}

	if asJSON {
		v := yamlNodeToAny(node)
		if s, ok := v.(string); ok && !rawOut && isSecretKey(key) {
			v = redactSecret(s)
		}
		b, _ := json.Marshal(v)
		fmt.Fprintln(out, string(b))
		return 0
	}

	switch node.Kind {
	case yamlScalar:
		val := node.Scalar
		if !rawOut && isSecretKey(key) {
			val = redactSecret(val)
		}
		fmt.Fprintln(out, val)
	case yamlList:
		for _, item := range node.Items {
			fmt.Fprintln(out, item)
		}
	case yamlMap:
		var b strings.Builder
		renderYAMLNode(&b, node, 0)
		fmt.Fprint(out, b.String())
	}
	return 0
}

func runConfigSet(p paths.ProfilePaths, args []string, out, errOut io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(errOut, "usage: config set <key> <value>")
		return 1
	}
	key := strings.TrimSpace(args[0])
	val := strings.Join(args[1:], " ")
	if key == "" {
		fmt.Fprintln(errOut, "config set: key cannot be empty")
		return 1
	}

	if isUpperSnakeKey(key) {
		if isDangerousEnvKey(key) {
			fmt.Fprintf(errOut, "config set: refusing to set dangerous environment variable %q in .env\n", key)
			return 1
		}
		if err := setDotEnvKey(p.EnvFile, key, val); err != nil {
			fmt.Fprintf(errOut, "config set: failed to write %s: %v\n", p.EnvFile, err)
			return 1
		}
		display := val
		if isSecretKey(key) {
			display = redactSecret(val)
		}
		fmt.Fprintf(out, "✓ Set %s=%s in %s\n", key, display, p.EnvFile)
		return 0
	}

	doc, err := loadYAMLDoc(p.ConfigFile)
	if err != nil {
		fmt.Fprintf(errOut, "config set: %v\n", err)
		return 1
	}
	doc.setScalarPath(key, val)
	if err := saveYAMLDoc(p.ConfigFile, doc); err != nil {
		fmt.Fprintf(errOut, "config set: failed to write %s: %v\n", p.ConfigFile, err)
		return 1
	}
	fmt.Fprintf(out, "✓ Set %s = %s in %s\n", key, val, p.ConfigFile)
	return 0
}

func runConfigUnset(p paths.ProfilePaths, args []string, out, errOut io.Writer) int {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(errOut, "usage: config unset <key>")
		return 1
	}
	key := strings.TrimSpace(args[0])

	if isUpperSnakeKey(key) {
		removed, err := unsetDotEnvKey(p.EnvFile, key)
		if err != nil {
			fmt.Fprintf(errOut, "config unset: %v\n", err)
			return 1
		}
		if !removed {
			fmt.Fprintf(errOut, "config unset: %s is not set\n", key)
			return 1
		}
		fmt.Fprintf(out, "✓ Unset %s from %s\n", key, p.EnvFile)
		return 0
	}

	doc, err := loadYAMLDoc(p.ConfigFile)
	if err != nil {
		fmt.Fprintf(errOut, "config unset: %v\n", err)
		return 1
	}
	if !doc.deletePath(key) {
		fmt.Fprintf(errOut, "config unset: %s is not set\n", key)
		return 1
	}
	if err := saveYAMLDoc(p.ConfigFile, doc); err != nil {
		fmt.Fprintf(errOut, "config unset: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "✓ Unset %s from %s\n", key, p.ConfigFile)
	return 0
}

func runConfigCheck(p paths.ProfilePaths, out, errOut io.Writer) int {
	cfg, err := config.Load(p.ConfigFile, p.EnvFile)
	if err != nil {
		fmt.Fprintf(errOut, "config check: %v\n", err)
		return 1
	}
	doc, _ := loadYAMLDoc(p.ConfigFile)

	mark := func(ok bool) string {
		if ok {
			return "✓"
		}
		return "○"
	}

	_, cfgErr := os.Stat(p.ConfigFile)
	_, envErr := os.Stat(p.EnvFile)

	fmt.Fprintf(out, "Hermes Config Check (profile: %s)\n", p.ProfileName)
	fmt.Fprintf(out, "  %s config.yaml: %s\n", mark(cfgErr == nil), p.ConfigFile)
	fmt.Fprintf(out, "  %s .env:        %s\n", mark(envErr == nil), p.EnvFile)
	fmt.Fprintf(out, "  ✓ profile:     %s\n", p.ProfileName)
	fmt.Fprintf(out, "  %s model:       %s (provider: %s)\n", mark(cfg.Model != ""), emptyDash(cfg.Model), emptyDash(cfg.Provider))
	fmt.Fprintf(out, "  ✓ skin:        %s\n", cfg.Skin)
	fmt.Fprintf(out, "  ✓ toolset:     %s\n", cfg.Toolset)

	optionalKeys := []string{"model.provider", "model.default", "terminal.backend", "agent.max_turns", "display.skin"}
	var missing []string
	for _, k := range optionalKeys {
		if doc == nil {
			missing = append(missing, k)
			continue
		}
		if _, ok := doc.getPath(k); !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		fmt.Fprintln(out, "  ✓ optional keys: all core keys configured")
	} else {
		fmt.Fprintf(out, "  ○ missing optional keys: %s\n", strings.Join(missing, ", "))
	}
	return 0
}

// ── 3. model subcommand ──────────────────────────────────────────────────────

func runModelSubcommand(env clihook.Env, args []string) int {
	out := envOut(env)
	errOut := envErr(env)
	p := paths.ResolveProfilePaths("")

	var (
		setVal    string
		effortVal string
		listOnly  bool
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--list":
			listOnly = true
		case "--set":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "model: --set requires a value")
				return 1
			}
			i++
			setVal = strings.TrimSpace(args[i])
		case "--effort":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "model: --effort requires a level")
				return 1
			}
			i++
			effortVal = strings.ToLower(strings.TrimSpace(args[i]))
			if !validEffortLevels[effortVal] {
				fmt.Fprintf(errOut, "model: invalid reasoning effort %q\n", effortVal)
				return 1
			}
		case "-h", "--help":
			fmt.Fprintln(out, "Usage: model [--set <provider/model>] [--effort <level>] [--list]")
			return 0
		default:
			fmt.Fprintf(errOut, "model: unknown argument %q\n", args[i])
			return 1
		}
	}

	doc, err := loadYAMLDoc(p.ConfigFile)
	if err != nil {
		fmt.Fprintf(errOut, "model: failed to read %s: %v\n", p.ConfigFile, err)
		return 1
	}

	getScalar := func(k string) string {
		if n, ok := doc.getPath(k); ok && n.Kind == yamlScalar {
			return n.Scalar
		}
		return ""
	}

	if setVal != "" || effortVal != "" {
		if setVal != "" {
			if prov, mod, ok := strings.Cut(setVal, "/"); ok && prov != "" && mod != "" {
				doc.setScalarPath("model.provider", prov)
				doc.setScalarPath("model.default", mod)
			} else {
				doc.setScalarPath("model.default", setVal)
			}
		}
		if effortVal != "" {
			doc.setScalarPath("agent.reasoning_effort", effortVal)
		}
		if err := saveYAMLDoc(p.ConfigFile, doc); err != nil {
			fmt.Fprintf(errOut, "model: failed to save %s: %v\n", p.ConfigFile, err)
			return 1
		}
		fmt.Fprintf(out, "✓ Updated model configuration:\n")
		fmt.Fprintf(out, "  provider: %s\n", emptyDash(getScalar("model.provider")))
		fmt.Fprintf(out, "  default:  %s\n", emptyDash(getScalar("model.default")))
		if eff := getScalar("agent.reasoning_effort"); eff != "" {
			fmt.Fprintf(out, "  effort:   %s\n", eff)
		}
		return 0
	}

	fmt.Fprintln(out, "Current Model Configuration")
	fmt.Fprintf(out, "  provider: %s\n", emptyDash(getScalar("model.provider")))
	fmt.Fprintf(out, "  default:  %s\n", emptyDash(getScalar("model.default")))
	fmt.Fprintf(out, "  effort:   %s\n", emptyDash(getScalar("agent.reasoning_effort")))
	fmt.Fprintln(out, "\nProvider Presets")
	for i, pr := range commonModelPresets {
		fmt.Fprintf(out, "  %d. %-16s %-26s %s\n", i+1, pr.Provider, pr.DefaultModel, pr.Description)
	}

	if listOnly || env.Stdin == nil {
		return 0
	}

	// Interactive selection on stdin when provided.
	fmt.Fprint(out, "\nSelect preset [1-6] or enter provider/model (blank to keep): ")
	reader := bufio.NewReader(env.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return 0
	}
	choice := strings.TrimSpace(line)
	if choice == "" {
		return 0
	}
	if idx, err := strconv.Atoi(choice); err == nil && idx >= 1 && idx <= len(commonModelPresets) {
		pr := commonModelPresets[idx-1]
		doc.setScalarPath("model.provider", pr.Provider)
		doc.setScalarPath("model.default", pr.DefaultModel)
	} else if prov, mod, ok := strings.Cut(choice, "/"); ok && prov != "" && mod != "" {
		doc.setScalarPath("model.provider", prov)
		doc.setScalarPath("model.default", mod)
	} else {
		doc.setScalarPath("model.default", choice)
	}
	if err := saveYAMLDoc(p.ConfigFile, doc); err != nil {
		fmt.Fprintf(errOut, "model: failed to save %s: %v\n", p.ConfigFile, err)
		return 1
	}
	fmt.Fprintf(out, "✓ Updated model: %s / %s\n", emptyDash(getScalar("model.provider")), emptyDash(getScalar("model.default")))
	return 0
}

// ── 4. mcp subcommand ────────────────────────────────────────────────────────

func runMCPSubcommand(env clihook.Env, args []string) int {
	out := envOut(env)
	errOut := envErr(env)
	p := paths.ResolveProfilePaths("")

	action := "list"
	rest := args
	if len(args) > 0 {
		action = args[0]
		rest = args[1:]
	}

	switch action {
	case "list", "ls":
		return runMCPList(p, out, errOut)
	case "add":
		return runMCPAdd(p, rest, out, errOut)
	case "remove", "rm":
		return runMCPRemove(p, rest, out, errOut)
	case "test":
		return runMCPTest(p, rest, out, errOut)
	default:
		fmt.Fprintf(errOut, "mcp: unknown action %q (valid: list, add, remove, test)\n", action)
		return 1
	}
}

func runMCPList(p paths.ProfilePaths, out, errOut io.Writer) int {
	cfg, err := config.Load(p.ConfigFile, p.EnvFile)
	if err != nil {
		fmt.Fprintf(errOut, "mcp list: %v\n", err)
		return 1
	}
	if len(cfg.MCPServers) == 0 {
		fmt.Fprintln(out, "No MCP servers configured.")
		return 0
	}
	names := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Fprintf(out, "%-20s %-10s %-36s %s\n", "Name", "Transport", "Target", "Status")
	fmt.Fprintf(out, "%-20s %-10s %-36s %s\n", "----", "---------", "------", "------")
	for _, name := range names {
		srv := cfg.MCPServers[name]
		transport := "stdio"
		target := srv.Command
		if len(srv.Args) > 0 {
			target = strings.TrimSpace(target + " " + strings.Join(srv.Args, " "))
		}
		if srv.URL != "" {
			transport = "http"
			target = srv.URL
		}
		status := "enabled"
		if !srv.Enabled {
			status = "disabled"
		}
		fmt.Fprintf(out, "%-20s %-10s %-36s %s\n", name, transport, truncateCol(target, 36), status)
	}
	return 0
}

func runMCPAdd(p paths.ProfilePaths, args []string, out, errOut io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(errOut, "usage: mcp add <name> (--url <url> | --command <cmd> [--args <a1,a2>])")
		return 1
	}
	name := strings.TrimSpace(args[0])
	if !validMCPNameRe.MatchString(name) {
		fmt.Fprintf(errOut, "mcp add: invalid server name %q\n", name)
		return 1
	}
	var (
		rawURL  string
		command string
		rawArgs string
	)
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--url":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "mcp add: --url requires a value")
				return 1
			}
			i++
			rawURL = strings.TrimSpace(args[i])
		case "--command":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "mcp add: --command requires a value")
				return 1
			}
			i++
			command = strings.TrimSpace(args[i])
		case "--args":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "mcp add: --args requires a value")
				return 1
			}
			i++
			rawArgs = args[i]
		default:
			fmt.Fprintf(errOut, "mcp add: unknown flag %q\n", args[i])
			return 1
		}
	}

	if (rawURL == "" && command == "") || (rawURL != "" && command != "") {
		fmt.Fprintln(errOut, "mcp add: specify exactly one of --url or --command")
		return 1
	}
	if rawURL != "" {
		if err := validateHTTPURL(rawURL); err != nil {
			fmt.Fprintf(errOut, "mcp add: invalid --url: %v\n", err)
			return 1
		}
	}

	doc, err := loadYAMLDoc(p.ConfigFile)
	if err != nil {
		fmt.Fprintf(errOut, "mcp add: %v\n", err)
		return 1
	}
	base := "mcp_servers." + name
	doc.deletePath(base)
	if rawURL != "" {
		doc.setScalarPath(base+".url", rawURL)
	} else {
		doc.setScalarPath(base+".command", command)
		if rawArgs != "" {
			var parsed []string
			for _, part := range strings.Split(rawArgs, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					parsed = append(parsed, trimmed)
				}
			}
			if len(parsed) > 0 {
				doc.setListPath(base+".args", parsed)
			}
		}
	}
	doc.setScalarPath(base+".enabled", "true")

	if err := saveYAMLDoc(p.ConfigFile, doc); err != nil {
		fmt.Fprintf(errOut, "mcp add: failed to save %s: %v\n", p.ConfigFile, err)
		return 1
	}
	fmt.Fprintf(out, "✓ Added MCP server %q to %s\n", name, p.ConfigFile)
	return 0
}

func runMCPRemove(p paths.ProfilePaths, args []string, out, errOut io.Writer) int {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(errOut, "usage: mcp remove <name>")
		return 1
	}
	name := strings.TrimSpace(args[0])
	doc, err := loadYAMLDoc(p.ConfigFile)
	if err != nil {
		fmt.Fprintf(errOut, "mcp remove: %v\n", err)
		return 1
	}
	if !doc.deletePath("mcp_servers." + name) {
		fmt.Fprintf(errOut, "mcp remove: server %q not found\n", name)
		return 1
	}
	if err := saveYAMLDoc(p.ConfigFile, doc); err != nil {
		fmt.Fprintf(errOut, "mcp remove: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "✓ Removed MCP server %q\n", name)
	return 0
}

func runMCPTest(p paths.ProfilePaths, args []string, out, errOut io.Writer) int {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(errOut, "usage: mcp test <name>")
		return 1
	}
	name := strings.TrimSpace(args[0])
	cfg, err := config.Load(p.ConfigFile, p.EnvFile)
	if err != nil {
		fmt.Fprintf(errOut, "mcp test: %v\n", err)
		return 1
	}
	srv, ok := cfg.MCPServers[name]
	if !ok {
		fmt.Fprintf(errOut, "mcp test: unknown server %q\n", name)
		return 3
	}
	if srv.URL != "" {
		if err := validateHTTPURL(srv.URL); err != nil {
			fmt.Fprintf(errOut, "✗ %s (http): invalid URL %q: %v\n", name, srv.URL, err)
			return 1
		}
		fmt.Fprintf(out, "✓ %s (http): endpoint URL valid (%s)\n", name, srv.URL)
		return 0
	}
	if srv.Command != "" {
		resolved, err := exec.LookPath(srv.Command)
		if err != nil {
			fmt.Fprintf(errOut, "✗ %s (stdio): command not found: %s\n", name, srv.Command)
			return 1
		}
		fmt.Fprintf(out, "✓ %s (stdio): command found (%s)\n", name, resolved)
		return 0
	}
	fmt.Fprintf(errOut, "✗ %s: neither url nor command configured\n", name)
	return 1
}

func validateHTTPURL(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return fmt.Errorf("URL host is empty")
	}
	return nil
}

// ── 5. sessions subcommand & shared session loader ───────────────────────────

type cliSessionRecord struct {
	ID        string
	Title     string
	Source    string
	Model     string
	FilePath  string // JSONL path if from PiG; state.db path if from SQLite
	IsStateDB bool
	StartedAt time.Time
	UpdatedAt time.Time
	Pinned    bool
	Messages  []sessions.SessionMessage
}

type sessionMetaStore struct {
	Pinned  map[string]bool   `json:"pinned,omitempty"`
	Renamed map[string]string `json:"renamed,omitempty"`
	Deleted map[string]bool   `json:"deleted,omitempty"`
}

func sessionMetaPath(p paths.ProfilePaths) string {
	return filepath.Join(p.Home, "session_meta.json")
}

func loadSessionMeta(p paths.ProfilePaths) sessionMetaStore {
	st := sessionMetaStore{
		Pinned:  make(map[string]bool),
		Renamed: make(map[string]string),
		Deleted: make(map[string]bool),
	}
	raw, err := os.ReadFile(sessionMetaPath(p))
	if err != nil {
		return st
	}
	_ = json.Unmarshal(raw, &st)
	if st.Pinned == nil {
		st.Pinned = make(map[string]bool)
	}
	if st.Renamed == nil {
		st.Renamed = make(map[string]string)
	}
	if st.Deleted == nil {
		st.Deleted = make(map[string]bool)
	}
	return st
}

func saveSessionMeta(p paths.ProfilePaths, st sessionMetaStore) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(sessionMetaPath(p), data, 0o644)
}

func collectAllSessions(p paths.ProfilePaths) ([]cliSessionRecord, int64) {
	meta := loadSessionMeta(p)
	var records []cliSessionRecord
	seenID := make(map[string]bool)
	var totalBytes int64

	// 1. PiG JSONL session files.
	dirs := sessions.DefaultSessionDirs("")
	if p.Home != "" {
		dirs = append(dirs, filepath.Join(p.Home, "sessions"))
	}
	seenDir := make(map[string]bool)
	for _, dir := range dirs {
		clean := filepath.Clean(dir)
		if seenDir[clean] {
			continue
		}
		seenDir[clean] = true
		info, err := os.Stat(clean)
		if err != nil || !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(clean, func(fp string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
				return nil
			}
			if fi, err := d.Info(); err == nil {
				totalBytes += fi.Size()
			}
			ps, err := sessions.ParseSessionFile(fp)
			if err != nil || ps == nil || ps.ID == "" {
				return nil
			}
			if meta.Deleted[ps.ID] || seenID[ps.ID] {
				return nil
			}
			seenID[ps.ID] = true
			title := ps.Title
			if title == "" {
				title = firstUserPreview(ps.Messages)
			}
			if renamed, ok := meta.Renamed[ps.ID]; ok && renamed != "" {
				title = renamed
			}
			records = append(records, cliSessionRecord{
				ID:        ps.ID,
				Title:     title,
				Source:    "pig",
				Model:     ps.Model,
				FilePath:  ps.FilePath,
				StartedAt: ps.StartedAt,
				UpdatedAt: ps.UpdatedAt,
				Pinned:    meta.Pinned[ps.ID],
				Messages:  ps.Messages,
			})
			return nil
		})
	}

	// 2. Read-only upstream ~/.hermes/state.db (via sqlite3 -readonly -json).
	dbPaths := candidateStateDBPaths(p)
	for _, dbPath := range dbPaths {
		if fi, err := os.Stat(dbPath); err == nil && !fi.IsDir() {
			totalBytes += fi.Size()
		}
		for _, rec := range readStateDBSessions(dbPath) {
			if meta.Deleted[rec.ID] || seenID[rec.ID] {
				continue
			}
			seenID[rec.ID] = true
			if renamed, ok := meta.Renamed[rec.ID]; ok && renamed != "" {
				rec.Title = renamed
			}
			rec.Pinned = meta.Pinned[rec.ID]
			records = append(records, rec)
		}
	}

	sort.Slice(records, func(i, j int) bool {
		if !records[i].UpdatedAt.Equal(records[j].UpdatedAt) {
			return records[i].UpdatedAt.After(records[j].UpdatedAt)
		}
		return records[i].ID < records[j].ID
	})
	return records, totalBytes
}

func candidateStateDBPaths(p paths.ProfilePaths) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(fp string) {
		fp = strings.TrimSpace(fp)
		if fp == "" {
			return
		}
		clean := filepath.Clean(fp)
		if seen[clean] {
			return
		}
		if fi, err := os.Stat(clean); err == nil && !fi.IsDir() && fi.Size() > 0 {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	if p.Home != "" {
		add(filepath.Join(p.Home, "state.db"))
	}
	if p.RootHome != "" {
		add(filepath.Join(p.RootHome, "state.db"))
	}
	return out
}

type cliStateDBSessionRow struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Model     string `json:"model"`
	Source    string `json:"source"`
	StartedAt any    `json:"started_at"`
	EndedAt   any    `json:"ended_at"`
}

type cliStateDBMessageRow struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolName  string `json:"tool_name"`
	ToolCalls string `json:"tool_calls"`
	Timestamp any    `json:"timestamp"`
}

func readStateDBSessions(dbPath string) []cliSessionRecord {
	sqliteBin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil
	}
	rows, ok := runCLIStateDBQuery[cliStateDBSessionRow](sqliteBin, dbPath,
		`SELECT COALESCE(id,'') AS id,
		        COALESCE(title,'') AS title,
		        COALESCE(model,'') AS model,
		        COALESCE(source,'hermes') AS source,
		        started_at,
		        ended_at
		   FROM sessions
		  ORDER BY started_at DESC`)
	if !ok {
		rows, ok = runCLIStateDBQuery[cliStateDBSessionRow](sqliteBin, dbPath,
			`SELECT COALESCE(id,'') AS id,
			        COALESCE(title,'') AS title,
			        COALESCE(model,'') AS model,
			        'hermes' AS source,
			        started_at,
			        ended_at
			   FROM sessions
			  ORDER BY started_at DESC`)
		if !ok {
			return nil
		}
	}
	if len(rows) == 0 {
		return nil
	}
	msgRows, _ := runCLIStateDBQuery[cliStateDBMessageRow](sqliteBin, dbPath,
		`SELECT id,
		        COALESCE(session_id,'') AS session_id,
		        COALESCE(role,'') AS role,
		        COALESCE(content,'') AS content,
		        COALESCE(tool_name,'') AS tool_name,
		        COALESCE(tool_calls,'') AS tool_calls,
		        timestamp
		   FROM messages
		  ORDER BY session_id ASC, id ASC`)

	bySess := make(map[string][]cliStateDBMessageRow, len(rows))
	for _, m := range msgRows {
		if m.SessionID != "" {
			bySess[m.SessionID] = append(bySess[m.SessionID], m)
		}
	}

	fi, _ := os.Stat(dbPath)
	out := make([]cliSessionRecord, 0, len(rows))
	for _, r := range rows {
		id := strings.TrimSpace(r.ID)
		if id == "" {
			continue
		}
		started := parseFlexTimestamp(r.StartedAt)
		updated := parseFlexTimestamp(r.EndedAt)
		if updated.IsZero() {
			updated = started
		}
		src := strings.TrimSpace(r.Source)
		if src == "" {
			src = "hermes"
		}

		var msgs []sessions.SessionMessage
		for idx, m := range bySess[id] {
			ts := parseFlexTimestamp(m.Timestamp)
			if !ts.IsZero() && ts.After(updated) {
				updated = ts
			}
			if started.IsZero() && !ts.IsZero() {
				started = ts
			}
			tcs := parseCLIToolCalls(m.ToolCalls)
			msgs = append(msgs, sessions.SessionMessage{
				Index:     idx + 1,
				EntryID:   strconv.FormatInt(m.ID, 10),
				Role:      strings.ToLower(strings.TrimSpace(m.Role)),
				Content:   m.Content,
				ToolName:  strings.TrimSpace(m.ToolName),
				ToolCalls: tcs,
				Timestamp: ts,
			})
		}
		if started.IsZero() && fi != nil {
			started = fi.ModTime().UTC()
		}
		if updated.IsZero() {
			updated = started
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = firstUserPreview(msgs)
		}
		out = append(out, cliSessionRecord{
			ID:        id,
			Title:     title,
			Source:    src,
			Model:     strings.TrimSpace(r.Model),
			FilePath:  dbPath,
			IsStateDB: true,
			StartedAt: started,
			UpdatedAt: updated,
			Messages:  msgs,
		})
	}
	return out
}

func runCLIStateDBQuery[T any](sqliteBin, dbPath, query string) ([]T, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sqliteBin, "-readonly", "-json", dbPath, query)
	raw, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" {
		return nil, true
	}
	var rows []T
	if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
		return nil, false
	}
	return rows, true
}

func parseFlexTimestamp(v any) time.Time {
	switch t := v.(type) {
	case float64:
		if t <= 0 {
			return time.Time{}
		}
		if t > 1e12 {
			t = t / 1000.0
		}
		sec, frac := math.Modf(t)
		return time.Unix(int64(sec), int64(frac*1e9)).UTC()
	case int64:
		if t <= 0 {
			return time.Time{}
		}
		return time.Unix(t, 0).UTC()
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return time.Time{}
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return parseFlexTimestamp(f)
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if parsed, err := time.Parse(layout, s); err == nil {
				return parsed.UTC()
			}
		}
	}
	return time.Time{}
}

func parseCLIToolCalls(raw string) []sessions.ToolCallSummary {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var items []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}
	var out []sessions.ToolCallSummary
	for _, it := range items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			name = strings.TrimSpace(it.Function.Name)
		}
		if name != "" {
			out = append(out, sessions.ToolCallSummary{ID: it.ID, Name: name, Arguments: it.Function.Arguments})
		}
	}
	return out
}

func firstUserPreview(msgs []sessions.SessionMessage) string {
	for _, m := range msgs {
		if m.Role == "user" {
			s := strings.TrimSpace(strings.ReplaceAll(m.Content, "\n", " "))
			if s != "" {
				return truncateCol(s, 40)
			}
		}
	}
	return "(untitled)"
}

func truncateCol(s string, maxLen int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if maxLen <= 3 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func runSessionsSubcommand(env clihook.Env, args []string) int {
	out := envOut(env)
	errOut := envErr(env)
	p := paths.ResolveProfilePaths("")

	action := "list"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		rest = args[1:]
	}

	switch action {
	case "list", "ls":
		return runSessionsList(p, rest, out, errOut)
	case "stats":
		return runSessionsStats(p, out)
	case "export":
		return runSessionsExport(p, rest, out, errOut)
	case "delete", "rm":
		return runSessionsDelete(p, rest, out, errOut)
	case "rename":
		return runSessionsRename(p, rest, out, errOut)
	case "pin":
		return runSessionsPin(p, rest, true, out, errOut)
	case "unpin":
		return runSessionsPin(p, rest, false, out, errOut)
	case "pinned":
		return runSessionsPinned(p, rest, out, errOut)
	default:
		fmt.Fprintf(errOut, "sessions: unknown action %q (valid: list, stats, export, delete, rename, pin, unpin, pinned)\n", action)
		return 1
	}
}

func runSessionsList(p paths.ProfilePaths, args []string, out, errOut io.Writer) int {
	limit := 20
	sourceFilter := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit", "-n":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "sessions list: --limit requires a number")
				return 1
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				fmt.Fprintf(errOut, "sessions list: invalid --limit %q\n", args[i])
				return 1
			}
			limit = n
		case "--source":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "sessions list: --source requires a value")
				return 1
			}
			i++
			sourceFilter = strings.ToLower(strings.TrimSpace(args[i]))
		default:
			fmt.Fprintf(errOut, "sessions list: unknown flag %q\n", args[i])
			return 1
		}
	}

	recs, _ := collectAllSessions(p)
	var filtered []cliSessionRecord
	for _, r := range recs {
		if sourceFilter != "" && strings.ToLower(r.Source) != sourceFilter {
			continue
		}
		filtered = append(filtered, r)
	}
	if len(filtered) == 0 {
		fmt.Fprintln(out, "No sessions found.")
		return 0
	}
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	fmt.Fprintf(out, "%-36s %-20s %-10s %s\n", "Title/Preview", "Last Active", "Source", "ID")
	fmt.Fprintf(out, "%-36s %-20s %-10s %s\n", "-------------", "-----------", "------", "--")
	for _, r := range filtered {
		title := r.Title
		if r.Pinned {
			title = "★ " + title
		}
		when := "-"
		if !r.UpdatedAt.IsZero() {
			when = r.UpdatedAt.UTC().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(out, "%-36s %-20s %-10s %s\n", truncateCol(title, 36), when, r.Source, r.ID)
	}
	return 0
}

func runSessionsStats(p paths.ProfilePaths, out io.Writer) int {
	recs, dbSize := collectAllSessions(p)
	totalMsgs := 0
	bySource := make(map[string]int)
	for _, r := range recs {
		totalMsgs += len(r.Messages)
		bySource[r.Source]++
	}
	fmt.Fprintf(out, "Total sessions: %d\n", len(recs))
	fmt.Fprintf(out, "Total messages: %d\n", totalMsgs)
	fmt.Fprintf(out, "Database size:  %s\n", humanBytes(dbSize))
	if len(bySource) > 0 {
		sources := make([]string, 0, len(bySource))
		for s := range bySource {
			sources = append(sources, s)
		}
		sort.Strings(sources)
		fmt.Fprintln(out, "By source:")
		for _, s := range sources {
			fmt.Fprintf(out, "  %-12s %d\n", s, bySource[s])
		}
	}
	return 0
}

func runSessionsExport(p paths.ProfilePaths, args []string, out, errOut io.Writer) int {
	var (
		sessionID string
		format    = "md"
		outPath   string
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--format", "-f":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "sessions export: --format requires md|json")
				return 1
			}
			i++
			format = strings.ToLower(strings.TrimSpace(args[i]))
		case "--out", "-o":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "sessions export: --out requires a path")
				return 1
			}
			i++
			outPath = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(errOut, "sessions export: unknown flag %q\n", args[i])
				return 1
			}
			if sessionID == "" {
				sessionID = args[i]
			}
		}
	}
	if sessionID == "" {
		fmt.Fprintln(errOut, "usage: sessions export <session_id> [--format md|json] [--out <path>]")
		return 1
	}
	if format != "md" && format != "json" {
		fmt.Fprintf(errOut, "sessions export: unsupported format %q (valid: md, json)\n", format)
		return 1
	}

	recs, _ := collectAllSessions(p)
	var target *cliSessionRecord
	for i := range recs {
		if recs[i].ID == sessionID {
			target = &recs[i]
			break
		}
	}
	if target == nil {
		fmt.Fprintf(errOut, "sessions export: session %q not found\n", sessionID)
		return 1
	}

	var payload []byte
	if format == "json" {
		obj := map[string]any{
			"id":         target.ID,
			"title":      target.Title,
			"source":     target.Source,
			"model":      target.Model,
			"started_at": target.StartedAt.UTC().Format(time.RFC3339),
			"updated_at": target.UpdatedAt.UTC().Format(time.RFC3339),
			"messages":   target.Messages,
		}
		var err error
		payload, err = json.MarshalIndent(obj, "", "  ")
		if err != nil {
			fmt.Fprintf(errOut, "sessions export: %v\n", err)
			return 1
		}
		payload = append(payload, '\n')
	} else {
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n", target.Title)
		fmt.Fprintf(&b, "- **Session ID:** %s\n", target.ID)
		fmt.Fprintf(&b, "- **Source:** %s\n", target.Source)
		if target.Model != "" {
			fmt.Fprintf(&b, "- **Model:** %s\n", target.Model)
		}
		fmt.Fprintf(&b, "- **Updated:** %s\n\n", target.UpdatedAt.UTC().Format(time.RFC3339))
		for _, m := range target.Messages {
			fmt.Fprintf(&b, "### %s\n\n%s\n\n", strings.ToUpper(m.Role), m.Content)
		}
		payload = []byte(b.String())
	}

	if outPath != "" {
		if err := atomicWriteFile(outPath, payload, 0o644); err != nil {
			fmt.Fprintf(errOut, "sessions export: failed to write %s: %v\n", outPath, err)
			return 1
		}
		fmt.Fprintf(out, "✓ Exported session %s to %s\n", target.ID, outPath)
		return 0
	}
	_, _ = out.Write(payload)
	return 0
}

func runSessionsDelete(p paths.ProfilePaths, args []string, out, errOut io.Writer) int {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(errOut, "usage: sessions delete <session_id>")
		return 1
	}
	id := strings.TrimSpace(args[0])
	recs, _ := collectAllSessions(p)
	var target *cliSessionRecord
	for i := range recs {
		if recs[i].ID == id {
			target = &recs[i]
			break
		}
	}
	if target == nil {
		fmt.Fprintf(errOut, "sessions delete: session %q not found\n", id)
		return 1
	}

	meta := loadSessionMeta(p)
	if !target.IsStateDB && target.FilePath != "" {
		if err := os.Remove(target.FilePath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(errOut, "sessions delete: %v\n", err)
			return 1
		}
	} else {
		meta.Deleted[id] = true
	}
	delete(meta.Pinned, id)
	delete(meta.Renamed, id)
	if err := saveSessionMeta(p, meta); err != nil {
		fmt.Fprintf(errOut, "sessions delete: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "✓ Deleted session %s\n", id)
	return 0
}

func runSessionsRename(p paths.ProfilePaths, args []string, out, errOut io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(errOut, "usage: sessions rename <session_id> <title>")
		return 1
	}
	id := strings.TrimSpace(args[0])
	title := strings.TrimSpace(strings.Join(args[1:], " "))
	if id == "" || title == "" {
		fmt.Fprintln(errOut, "sessions rename: session_id and title are required")
		return 1
	}
	meta := loadSessionMeta(p)
	meta.Renamed[id] = title
	if err := saveSessionMeta(p, meta); err != nil {
		fmt.Fprintf(errOut, "sessions rename: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "✓ Renamed session %s to %q\n", id, title)
	return 0
}

func runSessionsPin(p paths.ProfilePaths, args []string, pin bool, out, errOut io.Writer) int {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		if pin {
			fmt.Fprintln(errOut, "usage: sessions pin <session_id>")
		} else {
			fmt.Fprintln(errOut, "usage: sessions unpin <session_id>")
		}
		return 1
	}
	id := strings.TrimSpace(args[0])
	meta := loadSessionMeta(p)
	if pin {
		meta.Pinned[id] = true
	} else {
		delete(meta.Pinned, id)
	}
	if err := saveSessionMeta(p, meta); err != nil {
		fmt.Fprintf(errOut, "sessions: %v\n", err)
		return 1
	}
	if pin {
		fmt.Fprintf(out, "✓ Pinned session %s\n", id)
	} else {
		fmt.Fprintf(out, "✓ Unpinned session %s\n", id)
	}
	return 0
}

func runSessionsPinned(p paths.ProfilePaths, args []string, out, errOut io.Writer) int {
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else {
			fmt.Fprintf(errOut, "sessions pinned: unknown flag %q\n", a)
			return 1
		}
	}

	meta := loadSessionMeta(p)
	recs, _ := collectAllSessions(p)
	recByID := make(map[string]cliSessionRecord, len(recs))
	for _, r := range recs {
		recByID[r.ID] = r
	}

	type pinnedEntry struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Source string `json:"source"`
	}
	var pinned []pinnedEntry
	for id, ok := range meta.Pinned {
		if !ok {
			continue
		}
		entry := pinnedEntry{ID: id, Title: meta.Renamed[id], Source: "unknown"}
		if rec, found := recByID[id]; found {
			entry.Title = rec.Title
			entry.Source = rec.Source
		}
		if entry.Title == "" {
			entry.Title = "(untitled)"
		}
		pinned = append(pinned, entry)
	}
	sort.Slice(pinned, func(i, j int) bool { return pinned[i].ID < pinned[j].ID })

	if asJSON {
		b, _ := json.MarshalIndent(pinned, "", "  ")
		fmt.Fprintln(out, string(b))
		return 0
	}
	if len(pinned) == 0 {
		fmt.Fprintln(out, "No pinned sessions.")
		return 0
	}
	fmt.Fprintf(out, "%-36s %-10s %s\n", "Title", "Source", "ID")
	fmt.Fprintf(out, "%-36s %-10s %s\n", "-----", "------", "--")
	for _, pe := range pinned {
		fmt.Fprintf(out, "%-36s %-10s %s\n", truncateCol(pe.Title, 36), pe.Source, pe.ID)
	}
	return 0
}

// ── 6. insights subcommand ───────────────────────────────────────────────────

func runInsightsSubcommand(env clihook.Env, args []string) int {
	out := envOut(env)
	errOut := envErr(env)
	p := paths.ResolveProfilePaths("")

	days := 30
	sourceFilter := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--days":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "insights: --days requires an integer")
				return 1
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				fmt.Fprintf(errOut, "insights: invalid --days %q\n", args[i])
				return 1
			}
			days = n
		case "--source":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "insights: --source requires a value")
				return 1
			}
			i++
			sourceFilter = strings.ToLower(strings.TrimSpace(args[i]))
		default:
			fmt.Fprintf(errOut, "insights: unknown argument %q\n", args[i])
			return 1
		}
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	recs, _ := collectAllSessions(p)

	var (
		sessCount    int
		msgCount     int
		userMsgCount int
		toolCalls    int
		modelCounts  = make(map[string]int)
		toolCounts   = make(map[string]int)
		dayOfWeek    [7]int // 0=Sun..6=Sat
		activeDays   = make(map[string]bool)
	)

	for _, r := range recs {
		if sourceFilter != "" && strings.ToLower(r.Source) != sourceFilter {
			continue
		}
		ts := r.UpdatedAt
		if ts.IsZero() {
			ts = r.StartedAt
		}
		if !ts.IsZero() && ts.Before(cutoff) {
			continue
		}
		sessCount++
		msgCount += len(r.Messages)
		mName := r.Model
		if mName == "" {
			mName = "(unknown)"
		}
		modelCounts[mName]++
		if !ts.IsZero() {
			dayOfWeek[int(ts.Weekday())]++
			activeDays[ts.UTC().Format("2006-01-02")] = true
		}
		for _, m := range r.Messages {
			if m.Role == "user" {
				userMsgCount++
			}
			for _, tc := range m.ToolCalls {
				if tc.Name != "" {
					toolCalls++
					toolCounts[tc.Name]++
				}
			}
			if m.Role == "tool" && m.ToolName != "" && len(m.ToolCalls) == 0 {
				toolCalls++
				toolCounts[m.ToolName]++
			}
		}
	}

	const boxWidth = 60
	borderTop := "╭" + strings.Repeat("─", boxWidth-2) + "╮"
	borderMid := "├" + strings.Repeat("─", boxWidth-2) + "┤"
	borderBot := "╰" + strings.Repeat("─", boxWidth-2) + "╯"
	writeBoxLine := func(s string) {
		runes := []rune(s)
		maxInner := boxWidth - 4
		if len(runes) > maxInner {
			runes = append(runes[:maxInner-1], '…')
		}
		pad := maxInner - len(runes)
		if pad < 0 {
			pad = 0
		}
		fmt.Fprintf(out, "│ %s%s │\n", string(runes), strings.Repeat(" ", pad))
	}

	fmt.Fprintln(out, borderTop)
	title := fmt.Sprintf("Hermes Insights (last %d days)", days)
	if sourceFilter != "" {
		title += fmt.Sprintf(" [%s]", sourceFilter)
	}
	writeBoxLine(title)
	fmt.Fprintln(out, borderMid)

	if sessCount == 0 {
		writeBoxLine("Overview")
		writeBoxLine("  No session data found for this period.")
		fmt.Fprintln(out, borderBot)
		return 0
	}

	writeBoxLine("Overview")
	writeBoxLine(fmt.Sprintf("  Sessions: %-8d Messages: %-8d Active days: %d", sessCount, msgCount, len(activeDays)))
	writeBoxLine(fmt.Sprintf("  User msgs: %-7d Tool calls: %d", userMsgCount, toolCalls))

	fmt.Fprintln(out, borderMid)
	writeBoxLine("Models Used")
	type kv struct {
		k string
		v int
	}
	models := make([]kv, 0, len(modelCounts))
	for k, v := range modelCounts {
		models = append(models, kv{k, v})
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].v != models[j].v {
			return models[i].v > models[j].v
		}
		return models[i].k < models[j].k
	})
	for _, m := range models {
		pct := (m.v * 100) / sessCount
		writeBoxLine(fmt.Sprintf("  %-30s %4d (%3d%%)", truncateCol(m.k, 30), m.v, pct))
	}

	fmt.Fprintln(out, borderMid)
	writeBoxLine("Top Tools")
	if len(toolCounts) == 0 {
		writeBoxLine("  (no tool calls recorded)")
	} else {
		tools := make([]kv, 0, len(toolCounts))
		for k, v := range toolCounts {
			tools = append(tools, kv{k, v})
		}
		sort.Slice(tools, func(i, j int) bool {
			if tools[i].v != tools[j].v {
				return tools[i].v > tools[j].v
			}
			return tools[i].k < tools[j].k
		})
		if len(tools) > 10 {
			tools = tools[:10]
		}
		for _, t := range tools {
			writeBoxLine(fmt.Sprintf("  %-30s %5d", truncateCol(t.k, 30), t.v))
		}
	}

	fmt.Fprintln(out, borderMid)
	writeBoxLine("Activity Patterns")
	dayOrder := []time.Weekday{
		time.Monday, time.Tuesday, time.Wednesday, time.Thursday,
		time.Friday, time.Saturday, time.Sunday,
	}
	dayLabels := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	maxDay := 1
	for _, d := range dayOfWeek {
		if d > maxDay {
			maxDay = d
		}
	}
	const maxBar = 24
	for i, wd := range dayOrder {
		cnt := dayOfWeek[int(wd)]
		barLen := 0
		if cnt > 0 {
			barLen = (cnt * maxBar) / maxDay
			if barLen == 0 {
				barLen = 1
			}
		}
		bar := strings.Repeat("█", barLen)
		writeBoxLine(fmt.Sprintf("  %s  %-24s %d", dayLabels[i], bar, cnt))
	}
	fmt.Fprintln(out, borderBot)
	return 0
}

// ── 7. logs subcommand ───────────────────────────────────────────────────────

func runLogsSubcommand(env clihook.Env, args []string) int {
	out := envOut(env)
	errOut := envErr(env)
	p := paths.ResolveProfilePaths("")

	logName := "agent"
	numLines := 50
	levelFilter := ""
	nameSet := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n", "--lines":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "logs: -n requires a positive integer")
				return 1
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				fmt.Fprintf(errOut, "logs: invalid line count %q\n", args[i])
				return 1
			}
			numLines = n
		case "--level":
			if i+1 >= len(args) {
				fmt.Fprintln(errOut, "logs: --level requires a value")
				return 1
			}
			i++
			levelFilter = strings.ToUpper(strings.TrimSpace(args[i]))
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(errOut, "logs: unknown flag %q\n", args[i])
				return 1
			}
			if !nameSet {
				logName = args[i]
				nameSet = true
			} else {
				fmt.Fprintf(errOut, "logs: unexpected argument %q\n", args[i])
				return 1
			}
		}
	}

	logsDir := filepath.Join(p.Home, "logs")
	if logName == "list" {
		type logFileInfo struct {
			name string
			size int64
		}
		var found []logFileInfo
		seen := make(map[string]bool)
		if entries, err := os.ReadDir(logsDir); err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
					continue
				}
				if fi, err := e.Info(); err == nil {
					seen[e.Name()] = true
					found = append(found, logFileInfo{name: e.Name(), size: fi.Size()})
				}
			}
		}
		// Include root update.log if profile home differs from root home.
		if p.RootHome != "" && filepath.Clean(p.RootHome) != filepath.Clean(p.Home) {
			upd := filepath.Join(p.RootHome, "logs", "update.log")
			if fi, err := os.Stat(upd); err == nil && !fi.IsDir() && !seen["update.log"] {
				found = append(found, logFileInfo{name: "update.log", size: fi.Size()})
			}
		}
		if len(found) == 0 {
			fmt.Fprintf(out, "No log files found in %s\n", logsDir)
			return 0
		}
		sort.Slice(found, func(i, j int) bool { return found[i].name < found[j].name })
		fmt.Fprintf(out, "%-24s %s\n", "Log File", "Size")
		fmt.Fprintf(out, "%-24s %s\n", "--------", "----")
		for _, f := range found {
			fmt.Fprintf(out, "%-24s %s\n", f.name, humanBytes(f.size))
		}
		return 0
	}

	var targetPath string
	switch logName {
	case "agent":
		targetPath = filepath.Join(logsDir, "agent.log")
	case "errors":
		targetPath = filepath.Join(logsDir, "errors.log")
	case "gateway":
		targetPath = filepath.Join(logsDir, "gateway.log")
	case "mcp":
		targetPath = filepath.Join(logsDir, "mcp-stderr.log")
	case "update":
		rootLogs := filepath.Join(p.RootHome, "logs")
		if p.RootHome == "" {
			rootLogs = logsDir
		}
		targetPath = filepath.Join(rootLogs, "update.log")
	default:
		fmt.Fprintf(errOut, "logs: unknown log %q (valid: agent, errors, gateway, mcp, update, list)\n", logName)
		return 1
	}

	raw, err := os.ReadFile(targetPath)
	if err != nil {
		fmt.Fprintf(errOut, "logs: %s not found (%v)\n", targetPath, err)
		return 1
	}

	allLines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	var matched []string
	for _, line := range allLines {
		if levelFilter != "" && !strings.Contains(strings.ToUpper(line), levelFilter) {
			continue
		}
		matched = append(matched, line)
	}
	if len(matched) > numLines {
		matched = matched[len(matched)-numLines:]
	}

	fmt.Fprintf(out, "--- %s (last %d lines) ---\n", targetPath, len(matched))
	for _, line := range matched {
		fmt.Fprintln(out, line)
	}
	return 0
}
