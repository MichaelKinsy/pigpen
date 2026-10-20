package websearch

import (
	"reflect"
	"testing"
)

func TestOpenCodeSessionHeaders(t *testing.T) {
	want := map[string]string{"x-opencode-session": "s-1", "x-opencode-client": "pi"}
	for _, tc := range []struct {
		name      string
		model     map[string]any
		sessionID string
		want      map[string]string
	}{
		{"opencode provider", map[string]any{"provider": "opencode"}, "s-1", want},
		{"opencode-go provider", map[string]any{"provider": "opencode-go", "baseUrl": "https://example.com/v1"}, "s-1", want},
		{"opencode.ai host", map[string]any{"provider": "custom", "baseUrl": "https://opencode.ai/zen/v1"}, "s-1", want},
		{"host compare is case-insensitive", map[string]any{"provider": "custom", "baseUrl": "https://OpenCode.AI/zen"}, "s-1", want},
		{"another provider", map[string]any{"provider": "openai", "baseUrl": "https://api.openai.com/v1"}, "s-1", nil},
		{"lookalike host", map[string]any{"provider": "custom", "baseUrl": "https://opencode.ai.evil.example/v1"}, "s-1", nil},
		{"subdomain is not opencode.ai", map[string]any{"provider": "custom", "baseUrl": "https://api.opencode.ai/v1"}, "s-1", nil},
		{"unparseable baseUrl", map[string]any{"provider": "custom", "baseUrl": "opencode.ai"}, "s-1", nil},
		{"no baseUrl", map[string]any{"provider": "custom"}, "s-1", nil},
		{"no session id", map[string]any{"provider": "opencode"}, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := OpenCodeSessionHeaders(tc.model, tc.sessionID); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("headers = %v, want %v", got, tc.want)
			}
		})
	}
}
