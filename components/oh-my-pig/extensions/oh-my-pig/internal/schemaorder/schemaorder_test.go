package schemaorder_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/agents"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ask"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ast"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/autolearn"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/autoresearch"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/checkpoint"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ctxnotes"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/debug"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/files"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/github"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lsp"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/memory"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/modes"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/search"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/shell"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/think"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/todo"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/web"
)

type areaRegistrar struct {
	area     string
	register func(*sdk.Extension)
}

var allToolAreas = []areaRegistrar{
	{area: "internal/files", register: files.Register},
	{area: "internal/search", register: search.Register},
	{area: "internal/shell", register: shell.Register},
	{area: "internal/ask", register: ask.Register},
	{area: "internal/todo", register: todo.Register},
	{area: "internal/web", register: web.Register},
	{area: "internal/eval", register: eval.Register},
	{area: "internal/agents", register: agents.Register},
	{area: "internal/ast", register: ast.Register},
	{area: "internal/modes", register: modes.Register},
	{area: "internal/checkpoint", register: checkpoint.Register},
	{area: "internal/debug", register: debug.Register},
	{area: "internal/github", register: github.Register},
	{area: "internal/lsp", register: lsp.Register},
	{area: "internal/autoresearch", register: autoresearch.Register},
	{area: "internal/autolearn", register: autolearn.Register},
	{area: "internal/memory", register: memory.Register},
	{area: "internal/think", register: think.Register},
	{area: "internal/ctxnotes", register: ctxnotes.Register},
}

type wireRegisteredTool struct {
	Name        string          `json:"name"`
	Label       string          `json:"label"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools []wireRegisteredTool `json:"tools"`
	} `json:"register,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
}

// captureRegisteredWire runs ext through sdk.Extension.RunWithConn over a
// net.Pipe and returns the exact raw JSON `parameters` bytes encoded by the
// SDK's wire serializer for every registered tool.
func captureRegisteredWire(t *testing.T, ext *sdk.Extension) map[string]wireRegisteredTool {
	t.Helper()
	hostSide, extSide := net.Pipe()
	runDone := make(chan error, 1)
	go func() { runDone <- ext.RunWithConn(extSide) }()

	readFrame := func() wireFrame {
		var lenBuf [4]byte
		if _, err := io.ReadFull(hostSide, lenBuf[:]); err != nil {
			t.Fatalf("read frame length: %v", err)
		}
		n := binary.BigEndian.Uint32(lenBuf[:])
		payload := make([]byte, n)
		if _, err := io.ReadFull(hostSide, payload); err != nil {
			t.Fatalf("read frame payload: %v", err)
		}
		var f wireFrame
		if err := json.Unmarshal(payload, &f); err != nil {
			t.Fatalf("unmarshal frame: %v", err)
		}
		return f
	}
	writeFrame := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal frame: %v", err)
		}
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(b)))
		_, _ = hostSide.Write(lenBuf[:])
		_, _ = hostSide.Write(b)
	}

	first := readFrame()
	for first.Type == "call" && first.Call != nil {
		writeFrame(map[string]any{
			"type":     "response",
			"id":       first.ID,
			"response": map[string]any{"result": map[string]any{}},
		})
		first = readFrame()
	}
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register frame, got type=%q", first.Type)
	}

	// Shut down the extension cleanly right after capturing the register frame.
	writeFrame(map[string]any{
		"type":     "shutdown",
		"shutdown": map[string]any{"reason": "schemaorder-test"},
	})
	_ = hostSide.Close()
	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("extension RunWithConn did not exit on shutdown")
	}

	out := make(map[string]wireRegisteredTool, len(first.Register.Tools))
	for _, tl := range first.Register.Tools {
		out[tl.Name] = tl
	}
	return out
}

type capturedPortTool struct {
	Area       string
	Parameters json.RawMessage
}

