package intent_test

// Differential tests against omp v19.1.1's real argument validation
// (`validateAgentToolArguments` -> `validateToolArguments`).
//
//   - TestOmpDifferentialReduced runs by default against the checked-in reduced
//     corpus (`testdata/omp_diff_reduced.jsonl`, 1,199 cases covering all 24
//     tools and every defect/residual class) and asserts:
//       * port rejects / omp accepts == 0 (strict tools)
//       * port accepts / omp rejects == 0 (strict tools)
//       * normalized differ (excluding undeclared-key drops and array-for-object
//         conversion to `{}` for PiG host) == 0 (strict tools)
//       * text differ == 0 (strict tools, bar Received-arguments key order)
//     To regenerate `testdata/omp_diff_reduced.jsonl` from the repo:
//       OMP_SRC=/path/to/omp-v19.1.1 DIFF_OUT=/tmp/omp-intent-diff \
//         piglets/oh-my-pig/proof/diff/intent/make-corpus.sh
//       PIG_SRC=/path/to/pig-080-link13 DIFF_OUT=/tmp/omp-intent-diff \
//         piglets/oh-my-pig/proof/diff/intent/measure.sh
//       python3 piglets/oh-my-pig/proof/diff/intent/select-reduced.py \
//         /tmp/omp-intent-diff/corpus.jsonl \
//         components/oh-my-pig/extensions/oh-my-pig/internal/intent/testdata/omp_diff_reduced.jsonl \
//         /tmp/omp-intent-diff/results/port-prepare-only.jsonl
//   - TestOmpDifferentialFull runs against an external corpus specified by
//     OMP_INTENT_DIFF_CORPUS (skipping when unset), supports
//     OMP_INTENT_DIFF_REPORT, OMP_INTENT_DIFF_OUT, OMP_INTENT_DIFF_EXAMPLES,
//     and fails when OMP_INTENT_DIFF_FAIL=1.
//   - TestAllRegisteredInjectableToolsAreTracked verifies the registration
//     contract across `ohmypig.Extension()`.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	ohmypig "github.com/MichaelKinsy/pigpen/oh-my-pig"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
)

const ompDiffBaseCases = 15922
const ompDiffReceived = "\n\nReceived arguments:\n"

type ompDiffTool struct {
	Name              string          `json:"name"`
	Parameters        json.RawMessage `json:"parameters"`
	PreparesArguments bool            `json:"prepares_arguments"`
}

type ompDiffHandler struct {
	Event     string `json:"event"`
	HandlerID int    `json:"handler_id"`
}

type ompDiffFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools    []ompDiffTool    `json:"tools"`
		Handlers []ompDiffHandler `json:"handlers"`
	} `json:"register,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
}

type ompDiffHost struct {
	t             *testing.T
	nc            net.Conn
	sessionID     string
	wmu           sync.Mutex
	mu            sync.Mutex
	pending       map[string]chan ompDiffFrame
	tools         map[string]ompDiffTool
	handlers      map[string][]int
	registerCalls []string
	next          int
}

func (h *ompDiffHost) read() (ompDiffFrame, error) {
	var lb [4]byte
	if _, err := io.ReadFull(h.nc, lb[:]); err != nil {
		return ompDiffFrame{}, err
	}
	p := make([]byte, binary.BigEndian.Uint32(lb[:]))
	if _, err := io.ReadFull(h.nc, p); err != nil {
		return ompDiffFrame{}, err
	}
	var f ompDiffFrame
	return f, json.Unmarshal(p, &f)
}

func (h *ompDiffHost) write(v any) {
	d, err := json.Marshal(v)
	if err != nil {
		h.t.Errorf("marshal frame: %v", err)
		return
	}
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(d)))
	h.wmu.Lock()
	defer h.wmu.Unlock()
	_, _ = h.nc.Write(lb[:])
	_, _ = h.nc.Write(d)
}

func (h *ompDiffHost) answer(f ompDiffFrame) {
	var result any = map[string]any{}
	switch f.Call.Method {
	case "getSessionID":
		result = map[string]any{"sessionId": h.sessionID}
	case "session.getSessionId":
		result = h.sessionID
	case "registerTool":
		var tl ompDiffTool
		if json.Unmarshal(f.Call.Args, &tl) == nil && tl.Name != "" {
			h.mu.Lock()
			h.tools[tl.Name] = tl
			h.registerCalls = append(h.registerCalls, tl.Name)
			h.mu.Unlock()
		}
	}
	if f.ID == "" {
		return
	}
	raw, _ := json.Marshal(result)
	h.write(map[string]any{"type": "call_result", "id": f.ID, "call_result": map[string]any{"result": json.RawMessage(raw)}})
}

func (h *ompDiffHost) serve() {
	for {
		f, err := h.read()
		if err != nil {
			return
		}
		switch f.Type {
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		case "call":
			if f.Call != nil {
				h.answer(f)
			}
		}
	}
}

func (h *ompDiffHost) request(req map[string]any) ompDiffFrame {
	h.mu.Lock()
	h.next++
	id := "req-" + strconv.Itoa(h.next)
	ch := make(chan ompDiffFrame, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.write(map[string]any{"type": "request", "id": id, "request": req})
	select {
	case r := <-ch:
		return r
	case <-time.After(20 * time.Second):
		h.t.Fatalf("timeout waiting for %v %v", req["method"], req["tool"])
		return ompDiffFrame{}
	}
}

func (h *ompDiffHost) emitEvent(event string, args map[string]any) {
	h.t.Helper()
	h.mu.Lock()
	ids := append([]int(nil), h.handlers[event]...)
	h.mu.Unlock()
	raw, _ := json.Marshal(args)
	for _, hid := range ids {
		resp := h.request(map[string]any{
			"method":     "event",
			"event":      event,
			"handler_id": hid,
			"args":       json.RawMessage(raw),
		})
		if resp.Response != nil && resp.Response.Error != nil {
			h.t.Fatalf("event %s error: %s", event, resp.Response.Error.Message)
		}
	}
}

func startOmpDiffExtension(t *testing.T, sessionID string) (*ompDiffHost, func()) {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, home)
	}
	t.Chdir(t.TempDir())

	ext := ohmypig.Extension()
	hostSide, extSide := net.Pipe()
	h := &ompDiffHost{
		t:         t,
		nc:        hostSide,
		sessionID: sessionID,
		pending:   map[string]chan ompDiffFrame{},
		tools:     map[string]ompDiffTool{},
		handlers:  map[string][]int{},
	}
	done := make(chan error, 1)
	go func() { done <- ext.RunWithConn(extSide) }()
	first, err := h.read()
	for err == nil && first.Type == "call" && first.Call != nil {
		h.answer(first)
		first, err = h.read()
	}
	if err != nil || first.Type != "register" || first.Register == nil {
		t.Fatalf("no register frame (err=%v type=%q)", err, first.Type)
	}
	for _, tl := range first.Register.Tools {
		h.tools[tl.Name] = tl
	}
	for _, hd := range first.Register.Handlers {
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
	}
	state, _ := json.Marshal(map[string]any{"hasUI": true})
	h.write(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          home,
			"mode":         "tui",
			"width":        80,
			"state":        json.RawMessage(state),
		},
	})
	go h.serve()
	cleanup := func() {
		h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "ompdiff"}})
		_ = h.nc.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	return h, cleanup
}

type ompDiffCase struct {
	ID      int    `json:"id"`
	Tool    string `json:"tool"`
	Lenient bool   `json:"lenient"`
	Tag     string `json:"tag"`
	Args    string `json:"args"`
	Omp     struct {
		OK         bool    `json:"ok"`
		Normalized *string `json:"normalized"`
		Error      *string `json:"error"`
		SchemaOK   *bool   `json:"schemaOk"`
	} `json:"omp"`
}

type ompDiffPort struct {
	OK         bool   `json:"ok"`
	Normalized string `json:"normalized,omitempty"`
	Error      string `json:"error,omitempty"`
	By         string `json:"by,omitempty"`
}

func ompDiffValue(text string) (any, bool) {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, false
	}
	if m, ok := v.(map[string]any); ok {
		delete(m, "i")
	}
	return v, true
}

func ompDiffSameJSON(a, b string) bool {
	va, oka := ompDiffValue(a)
	vb, okb := ompDiffValue(b)
	if !oka || !okb {
		return a == b
	}
	return reflect.DeepEqual(va, vb)
}

func ompDiffSameText(omp, port string) (issuesEqual, receivedEqual, identical bool) {
	if omp == port {
		return true, true, true
	}
	oh, ot, oSplit := strings.Cut(omp, ompDiffReceived)
	ph, pt, pSplit := strings.Cut(port, ompDiffReceived)
	if oh != ph {
		return false, false, false
	}
	if oSplit != pSplit {
		return true, false, false
	}
	var ov, pv any
	if json.Unmarshal([]byte(ot), &ov) != nil || json.Unmarshal([]byte(pt), &pv) != nil {
		return true, ot == pt, false
	}
	return true, reflect.DeepEqual(ov, pv), false
}

func ompDiffPrune(v any, schema map[string]any) any {
	if schema == nil {
		return v
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if branches, ok := schema[key].([]any); ok {
			for _, b := range branches {
				bm, _ := b.(map[string]any)
				if bm == nil {
					continue
				}
				_, isObj := v.(map[string]any)
				_, isArr := v.([]any)
				if (isObj && bm["type"] == "object") || (isArr && bm["type"] == "array") {
					return ompDiffPrune(v, bm)
				}
			}
			return v
		}
	}
	switch x := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		closed := schema["additionalProperties"] == false
		out := make(map[string]any, len(x))
		for k, val := range x {
			ps, declared := props[k].(map[string]any)
			if !declared {
				if closed && props != nil {
					continue
				}
				out[k] = val
				continue
			}
			out[k] = ompDiffPrune(val, ps)
		}
		return out
	case []any:
		items, _ := schema["items"].(map[string]any)
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = ompDiffPrune(val, items)
		}
		return out
	default:
		return v
	}
}

func ompDiffNormalizeArrayForObject(v any, schema map[string]any) any {
	if schema == nil {
		return v
	}
	if schema["type"] == "object" {
		if _, isArr := v.([]any); isArr {
			return map[string]any{}
		}
	}
	switch x := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		out := make(map[string]any, len(x))
		for k, val := range x {
			ps, _ := props[k].(map[string]any)
			out[k] = ompDiffNormalizeArrayForObject(val, ps)
		}
		return out
	case []any:
		items, _ := schema["items"].(map[string]any)
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = ompDiffNormalizeArrayForObject(val, items)
		}
		return out
	default:
		return v
	}
}

func ompDiffSameBarUndeclared(omp, port string, schema json.RawMessage) bool {
	var sm map[string]any
	if json.Unmarshal(schema, &sm) != nil {
		return false
	}
	ov, ok1 := ompDiffValue(omp)
	pv, ok2 := ompDiffValue(port)
	if !ok1 || !ok2 {
		return false
	}
	pruned := ompDiffPrune(ov, sm)
	if m, ok := pruned.(map[string]any); ok {
		delete(m, "i")
	}
	return reflect.DeepEqual(pruned, pv)
}

func ompDiffSameBarArrayForObject(omp, port string, schema json.RawMessage) bool {
	var sm map[string]any
	if json.Unmarshal(schema, &sm) != nil {
		return false
	}
	ov, ok1 := ompDiffValue(omp)
	pv, ok2 := ompDiffValue(port)
	if !ok1 || !ok2 {
		return false
	}
	pruned := ompDiffPrune(ompDiffNormalizeArrayForObject(ov, sm), sm)
	if m, ok := pruned.(map[string]any); ok {
		delete(m, "i")
	}
	return reflect.DeepEqual(pruned, pv)
}

func ompDiffClassify(c ompDiffCase, p ompDiffPort, schema json.RawMessage) string {
	ompAccepts := c.Omp.OK
	if c.Lenient && c.Omp.SchemaOK != nil && !*c.Omp.SchemaOK {
		switch {
		case !p.OK:
			return "lenient raw fallback: tool refuses in PrepareArguments (text not compared)"
		case ompDiffSameJSON(*c.Omp.Normalized, p.Normalized):
			return "lenient raw fallback: raw arguments reach the tool (equal)"
		default:
			return "lenient raw fallback: port changed the raw arguments"
		}
	}
	switch {
	case ompAccepts && p.OK:
		if ompDiffSameJSON(*c.Omp.Normalized, p.Normalized) {
			return "equal (both accept, same normalized arguments)"
		}
		if ompDiffSameBarUndeclared(*c.Omp.Normalized, p.Normalized, schema) {
			return "normalized differ only by undeclared keys the port drops (omp keeps them; PiG's host rejects them)"
		}
		if ompDiffSameBarArrayForObject(*c.Omp.Normalized, p.Normalized, schema) {
			return "normalized differ only by array-for-object converted to {} for PiG host"
		}
		return "normalized differ (both accept)"
	case ompAccepts && !p.OK:
		return "PORT REJECTS / omp accepts"
	case !ompAccepts && p.OK:
		return "port accepts / omp rejects"
	default:
		issues, received, _ := ompDiffSameText(*c.Omp.Error, p.Error)
		switch {
		case issues && received:
			return "equal (both reject, same text bar Received-arguments key order)"
		case issues:
			return "text differ (both reject; only Received arguments content differs)"
		default:
			return "text differ (both reject; issue lines differ)"
		}
	}
}

func runOmpDifferentialCorpus(t *testing.T, corpus string, strictFail bool) {
	t.Helper()
	h, cleanup := startOmpDiffExtension(t, "sess-ompdiff")
	defer cleanup()

	in, err := os.Open(corpus)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	var out *bufio.Writer
	if path := os.Getenv("OMP_INTENT_DIFF_OUT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		out = bufio.NewWriter(f)
		defer out.Flush()
	}

	examples := 3
	if n, err := strconv.Atoi(os.Getenv("OMP_INTENT_DIFF_EXAMPLES")); err == nil {
		examples = n
	}

	type bucket struct {
		n         int
		identical int
		samples   []string
	}
	counts := map[string]map[string]*bucket{}
	totals := map[string]int{}

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 1<<28)
	for sc.Scan() {
		var c ompDiffCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("corpus line: %v", err)
		}
		h.mu.Lock()
		tl, registered := h.tools[c.Tool]
		h.mu.Unlock()
		if !registered {
			continue
		}
		section := "base"
		if c.ID >= ompDiffBaseCases {
			section = "extra"
		}
		if c.Lenient {
			section += " / lenient tools"
		} else {
			section += " / strict tools"
		}
		p := ompDiffPort{OK: true}
		args := json.RawMessage(c.Args)
		if tl.PreparesArguments {
			resp := h.request(map[string]any{
				"method": "tool_prepare_arguments",
				"tool":   c.Tool,
				"args":   json.RawMessage(c.Args),
			})
			switch {
			case resp.Response != nil && resp.Response.Error != nil:
				p = ompDiffPort{Error: resp.Response.Error.Message, By: "prepare"}
			case resp.Response != nil && len(resp.Response.Result) > 0 && string(bytes.TrimSpace(resp.Response.Result)) != "null":
				args = resp.Response.Result
			}
		}
		if p.OK {
			p.Normalized = string(args)
		}
		class := ompDiffClassify(c, p, tl.Parameters)
		if counts[section] == nil {
			counts[section] = map[string]*bucket{}
		}
		b := counts[section][class]
		if b == nil {
			b = &bucket{}
			counts[section][class] = b
		}
		b.n++
		totals[section]++
		if !p.OK && !c.Omp.OK {
			if _, _, identical := ompDiffSameText(*c.Omp.Error, p.Error); identical {
				b.identical++
			}
		}
		if !strings.HasPrefix(class, "equal") && !strings.HasSuffix(class, "(equal)") && len(b.samples) < examples {
			ompSide := "ERR " + ompDiffFirstIssue(c.Omp.Error)
			if c.Omp.OK {
				ompSide = "OK  " + ompDiffClip(*c.Omp.Normalized, 180)
			}
			portSide := "ERR[" + p.By + "] " + ompDiffFirstIssue(&p.Error)
			if p.OK {
				portSide = "OK  " + ompDiffClip(p.Normalized, 180)
			}
			b.samples = append(b.samples, fmt.Sprintf("#%d %s [%s]\n  args: %s\n  omp : %s\n  port: %s",
				c.ID, c.Tool, c.Tag, ompDiffClip(c.Args, 180), ompSide, portSide))
		}
		if out != nil {
			line, _ := json.Marshal(map[string]any{"id": c.ID, "tool": c.Tool, "tag": c.Tag, "section": section, "class": class, "port": p})
			_, _ = out.Write(line)
			_ = out.WriteByte('\n')
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	var report strings.Builder
	fmt.Fprintf(&report, "\nomp v19.1.1 differential: %s\n", corpus)
	sections := make([]string, 0, len(counts))
	for s := range counts {
		sections = append(sections, s)
	}
	sort.Strings(sections)
	strictFailing := 0
	for _, s := range sections {
		fmt.Fprintf(&report, "\n== %s: %d cases\n", s, totals[s])
		classes := make([]string, 0, len(counts[s]))
		for c := range counts[s] {
			classes = append(classes, c)
		}
		sort.Slice(classes, func(i, j int) bool {
			ei, ej := strings.HasPrefix(classes[i], "equal"), strings.HasPrefix(classes[j], "equal")
			if ei != ej {
				return ei
			}
			return classes[i] < classes[j]
		})
		for _, c := range classes {
			b := counts[s][c]
			extra := ""
			if strings.Contains(c, "both reject, same text") {
				extra = fmt.Sprintf(" (%d byte-identical)", b.identical)
			}
			fmt.Fprintf(&report, "  %7d  %s%s\n", b.n, c, extra)
			if strings.Contains(s, "strict") &&
				!strings.HasPrefix(c, "equal") &&
				!strings.HasPrefix(c, "normalized differ only by undeclared keys") &&
				!strings.HasPrefix(c, "normalized differ only by array-for-object") {
				strictFailing += b.n
			}
		}
		for _, c := range classes {
			for _, sample := range counts[s][c].samples {
				fmt.Fprintf(&report, "      e.g. [%s] %s\n", c, sample)
			}
		}
	}
	t.Log(report.String())
	if path := os.Getenv("OMP_INTENT_DIFF_REPORT"); path != "" {
		if err := os.WriteFile(path, []byte(report.String()), 0o644); err != nil {
			t.Errorf("write report: %v", err)
		}
	}
	if strictFail && strictFailing > 0 {
		t.Fatalf("%d strict-tool cases differ from omp v19.1.1", strictFailing)
	}
}

func ompDiffClip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func ompDiffFirstIssue(s *string) string {
	if s == nil {
		return "<nil>"
	}
	head, _, _ := strings.Cut(*s, ompDiffReceived)
	if _, rest, ok := strings.Cut(head, "\n"); ok && strings.HasPrefix(head, "Validation failed for tool") {
		head = rest
	}
	return ompDiffClip(strings.ReplaceAll(head, "\n", " | "), 260)
}

func TestOmpDifferentialReduced(t *testing.T) {
	corpus, err := filepath.Abs(filepath.Join("testdata", "omp_diff_reduced.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	runOmpDifferentialCorpus(t, corpus, true)
}

func TestOmpDifferentialFull(t *testing.T) {
	corpus := os.Getenv("OMP_INTENT_DIFF_CORPUS")
	if corpus == "" {
		t.Skip("OMP_INTENT_DIFF_CORPUS unset")
	}
	runOmpDifferentialCorpus(t, corpus, os.Getenv("OMP_INTENT_DIFF_FAIL") == "1")
}

// TestAllRegisteredInjectableToolsAreTracked verifies the registration contract
// across the real fused extension:
//  1. After `session_start`, every injectable (non-omit) tool registered on
//     `ohmypig.Extension()` is tracked in `intent.TrackedToolNames(sid)`.
//  2. The first `before_agent_start` after `session_start` with no setting
//     change issues 0 `registerTool` host calls.
//  3. Shutting down the session releases its tracked names.
func TestAllRegisteredInjectableToolsAreTracked(t *testing.T) {
	intent.ResetForTests()
	t.Cleanup(intent.ResetForTests)
	t.Setenv("PI_NO_INTENT", "")
	t.Setenv("PI_INTENT_TRACING", "")

	const sid = "sess-track-audit"
	h, cleanup := startOmpDiffExtension(t, sid)
	defer cleanup()

	h.emitEvent("session_start", map[string]any{"reason": "startup"})

	// 1. Every injectable tool registered on the extension must be tracked.
	tracked := intent.TrackedToolNames(sid)
	h.mu.Lock()
	registeredNames := make([]string, 0, len(h.tools))
	for name := range h.tools {
		registeredNames = append(registeredNames, name)
	}
	h.mu.Unlock()
	sort.Strings(registeredNames)

	for _, name := range registeredNames {
		if intent.ModeFor(name) == intent.ModeOmit {
			continue
		}
		if !slices.Contains(tracked, name) {
			t.Errorf("injectable tool %q registered on ohmypig.Extension() is NOT tracked for session %q (tracked=%v)", name, sid, tracked)
		}
	}

	// 2. First before_agent_start with unchanged state must issue 0 registerTool calls.
	h.mu.Lock()
	h.registerCalls = nil
	h.mu.Unlock()

	h.emitEvent("before_agent_start", map[string]any{"systemPromptOptions": map[string]any{}})

	h.mu.Lock()
	noopCalls := slices.Clone(h.registerCalls)
	h.mu.Unlock()
	if len(noopCalls) != 0 {
		t.Errorf("first before_agent_start with unchanged intentTracing issued %d registerTool calls (%v), want 0", len(noopCalls), noopCalls)
	}

	// 3. No session-global leak: shutting down the session clears tracked names.
	h.emitEvent("session_shutdown", map[string]any{"reason": "exit"})
	if after := intent.TrackedToolNames(sid); len(after) != 0 {
		t.Errorf("TrackedToolNames(%q) after session_shutdown = %v, want empty", sid, after)
	}
}
