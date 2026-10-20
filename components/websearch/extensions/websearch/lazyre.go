package websearch

import (
	"regexp"
	"sync"
)

// lazyRE is a regular expression that compiles on its first use. The Defuddle port has some 400 package-level
// expressions; compiling them all when the extension starts cost the process about 20 ms (a Go regexp compile takes
// tens of microseconds and allocates heavily), and a session that never fetches a page that needs Defuddle never
// uses them. The methods are those of *regexp.Regexp that the port calls.
type lazyRE struct {
	once    sync.Once
	pattern string // Go syntax, or JavaScript syntax when js is set
	flags   string // i, m, s for a JavaScript pattern
	js      bool
	re      *regexp.Regexp
}

// lazyREs lists the expressions that exist when the package is initialized, so that a test can compile all of them and
// report the one with a syntax error, which the lazy compile would otherwise only meet when the pattern is first used.
var (
	lazyREMu  sync.Mutex
	lazyREAll []*lazyRE
)

func newLazyRE(pattern, flags string, js bool) *lazyRE {
	l := &lazyRE{pattern: pattern, flags: flags, js: js}
	lazyREMu.Lock()
	if len(lazyREAll) < 2048 {
		lazyREAll = append(lazyREAll, l)
	}
	lazyREMu.Unlock()
	return l
}

// mustRE is regexp.MustCompile, deferred: a bad pattern panics at the first use (TestLazyExpressionsCompile finds
// every one at once).
func mustRE(pattern string) *lazyRE { return newLazyRE(pattern, "", false) }

func (l *lazyRE) get() *regexp.Regexp {
	l.once.Do(func() {
		if l.js {
			l.re = regexp.MustCompile(jsTranslate(l.pattern, l.flags))
		} else {
			l.re = regexp.MustCompile(l.pattern)
		}
	})
	return l.re
}

func (l *lazyRE) MatchString(s string) bool            { return l.get().MatchString(s) }
func (l *lazyRE) FindString(s string) string           { return l.get().FindString(s) }
func (l *lazyRE) FindStringIndex(s string) []int       { return l.get().FindStringIndex(s) }
func (l *lazyRE) FindStringSubmatch(s string) []string { return l.get().FindStringSubmatch(s) }
func (l *lazyRE) FindStringSubmatchIndex(s string) []int {
	return l.get().FindStringSubmatchIndex(s)
}
func (l *lazyRE) FindAllString(s string, n int) []string { return l.get().FindAllString(s, n) }
func (l *lazyRE) FindAllStringIndex(s string, n int) [][]int {
	return l.get().FindAllStringIndex(s, n)
}
func (l *lazyRE) FindAllStringSubmatch(s string, n int) [][]string {
	return l.get().FindAllStringSubmatch(s, n)
}
func (l *lazyRE) FindAllStringSubmatchIndex(s string, n int) [][]int {
	return l.get().FindAllStringSubmatchIndex(s, n)
}
func (l *lazyRE) ReplaceAllString(s, repl string) string { return l.get().ReplaceAllString(s, repl) }
func (l *lazyRE) ReplaceAllLiteralString(s, repl string) string {
	return l.get().ReplaceAllLiteralString(s, repl)
}
func (l *lazyRE) ReplaceAllStringFunc(s string, repl func(string) string) string {
	return l.get().ReplaceAllStringFunc(s, repl)
}
func (l *lazyRE) Split(s string, n int) []string { return l.get().Split(s, n) }
