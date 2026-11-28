// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Michael Kinsy

package intent_test

// Differential against omp v19.1.1's real argument validation.
//
// The corpus (OMP_INTENT_DIFF_CORPUS, a JSONL file written by proof/diff/intent/oracle.ts) holds, per
// case, the raw arguments a model sent and what omp's `validateAgentToolArguments`
// (packages/agent/src/tool-arguments.ts:23, called from agent-loop.ts:3226) did with them for the real tool:
// the arguments it hands the tool, or the error text. This test sends the same arguments to the tool
// definitions the extension REALLY registers (ohmypig.Extension(), over the SDK wire: `tool_prepare_arguments`),
// and prints how the results compare. It measures; it fails only with OMP_INTENT_DIFF_FAIL=1.
//
//	OMP_INTENT_DIFF_CORPUS    corpus path (the test is skipped without it)
//	OMP_INTENT_DIFF_REPORT    optional: also write the printed report to this file (test-ext.sh runs without -v,
//	                          so a passing test's log is not shown)
//	OMP_INTENT_DIFF_OUT       optional: write one JSON line per case with the port's result and its class
//	OMP_INTENT_DIFF_EXAMPLES  examples printed per non-equal class (default 3)
//	OMP_INTENT_DIFF_FAIL      "1": fail when a strict-tool class other than "equal" is not empty
//
// PiG's host validator runs after PrepareArguments and is not part of the extension module. Built with
// `-tags pighost` (see zz_ompdiff_host_test.go and run-port.sh --host) the test also runs PiG's real
// `ai.ValidateToolArgumentsJSON` on what PrepareArguments returned, which is what a model finally sees.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	ohmypig "github.com/MichaelKinsy/pigpen/oh-my-pig"
)

// ompDiffHostValidate is PiG's host validator when the test is built with -tags pighost, else nil.
var ompDiffHostValidate func(toolName string, schema, args json.RawMessage) (json.RawMessage, error)

// ompDiffBaseCases is the number of cases in the base corpus the review measured; later ids are the extra classes.
const ompDiffBaseCases = 15922

type ompDiffTool struct {
	Name              string          `json:"name"`
	Parameters        json.RawMessage `json:"parameters"`
	PreparesArguments bool            `json:"prepares_arguments"`
}

type ompDiffFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools []ompDiffTool `json:"tools"`
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
	t       *testing.T
	nc      net.Conn
	wmu     sync.Mutex
	mu      sync.Mutex
	pending map[string]chan ompDiffFrame
	tools   map[string]ompDiffTool
	next    int
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
		result = map[string]any{"sessionId": "sess-ompdiff"}
	case "session.getSessionId":
		result = "sess-ompdiff"
	case "registerTool":
		var tl ompDiffTool
		if json.Unmarshal(f.Call.Args, &tl) == nil && tl.Name != "" {
			h.mu.Lock()
			h.tools[tl.Name] = tl
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
	// By names who refused: "prepare" (the tool's PrepareArguments chain, the intent wrapper included) or "host" (PiG's validator).
	By string `json:"by,omitempty"`
}

const ompDiffReceived = "\n\nReceived arguments:\n"

// ompDiffValue decodes JSON text for comparison and drops the harness field `i` at the root: omp's loop
// strips it before validation (agent-loop.ts:3202-3206) and the port's wrapper re-attaches it for the host.
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

// ompDiffSameText reports whether two validation errors are equal except for the key order inside
// `Received arguments:` (PiG's SDK decodes PrepareArguments input into a map: extension.go:1662-1670),
// and whether they are byte-identical.
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

// ompDiffPrune removes from v the keys a closed object schema (`additionalProperties: false`) does not
// declare, at any depth. omp's own schemas are open for most tools, so it keeps such keys
// (validation.ts:1775-1778); PiG's host rejects them, so the port has to drop them. Pruning omp's result
// shows whether that is the ONLY difference.
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

