package ask_user_question_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	ask "github.com/MichaelKinsy/pigpen/ask-user-question"
)

// Pi starts every call of a parallel batch before it handles any dialog answer: each execute runs up to its
// first await, the dialog (pi-agent-core agent-loop.js:411-453), so an RPC client sees the second call's
// dialog before the first call ends (golden two-calls.jsonl). A Go call reaches its dialog only after two
// event-bus round trips; here the second call's prompt event is slow and the first call's answer is not.
//
// The port cannot meet this with the public Go SDK (PORT.md G11): an event-bus emit is a host round trip where
// Pi's is synchronous, and the SDK reports no "dialog request written" point a call could wait for. Holding a
// call's result until the later calls are about to ask only narrows the window (two-calls failed 2 of 32 runs
// that way, against 5 of 26 without), so the port does not carry it. Un-skip when the SDK closes G11.
func TestParallelCallsAskEveryDialogBeforeTheFirstEnds(t *testing.T) {
	t.Skip("G11: needs a synchronous event emit or a dialog-sent hook in the Go SDK (PORT.md)")
	var mu sync.Mutex
	var order []string
	note := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }
	firstStarted, secondStarted, firstAnswered, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var startedOnce, secondOnce sync.Once
	opts := rpcOpts()
	opts.OnCallValue = func(method string, args map[string]any) (any, string) {
		switch method {
		case "events.emit":
			var payload struct {
				Questions []struct {
					Question string `json:"question"`
				} `json:"questions"`
			}
			b, _ := json.Marshal(args["json"]) // the payload's JSON, as a string or inline
			var s string
			if json.Unmarshal(b, &s) == nil && s != "" {
				b = []byte(s)
			}
			_ = json.Unmarshal(b, &payload)
			if len(payload.Questions) == 1 && payload.Questions[0].Question == "First?" {
				startedOnce.Do(func() { close(firstStarted) })
			}
			if len(payload.Questions) == 1 && payload.Questions[0].Question == "Second?" {
				secondOnce.Do(func() { close(secondStarted) })
				<-release // the second call's prompt event is slow
			}
		case "ui.select":
			title := str(args["title"])
			note("select " + title)
			if strings.Contains(title, "First?") {
				<-secondStarted // both calls are inside their handlers
				defer close(firstAnswered)
			}
			options, _ := args["options"].([]any)
			return map[string]any{"selected": str(options[0]), "ok": true}, ""
		}
		return map[string]any{}, ""
	}
	h := StartHost(t, ask.Extension(), opts)
	call := func(id, q string) chan struct{} {
		done := make(chan struct{})
		argv, _ := json.Marshal(askArgs(questionArg(q, strings.TrimSuffix(q, "?"), false, optArg("a", "A choice"), optArg("b", "A choice"))))
		go func() {
			defer close(done)
			if _, failure := h.roundTrip(map[string]any{"method": "tool_call", "tool": "ask_user_question", "tool_call_id": id, "args": json.RawMessage(argv)}); failure != "" {
				t.Errorf("%s: %s", id, failure)
			}
			note("result " + q)
		}()
		return done
	}
	first := call("call-1", "First?")
	<-firstStarted // the first request reaches the extension first, as the host writes a batch in source order
	second := call("call-2", "Second?")
	<-firstAnswered
	select {
	case <-first: // an implementation that ends the first call now has had its chance
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	<-first
	<-second
	mu.Lock()
	defer mu.Unlock()
	eq(t, order, []string{"select [First] First?", "select [Second] Second?", "result First?", "result Second?"}, "order")
}
