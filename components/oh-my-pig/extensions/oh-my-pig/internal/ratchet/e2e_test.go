package ratchet

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
)

func startTestPreludeBridge(t *testing.T, p eval.Prelude) (func(), string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen bridge: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if req.Name != "ratchet" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "unexpected prelude " + req.Name})
			return
		}
		res, err := p.Invoke(eval.PreludeCall{CallID: "e2e"}, req.Parameters)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		val := map[string]any{"text": res.Text}
		if res.Details != nil {
			val["details"] = res.Details
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "value": val})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return stop, "http://" + ln.Addr().String() + "/"
}

// ---------------------------------------------------------------------------
// Minimal PiG fake host for real eval.Manager Python & JS kernel integration
// ---------------------------------------------------------------------------

type ratchetFakeHost struct {
	t         *testing.T
	nc        net.Conn
	cwd       string
	sessionID string
	hasUI     bool
	settings  map[string]any
	selectAns string
	inputAns  string
	runDone   chan error
	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[string]chan json.RawMessage
	nextID    int
	sentMsgs  []map[string]any
	notified  []map[string]any
	tools     []string
}

type hostWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
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
	Notify *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"notify,omitempty"`
}

func startRatchetFakeHost(t *testing.T, ext *sdk.Extension, cwd, sessionID string, hasUI bool, settings map[string]any, activeTools []string) *ratchetFakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &ratchetFakeHost{
		t:         t,
		nc:        hostSide,
		cwd:       cwd,
		sessionID: sessionID,
		hasUI:     hasUI,
		settings:  settings,
		selectAns: "Approve",
		inputAns:  "fix it",
		runDone:   make(chan error, 1),
		pending:   map[string]chan json.RawMessage{},
		tools:     activeTools,
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()
	first := h.readFrame()
	for first.Type == "call" && first.Call != nil {
		h.answerCall(first)
		first = h.readFrame()
	}
	if first.Type != "register" {
		t.Fatalf("expected register frame, got %+v", first)
	}
	stateMap := map[string]any{"hasUI": hasUI}
	if settings != nil {
		stateMap["settings"] = settings
	}
	stateJSON, _ := json.Marshal(stateMap)
	mode := "print"
	if hasUI {
		mode = "tui"
	}
	h.writeFrame(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"session_name": "",
			"cwd":          cwd,
			"mode":         mode,
			"width":        80,
			"state":        json.RawMessage(stateJSON),
		},
	})
	go h.serve()
	t.Cleanup(func() {
		_ = h.nc.Close()
		select {
		case <-h.runDone:
		case <-time.After(3 * time.Second):
		}
	})
	return h
}

func (h *ratchetFakeHost) readFrame() hostWireFrame {
	var lenBuf [4]byte
	if _, err := io.ReadFull(h.nc, lenBuf[:]); err != nil {
		return hostWireFrame{}
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	body := make([]byte, n)
	if _, err := io.ReadFull(h.nc, body); err != nil {
		return hostWireFrame{}
	}
	var f hostWireFrame
	_ = json.Unmarshal(body, &f)
	return f
}

func (h *ratchetFakeHost) writeFrame(v any) {
	body, _ := json.Marshal(v)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(body)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, _ = h.nc.Write(lenBuf[:])
	_, _ = h.nc.Write(body)
}

func (h *ratchetFakeHost) serve() {
	for {
		f := h.readFrame()
		if f.Type == "" {
			return
		}
		switch f.Type {
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch != nil {
				if f.Response != nil && f.Response.Error != nil {
					b, _ := json.Marshal(map[string]any{"__error": f.Response.Error.Message})
					ch <- b
				} else if f.Response != nil {
					ch <- f.Response.Result
				} else {
					ch <- nil
				}
			}
		case "call":
			go h.answerCall(f)
		case "notify":
			if f.Notify != nil {
				var m map[string]any
				_ = json.Unmarshal(f.Notify.Args, &m)
				h.mu.Lock()
				switch f.Notify.Method {
				case "ui.notify":
					h.notified = append(h.notified, m)
				case "sendMessage":
					h.sentMsgs = append(h.sentMsgs, m)
				}
				h.mu.Unlock()
			}
		}
	}
}

func (h *ratchetFakeHost) answerCall(f hostWireFrame) {
	if f.Call == nil {
		return
	}
	var args map[string]any
	if len(f.Call.Args) > 0 {
		_ = json.Unmarshal(f.Call.Args, &args)
	}
	var res any
	switch f.Call.Method {
	case "sessionRead":
		method, _ := args["method"].(string)
		switch method {
		case "getSessionId":
			res = h.sessionID
		case "getSessionFile":
			res = nil
		case "getCwd":
			res = h.cwd
		default:
			res = nil
		}
	case "session.getSessionId", "getSessionID":
		res = h.sessionID
	case "session.getSessionFile", "getSessionFile":
		res = nil
	case "getSettings":
		res = h.settings
	case "getActiveTools":
		res = map[string]any{"tools": h.tools}
	case "getModelRegistryState":
		res = map[string]any{"models": []any{}, "providers": map[string]any{}}
	case "ui.select":
		h.mu.Lock()
		ans := h.selectAns
		h.mu.Unlock()
		res = map[string]any{"ok": true, "selected": ans}
	case "ui.input":
		h.mu.Lock()
		ans := h.inputAns
		h.mu.Unlock()
		res = map[string]any{"ok": true, "text": ans}
	case "sendMessage":
		h.mu.Lock()
		h.sentMsgs = append(h.sentMsgs, args)
		h.mu.Unlock()
		res = map[string]any{}
	case "ui.notify":
		h.mu.Lock()
		h.notified = append(h.notified, args)
		h.mu.Unlock()
		res = map[string]any{}
	default:
		res = map[string]any{}
	}
	raw, _ := json.Marshal(res)
	h.writeFrame(map[string]any{
		"type": "call_result",
		"id":   f.ID,
		"call_result": map[string]any{
			"result": json.RawMessage(raw),
		},
	})
}

func (h *ratchetFakeHost) roundTrip(req map[string]any) (json.RawMessage, string) {
	h.t.Helper()
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.writeFrame(map[string]any{"type": "request", "id": id, "request": req})
	select {
	case raw := <-ch:
		var errWrap struct {
			Err string `json:"__error"`
		}
		if json.Unmarshal(raw, &errWrap) == nil && errWrap.Err != "" {
			return nil, errWrap.Err
		}
		return raw, ""
	case <-time.After(30 * time.Second):
		h.t.Fatalf("timeout waiting for response to %v", req["method"])
		return nil, "timeout"
	}
}

func (h *ratchetFakeHost) callEval(language, code string) (string, string) {
	h.t.Helper()
	argv, _ := json.Marshal(map[string]any{"language": language, "code": code})
	raw, errStr := h.roundTrip(map[string]any{
		"method":       "tool_call",
		"tool":         "eval",
		"tool_call_id": "call-eval",
		"args":         json.RawMessage(argv),
	})
	if errStr != "" {
		return "", errStr
	}
	var toolRes struct {
		Content any  `json:"content"`
		IsError bool `json:"isError"`
	}
	_ = json.Unmarshal(raw, &toolRes)
	var text string
	switch c := toolRes.Content.(type) {
	case string:
		text = c
	case []any:
		var parts []string
		for _, item := range c {
			if m, ok := item.(map[string]any); ok && m["type"] == "text" {
				if s, ok := m["text"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
		text = strings.Join(parts, "\n")
	}
	if toolRes.IsError {
		return text, text
	}
	return text, ""
}

func (h *ratchetFakeHost) callCommand(name, args string) string {
	h.t.Helper()
	_, errStr := h.roundTrip(map[string]any{
		"method": "command",
		"tool":   name,
		"args":   args,
	})
	return errStr
}

// TestRatchetRealEvalKernelAndSlashCommand exercises the real internal/eval
// Python & JS kernels through the loopback HTTP bridge, including:
// - disabled refusal when ratchet.enabled is false
// - /ratchet slash command enabling the session override and delivering kickoff
// - real interactive ui.select approval dialog via PiG host
// - baseline -> keep -> revert workflow inside real Python and JS eval cells
// - approval tier enforcement (read vs write) through eval's bridge
func TestRatchetRealEvalKernelAndSlashCommand(t *testing.T) {
	repo := initTempGitRepo(t)

	eval.ResetPreludesForTests()
	t.Cleanup(eval.ResetPreludesForTests)

	prevSetting := Setting
	prevOverride := SetSessionOverride
	prevAppSetting := approval.Setting
	prevAppSession := approval.SessionSetting
	t.Cleanup(func() {
		Setting = prevSetting
		SetSessionOverride = prevOverride
		approval.Setting = prevAppSetting
		approval.SessionSetting = prevAppSession
	})

	var mu sync.Mutex
	ratchetOn := false
	approvalMode := "yolo"

	Setting = func(_ sdk.Context, key string) any {
		mu.Lock()
		defer mu.Unlock()
		if key == SettingRatchetEnabled {
			return ratchetOn
		}
		return nil
	}
	SetSessionOverride = func(_ sdk.Context, key string, value any) error {
		mu.Lock()
		defer mu.Unlock()
		if key == SettingRatchetEnabled {
			ratchetOn, _ = value.(bool)
		}
		return nil
	}
	appSettingFn := func(_ sdk.Context, key string) any {
		mu.Lock()
		defer mu.Unlock()
		if key == "tools.approvalMode" {
			return approvalMode
		}
		return nil
	}
	approval.Setting = appSettingFn
	approval.SessionSetting = appSettingFn

	ext := sdk.New("test-ratchet-real-eval")
	eval.Register(ext)
	Register(ext)

	sessID := "ratchet-real-kernel-session"
	t.Cleanup(func() { eval.DefaultManager().ShutdownSession(sessID) })
	h := startRatchetFakeHost(t, ext, repo, sessID, true, map[string]any{}, []string{"eval", "task", "ask"})

	// 1. Before /ratchet, ratchet.enabled is false -> calling __omp_prelude__("ratchet", ...) is refused.
	if _, err := exec.LookPath("python3"); err == nil {
		out, errStr := h.callEval("py", `
