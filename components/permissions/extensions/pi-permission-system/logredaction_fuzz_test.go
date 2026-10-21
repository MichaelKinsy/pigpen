package pi_permission_system

import (
	"math/rand"
	"regexp"
	"testing"
)

// The hand-written matchers of IsSensitiveName must agree with the original's two patterns (compiled here as the reference).
var (
	refSensitive = regexp.MustCompile(`authorization|api[-_]?keys?|private[-_]?keys?|secret|token|password|passwd|credential|cookie|(?:^|[-_])keys?(?:$|[-_])`)
	refCamelKey  = regexp.MustCompile(`[a-z0-9](?:Key|Keys)(?:$|[A-Z_-])`)
)

func TestSensitiveNameMatchersAgreeWithThePatterns(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	pieces := []string{"api", "Api", "API", "key", "Key", "KEY", "keys", "Keys", "s", "S", "_", "-", ".", "x", "X", "0", "9", "private", "Private", "token", "Token", "secret", "authorization", "pass", "word", "passwd", "credential", "cookie", "é", "k", "K", "ey", "sortKeys", "apiKey", "my_key", "a", "A", "z", "Z", "/", " "}
	for n := 0; n < 400000; n++ {
		var b []byte
		for k := rnd.Intn(6) + 1; k > 0; k-- {
			b = append(b, pieces[rnd.Intn(len(pieces))]...)
		}
		name := string(b)
		wantS, wantC := refSensitive.MatchString(asciiLower(name)), refCamelKey.MatchString(name)
		if got := matchesSensitive(asciiLower(name)); got != wantS {
			t.Fatalf("matchesSensitive(%q) = %v, pattern %v", name, got, wantS)
		}
		if got := matchesCamelKey(name); got != wantC {
			t.Fatalf("matchesCamelKey(%q) = %v, pattern %v", name, got, wantC)
		}
	}
}
