// Package lazyre is a regular expression that is compiled the first time it is used.
//
// A package-level regexp.MustCompile runs when the package is loaded. pig-music is loaded by every PiG that selects it, and
// most sessions never type /music, so its start-up work (and the memory it leaves) must be paid on first use instead.
package lazyre

import (
	"regexp"
	"sync"
)

// Regexp is a pattern compiled on first use. A bad pattern panics then, as regexp.MustCompile would at load.
type Regexp struct {
	pattern string
	once    sync.Once
	re      *regexp.Regexp
}

// New returns a Regexp for pattern. Nothing is compiled yet.
func New(pattern string) *Regexp { return &Regexp{pattern: pattern} }

func (r *Regexp) get() *regexp.Regexp {
	r.once.Do(func() { r.re = regexp.MustCompile(r.pattern) })
	return r.re
}

func (r *Regexp) compiled() bool {
	done := true
	r.once.Do(func() { done = false; r.re = regexp.MustCompile(r.pattern) })
	return done
}

func (r *Regexp) MatchString(s string) bool              { return r.get().MatchString(s) }
func (r *Regexp) FindStringSubmatch(s string) []string   { return r.get().FindStringSubmatch(s) }
func (r *Regexp) FindStringSubmatchIndex(s string) []int { return r.get().FindStringSubmatchIndex(s) }
func (r *Regexp) FindString(s string) string             { return r.get().FindString(s) }
func (r *Regexp) FindAllString(s string, n int) []string { return r.get().FindAllString(s, n) }
func (r *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	return r.get().FindAllStringSubmatch(s, n)
}
func (r *Regexp) ReplaceAllString(s, repl string) string { return r.get().ReplaceAllString(s, repl) }
func (r *Regexp) ReplaceAllStringFunc(s string, f func(string) string) string {
	return r.get().ReplaceAllStringFunc(s, f)
}
func (r *Regexp) Split(s string, n int) []string { return r.get().Split(s, n) }
func (r *Regexp) Find(b []byte) []byte           { return r.get().Find(b) }