try:
    await _omp_prelude("ratchet", {"flow": "inbox", "action": "status"})
    print("UNEXPECTED_OK")
except Exception as e:
    print("DISABLED_ERR:", e)
`)
		if errStr != "" {
			t.Fatalf("py disabled probe error: %s", errStr)
		}
		if !strings.Contains(out, "DISABLED_ERR:") || !strings.Contains(out, "not enabled") {
			t.Fatalf("expected disabled prelude refusal, got:\n%s", out)
		}
	}

	// 2. Run /ratchet command: enables session override and queues kickoff message.
	if errStr := h.callCommand("ratchet", "inbox router in src/prompt.md"); errStr != "" {
		t.Fatalf("/ratchet command failed: %s", errStr)
	}
	mu.Lock()
	isOn := ratchetOn
	mu.Unlock()
	if !isOn {
		t.Fatalf("expected /ratchet to enable ratchet.enabled override")
	}
	h.mu.Lock()
	msgCount := len(h.sentMsgs)
	h.mu.Unlock()
	if msgCount != 1 {
		t.Fatalf("expected 1 kickoff message from /ratchet, got %d", msgCount)
	}

	// 3. Real Python eval kernel round-trip (init, plan, split, interactive ui.select approve, baseline, keep, revert).
	if _, err := exec.LookPath("python3"); err == nil {
		out, errStr := h.callEval("py", `
