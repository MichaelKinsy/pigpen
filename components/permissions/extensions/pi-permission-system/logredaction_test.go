package pi_permission_system

import (
	"math"
	"math/big"
	"testing"
)

func nanValue() float64 { return math.NaN() }

func lgRedacted(t *testing.T, v any) string {
	t.Helper()
	s, ok := RedactedJSONStringify(v)
	if !ok {
		t.Fatalf("undefined for %#v", v)
	}
	return s
}

func TestLogRedactionTwins(t *testing.T) {
	const f = "logging/log-redaction"
	table := func(t *testing.T, names []string, want bool) {
		for _, n := range names {
			t.Run(n, func(t *testing.T) { eq(t, IsSensitiveName(n), want, n) })
		}
	}
	t.Run("names the shipped key-name pattern already masked", func(t *testing.T) {
		table(t, []string{"authorization", "Authorization", "authorization_header", "apiKey", "api_key", "api-key", "apikey", "x-api-key",
			"ANTHROPIC_API_KEY", "secret", "clientSecret", "token", "tokenCount", "accessToken", "refresh_token", "password", "passwd",
			"credential", "credentials", "cookie", "privateKey", "private_key", "privatekey"}, true)
	})
	t.Run("a bare or suffixed key, which the shipped pattern missed", func(t *testing.T) {
		table(t, []string{"KEY", "key", "keys", "OPENROUTER_KEY", "MY_KEY", "my-key", "X-Api-Key", "cacheKey", "sortKeys"}, true)
	})
	t.Run("names that bind no credential", func(t *testing.T) {
		table(t, []string{"toolName", "command", "path", "target", "origin", "matchedPattern", "resolution", "toolInputPreview",
			"requesterAgentName", "denialReason", "Content-Type", "monkey", "keyboard", "turnkey", "donkeys", "whiskey", ""}, false)
	})
	t.Run("JavaScript case folding: no non-ASCII character reads as an ASCII letter", func(t *testing.T) {
		table(t, []string{"\u212Aey", "to\u212Aen", "\u017Fecret", "pa\u017F\u017Fword"}, false)
	})
	tw(t, f, "misses a camel-cased key used as a name prefix", func(t *testing.T) {
		eq(t, IsSensitiveName("keySet"), false, "keySet")
	})
	obj := func(kv ...any) *jsObject {
		o := newObject()
		for i := 0; i < len(kv); i += 2 {
			o.set(kv[i].(string), kv[i+1])
		}
		return o
	}
	tw(t, f, "masks a top-level sensitive value", func(t *testing.T) {
		eq(t, lgRedacted(t, obj("token", "abc123")), `{"token":"`+RedactedPlaceholder+`"}`, "json")
	})
	tw(t, f, "masks a nested sensitive value", func(t *testing.T) {
		d := obj("toolName", "http", "headers", obj("authorization", "Bearer TEST_VALUE"))
		eq(t, lgRedacted(t, d), `{"toolName":"http","headers":{"authorization":"`+RedactedPlaceholder+`"}}`, "json")
	})
	tw(t, f, "masks a sensitive value inside an array element", func(t *testing.T) {
		d := obj("entries", []any{obj("name", "prod", "apiKey", "sk-real-value")})
		eq(t, lgRedacted(t, d), `{"entries":[{"name":"prod","apiKey":"`+RedactedPlaceholder+`"}]}`, "json")
	})
	tw(t, f, "masks an object-valued sensitive key without descending into it", func(t *testing.T) {
		d := obj("credentials", obj("user", "root", "password", "hunter2"))
		eq(t, lgRedacted(t, d), `{"credentials":"`+RedactedPlaceholder+`"}`, "json")
	})
	tw(t, f, "leaves non-sensitive keys untouched", func(t *testing.T) {
		d := obj("toolName", "bash", "command", "echo hello", "matchedPattern", "echo *")
		eq(t, lgRedacted(t, d), `{"toolName":"bash","command":"echo hello","matchedPattern":"echo *"}`, "json")
	})
	tw(t, f, "leaves a null or absent sensitive value as-is rather than reading as suppressed", func(t *testing.T) {
		eq(t, lgRedacted(t, obj("token", nil)), `{"token":null}`, "null")
		eq(t, lgRedacted(t, obj("token", undef{})), `{}`, "undefined")
	})
	tw(t, f, "retains the Error, bigint, and cycle handling of the plain serializer", func(t *testing.T) {
		node := obj("size", big.NewInt(10), "error", &JSError{Name: "Error", Message: "boom", Stack: lgStack("trace")})
		node.set("self", node)
		eq(t, lgRedacted(t, node), `{"size":"10","error":{"name":"Error","message":"boom","stack":"trace"},"self":"[Circular]"}`, "json")
	})
}
