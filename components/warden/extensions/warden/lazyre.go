package warden

import (
	"regexp"
	"sync"
	"sync/atomic"
)

// The package holds about a hundred regular expressions, and compiling them at process start cost about 2 ms and
// 1 MB (7,000 allocations) in every pig that selects warden, whether or not it is on. A lazyRegexp compiles its
// expression the first time it is used, so a pig that never enables warden never pays.
type lazyRegexp struct {
	expr    string
	build   func() string
	srcOnce sync.Once
	once    sync.Once
	re      *regexp.Regexp
	gate    *gate
}

// lazyCompiled counts compilations, for the test that no expression is compiled at start.
var lazyCompiled atomic.Int64

// lazyRegistry holds every lazyRegexp made, so a test can compile them all: a bad pattern no longer fails at
// package init, only on the first call that reaches it.
var lazyRegistry struct {
	mu  sync.Mutex
	all []*lazyRegexp
}

func lazyRE(expr string) *lazyRegexp {
	l := &lazyRegexp{expr: expr}
	lazyRegistry.mu.Lock()
	lazyRegistry.all = append(lazyRegistry.all, l)
	lazyRegistry.mu.Unlock()
	return l
}

// lazyREBuilt is lazyRE for an expression that is assembled from parts: build runs on first use.
func lazyREBuilt(build func() string) *lazyRegexp {
	l := &lazyRegexp{build: build}
	lazyRegistry.mu.Lock()
	lazyRegistry.all = append(lazyRegistry.all, l)
	lazyRegistry.mu.Unlock()
	return l
}

// lazyExpressions returns every lazyRegexp made so far.
func lazyExpressions() []*lazyRegexp {
	lazyRegistry.mu.Lock()
	defer lazyRegistry.mu.Unlock()
	return append([]*lazyRegexp(nil), lazyRegistry.all...)
}

func (l *lazyRegexp) get() *regexp.Regexp {
	l.once.Do(func() {
		lazyCompiled.Add(1)
		l.re = regexp.MustCompile(l.source())
		l.gate = buildGate(l.expr)
		if lazyObserve != nil {
			lazyObserve(l)
		}
	})
	return l.re
}

// lazyObserve is set by a test to learn which expressions the process compiled; nil in production.
var lazyObserve func(*lazyRegexp)

// source is the expression's text; a built expression (lazyREBuilt) is produced on first use too, so that
// assembling a long pattern does not allocate at package init either.
func (l *lazyRegexp) source() string {
	l.srcOnce.Do(func() {
		if l.build != nil {
			l.expr = l.build()
		}
	})
	return l.expr
}

// Every matching method asks the gate first (lazygate.go): a text that cannot match gets the answer the engine
// gives for no match, without a scan of the text by the engine.
func (l *lazyRegexp) MatchString(s string) bool {
	re := l.get()
	return l.gate.may(s) && re.MatchString(s)
}
func (l *lazyRegexp) String() string                 { return l.source() }
func (l *lazyRegexp) Split(s string, n int) []string { return l.get().Split(s, n) }
func (l *lazyRegexp) FindString(s string) string {
	re := l.get()
	if !l.gate.may(s) {
		return ""
	}
	return re.FindString(s)
}
func (l *lazyRegexp) FindStringIndex(s string) []int {
	re := l.get()
	if !l.gate.may(s) {
		return nil
	}
	return re.FindStringIndex(s)
}
func (l *lazyRegexp) FindStringSubmatch(s string) []string {
	re := l.get()
	if !l.gate.may(s) {
		return nil
	}
	return re.FindStringSubmatch(s)
}
func (l *lazyRegexp) FindStringSubmatchIndex(s string) []int {
	re := l.get()
	if !l.gate.may(s) {
		return nil
	}
	return re.FindStringSubmatchIndex(s)
}
func (l *lazyRegexp) FindAllString(s string, n int) []string {
	re := l.get()
	if !l.gate.may(s) {
		return nil
	}
	return re.FindAllString(s, n)
}
func (l *lazyRegexp) FindAllStringIndex(s string, n int) [][]int {
	re := l.get()
	if !l.gate.may(s) {
		return nil
	}
	return re.FindAllStringIndex(s, n)
}
func (l *lazyRegexp) FindAllStringSubmatch(s string, n int) [][]string {
	re := l.get()
	if !l.gate.may(s) {
		return nil
	}
	return re.FindAllStringSubmatch(s, n)
}
func (l *lazyRegexp) ReplaceAllString(src, repl string) string {
	re := l.get()
	if !l.gate.may(src) {
		return src
	}
	return re.ReplaceAllString(src, repl)
}
func (l *lazyRegexp) ReplaceAllStringFunc(src string, f func(string) string) string {
	re := l.get()
	if !l.gate.may(src) {
		return src
	}
	return re.ReplaceAllStringFunc(src, f)
}
func (l *lazyRegexp) ReplaceAllLiteralString(src, repl string) string {
	re := l.get()
	if !l.gate.may(src) {
		return src
	}
	return re.ReplaceAllLiteralString(src, repl)
}