import json, pathlib

r = ratchet("pyflow")
await r.init(cases=["eval/cases.jsonl"], harness=["eval/run.sh"], change=["src/prompt.md"])
await r.plan(goal={"target": "quality"}, reps=1, command="sh eval/run.sh {variant}")
sp = await r.split({f"case_{i}": "billing" for i in range(10)}, seed=7)
for st in ("inputs", "grader", "plan"):
    a = await r.approve(st, question="ok?", preview=f"{st} preview")
    assert a["approved"] is True

def write_v(v, score):
    d = pathlib.Path(".omp/ratchet/pyflow") / v
    d.mkdir(parents=True, exist_ok=True)
    lines = [json.dumps({"prompt_id": f"case_{i}", "rep": 0, "grade": {"quality": score}}) for i in range(10)]
    (d / "results.jsonl").write_text("\n".join(lines) + "\n")

write_v("baseline", 0.2)
g0 = await r.gate("baseline")
write_v("v1", 0.7)
g1 = await r.gate("v1", change="improve prompt")
write_v("v2", 0.4)
g2 = await r.gate("v2", change="regressed prompt")
assert g0["decision"] == "baseline"
assert g1["decision"] == "keep"
assert g2["decision"] == "revert"
assert g2["best"]["variant"] == "v1"
print("REAL_PY_KERNEL_OK")
`)
		if errStr != "" {
			t.Fatalf("real py kernel failed: %s\nout:\n%s", errStr, out)
		}
		if !strings.Contains(out, "REAL_PY_KERNEL_OK") {
			t.Fatalf("missing REAL_PY_KERNEL_OK in output:\n%s", out)
		}
	}

	// 4. Real JS eval kernel round-trip (if bun or node 22+ is available).
	hasJS := false
	if _, err := exec.LookPath("bun"); err == nil {
		hasJS = true
	} else if _, err := exec.LookPath("node"); err == nil {
		hasJS = true
	}
	if hasJS {
		out, errStr := h.callEval("js", `