// collectPortTools registers every tool-owning area on a fresh sdk.Extension,
// captures its exact wire-encoded schemas through sdk.Extension.RunWithConn,
// and also includes the dynamic `yield` tool built by agents.BuildYieldToolDefinition.
func collectPortTools(t *testing.T) map[string]capturedPortTool {
	t.Helper()
	t.Setenv("PI_NO_INTENT", "1")
	out := map[string]capturedPortTool{}
	for _, a := range allToolAreas {
		ext := sdk.New("oh-my-pig-test-" + strings.ReplaceAll(a.area, "/", "-"))
		a.register(ext)
		tools := captureRegisteredWire(t, ext)
		for name, tl := range tools {
			out[name] = capturedPortTool{Area: a.area, Parameters: tl.Parameters}
		}
	}
	// Dynamic subagent tool: `yield` (constructed per structured child session).
	yieldExt := sdk.New("oh-my-pig-test-yield")
	yieldExt.RegisterTool(agents.BuildYieldToolDefinition(true, nil, nil))
	for name, tl := range captureRegisteredWire(t, yieldExt) {
		out[name] = capturedPortTool{Area: "internal/agents", Parameters: tl.Parameters}
	}
	return out
}

// reRegisterWithApply takes raw captured wire JSON for a tool, unmarshals into
// a plain sdk.Schema (preserving nothing of wire order, matching plain Go maps),
// applies schemaorder.Apply, registers on a fresh sdk.Extension, and returns
// the resulting wire JSON bytes.
func reRegisterWithApply(t *testing.T, name string, rawParams json.RawMessage) json.RawMessage {
	t.Helper()
	var plain sdk.Schema
	dec := json.NewDecoder(bytes.NewReader(rawParams))
	dec.UseNumber()
	if err := dec.Decode(&plain); err != nil {
		t.Fatalf("decode plain schema for %s: %v", name, err)
	}
	def := schemaorder.Apply(sdk.ToolDefinition{
		Name:       name,
		Parameters: plain,
		Execute:    func(sdk.Context, map[string]any) (any, error) { return nil, nil },
	})
	ext := sdk.New("schemaorder-apply-" + name)
	ext.RegisterTool(def)
	got := captureRegisteredWire(t, ext)
	return got[name].Parameters
}

type schemaDiff struct {
	Tool string `json:"tool"`
	Path string `json:"path"`
	Port string `json:"port"`
	Omp  string `json:"omp"`
	Area string `json:"area"`
}

func (d schemaDiff) key() string {
	return d.Tool + "|" + d.Path
}

func diffValues(tool, area, path string, portVal, ompVal any, out *[]schemaDiff) {
	// Normalize json.Number vs float64/int.
	portVal = normalizeJSONNumber(portVal)
	ompVal = normalizeJSONNumber(ompVal)

	pm, pIsMap := portVal.(map[string]any)
	om, oIsMap := ompVal.(map[string]any)
	if pIsMap && oIsMap {
		keys := map[string]struct{}{}
		for k := range pm {
			keys[k] = struct{}{}
		}
		for k := range om {
			keys[k] = struct{}{}
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			subPath := k
			if path != "" {
				subPath = path + "." + k
			}
			pv, pOK := pm[k]
			ov, oOK := om[k]
			if !pOK {
				*out = append(*out, schemaDiff{Tool: tool, Path: subPath, Port: "<missing>", Omp: compactJSON(ov), Area: area})
				continue
			}
			if !oOK {
				*out = append(*out, schemaDiff{Tool: tool, Path: subPath, Port: compactJSON(pv), Omp: "<missing>", Area: area})
				continue
			}
			diffValues(tool, area, subPath, pv, ov, out)
		}
		return
	}

	ps, pIsSlice := portVal.([]any)
	os, oIsSlice := ompVal.([]any)
	if pIsSlice && oIsSlice {
		if len(ps) != len(os) {
			*out = append(*out, schemaDiff{Tool: tool, Path: path, Port: compactJSON(ps), Omp: compactJSON(os), Area: area})
			return
		}
		// Compare array elements in order (exact on enums, required, anyOf, type tuples).
		for i := range ps {
			diffValues(tool, area, fmt.Sprintf("%s[%d]", path, i), ps[i], os[i], out)
		}
		return
	}

	if !reflect.DeepEqual(portVal, ompVal) {
		*out = append(*out, schemaDiff{Tool: tool, Path: path, Port: compactJSON(portVal), Omp: compactJSON(ompVal), Area: area})
	}
}

