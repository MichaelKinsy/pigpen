package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Demo helpers: `basket -mode send|approve` sends one message or one approval and prints the A2A events as they arrive.
// The conversation state (context, task, pending approval) is kept in a small JSON file so a script can pause between the
// calls (for example to replace the worker pod while a task waits for approval).

var live bool

var (
	cReset  = "\033[0m"
	cDim    = "\033[2m"
	cCyan   = "\033[36m"
	cYellow = "\033[33m"
	cGreen  = "\033[32m"
)

func init() {
	if os.Getenv("NO_COLOR") != "" {
		cReset, cDim, cCyan, cYellow, cGreen = "", "", "", "", ""
	}
}

func livef(at float64, kind, format string, args ...any) {
	if !live {
		return
	}
	col := cDim
	switch kind {
	case "tool call", "tool result":
		col = cCyan
	case "input-required":
		col = cYellow
	case "completed", "answer":
		col = cGreen
	}
	fmt.Printf("  %s[%5.2fs] %-14s%s %s\n", cDim, at/1000, kind, cReset+col, fmt.Sprintf(format, args...)+cReset)
}

func short(v any, n int) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	s := strings.TrimRight(buf.String(), "\n")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}

type demoState struct {
	ContextID string           `json:"context_id"`
	TaskID    string           `json:"task_id"`
	State     string           `json:"state"`
	Approval  *approvalRequest `json:"approval,omitempty"`
	Text      string           `json:"text"`
}

func writeState(path string, tr *turnResult) {
	st := demoState{ContextID: tr.ContextID, TaskID: tr.TaskID, State: tr.FinalState, Approval: tr.approvalNeeded, Text: strings.TrimSpace(tr.Text)}
	b, _ := json.MarshalIndent(st, "", " ")
	os.WriteFile(path, append(b, '\n'), 0o644)
}

func finishLive(tr *turnResult) {
	t := strings.TrimSpace(tr.Text)
	if t != "" {
		livef(tr.TotalMs, "answer", "%s", t)
	}
	livef(tr.TotalMs, strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(tr.FinalState, "TASK_STATE_")), "_", "-"), "")
}

func demoSend(c *client, text, ctxID, taskID, stateFile string) int {
	ctx, cancel := context.WithTimeout(context.Background(), c.cfg.Timeout)
	defer cancel()
	tr, err := c.sendStream(ctx, "send", userMessage(text, ctxID, taskID))
	if err != nil {
		fmt.Fprintln(os.Stderr, "basket:", err)
		return 1
	}
	finishLive(tr)
	if stateFile != "" {
		writeState(stateFile, tr)
	}
	return 0
}

func demoApprove(c *client, stateFile string, approve bool) int {
	b, err := os.ReadFile(stateFile)
	var st demoState
	if err != nil || json.Unmarshal(b, &st) != nil || st.Approval == nil {
		fmt.Fprintln(os.Stderr, "basket: no pending approval in", stateFile)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.cfg.Timeout)
	defer cancel()
	tr, err := c.sendStream(ctx, "approval", approvalMessage(st.Approval, approve, st.ContextID, st.TaskID))
	if err != nil {
		fmt.Fprintln(os.Stderr, "basket:", err)
		return 1
	}
	// the first stream's approval request is in the state file; this stream carries the continuation
	finishLive(tr)
	if tr.ContextID == "" {
		tr.ContextID = st.ContextID
	}
	if tr.TaskID == "" {
		tr.TaskID = st.TaskID
	}
	writeState(stateFile, tr)
	return 0
}