func ompDiffClassify(c ompDiffCase, p ompDiffPort, schema json.RawMessage) string {
	ompAccepts := c.Omp.OK
	if c.Lenient && c.Omp.SchemaOK != nil && !*c.Omp.SchemaOK {
		// omp's validator refused, so the lenient tool gets its raw arguments and refuses in execute() in its
		// own words (tool-arguments.ts:32-42). That text is the tool's, not in the corpus.
		switch {
		case !p.OK && p.By == "host":
			return "lenient raw fallback: PiG host refuses (PiG wording, tool never asked)"
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
		return "normalized differ (both accept)"
	case ompAccepts && !p.OK:
		if p.By == "host" {
			return "PORT REJECTS / omp accepts (refused by PiG host, PiG wording)"
		}
		return "PORT REJECTS / omp accepts"
	case !ompAccepts && p.OK:
		return "port accepts / omp rejects"
	default:
		if p.By == "host" {
			return "text differ (both reject; answered by PiG host in PiG wording)"
		}
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

func TestZZOmpDifferential(t *testing.T) {
	corpus := os.Getenv("OMP_INTENT_DIFF_CORPUS")
	if corpus == "" {
		t.Skip("OMP_INTENT_DIFF_CORPUS unset")
	}
	home := t.TempDir()
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, home)
	}
	t.Chdir(t.TempDir())

	ext := ohmypig.Extension()
	hostSide, extSide := net.Pipe()
	h := &ompDiffHost{t: t, nc: hostSide, pending: map[string]chan ompDiffFrame{}, tools: map[string]ompDiffTool{}}
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
	state, _ := json.Marshal(map[string]any{"hasUI": true})
	h.write(map[string]any{"type": "ready", "ready": map[string]any{"session_name": "", "cwd": home, "mode": "tui", "width": 80, "state": json.RawMessage(state)}})
	go h.serve()
	defer func() {
		h.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "ompdiff"}})
		_ = h.nc.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}()

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
	// section -> class -> bucket
	counts := map[string]map[string]*bucket{}
	totals := map[string]int{}
	missing := map[string]int{}
	hostAnswered := 0

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
		section := "base"
		if c.ID >= ompDiffBaseCases {
			section = "extra"
		}
		if c.Lenient {
			section += " / lenient tools"
		} else {
			section += " / strict tools"
		}
		if !registered {
			missing[c.Tool]++
			continue
		}
		p := ompDiffPort{OK: true}
		args := json.RawMessage(c.Args)
		if tl.PreparesArguments {
			resp := h.request(map[string]any{"method": "tool_prepare_arguments", "tool": c.Tool, "args": json.RawMessage(c.Args)})
			switch {
			case resp.Response != nil && resp.Response.Error != nil:
				p = ompDiffPort{Error: resp.Response.Error.Message, By: "prepare"}
			case resp.Response != nil && len(resp.Response.Result) > 0 && string(bytes.TrimSpace(resp.Response.Result)) != "null":
				args = resp.Response.Result
			}
		}
		if p.OK && ompDiffHostValidate != nil {
			validated, herr := ompDiffHostValidate(c.Tool, tl.Parameters, args)
			if herr != nil {
				p = ompDiffPort{Error: herr.Error(), By: "host"}
				hostAnswered++
			} else if len(validated) > 0 {
				args = validated
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
				ompSide = "OK  " + ompDiffClip(*c.Omp.Normalized, 220)
			}
			portSide := "ERR[" + p.By + "] " + ompDiffFirstIssue(&p.Error)
			if p.OK {
				portSide = "OK  " + ompDiffClip(p.Normalized, 220)
			}
			b.samples = append(b.samples, fmt.Sprintf("#%d %s [%s]\n          args: %s\n          omp : %s\n          port: %s", c.ID, c.Tool, c.Tag, ompDiffClip(c.Args, 220), ompSide, portSide))
		}
		if out != nil {
			line, _ := json.Marshal(map[string]any{"id": c.ID, "tool": c.Tool, "tag": c.Tag, "section": section, "class": class, "port": p})
			out.Write(line)
			out.WriteByte('\n')
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	var report strings.Builder
	stage := "PrepareArguments only (wrapper + the tool's own PrepareArguments); PiG's host validator NOT run"
	if ompDiffHostValidate != nil {
		stage = "PrepareArguments, then PiG's real host validator (ai.ValidateToolArgumentsJSON)"
	}
	fmt.Fprintf(&report, "\nomp v19.1.1 differential: %s\nstage: %s\n", corpus, stage)
	sections := make([]string, 0, len(counts))
	for s := range counts {
		sections = append(sections, s)
	}
	sort.Strings(sections)
	failing := 0
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
				extra = fmt.Sprintf("   (%d byte-identical)", b.identical)
			}
			fmt.Fprintf(&report, "  %7d  %s%s\n", b.n, c, extra)
			if !strings.HasPrefix(c, "equal") && strings.Contains(s, "strict") {
				failing += b.n
			}
		}
		for _, c := range classes {
			for _, sample := range counts[s][c].samples {
				fmt.Fprintf(&report, "      e.g. [%s] %s\n", c, sample)
			}
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(&report, "\ntools in the corpus that the extension did not register (cases skipped): %v\n", missing)
	}
	if ompDiffHostValidate != nil {
		fmt.Fprintf(&report, "\ncases answered by PiG's host validator instead of the port: %d\n", hostAnswered)
	}
	t.Log(report.String())
	if path := os.Getenv("OMP_INTENT_DIFF_REPORT"); path != "" {
		if err := os.WriteFile(path, []byte(report.String()), 0o644); err != nil {
			t.Errorf("write report: %v", err)
		}
	}
	if os.Getenv("OMP_INTENT_DIFF_FAIL") == "1" && failing > 0 {
		t.Fatalf("%d strict-tool cases differ from omp v19.1.1", failing)
	}
}

func ompDiffClip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func ompDiffFirstIssue(msg *string) string {
	if msg == nil {
		return ""
	}
	head, _, _ := strings.Cut(*msg, ompDiffReceived)
	if _, rest, ok := strings.Cut(head, "\n"); ok && strings.HasPrefix(head, "Validation failed for tool") {
		head = rest
	}
	return ompDiffClip(strings.ReplaceAll(head, "\n", " | "), 260)
}
