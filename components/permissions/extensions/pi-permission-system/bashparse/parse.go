package bashparse

import "sync"

// memoizeWithRetry memoizes a factory but drops a failed result so the next call retries
// (async-cache.ts memoizeAsyncWithRetry). On success the value is cached and shared by every
// caller, the factory runs once; on failure nothing is cached, so a transient initialisation
// failure does not poison the memo for the process lifetime. Safe for concurrent use: concurrent
// callers share one in-flight attempt, and every one of them sees its error.
func memoizeWithRetry[T any](factory func() (T, error)) func() (T, error) {
	var mu sync.Mutex
	var cached T
	var ok bool
	return func() (T, error) {
		mu.Lock()
		defer mu.Unlock()
		if ok {
			return cached, nil
		}
		v, err := factory()
		if err != nil {
			var zero T
			return zero, err
		}
		cached, ok = v, true
		return v, nil
	}
}

// GrammarParser is tree-sitter-bash's own parser, whose trees are exactly what the grammar
// produced (getGrammarParser). Production code reads [GetParser]; this one exists so a test whose
// subject is the grammar's own shape can still see it.
var GrammarParser = memoizeWithRetry(initGrammarParser)

// GetParser is the parser every consumer reads the bash grammar through (getParser): the grammar's
// trees with two corrections applied where they enter the package, hoistHeredocTails and then
// reattachRedirectArguments, in that order (a redirect hoisted out of a heredoc can itself carry
// the command's words, which the second pass then hands back).
var GetParser = memoizeWithRetry(func() (Parser, error) {
	grammar, err := GrammarParser()
	if err != nil {
		return nil, err
	}
	return correctingParser{grammar}, nil
})

type correctingParser struct{ grammar Parser }

func (c correctingParser) Parse(input string) *Tree {
	tree := c.grammar.Parse(input)
	if tree == nil {
		return nil
	}
	return &Tree{Root: ReattachRedirectArguments(HoistHeredocTails(tree.Root))}
}
