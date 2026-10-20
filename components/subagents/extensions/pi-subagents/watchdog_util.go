package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"unicode/utf16"
)

// UTF-16 string helpers: the watchdog bounds text by JavaScript string length (code units), as the original does.

func wdLen(s string) int { return utf16Units(s) }

// wdSlice is s.slice(from, to) for from and to within [0, length].
func wdSlice(s string, from, to int) string {
	units := utf16.Encode([]rune(s))
	if from < 0 {
		from = 0
	}
	if to > len(units) {
		to = len(units)
	}
	if from >= to {
		return ""
	}
	return string(utf16.Decode(units[from:to]))
}

func wdSliceFrom(s string, from int) string { return wdSlice(s, from, wdLen(s)) }

func wdSHA256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// wdAbort is an AbortController: signal closes when abort is called (once).
type wdAbort struct {
	once sync.Once
	ch   chan struct{}
}

func newWdAbort() *wdAbort { return &wdAbort{ch: make(chan struct{})} }

func (a *wdAbort) abort()                  { a.once.Do(func() { close(a.ch) }) }
func (a *wdAbort) signal() <-chan struct{} { return a.ch }

// wdClosed reports a closed signal; a nil signal never closes (an AbortSignal that is undefined).
func wdClosed(signal <-chan struct{}) bool {
	if signal == nil {
		return false
	}
	select {
	case <-signal:
		return true
	default:
		return false
	}
}

// wdPanicError is the error a recovered panic stands for (a rejected promise in the original).
func wdPanicError(p any) error {
	if e, ok := p.(error); ok {
		return e
	}
	return fmt.Errorf("%v", p)
}
