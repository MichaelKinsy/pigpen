// Command basket runs the perf basket against a kagent agent through the
// public A2A gateway (JSON-RPC over HTTP, SSE streaming) and records timings.
//
// One iteration (one session):
//
//	turn 1  "[run-<id>] [basket] ..."  -> shell call, MCP probe call, BASKET-DONE <sha>
//	        (when the agent requires approval, input-required is answered)
//	idle    -idle-wait (lets the platform suspend the actor)
//	turn 2  "[recall]"                 -> RECALL [run-<id>]  (proves session memory / resume)
//
// Output is JSON on stdout or -out. Phase windows are epoch seconds so a memory
// collector can be joined to them (see collect.py report).
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	hitlURI       = "https://kagent.dev/extensions/hitl/v1"
	usageURI      = "https://kagent.dev/extensions/usage/v1"
	partTypeKey   = "kagent.dev/a2a/part-type"
	thoughtKey    = "kagent.dev/a2a/thought"
	stateInput    = "TASK_STATE_INPUT_REQUIRED"
	stateComplete = "TASK_STATE_COMPLETED"
)

type config struct {
	BaseURL     string
	Agent       string // namespace/name
	Headers     []string
	Token       string
	Repeat      int
	Warmup      int
	Concurrency int
	Approve     string // auto, reject, none
	IdleWait    time.Duration
	Hold        time.Duration
	Turn2       bool
	ExtraTurns  int
	Timeout     time.Duration
	Prompt      string
	Scenario    string
}

