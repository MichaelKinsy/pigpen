package warden

import "sync"

// SensitivePath is asked of every command and every written path, and almost none of them name a secrets file. Its two
// halves (`dotEnvPath`, `reSensitiveRest`) are each gated on their literals (lazygate.go), which costs one folded
// search per literal; this is one pass over an ASCII text that finds where any of those literals can start, as
// redact_prefilter_fast.go does for the credential words. It only ever says "cannot match": a text with a place where a
// literal starts goes to the full check, so the answer is the one the two halves give.

// sensitiveLiterals are the lower-case ASCII literals of which one occurs, in some case, in every text SensitivePath
// accepts: `.env` and the literals of reSensitiveRest's gate. Nil when the gate does not give one set (the pattern
// changed): the fast path is then off and every text goes to the full check.
var sensitiveLiterals = sync.OnceValue(func() []string {
	g := buildGate(reSensitiveRest.expr)
	if g == nil || len(g.sets) != 1 || !g.sets[0].fold {
		return nil
	}
	return append([]string{".env"}, g.sets[0].lits...)
})

var (
	sensitivePairOnce sync.Once
	// sensitivePair marks the first two bytes, in either case, of every literal.
	sensitivePair [65536]bool
)

func buildSensitivePair() {
	both := func(c byte) []byte {
		if c >= 'a' && c <= 'z' {
			return []byte{c, c - 'a' + 'A'}
		}
		return []byte{c}
	}
	for _, lit := range sensitiveLiterals() {
		for _, a := range both(lit[0]) {
			for _, b := range both(lit[1]) {
				sensitivePair[uint(a)<<8|uint(b)] = true
			}
		}
	}
}

// sensitiveMayMatch reports whether a literal starts anywhere in s, an ASCII text. False means SensitivePath is false.
func sensitiveMayMatch(s string) bool {
	sensitivePairOnce.Do(buildSensitivePair)
	lits := sensitiveLiterals()
	if lits == nil {
		return true
	}
	for i := 0; i+1 < len(s); i++ {
		if !sensitivePair[uint(s[i])<<8|uint(s[i+1])] {
			continue
		}
		for _, lit := range lits {
			if foldPrefix(s[i:], lit) {
				return true
			}
		}
	}
	return false
}

// sensitivePathFull is SensitivePath without the single-pass check.
func sensitivePathFull(s string) bool { return dotEnvPath(s) || reSensitiveRest.MatchString(s) }
