package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Restart series: `basket -mode recall|resume` sends ONE logical message after the worker pod was replaced and records what
// the first attempt did, exactly, then retries the same message with backoff until it succeeds or the retry budget runs out.
//
//	-mode recall  -context C -text '[recall] go' -expect '[run-x]'   the recall turn on an existing session
//	-mode resume  -state S   -expect APPROVE-DONE                    approve the approval parked in the state file S
//
// The first attempt is never retried inside the client (cfg.NoSendRetry), so a gateway that rejects it is visible as such.

// Attempt outcomes.
const (
	outSuccess     = "success"
	outJSONRPC     = "jsonrpc_error"
	outHTTP        = "http_error"
	outEmpty       = "empty_reply"
	outTimeout     = "timeout"
	outWrongAnswer = "wrong_answer"
	outTaskFailed  = "task_failed"
	outError       = "error"
)

type attemptRec struct {
	N          int     `json:"n"`
	Outcome    string  `json:"outcome"`
	RPCCode    *int    `json:"rpc_code,omitempty"`
	HTTPStatus int     `json:"http_status,omitempty"`
	StartMs    float64 `json:"start_ms"` // since the first attempt began
	DurationMs float64 `json:"duration_ms"`
	Detail     string  `json:"detail,omitempty"`
}

type retrySeries struct {
	Mode              string       `json:"mode"`
	ContextID         string       `json:"context_id"`
	First             attemptRec   `json:"first_attempt"`
	FirstSuccess      bool         `json:"first_attempt_success"`
	Attempts          []attemptRec `json:"attempts"`
	EventualSuccess   bool         `json:"eventual_success"`
	AttemptsToSuccess int          `json:"attempts_to_success,omitempty"`
	MsToSuccess       float64      `json:"ms_to_success,omitempty"` // first attempt start to the end of the successful attempt
	Answer            string       `json:"answer,omitempty"`
	Error             string       `json:"error,omitempty"`
}

type retryOpts struct {
	AttemptTimeout time.Duration
	MaxTotal       time.Duration
	BackoffInitial time.Duration
	BackoffMax     time.Duration
}

var (
	rpcCodeRe = regexp.MustCompile(`"code"\s*:\s*(-?\d+)`)
	httpRe    = regexp.MustCompile(`HTTP (\d{3})`)
)

// classify turns the result of one sendStream call into an attempt record (without timing fields).
func classify(tr *turnResult, err error, attemptCtx context.Context, expect string) attemptRec {
	if err != nil {
		msg := err.Error()
		rec := attemptRec{Detail: trunc(msg, 300)}
		switch {
		case attemptCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded):
			rec.Outcome = outTimeout
		case strings.Contains(msg, "JSON-RPC error") || rpcCodeRe.MatchString(msg):
			rec.Outcome = outJSONRPC
			if m := rpcCodeRe.FindStringSubmatch(msg); m != nil {
				c, _ := strconv.Atoi(m[1])
				rec.RPCCode = &c
			}
			if m := httpRe.FindStringSubmatch(msg); m != nil {
				rec.HTTPStatus, _ = strconv.Atoi(m[1])
			}
		case httpRe.MatchString(msg):
			rec.Outcome = outHTTP
			rec.HTTPStatus, _ = strconv.Atoi(httpRe.FindStringSubmatch(msg)[1])
		default:
			rec.Outcome = outError
		}
		return rec
	}
	text := strings.TrimSpace(tr.Text)
	switch {
	case tr.Events == 0 || text == "":
		return attemptRec{Outcome: outEmpty, Detail: fmt.Sprintf("events=%d state=%s", tr.Events, tr.FinalState)}
	case strings.Contains(tr.FinalState, "FAILED") || strings.Contains(tr.FinalState, "REJECTED") || strings.Contains(tr.FinalState, "CANCELED"):
		return attemptRec{Outcome: outTaskFailed, Detail: tr.FinalState + ": " + trunc(text, 200)}
	case expect != "" && !strings.Contains(tr.Text, expect):
		return attemptRec{Outcome: outWrongAnswer, Detail: trunc(text, 200)}
	}
	return attemptRec{Outcome: outSuccess}
}

// runRetrySeries sends the message built by build until it succeeds (see classify), recording every attempt.
func (c *client) runRetrySeries(ctx context.Context, mode string, build func() map[string]any, contextID, expect string, o retryOpts) *retrySeries {
	rs := &retrySeries{Mode: mode, ContextID: contextID}
	t0 := time.Now()
	backoff := o.BackoffInitial
	for n := 1; ; n++ {
		start := time.Now()
		actx, cancel := context.WithTimeout(ctx, o.AttemptTimeout)
		tr, err := c.sendStream(actx, fmt.Sprintf("%s%d", mode, n), build())
		rec := classify(tr, err, actx, expect)
		cancel()
		rec.N = n
		rec.StartMs = ms(start.Sub(t0))
		rec.DurationMs = ms(time.Since(start))
		rs.Attempts = append(rs.Attempts, rec)
		if n == 1 {
			rs.First = rec
			rs.FirstSuccess = rec.Outcome == outSuccess
		}
		if rec.Outcome == outSuccess {
			rs.EventualSuccess = true
			rs.AttemptsToSuccess = n
			rs.MsToSuccess = rec.StartMs + rec.DurationMs
			rs.Answer = trunc(strings.TrimSpace(tr.Text), 300)
			return rs
		}
		if time.Since(t0)+backoff > o.MaxTotal || ctx.Err() != nil {
			rs.Error = fmt.Sprintf("no success after %d attempts in %.1fs", n, time.Since(t0).Seconds())
			return rs
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			rs.Error = ctx.Err().Error()
			return rs
		}
		if backoff *= 2; backoff > o.BackoffMax {
			backoff = o.BackoffMax
		}
	}
}

// seriesMain implements -mode recall and -mode resume.
func seriesMain(c *client, mode, text, ctxID, stateFile, expect, out string, o retryOpts) int {
	c.cfg.NoSendRetry = true
	var build func() map[string]any
	switch mode {
	case "recall":
		if ctxID == "" {
			fmt.Fprintln(os.Stderr, "basket: -mode recall needs -context")
			return 2
		}
		build = func() map[string]any { return userMessage(text, ctxID, "") }
	case "resume":
		b, err := os.ReadFile(stateFile)
		var st demoState
		if err != nil || json.Unmarshal(b, &st) != nil || st.Approval == nil {
			fmt.Fprintln(os.Stderr, "basket: no pending approval in", stateFile)
			return 2
		}
		ctxID = st.ContextID
		build = func() map[string]any { return approvalMessage(st.Approval, true, st.ContextID, st.TaskID) }
	}
	rs := c.runRetrySeries(context.Background(), mode, build, ctxID, expect, o)
	b, _ := json.MarshalIndent(rs, "", " ")
	b = append(b, '\n')
	if out == "" {
		os.Stdout.Write(b)
	} else if err := os.WriteFile(out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if !rs.EventualSuccess {
		return 1
	}
	return 0
}