type phase struct {
	Name  string  `json:"name"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type toolEvent struct {
	Kind string  `json:"kind"`
	Name string  `json:"name"`
	AtMs float64 `json:"at_ms"`
}

type turnResult struct {
	Name           string         `json:"name"`
	FirstEventMs   float64        `json:"first_event_ms"`
	TaskCreatedMs  float64        `json:"task_created_ms"`
	FirstTextMs    float64        `json:"first_text_ms"`
	InputRequired  float64        `json:"input_required_ms,omitempty"`
	TotalMs        float64        `json:"total_ms"`
	Events         int            `json:"events"`
	SendRetries    int            `json:"send_retries,omitempty"`
	FinalState     string         `json:"final_state"`
	Text           string         `json:"text"`
	Tools          []toolEvent    `json:"tools,omitempty"`
	Usage          map[string]any `json:"usage,omitempty"`
	TaskID         string         `json:"task_id"`
	ContextID      string         `json:"context_id"`
	approvalNeeded *approvalRequest
	art            map[string]*strings.Builder // artifact id -> text (chunks append; the terminal task repeats them whole)
	artOrder       []string
	toolSeen       map[string]bool
}

type approvalRequest struct {
	Tools []struct {
		ID   string         `json:"id"`
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	} `json:"tools"`
}

type runResult struct {
	Index    int                `json:"index"`
	Warmup   bool               `json:"warmup"`
	OK       bool               `json:"ok"`
	Error    string             `json:"error,omitempty"`
	Checks   map[string]bool    `json:"checks"`
	Phases   []phase            `json:"phases"`
	Turns    []turnResult       `json:"turns"`
	Timings  map[string]float64 `json:"timings_ms"`
	Marker   string             `json:"marker"`
	Approval string             `json:"approval,omitempty"`
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type client struct {
	cfg  config
	http *http.Client
}

func (c *client) post(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.cfg.BaseURL, "/")+"/agents/"+c.cfg.Agent, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", "1.0")
	req.Header.Set("A2A-Extensions", hitlURI+","+usageURI)
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	for _, h := range c.cfg.Headers {
		k, v, _ := strings.Cut(h, ":")
		req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	return c.http.Do(req)
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asStr(v any) string         { s, _ := v.(string); return s }

// sendStream sends one message with SendStreamingMessage and consumes the SSE stream.
func (c *client) sendStream(ctx context.Context, name string, msg map[string]any) (*turnResult, error) {
	tr := &turnResult{Name: name}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": newID(), "method": "SendStreamingMessage",
		"params": map[string]any{"message": msg}})
	var resp *http.Response
	var t0 time.Time
	for attempt := 0; ; attempt++ {
		t0 = time.Now()
		var err error
		resp, err = c.post(ctx, body, true)
		if err != nil {
			return tr, err
		}
		if resp.StatusCode == 200 && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			break
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		// a rejected send with no dispatch may be retried with the same message (a2a-gateway.md)
		if (strings.Contains(string(raw), "KAGENT_SEND_NOT_ACCEPTED") || strings.Contains(string(raw), "input was not accepted")) && attempt < 100 {
			tr.SendRetries++
			if tr.SendRetries == 1 {
				livef(ms(time.Since(t0)), "retry", "gateway: session not available yet, retrying the same message")
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return tr, fmt.Errorf("send %s: HTTP %d: %s", name, resp.StatusCode, trunc(string(raw), 300))
	}
	defer resp.Body.Close()
	tr.art, tr.toolSeen = map[string]*strings.Builder{}, map[string]bool{}
	var lastStatusText string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		payload := data.String()
		data.Reset()
		return c.onEvent(tr, t0, payload, &lastStatusText)
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return tr, err
			}
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := flush(); err != nil {
		return tr, err
	}
	if err := sc.Err(); err != nil {
		return tr, err
	}
	tr.TotalMs = ms(time.Since(t0))
	var sb strings.Builder
	for _, id := range tr.artOrder {
		sb.WriteString(tr.art[id].String())
	}
	all := sb.String()
	if lastStatusText != "" {
		all += "\n" + lastStatusText
	}
	tr.Text = all
	return tr, nil
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

var (
	traceMu sync.Mutex
	traceF  *os.File
)

func traceEvent(turn string, atMs float64, payload string) {
	if traceF == nil {
		return
	}
	traceMu.Lock()
	fmt.Fprintf(traceF, "%s\t%.1f\t%s\n", turn, atMs, payload)
	traceMu.Unlock()
}

func (c *client) onEvent(tr *turnResult, t0 time.Time, payload string, lastStatusText *string) error {
	at := ms(time.Since(t0))
	traceEvent(tr.Name, at, payload)
	tr.Events++
	if tr.Events == 1 {
		tr.FirstEventMs = at
	}
	var env struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return fmt.Errorf("bad SSE payload: %v", err)
	}
	if env.Error != nil {
		return fmt.Errorf("JSON-RPC error: %s", trunc(payload, 400))
	}
	res := env.Result
	// artifactParts folds one artifact update into the turn. A chunk with append=true extends the artifact; any other
	// update (including the whole artifacts of the terminal task) replaces it, so repeats never duplicate text.
	artifactParts := func(id string, appendMode bool, parts any) {
		b, ok := tr.art[id]
		if !ok {
			b = &strings.Builder{}
			tr.art[id] = b
			tr.artOrder = append(tr.artOrder, id)
		} else if !appendMode {
			b.Reset()
		}
		collectPartsInto(tr, at, parts, b, lastStatusText)
	}
	collectParts := func(parts any) { collectPartsInto(tr, at, parts, nil, lastStatusText) }
	setIDs := func(m map[string]any) {
		if v := asStr(m["taskId"]); v != "" {
			tr.TaskID = v
		}
		if v := asStr(m["id"]); v != "" && m["status"] != nil && m["contextId"] != nil {
			tr.TaskID = v
		}
		if v := asStr(m["contextId"]); v != "" {
			tr.ContextID = v
		}
	}
	status := func(m map[string]any) {
		st := asMap(m["status"])
		state := asStr(st["state"])
		if state != "" {
			if state != tr.FinalState && state != stateInput && state != stateComplete {
				livef(at, "state", "%s", strings.ToLower(strings.TrimPrefix(state, "TASK_STATE_")))
			}
			tr.FinalState = state
		}
		msg := asMap(st["message"])
		if msg != nil {
			collectParts(msg["parts"])
		}
		if state == stateInput {
			tr.InputRequired = at
			if msg != nil {
				if raw := asMap(asMap(msg["metadata"])[hitlURI]); raw != nil && asStr(raw["type"]) == "tool_approval_request" {
					b, _ := json.Marshal(raw)
					var ar approvalRequest
					if json.Unmarshal(b, &ar) == nil && len(ar.Tools) > 0 {
						tr.approvalNeeded = &ar
						livef(at, "input-required", "approval needed for %s %s", ar.Tools[0].Name, short(ar.Tools[0].Args, 60))
					}
				}
			}
		}
		if u := asMap(asMap(m["metadata"])[usageURI]); u != nil {
			tr.Usage = u
		}
		if u := asMap(asMap(st["message"])["metadata"]); u != nil && tr.Usage == nil {
			if uu := asMap(u[usageURI]); uu != nil {
				tr.Usage = uu
			}
		}
	}
	switch {
	case res["task"] != nil:
		t := asMap(res["task"])
		setIDs(t)
		if tr.TaskCreatedMs == 0 {
			tr.TaskCreatedMs = at
		}
		status(t)
		for _, a := range asSliceAny(t["artifacts"]) {
			am := asMap(a)
			artifactParts(asStr(am["artifactId"]), false, am["parts"])
		}
	case res["statusUpdate"] != nil:
		u := asMap(res["statusUpdate"])
		setIDs(u)
		status(u)
	case res["artifactUpdate"] != nil:
		u := asMap(res["artifactUpdate"])
		setIDs(u)
		art := asMap(u["artifact"])
		app, _ := u["append"].(bool)
		artifactParts(asStr(art["artifactId"]), app, art["parts"])
	case res["message"] != nil:
		m := asMap(res["message"])
		setIDs(m)
		collectParts(m["parts"])
		tr.FinalState = stateComplete
	}
	return nil
}

// collectPartsInto records tool activity (once per call/response id) and text. Text goes into b for artifacts,
// or into *statusText (last wins) for status messages when b is nil. Thought parts are skipped.
func collectPartsInto(tr *turnResult, at float64, parts any, b *strings.Builder, statusText *string) {
	for _, pv := range asSliceAny(parts) {
		p := asMap(pv)
		meta := asMap(p["metadata"])
		switch kind := asStr(meta[partTypeKey]); kind {
		case "function_call", "function_response":
			d := asMap(p["data"])
			key := kind + "|" + asStr(d["id"]) + "|" + asStr(d["name"])
			if !tr.toolSeen[key] {
				tr.toolSeen[key] = true
				tr.Tools = append(tr.Tools, toolEvent{Kind: kind, Name: asStr(d["name"]), AtMs: at})
				if kind == "function_call" {
					livef(at, "tool call", "%s %s", asStr(d["name"]), short(d["args"], 70))
				} else {
					livef(at, "tool result", "%s -> %s", asStr(d["name"]), short(d["response"], 60))
				}
			}
			continue
		}
		if th, _ := meta[thoughtKey].(bool); th {
			continue
		}
		if t, ok := p["text"].(string); ok && t != "" {
			if tr.FirstTextMs == 0 {
				tr.FirstTextMs = at
			}
			if b == nil {
				*statusText = t
			} else {
				b.WriteString(t)
			}
		}
	}
}

func asSliceAny(v any) []any { s, _ := v.([]any); return s }

func userMessage(text, contextID, taskID string) map[string]any {
	m := map[string]any{"messageId": newID(), "role": "ROLE_USER", "parts": []any{map[string]any{"text": text}}}
	if contextID != "" {
		m["contextId"] = contextID
	}
	if taskID != "" {
		m["taskId"] = taskID
	}
	return m
}

func approvalMessage(ar *approvalRequest, approve bool, contextID, taskID string) map[string]any {
	var items []any
	for _, t := range ar.Tools {
		a := map[string]any{"id": t.ID, "approved": approve}
		if !approve {
			a["rejection_reason"] = "rejected by basket"
		}
		items = append(items, a)
	}
	text := "approved"
	if !approve {
		text = "rejected"
	}
	m := userMessage(text, contextID, taskID)
	m["extensions"] = []any{hitlURI}
	m["metadata"] = map[string]any{hitlURI: map[string]any{"type": "tool_approval_response", "approvals": items}}
	return m
}

func (c *client) agentCard(ctx context.Context) (float64, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.cfg.BaseURL, "/")+"/agents/"+c.cfg.Agent+"/.well-known/agent-card.json", nil)
	req.Header.Set("A2A-Version", "1.0")
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	for _, h := range c.cfg.Headers {
		k, v, _ := strings.Cut(h, ":")
		req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	t0 := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("agent card: HTTP %d", resp.StatusCode)
	}
	return ms(time.Since(t0)), nil
}

var doneRe = regexp.MustCompile(`BASKET-DONE [0-9a-f]{64}`)

// runTurn sends a message and, if the task pauses for approval, answers it.
func (c *client) runTurn(ctx context.Context, rr *runResult, name string, msg map[string]any) (*turnResult, error) {
	start := now()
	tr, err := c.sendStream(ctx, name, msg)
	end := now()
	rr.Phases = append(rr.Phases, phase{name, start, end})
	rr.Turns = append(rr.Turns, *tr)
	if err != nil {
		return tr, err
	}
	hops := 0
	for tr.approvalNeeded != nil && tr.FinalState == stateInput && hops < 5 {
		hops++
		if c.cfg.Approve == "none" {
			return tr, nil
		}
		approve := c.cfg.Approve != "reject"
		rr.Approval = map[bool]string{true: "approved", false: "rejected"}[approve]
		ar := tr.approvalNeeded
		hitlStart := now()
		rr.Phases = append(rr.Phases, phase{name + "_input_required", end, hitlStart})
		next, err := c.sendStream(ctx, name+"_approval", approvalMessage(ar, approve, tr.ContextID, tr.TaskID))
		end = now()
		rr.Phases = append(rr.Phases, phase{name + "_approval", hitlStart, end})
		if next.TaskID == "" {
			next.TaskID = tr.TaskID
		}
		if next.ContextID == "" {
			next.ContextID = tr.ContextID
		}
		next.Text = tr.Text + "\n" + next.Text
		next.Tools = append(tr.Tools, next.Tools...)
		next.InputRequired = tr.InputRequired
		// timings of the first stream (cold start, first text) stay with the turn; the approval stream has its own phase
		next.FirstEventMs, next.TaskCreatedMs = tr.FirstEventMs, tr.TaskCreatedMs
		if tr.FirstTextMs > 0 {
			next.FirstTextMs = tr.FirstTextMs
		}
		rr.Turns = append(rr.Turns, *next)
		if err != nil {
			return next, err
		}
		tr = next
	}
	return tr, nil
}

func (c *client) iteration(ctx context.Context, idx int, warmup bool) *runResult {
	rr := &runResult{Index: idx, Warmup: warmup, Checks: map[string]bool{}, Timings: map[string]float64{}}
	rr.Marker = "[run-" + newID() + "]"
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	fail := func(err error) *runResult { rr.Error = err.Error(); return rr }

	prompt := fmt.Sprintf("%s [%s] %s", rr.Marker, c.cfg.Scenario, c.cfg.Prompt)
	t1, err := c.runTurn(ctx, rr, "turn1", userMessage(prompt, "", ""))
	if err != nil {
		return fail(err)
	}
	rr.Timings["turn1_first_event"] = t1.FirstEventMs
	rr.Timings["turn1_task_created"] = t1.TaskCreatedMs
	rr.Timings["turn1_first_text"] = t1.FirstTextMs
	rr.Timings["turn1_total"] = rr.Phases[0].End*1000 - rr.Phases[0].Start*1000
	if t1.InputRequired > 0 {
		rr.Timings["turn1_input_required"] = t1.InputRequired
	}
	for _, p := range rr.Phases {
		if p.Name == "turn1_approval" {
			rr.Timings["approval_to_done"] = (p.End - p.Start) * 1000
		}
	}
	end := rr.Phases[0].End
	for _, p := range rr.Phases {
		if strings.HasPrefix(p.Name, "turn1") && p.End > end {
			end = p.End
		}
	}
	rr.Timings["turn1_end_to_end"] = (end - rr.Phases[0].Start) * 1000
	switch c.cfg.Scenario {
	case "basket":
		rr.Checks["turn1_done"] = doneRe.MatchString(t1.Text)
	case "approve":
		rr.Checks["turn1_done"] = strings.Contains(t1.Text, "APPROVE-DONE")
	default:
		rr.Checks["turn1_done"] = t1.FinalState == stateComplete
	}
	rr.Checks["turn1_completed"] = t1.FinalState == stateComplete
	if t1.Usage != nil {
		for k, v := range t1.Usage {
			if f, ok := v.(float64); ok {
				rr.Timings["usage_"+k] = f
			}
		}
	}
	if t1.ContextID != "" && c.cfg.ExtraTurns > 0 {
		var sum float64
		for k := 1; k <= c.cfg.ExtraTurns; k++ {
			tk, err := c.runTurn(ctx, rr, fmt.Sprintf("extra%d", k), userMessage(fmt.Sprintf("%s [basket] extra turn %d", rr.Marker, k), t1.ContextID, ""))
			if err != nil {
				return fail(err)
			}
			p := rr.Phases[len(rr.Phases)-1]
			sum += (p.End - p.Start) * 1000
			rr.Checks[fmt.Sprintf("extra%d_done", k)] = doneRe.MatchString(tk.Text)
		}
		rr.Timings["extra_turn_total_mean"] = sum / float64(c.cfg.ExtraTurns)
	}
	if c.cfg.Turn2 && t1.ContextID != "" {
		if c.cfg.IdleWait > 0 {
			s := now()
			select {
			case <-time.After(c.cfg.IdleWait):
			case <-ctx.Done():
				return fail(ctx.Err())
			}
			rr.Phases = append(rr.Phases, phase{"idle", s, now()})
		}
		t2, err := c.runTurn(ctx, rr, "turn2", userMessage("[recall]", t1.ContextID, ""))
		if err != nil {
			return fail(err)
		}
		last := rr.Phases[len(rr.Phases)-1]
		rr.Timings["turn2_first_event"] = t2.FirstEventMs
		rr.Timings["turn2_first_text"] = t2.FirstTextMs
		rr.Timings["turn2_total"] = (last.End - last.Start) * 1000
		rr.Checks["turn2_recall"] = strings.Contains(t2.Text, "RECALL "+rr.Marker)
	}
	if c.cfg.Hold > 0 {
		s := now()
		time.Sleep(c.cfg.Hold)
		rr.Phases = append(rr.Phases, phase{"hold", s, now()})
	}
	rr.OK = true
	for _, v := range rr.Checks {
		rr.OK = rr.OK && v
	}
	return rr
}

type stat struct {
	N      int     `json:"n"`
	Min    float64 `json:"min"`
	Median float64 `json:"median"`
	P90    float64 `json:"p90"`
	P95    float64 `json:"p95"`
	Max    float64 `json:"max"`
	Mean   float64 `json:"mean"`
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	pos := q * float64(len(sorted)-1)
	lo := int(pos)
	if lo+1 >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[lo] + (pos-float64(lo))*(sorted[lo+1]-sorted[lo])
}

func summarize(vals []float64) stat {
	if len(vals) == 0 {
		return stat{}
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	var sum float64
	for _, v := range s {
		sum += v
	}
	return stat{len(s), s[0], quantile(s, .5), quantile(s, .9), quantile(s, .95), s[len(s)-1], sum / float64(len(s))}
}

func main() {
	var cfg config
	var headers multiFlag
	var out string
	flag.StringVar(&cfg.BaseURL, "url", envOr("BASKET_URL", "http://localhost:8083"), "kagent controller base URL (A2A gateway listener)")
	flag.StringVar(&cfg.Agent, "agent", os.Getenv("BASKET_AGENT"), "agent as namespace/name")
	flag.Var(&headers, "header", "extra header 'Name: value' (repeatable), e.g. 'X-User-Id: bench'")
	flag.IntVar(&cfg.Repeat, "repeat", 10, "measured iterations")
	flag.IntVar(&cfg.Warmup, "warmup", 2, "warmup iterations (recorded, excluded from summary)")
	flag.IntVar(&cfg.Concurrency, "concurrency", 1, "parallel sessions")
	flag.StringVar(&cfg.Approve, "approve", "auto", "approval policy when input-required: auto|reject|none")
	flag.DurationVar(&cfg.IdleWait, "idle-wait", 0, "wait between turn 1 and turn 2 (to let the actor suspend); 0 sends turn 2 at once")
	flag.DurationVar(&cfg.Hold, "hold", 0, "hold time after the last turn (steady-state window for the memory collector)")
	flag.BoolVar(&cfg.Turn2, "turn2", true, "send the [recall] turn")
	flag.IntVar(&cfg.ExtraTurns, "extra-turns", 0, "extra [basket] turns on the same session before turn 2 (steady-state growth per turn)")
	flag.DurationVar(&cfg.Timeout, "timeout", 5*time.Minute, "per-iteration timeout")
	trace := flag.String("trace", "", "append every raw SSE payload (turn name, ms since send, JSON) to this file, for diagnosing a harness")
	flag.StringVar(&cfg.Prompt, "prompt", "run the perf basket", "free text after the marker")
	flag.StringVar(&cfg.Scenario, "scenario", "basket", "mock scenario marker: basket|approve|echo")
	flag.StringVar(&out, "out", "", "write JSON result here (default stdout)")
	fake := flag.String("serve-fake", "", "serve a fake A2A gateway on this address (self-test of basket, perf.sh and the collector) and exit when killed")
	mode := flag.String("mode", "bench", "bench (default), send (one message, live events), approve (answer the pending approval in -state)")
	text := flag.String("text", "", "message text for -mode send")
	ctxFlag := flag.String("context", "", "contextId (session) to continue, for -mode send")
	stateFile := flag.String("state", "", "JSON file written by -mode send/approve with context, task and pending approval")
	liveFlag := flag.Bool("live", false, "print A2A events as they arrive")
	reject := flag.Bool("reject", false, "with -mode approve: reject instead of approve")
	flag.Parse()
	if *fake != "" {
		fmt.Fprintln(os.Stderr, "basket: fake gateway on", *fake)
		fmt.Fprintln(os.Stderr, http.ListenAndServe(*fake, newFakeGateway()))
		os.Exit(1)
	}
	cfg.Headers = headers
	cfg.Token = os.Getenv("BASKET_TOKEN")
	if cfg.Agent == "" {
		fmt.Fprintln(os.Stderr, "basket: -agent namespace/name is required")
		os.Exit(2)
	}
	if *trace != "" {
		f, err := os.OpenFile(*trace, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		traceF = f
	}
	c := &client{cfg: cfg, http: &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64}}}
	live = *liveFlag
	switch *mode {
	case "send":
		os.Exit(demoSend(c, *text, *ctxFlag, "", *stateFile))
	case "approve":
		os.Exit(demoApprove(c, *stateFile, !*reject))
	}
	ctx := context.Background()
	started := now()
	cardMs, cardErr := c.agentCard(ctx)
	total := cfg.Warmup + cfg.Repeat
	results := make([]*runResult, total)
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Concurrency)
	runStart := now()
	var measuredStart, measuredEnd float64
	var mu sync.Mutex
	for i := range total {
		if i == cfg.Warmup {
			wg.Wait() // measured window starts after every warmup finished
			measuredStart = now()
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			r := c.iteration(ctx, i, i < cfg.Warmup)
			mu.Lock()
			results[i] = r
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	measuredEnd = now()

	byMetric := map[string][]float64{}
	okN, failN := 0, 0
	for _, r := range results {
		if r.Warmup {
			continue
		}
		if r.OK {
			okN++
		} else {
			failN++
		}
		for k, v := range r.Timings {
			byMetric[k] = append(byMetric[k], v)
		}
	}
	summary := map[string]stat{}
	for k, v := range byMetric {
		summary[k] = summarize(v)
	}
	doc := map[string]any{
		"meta": map[string]any{"url": cfg.BaseURL, "agent": cfg.Agent, "scenario": cfg.Scenario, "started": started, "run_start": runStart,
			"measured_start": measuredStart, "measured_end": measuredEnd, "repeat": cfg.Repeat, "warmup": cfg.Warmup,
			"concurrency": cfg.Concurrency, "idle_wait_s": cfg.IdleWait.Seconds(), "agent_card_ms": cardMs, "agent_card_error": errStr(cardErr)},
		"summary":       summary,
		"ok":            okN,
		"failed":        failN,
		"throughput_ps": float64(okN) / maxf(measuredEnd-measuredStart, 1e-9),
		"runs":          results,
	}
	b, _ := json.MarshalIndent(doc, "", " ")
	if out == "" {
		os.Stdout.Write(append(b, '\n'))
	} else if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "basket: %d ok, %d failed\n", okN, failN)
	if failN > 0 || okN == 0 {
		os.Exit(1)
	}
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

var _ = errors.New
