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
	expr string
	once sync.Once
	re   *regexp.Regexp
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

// lazyExpressions returns every lazyRegexp made so far.
func lazyExpressions() []*lazyRegexp {
	lazyRegistry.mu.Lock()
	defer lazyRegistry.mu.Unlock()
	return append([]*lazyRegexp(nil), lazyRegistry.all...)
}

func (l *lazyRegexp) get() *regexp.Regexp {
	l.once.Do(func() {
		lazyCompiled.Add(1)
		l.re = regexp.MustCompile(l.expr)
	})
	return l.re
}

func (l *lazyRegexp) MatchString(s string) bool            { return l.get().MatchString(s) }
func (l *lazyRegexp) String() string                       { return l.expr }
func (l *lazyRegexp) Split(s string, n int) []string       { return l.get().Split(s, n) }
func (l *lazyRegexp) FindString(s string) string           { return l.get().FindString(s) }
func (l *lazyRegexp) FindStringIndex(s string) []int       { return l.get().FindStringIndex(s) }
func (l *lazyRegexp) FindStringSubmatch(s string) []string { return l.get().FindStringSubmatch(s) }
func (l *lazyRegexp) FindStringSubmatchIndex(s string) []int {
	return l.get().FindStringSubmatchIndex(s)
}
func (l *lazyRegexp) FindAllString(s string, n int) []string { return l.get().FindAllString(s, n) }
func (l *lazyRegexp) FindAllStringIndex(s string, n int) [][]int {
	return l.get().FindAllStringIndex(s, n)
}
func (l *lazyRegexp) FindAllStringSubmatch(s string, n int) [][]string {
	return l.get().FindAllStringSubmatch(s, n)
}
func (l *lazyRegexp) ReplaceAllString(src, repl string) string {
	return l.get().ReplaceAllString(src, repl)
}
func (l *lazyRegexp) ReplaceAllStringFunc(src string, f func(string) string) string {
	return l.get().ReplaceAllStringFunc(src, f)
}
func (l *lazyRegexp) ReplaceAllLiteralString(src, repl string) string {
	return l.get().ReplaceAllLiteralString(src, repl)
}
