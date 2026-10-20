package bashparse

import "sync"

// memoizeWithRetry memoizes a factory but drops a failed result so the next call retries
// (async-cache.ts memoizeAsyncWithRetry). On success the value is cached and shared by every
// caller, the factory runs once. On failure the cache is cleared before the error is returned, so a
// transient initialisation failure does not poison the memo for the process lifetime. Callers that
// arrive while an attempt is in flight share it, and each sees its outcome, as callers share one
// promise in the original.
func memoizeWithRetry[T any](factory func() (T, error)) func() (T, error) {
	type flight struct {
		done  chan struct{}
		value T
		err   error
	}
	var mu sync.Mutex
	var cached *flight
	return func() (T, error) {
		mu.Lock()
		f := cached
		if f != nil {
			mu.Unlock()
			<-f.done
			return f.value, f.err
		}
		f = &flight{done: make(chan struct{})}
		cached = f
		mu.Unlock()

		f.value, f.err = factory()
		if f.err != nil {
			mu.Lock()
			if cached == f {
				cached = nil // poisoned result cleared: the next call re-attempts
			}
			mu.Unlock()
		}
		close(f.done)
		return f.value, f.err
	}
}

// GrammarParser is tree-sitter-bash's own parser, whose trees are exactly what the grammar
// produced (getGrammarParser). Production code reads [GetParser]; this one exists so a test whose
// subject is the grammar's own shape can still see it.
var GrammarParser = memoizeWithRetry(initGrammarParser)

// GetParser is the parser every consumer reads the bash grammar through (getParser): the grammar's
// trees with two corrections applied where they enter the package, [HoistHeredocTails] and then
// [ReattachRedirectArguments], in that order (a redirect hoisted out of a heredoc can itself carry
// the command's words, which the second pass then hands back).
//
// Every walker, the salvage re-parse and the log masker read the corrected tree, so none of them
// has to learn the grammar's quirks on its own.
var GetParser = memoizeWithRetry(func() (Parser, error) {
	grammar, err := GrammarParser()
	if err != nil {
		return nil, err
	}
	return &correctingParser{grammar}, nil
})

type correctingParser struct{ grammar Parser }

// Parse is the grammar's parse with both corrections. A correction that meets a shape the grammar
// cannot produce throws in the original (a TypeError that rejects the parse); here it panics
// inside the pass and the parse yields no tree, which every caller treats as an unparsed command.
func (c *correctingParser) Parse(input string) (tree *Tree) {
	grammar := c.grammar.Parse(input)
	if grammar == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			tree = nil
		}
	}()
	return &Tree{Root: ReattachRedirectArguments(HoistHeredocTails(grammar.Root))}
}

var (
	warmMu     sync.Mutex
	warmParser Parser
)

// WarmBashParser warms the parser so [GetWarmBashParser] can hand it out synchronously
// (warmBashParser). It is best-effort and idempotent: an initialisation failure is swallowed (the
// synchronous accessor stays cold and callers fall back to whole-string matching), and it returns
// at once once warm. The parser is stateless (parse is a pure function of its input), so caching
// it at package scope is safe.
func WarmBashParser() {
	warmMu.Lock()
	warmed := warmParser != nil
	warmMu.Unlock()
	if warmed {
		return
	}
	p, err := GetParser()
	if err != nil {
		return // leave cold; GetParser's retry memoization re-attempts on the next call
	}
	warmMu.Lock()
	warmParser = p
	warmMu.Unlock()
}

// GetWarmBashParser is the warmed parser for synchronous use, or nil when it has not been warmed
// yet (the pre-warm window). Callers that get nil must degrade gracefully.
func GetWarmBashParser() Parser {
	warmMu.Lock()
	defer warmMu.Unlock()
	return warmParser
}

// ResetWarmBashParser clears the warmed-parser cache so cold and warm cases are isolatable
// (test-only in the original).
func ResetWarmBashParser() {
	warmMu.Lock()
	warmParser = nil
	warmMu.Unlock()
}
