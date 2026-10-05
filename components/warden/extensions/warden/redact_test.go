package warden

import (
	"regexp"
	"strings"
	"testing"
)

// twin: tests/guard.test.ts:100 "redact removes passwords from database and broker URLs, not just http(s)"
// (the last assertion, about findSecrets, belongs to the security guard and is skipped: see
// TestSkippedTwinsRedactFindSecrets).
func TestRedactRemovesPasswordsFromDatabaseAndBrokerURLs(t *testing.T) {
	leak := regexp.MustCompile(`hunter2secret|s3cr3t-pw|r3disPass|guestpw`)
	for _, url := range []string{"postgres://admin:hunter2secret@db.internal:5432/app", "mysql://root:s3cr3t-pw@127.0.0.1/app", "redis://default:r3disPass@cache:6379", "amqp://guest:guestpw@mq:5672"} {
		out := Redact("DATABASE_URL=" + url)
		if leak.MatchString(out) {
			t.Fatalf("leaked: %s", out)
		}
	}
	if got := Redact("git clone git@github.com:owner/repo.git"); got != "git clone git@github.com:owner/repo.git" {
		t.Fatalf("redacted an ssh remote: %q", got)
	}
}

// twin: tests/guard.test.ts:109
func TestRedactRemovesAWholeQuotedPassphraseSpacesIncluded(t *testing.T) {
	out := Redact(`password = "correct horse battery staple"`)
	if regexp.MustCompile(`horse|battery|staple`).MatchString(out) {
		t.Fatalf("leaked: %s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("no marker: %s", out)
	}
}

// twin: tests/guard.test.ts:115
func TestRedactRemovesCommonCredentialShapesAndKeepsTheRest(t *testing.T) {
	in := "curl -H 'Authorization: Bearer abc.def.ghi' -d 'TOKEN=sk-live-0123456789abcdef' https://user:pass@example.com AKIAABCDEFGHIJKLMNOP ghp_0123456789abcdefghijklmnopqrstuvwxyz"
	out := Redact(in)
	for _, secret := range []string{"abc.def.ghi", "sk-live-0123456789abcdef", "user:pass@", "AKIAABCDEFGHIJKLMNOP", "ghp_0123456789"} {
		mustNotContain(t, out, secret, "credential kept")
	}
	for _, kept := range []string{"curl -H", "https://", "[redacted]"} {
		if !strings.Contains(out, kept) {
			t.Fatalf("lost %q: %s", kept, out)
		}
	}
	if Redact("ls -la") != "ls -la" {
		t.Fatal("changed a plain command")
	}
}

// Skipped twins (security guard, out of this port's scope; see port/PORT.md "Scope"):
//
//	guard.test.ts:129 findSecrets, :155 syntheticish
func TestSkippedTwinsRedactFindSecrets(t *testing.T) {
	t.Skip("security guard (findSecrets, looksLikeSecretValue, syntheticish) is not part of this port: owner decision needed to add it")
}

// Redaction edge (guard.ts VALUE_AMPERSAND, redact.ts:12): `&` ends an unquoted value only as a
// separator, so a password that contains `&` is redacted whole. Go's RE2 has no lookahead, so the
// port scans the value by hand; this case pins the two behaviors.
func TestRedactAmpersandIsPartOfAPasswordUnlessItSeparatesAQuery(t *testing.T) {
	if got := Redact("password=ab&cd1 next"); got != "password=[redacted] next" {
		t.Fatalf("password with &: %q", got)
	}
	if got := Redact("https://x.test/a?token=abc&page=2"); got != "https://x.test/a?token=[redacted]&page=2" {
		t.Fatalf("query separator: %q", got)
	}
	if got := Redact("a=1 && password=xyz && b"); got != "a=1 && password=[redacted] && b" {
		t.Fatalf("shell &&: %q", got)
	}
}