const fs = require("node:fs");
const path = require("node:path");
const r = ratchet("jsflow");
await r.init({ cases: ["eval/cases.jsonl"], harness: ["eval/run.sh"], change: ["src/prompt.md"] });
await r.plan({ goal: { target: "quality" }, reps: 1, command: "sh eval/run.sh {variant}" });
await r.split(Object.fromEntries(Array.from({ length: 10 }, (_, i) => ["case_" + i, "billing"])), { seed: 7 });
for (const st of ["inputs", "grader", "plan"]) {
	const a = await r.approve(st, { question: "ok?", preview: st + " preview" });
	if (!a.approved) throw new Error("not approved: " + st);
}
const writeV = (v, score) => {
	const d = path.join(".omp/ratchet/jsflow", v);
	fs.mkdirSync(d, { recursive: true });
	const lines = Array.from({ length: 10 }, (_, i) => JSON.stringify({ prompt_id: "case_" + i, rep: 0, grade: { quality: score } }));
	fs.writeFileSync(path.join(d, "results.jsonl"), lines.join("\n") + "\n");
};
writeV("baseline", 0.2);
const g0 = await r.gate("baseline");
writeV("v1", 0.8);
const g1 = await r.gate("v1", { change: "better js prompt" });
writeV("v2", 0.3);
const g2 = await r.gate("v2", { change: "worse js prompt" });
if (g0.decision !== "baseline" || g1.decision !== "keep" || g2.decision !== "revert" || g2.best.variant !== "v1") {
	throw new Error("unexpected decisions: " + JSON.stringify({ g0: g0.decision, g1: g1.decision, g2: g2.decision }));
}
console.log("REAL_JS_KERNEL_OK");
`)
		if errStr != "" && strings.Contains(errStr, "not available") {
			t.Logf("skipping JS kernel assertion (runtime unavailable): %s", errStr)
		} else if errStr != "" {
			t.Fatalf("real js kernel failed: %s\nout:\n%s", errStr, out)
		} else if !strings.Contains(out, "REAL_JS_KERNEL_OK") {
			t.Fatalf("missing REAL_JS_KERNEL_OK in output:\n%s", out)
		}
	}

	// 5. Approval tier enforcement through real eval bridge:
	// under always-ask mode with user denying ui.select, status (read tier) succeeds while plan (write tier) is denied.
	if _, err := exec.LookPath("python3"); err == nil {
		mu.Lock()
		approvalMode = "always-ask"
		mu.Unlock()
		h.mu.Lock()
		h.selectAns = "Deny"
		h.mu.Unlock()

		out, errStr := h.callEval("py", `
r = ratchet("pyflow")
st = await r.status()
assert st["flow"] == "pyflow"
denied = False
try:
    await r.plan(reps=2)
except Exception as e:
    denied = "denied" in str(e).lower()
assert denied, "expected write-tier plan() to be denied"
print("APPROVAL_TIER_OK")
`)
		if errStr != "" {
			t.Fatalf("approval tier test failed: %s\nout:\n%s", errStr, out)
		}
		if !strings.Contains(out, "APPROVAL_TIER_OK") {
			t.Fatalf("missing APPROVAL_TIER_OK in output:\n%s", out)
		}
	}
}