func normalizeJSONNumber(v any) any {
	n, ok := v.(json.Number)
	if !ok {
		return v
	}
	if i, err := n.Int64(); err == nil {
		return float64(i)
	}
	if f, err := n.Float64(); err == nil {
		return f
	}
	return n.String()
}

func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func compactRawJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// unescapeGoHTML reverses Go's encoding/json default HTML escapes (\u003c,
// \u003e, \u0026) emitted by sdk.schemaRoot/OrderedObject.MarshalJSON via
// json.MarshalJS for `<`, `>`, `&` in strings (JS JSON.stringify writes them
// literal). Reversing only those three escapes asserts exact byte-for-byte
// wire identity for key order, syntax, and values.
func unescapeGoHTML(s string) string {
	return strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&").Replace(s)
}

func decodeOrderedJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// extractObjectKeyOrders parses a JSON payload by token stream and returns a map
// of JSON path -> key slice in exact wire appearance order.
func extractObjectKeyOrders(t *testing.T, raw []byte) map[string][]string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	out := map[string][]string{}
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("extractObjectKeyOrders: %v", err)
	}
	walkTokensForOrder(t, dec, tok, "$", out)
	return out
}

func walkTokensForOrder(t *testing.T, dec *json.Decoder, tok json.Token, path string, out map[string][]string) {
	t.Helper()
	delim, ok := tok.(json.Delim)
	if !ok {
		return
	}
	switch delim {
	case '{':
		var keys []string
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				t.Fatalf("read key at %s: %v", path, err)
			}
			k := kt.(string)
			keys = append(keys, k)
			vt, err := dec.Token()
			if err != nil {
				t.Fatalf("read val at %s.%s: %v", path, k, err)
			}
			walkTokensForOrder(t, dec, vt, path+"."+k, out)
		}
		_, _ = dec.Token() // '}'
		out[path] = keys
	case '[':
		idx := 0
		for dec.More() {
			et, err := dec.Token()
			if err != nil {
				t.Fatalf("read elem at %s[%d]: %v", path, idx, err)
			}
			walkTokensForOrder(t, dec, et, fmt.Sprintf("%s[%d]", path, idx), out)
			idx++
		}
		_, _ = dec.Token() // ']'
	}
}

type knownDiffEntry struct {
	Tool   string `json:"tool"`
	Path   string `json:"path"`
	Area   string `json:"area"`
	Reason string `json:"reason"`
}

func loadKnownContentDiffs(t *testing.T) map[string]knownDiffEntry {
	t.Helper()
	b, err := os.ReadFile("known_content_diffs.json")
	if os.IsNotExist(err) {
		return map[string]knownDiffEntry{}
	}
	if err != nil {
		t.Fatalf("read known_content_diffs.json: %v", err)
	}
	var entries []knownDiffEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		t.Fatalf("parse known_content_diffs.json: %v", err)
	}
	out := make(map[string]knownDiffEntry, len(entries))
	for _, e := range entries {
		out[e.Tool+"|"+e.Path] = e
	}
	return out
}

