package pi_subagents

import (
	"sync"
)

// UTF-16 string helpers: the watchdog bounds text by JavaScript string length (code units), as the original does.

func wdLen(s string) int { panic("not implemented") }

// wdSlice is s.slice(from, to) for from and to within [0, length].
func wdSlice(s string, from, to int) string { panic("not implemented") }

func wdSliceFrom(s string, from int) string { panic("not implemented") }

func wdSHA256(s string) string { panic("not implemented") }

// wdAbort is an AbortController: signal closes when abort is called (once).
type wdAbort struct {
	once sync.Once
	ch   chan struct{}
}

func newWdAbort() *wdAbort { panic("not implemented") }

func (a *wdAbort) abort()                  { panic("not implemented") }
func (a *wdAbort) signal() <-chan struct{} { panic("not implemented") }

// wdClosed reports a closed signal; a nil signal never closes (an AbortSignal that is undefined).
func wdClosed(signal <-chan struct{}) bool { panic("not implemented") }

// wdPanicError is the error a recovered panic stands for (a rejected promise in the original).
func wdPanicError(p any) error { panic("not implemented") }
