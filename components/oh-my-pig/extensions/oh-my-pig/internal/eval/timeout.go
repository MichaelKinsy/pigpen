package eval

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/eval/idle-timeout.ts, bridge-timeout.ts

const (
	// EvalTimeoutPauseOp is the synthetic status op emitted when a host bridge call starts.
	EvalTimeoutPauseOp = "timeout-pause"
	// EvalTimeoutResumeOp is the synthetic status op emitted when a host bridge call finishes.
	EvalTimeoutResumeOp = "timeout-resume"
)

// isEvalTimeoutControlEvent reports whether event is a synthetic timeout-control event.
func isEvalTimeoutControlEvent(event EvalStatusEvent) bool {
	if event == nil {
		return false
	}
	op, _ := event["op"].(string)
	return op == EvalTimeoutPauseOp || op == EvalTimeoutResumeOp
}

// withBridgeTimeoutPause runs fn while pausing the cell's idle timeout watchdog via emitStatus.
// upstream: packages/coding-agent/src/eval/bridge-timeout.ts:46
func withBridgeTimeoutPause[T any](emitStatus func(EvalStatusEvent), fn func() (T, error)) (T, error) {
	if emitStatus == nil {
		return fn()
	}
	emitStatus(EvalStatusEvent{"op": EvalTimeoutPauseOp})
	defer emitStatus(EvalStatusEvent{"op": EvalTimeoutResumeOp})
	return fn()
}

// IdleTimeout is a reference-counted watchdog that bounds time while the
// Python kernel or JS VM is in control, suspending during host-side bridge calls
// and starting a fresh window when control returns.
// upstream: packages/coding-agent/src/eval/idle-timeout.ts
type IdleTimeout struct {
	mu         sync.Mutex
	idle       time.Duration
	deadline   time.Time
	timer      *time.Timer
	pauseDepth int
	settled    bool
	timedOut   bool
	ctx        context.Context
	cancel     context.CancelCauseFunc
}

// NewIdleTimeout creates and arms an IdleTimeout with duration d (clamped to >= 1ms).
func NewIdleTimeout(parent context.Context, d time.Duration) *IdleTimeout {
	if d < time.Millisecond {
		d = time.Millisecond
	}
	ctx, cancel := context.WithCancelCause(parent)
	t := &IdleTimeout{
		idle:     d,
		deadline: time.Now().Add(d),
		ctx:      ctx,
		cancel:   cancel,
	}
	t.mu.Lock()
	t.armLocked(d)
	t.mu.Unlock()
	return t
}

// Context returns the watchdog's context, cancelled when parent cancels or the idle budget expires.
func (t *IdleTimeout) Context() context.Context {
	return t.ctx
}

// TimedOut reports whether the watchdog itself fired due to expiry.
func (t *IdleTimeout) TimedOut() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.timedOut
}

// IdleDuration returns the configured window.
func (t *IdleTimeout) IdleDuration() time.Duration {
	return t.idle
}

// Pause suspends timeout accounting while control is delegated to host-side work.
func (t *IdleTimeout) Pause() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.settled {
		return
	}
	t.pauseDepth++
	if t.pauseDepth != 1 {
		return
	}
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

// Resume resumes timeout accounting with a fresh window when pause depth returns to 0.
func (t *IdleTimeout) Resume() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.settled || t.pauseDepth == 0 {
		return
	}
	t.pauseDepth--
	if t.pauseDepth > 0 {
		return
	}
	t.deadline = time.Now().Add(t.idle)
	t.armLocked(t.idle)
}

// Dispose stops the watchdog timer and releases resources.
func (t *IdleTimeout) Dispose() {
	t.mu.Lock()
	if t.settled {
		t.mu.Unlock()
		return
	}
	t.settled = true
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.mu.Unlock()
	t.cancel(nil)
}

func (t *IdleTimeout) armLocked(delay time.Duration) {
	if delay < 0 {
		delay = 0
	}
	t.timer = time.AfterFunc(delay, t.onExpire)
}

func (t *IdleTimeout) onExpire() {
	t.mu.Lock()
	if t.settled || t.pauseDepth > 0 {
		t.mu.Unlock()
		return
	}
	remaining := time.Until(t.deadline)
	if remaining > 0 {
		t.armLocked(remaining)
		t.mu.Unlock()
		return
	}
	t.settled = true
	t.timedOut = true
	t.timer = nil
	secs := max(int(t.idle.Round(time.Second)/time.Second), 1)
	t.mu.Unlock()
	t.cancel(&timeoutError{message: fmt.Sprintf("Idle for %ds", secs)})
}

type timeoutError struct {
	message string
}

func (e *timeoutError) Error() string { return e.message }
func (e *timeoutError) Timeout() bool { return true }