func TestManifestMatchesEmbeddedSchemas(t *testing.T) {
	rawSchemas, err := os.ReadFile("omp_schemas.json")
	if err != nil {
		t.Fatalf("read omp_schemas.json: %v", err)
	}
	rawManifest, err := os.ReadFile("omp_schemas.manifest.json")
	if err != nil {
		t.Fatalf("read omp_schemas.manifest.json: %v", err)
	}
	var m struct {
		OmpTag        string `json:"ompTag"`
		OmpCommit     string `json:"ompCommit"`
		ToolCount     int    `json:"toolCount"`
		CapturedCount int    `json:"capturedCount"`
		Output        struct {
			File   string `json:"file"`
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"output"`
	}
	if err := json.Unmarshal(rawManifest, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if m.OmpTag != "v19.1.1" || m.OmpCommit != "31ffee513f27a01bba7d4dfbe12bae239cdc646a" {
		t.Fatalf("unexpected omp pin: tag=%q commit=%q", m.OmpTag, m.OmpCommit)
	}
	sum := sha256.Sum256(rawSchemas)
	gotHash := hex.EncodeToString(sum[:])
	if gotHash != m.Output.SHA256 {
		t.Fatalf("sha256 mismatch: file=%s manifest=%s", gotHash, m.Output.SHA256)
	}
	if len(rawSchemas) != m.Output.Bytes {
		t.Fatalf("byte length mismatch: file=%d manifest=%d", len(rawSchemas), m.Output.Bytes)
	}
	entries := schemaorder.Entries()
	if len(entries) != m.ToolCount {
		t.Fatalf("tool count mismatch: loaded=%d manifest=%d", len(entries), m.ToolCount)
	}
	// Verify generator files exist.
	for _, rel := range []string{"gen/capture.py", "gen/harness_isolation.py", "gen/README.md"} {
		if _, err := os.Stat(filepath.Join(".", rel)); err != nil {
			t.Errorf("missing generator file %s: %v", rel, err)
		}
	}
}

func TestPortSchemasEqualOmpContent(t *testing.T) {
	portTools := collectPortTools(t)
	ompEntries := schemaorder.Entries()
	allow := loadKnownContentDiffs(t)

	toolNames := make([]string, 0, len(portTools))
	for name := range portTools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	var allDiffs []schemaDiff
	for _, name := range toolNames {
		pt := portTools[name]
		ompEntry, ok := ompEntries[name]
		if !ok {
			t.Errorf("port tool %q (%s) has no entry in omp_schemas.json", name, pt.Area)
			continue
		}
		portVal, err := decodeOrderedJSON(pt.Parameters)
		if err != nil {
			t.Fatalf("decode port %s: %v", name, err)
		}
		ompVal, err := decodeOrderedJSON(ompEntry.RawParameters)
		if err != nil {
			t.Fatalf("decode omp %s: %v", name, err)
		}
		diffValues(name, pt.Area, "", portVal, ompVal, &allDiffs)
	}

	// Ensure every omp entry corresponds to a port tool.
	for name := range ompEntries {
		if _, ok := portTools[name]; !ok {
			t.Errorf("omp_schemas.json contains %q which was not registered by any port area", name)
		}
	}

	seenKeys := map[string]bool{}
	var unexpected []schemaDiff
	for _, d := range allDiffs {
		seenKeys[d.key()] = true
		if _, ok := allow[d.key()]; !ok {
			unexpected = append(unexpected, d)
		}
	}
	var stale []string
	for k := range allow {
		if !seenKeys[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)

	if len(unexpected) > 0 {
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d unallowlisted content diff(s) between port and omp v19.1.1:\n", len(unexpected))
		for _, d := range unexpected {
			fmt.Fprintf(&sb, "  tool=%s path=%s area=%s\n    port: %s\n    omp:  %s\n", d.Tool, d.Path, d.Area, d.Port, d.Omp)
		}
		t.Error(sb.String())
	}
	if len(stale) > 0 {
		t.Errorf("stale entries in known_content_diffs.json (port now matches omp; remove them): %v", stale)
	}
}

func TestWireOrderByteIdenticalAfterApply(t *testing.T) {
	portTools := collectPortTools(t)
	ompEntries := schemaorder.Entries()

	toolNames := make([]string, 0, len(portTools))
	for name := range portTools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	equalCount := 0
	for _, name := range toolNames {
		pt := portTools[name]
		ompEntry, ok := ompEntries[name]
		if !ok {
			continue
		}
		portVal, err := decodeOrderedJSON(pt.Parameters)
		if err != nil {
			t.Fatalf("decode port %s: %v", name, err)
		}
		ompVal, err := decodeOrderedJSON(ompEntry.RawParameters)
		if err != nil {
			t.Fatalf("decode omp %s: %v", name, err)
		}
		var diffs []schemaDiff
		diffValues(name, pt.Area, "", portVal, ompVal, &diffs)

		reorderedWire := reRegisterWithApply(t, name, pt.Parameters)

		if len(diffs) == 0 {
			equalCount++
			wantCompact := compactRawJSON(ompEntry.RawParameters)
			gotCompact := unescapeGoHTML(compactRawJSON(reorderedWire))
			if gotCompact != wantCompact {
				t.Errorf("tool %q: wire JSON after Apply is not byte-identical to omp:\n  got:  %s\n  want: %s", name, gotCompact, wantCompact)
			}
		} else {
			// Even for tools with content diffs, verify shared keys at every object
			// path follow omp's exact relative order and extra port keys trail sorted.
			ompOrders := extractObjectKeyOrders(t, ompEntry.RawParameters)
			gotOrders := extractObjectKeyOrders(t, reorderedWire)
			for path, gotKeys := range gotOrders {
				ompKeys, exists := ompOrders[path]
				if !exists {
					continue
				}
				var wantPrefix []string
				for _, k := range ompKeys {
					if slices.Contains(gotKeys, k) {
						wantPrefix = append(wantPrefix, k)
					}
				}
				if len(gotKeys) < len(wantPrefix) || !slices.Equal(gotKeys[:len(wantPrefix)], wantPrefix) {
					t.Errorf("tool %q path %s: key order prefix mismatch after Apply: got=%v wantPrefix=%v", name, path, gotKeys, wantPrefix)
				}
				tail := gotKeys[len(wantPrefix):]
				if !slices.IsSorted(tail) {
					t.Errorf("tool %q path %s: extra port keys not sorted: tail=%v", name, path, tail)
				}
			}
		}
	}
	if equalCount == 0 {
		t.Fatal("expected at least one content-equal tool to check byte-identical wire order")
	}
}

func TestApplyComposesWithIntent(t *testing.T) {
	// Use bash (rich nested schema with properties + nested `ready` object) and
	// glob (no `required` array initially) and resolve (intent omit) to verify
	// composition with real internal/intent.
	entries := schemaorder.Entries()

	for _, name := range []string{"bash", "glob", "read", "write", "debug", "github", "lsp"} {
		entry := entries[name]
		if entry == nil {
			t.Fatalf("missing omp entry for %s", name)
		}
		var plain sdk.Schema
		dec := json.NewDecoder(bytes.NewReader(entry.RawParameters))
		dec.UseNumber()
		if err := dec.Decode(&plain); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		def := sdk.ToolDefinition{
			Name:       name,
			Parameters: plain,
			Execute:    func(sdk.Context, map[string]any) (any, error) { return nil, nil },
		}
		wrapped := intent.Tool(schemaorder.Apply(def))
		ext := sdk.New("compose-" + name)
		ext.RegisterTool(wrapped)
		wire := captureRegisteredWire(t, ext)[name].Parameters

		ompOrders := extractObjectKeyOrders(t, entry.RawParameters)
		gotOrders := extractObjectKeyOrders(t, wire)

		// `properties` must start with "i" followed by omp's exact property order.
		wantProps := append([]string{intent.Field}, ompOrders["$.properties"]...)
		gotProps := gotOrders["$.properties"]
		if !slices.Equal(gotProps, wantProps) {
			t.Errorf("%s: properties order after intent.Tool(schemaorder.Apply(def)): got=%v want=%v", name, gotProps, wantProps)
		}

		// Root keys must keep omp's order (if `required` was newly added by intent
		// as for `glob`, it trails after omp's root keys).
		ompRoot := ompOrders["$"]
		gotRoot := gotOrders["$"]
		if len(gotRoot) < len(ompRoot) || !slices.Equal(gotRoot[:len(ompRoot)], ompRoot) {
			t.Errorf("%s: root key order after intent.Tool(schemaorder.Apply(def)): got=%v wantPrefix=%v", name, gotRoot, ompRoot)
		}

		// Nested objects (e.g. bash.properties.ready.properties) must stay in omp's exact order.
		for path, wantKeys := range ompOrders {
			if path == "$" || path == "$.properties" {
				continue
			}
			gotKeys, ok := gotOrders[path]
			if !ok {
				t.Errorf("%s: missing nested object at %s", name, path)
				continue
			}
			if !slices.Equal(gotKeys, wantKeys) {
				t.Errorf("%s: nested order at %s: got=%v want=%v", name, path, gotKeys, wantKeys)
			}
		}
	}

	// Verify omit tool (`resolve`) keeps exact byte-identical omp wire JSON under intent.Tool(schemaorder.Apply(def)).
	resolveEntry := entries["resolve"]
	var plainResolve sdk.Schema
	_ = json.Unmarshal(resolveEntry.RawParameters, &plainResolve)
	wrappedResolve := intent.Tool(schemaorder.Apply(sdk.ToolDefinition{
		Name:       "resolve",
		Parameters: plainResolve,
		Execute:    func(sdk.Context, map[string]any) (any, error) { return nil, nil },
	}))
	ext := sdk.New("compose-resolve")
	ext.RegisterTool(wrappedResolve)
	gotResolve := compactRawJSON(captureRegisteredWire(t, ext)["resolve"].Parameters)
	wantResolve := compactRawJSON(resolveEntry.RawParameters)
	if gotResolve != wantResolve {
		t.Errorf("resolve under intent.Tool(schemaorder.Apply(def)): got=%s want=%s", gotResolve, wantResolve)
	}
}

func TestApplyEdgeCases(t *testing.T) {
	// 1. Unknown tool is returned unchanged.
	orig := sdk.ToolDefinition{
		Name: "unknown_custom_tool",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"z": map[string]any{"type": "string"},
				"a": map[string]any{"type": "string"},
			},
		},
	}
	got := schemaorder.Apply(orig)
	if !reflect.DeepEqual(got.Parameters, orig.Parameters) {
		t.Fatalf("unknown tool should be returned unchanged")
	}

	// 2. Nil parameters returned unchanged.
	nilDef := sdk.ToolDefinition{Name: "bash", Parameters: nil}
	if schemaorder.Apply(nilDef).Parameters != nil {
		t.Fatalf("nil parameters should stay nil")
	}

	// 3. Extra port-only keys follow omp keys in sorted order; nothing added or removed; enum untouched.
	customBash := sdk.ToolDefinition{
		Name: "bash",
		Parameters: sdk.Schema{
			"additionalProperties": false,
			"zeta_root":            true,
			"alpha_root":           true,
			"type":                 "object",
			"required":             []string{"command"},
			"properties": map[string]any{
				"z_extra": map[string]any{"type": "string", "enum": []string{"z", "a", "m"}},
				"a_extra": map[string]any{"type": "boolean"},
				"ready": map[string]any{
					"additionalProperties": false,
					"timeout":              map[string]any{"type": "number"},
					"properties": map[string]any{
						"extra_b": map[string]any{"type": "string"},
						"extra_a": map[string]any{"type": "string"},
						"timeout": map[string]any{"type": "number"},
						"log":     map[string]any{"type": "string"},
					},
					"type": "object",
				},
				"cwd":     map[string]any{"type": "string"},
				"command": map[string]any{"type": "string"},
			},
		},
		Execute: func(sdk.Context, map[string]any) (any, error) { return nil, nil },
	}
	ext := sdk.New("edge-bash")
	ext.RegisterTool(schemaorder.Apply(customBash))
	wire := captureRegisteredWire(t, ext)["bash"].Parameters
	orders := extractObjectKeyOrders(t, wire)

	wantRoot := []string{"type", "properties", "required", "additionalProperties", "alpha_root", "zeta_root"}
	if !slices.Equal(orders["$"], wantRoot) {
		t.Errorf("root keys: got=%v want=%v", orders["$"], wantRoot)
	}
	wantProps := []string{"command", "cwd", "ready", "a_extra", "z_extra"}
	if !slices.Equal(orders["$.properties"], wantProps) {
		t.Errorf("properties keys: got=%v want=%v", orders["$.properties"], wantProps)
	}
	wantReadyProps := []string{"log", "timeout", "extra_a", "extra_b"}
	if !slices.Equal(orders["$.properties.ready.properties"], wantReadyProps) {
		t.Errorf("ready.properties keys: got=%v want=%v", orders["$.properties.ready.properties"], wantReadyProps)
	}

	// Verify enum array order is preserved verbatim ("z", "a", "m").
	var decoded map[string]any
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}
	zEnum := decoded["properties"].(map[string]any)["z_extra"].(map[string]any)["enum"].([]any)
	wantEnum := []any{"z", "a", "m"}
	if !reflect.DeepEqual(zEnum, wantEnum) {
		t.Errorf("enum order mutated: got=%v want=%v", zEnum, wantEnum)
	}
	// 4. Does not mutate caller's original Parameters map.
	if _, hasOrder := customBash.Parameters["~order"]; hasOrder {
		t.Errorf("Apply mutated caller's original Parameters map")
	}

	// 5. anyOf branches (e.g. yield.properties.type and github.properties.pr)
	// preserve omp key order inside each branch even when given plain maps.
	yieldPlain := sdk.ToolDefinition{
		Name: "yield",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"type": map[string]any{
					"description": "desc",
					"anyOf": []any{
						map[string]any{"type": "string"},
						map[string]any{
							"items":    map[string]any{"type": "string"},
							"minItems": 1,
							"type":     "array",
						},
					},
				},
			},
		},
		Execute: func(sdk.Context, map[string]any) (any, error) { return nil, nil },
	}
	yExt := sdk.New("edge-yield")
	yExt.RegisterTool(schemaorder.Apply(yieldPlain))
	yOrders := extractObjectKeyOrders(t, captureRegisteredWire(t, yExt)["yield"].Parameters)
	if got, want := yOrders["$.properties.type"], []string{"anyOf", "description"}; !slices.Equal(got, want) {
		t.Errorf("yield.properties.type keys: got=%v want=%v", got, want)
	}
	if got, want := yOrders["$.properties.type.anyOf[1]"], []string{"type", "minItems", "items"}; !slices.Equal(got, want) {
		t.Errorf("yield.properties.type.anyOf[1] keys: got=%v want=%v", got, want)
	}

	// 6. OrderedObject / TSchema input (like internal/lsp) is reordered to omp's
	// exact property key order (e.g., lsp.action has enum before type in omp).
	lspDef := sdk.ToolDefinition{
		Name:       "lsp",
		Parameters: lsp.ToolSchema(),
		Execute:    func(sdk.Context, map[string]any) (any, error) { return nil, nil },
	}
	lExt := sdk.New("edge-lsp")
	lExt.RegisterTool(schemaorder.Apply(lspDef))
	lOrders := extractObjectKeyOrders(t, captureRegisteredWire(t, lExt)["lsp"].Parameters)
	if got, want := lOrders["$.properties.action"], []string{"enum", "type"}; !slices.Equal(got, want) {
		t.Errorf("lsp.properties.action keys after Apply: got=%v want=%v", got, want)
	}
}

func TestApplyConcurrentSafe(t *testing.T) {
	// Exercise concurrent Apply + Entries calls under -race.
	done := make(chan struct{}, 8)
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			def := sdk.ToolDefinition{
				Name: "bash",
				Parameters: sdk.Schema{
					"type": "object",
					"properties": map[string]any{
						"timeout": map[string]any{"type": "number"},
						"command": map[string]any{"type": "string"},
					},
					"required": []string{"command"},
				},
			}
			for range 20 {
				out := schemaorder.Apply(def)
				if out.Parameters == nil {
					t.Errorf("Apply returned nil Parameters")
				}
			}
		}()
	}
	for range 8 {
		<-done
	}
}
