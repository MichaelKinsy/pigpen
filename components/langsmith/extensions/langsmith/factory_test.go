package langsmith_test

import (
	"fmt"
	"strings"
	"testing"

	langsmith "github.com/MichaelKinsy/pigpen/langsmith"
)

// Tests of the exported factory: configuration from the process, the host's Pi version, and the loud refusals.

func factoryEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, k := range []string{"TRACE_TO_LANGSMITH", "LANGSMITH_API_KEY", "LANGCHAIN_API_KEY", "LANGSMITH_ENDPOINT", "LANGCHAIN_ENDPOINT", "LANGSMITH_PROJECT", "LANGSMITH_RUNS_ENDPOINTS", "LANGSMITH_CONFIG_FILE", "LANGSMITH_PROFILE", "LANGSMITH_TRACING_MODE", "LANGSMITH_OTEL_ENABLED", "OTEL_ENABLED", "LANGSMITH_PI_API_KEY", "LANGSMITH_PI_ENDPOINT", "LANGSMITH_PI_PROJECT", "LANGSMITH_PI_METADATA", "LANGSMITH_PI_RUNS_ENDPOINTS", "PIG_EXT_HOST_INFO"} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func factoryPanic(t *testing.T) (msg string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	langsmith.Extension()
	return ""
}

func TestFactoryFailsOnAZodError(t *testing.T) {
	factoryEnv(t, map[string]string{"LANGSMITH_PI_METADATA": "null"})
	got := factoryPanic(t)
	if !strings.Contains(got, "Invalid input: expected record, received null") || !strings.Contains(got, `"path"`) {
		t.Fatalf("the factory must fail with the ZodError: %q", got)
	}
}

// The host shows only the first line of a factory panic: it must name the problem on its own.
func TestFactoryPanicFirstLineSummarisesTheZodError(t *testing.T) {
	for _, c := range []struct{ env, want string }{
		{`{}`, "langsmith: invalid configuration: replicas: Invalid input: expected array, received object"},
		{`[{"api_url":3}]`, "langsmith: invalid configuration: replicas.0.api_url: Invalid input: expected string, received number"},
		{`[null]`, "langsmith: invalid configuration: replicas.0: Invalid input: expected object, received null"},
	} {
		factoryEnv(t, map[string]string{"LANGSMITH_PI_RUNS_ENDPOINTS": c.env})
		got := factoryPanic(t)
		first, rest, _ := strings.Cut(got, "\n")
		if first != c.want {
			t.Errorf("%s: first line %q, want %q", c.env, first, c.want)
		}
		if !strings.HasPrefix(rest, "[\n  {\n    \"expected\"") {
			t.Errorf("%s: the full ZodError must follow: %q", c.env, rest)
		}
	}
	factoryEnv(t, map[string]string{"LANGSMITH_PI_METADATA": "null"})
	if first, _, _ := strings.Cut(factoryPanic(t), "\n"); first != "langsmith: invalid configuration: metadata: Invalid input: expected record, received null" {
		t.Errorf("first line %q", first)
	}
}

func TestFactoryRefusesOTelTracingLoudly(t *testing.T) {
	for _, env := range []map[string]string{
		{"LANGSMITH_TRACING_MODE": "otel"}, {"LANGSMITH_OTEL_ENABLED": "true"}, {"OTEL_ENABLED": "true"},
	} {
		env["TRACE_TO_LANGSMITH"] = "true"
		factoryEnv(t, env)
		if got := factoryPanic(t); !strings.Contains(strings.ToLower(got), "otel") {
			t.Errorf("%v: the unported OTEL mode must refuse loudly naming the option, got %q", env, got)
		}
	}
}

func TestFactoryDisabledByDefault(t *testing.T) {
	factoryEnv(t, nil)
	h := StartHost(t, langsmith.Extension(), HostOptions{})
	if h.Registered("before_agent_start") || !h.Registered("session_start") {
		t.Fatal("disabled: session_start only")
	}
	h.Fire("session_start", map[string]any{"reason": "startup"})
	if got := h.CallsTo("ui.setStatus"); len(got) != 1 || got[0].Args["text"] != "LangSmith: disabled" {
		t.Fatalf("%v", got)
	}
}

func TestFactoryReadsThePiVersionFromTheHost(t *testing.T) {
	for _, c := range []struct {
		hostInfo   string
		wantStatus string // after agent_end
	}{
		{`{"version":"0.80.3"}`, "LangSmith: traced"},
		{`{"version":"0.80.4"}`, "LangSmith: tracing run"},
		{`{"version":"1.5.0"}`, "LangSmith: tracing run"},
		{``, "LangSmith: traced"}, // a host that reports no version: the regexp does not match, so no agent_settled
	} {
		mock := newMockLS(t)
		factoryEnv(t, map[string]string{"TRACE_TO_LANGSMITH": "true", "LANGSMITH_PI_ENDPOINT": mock.srv.URL, "LANGSMITH_PI_API_KEY": "k", "PIG_EXT_HOST_INFO": c.hostInfo})
		h := StartHost(t, langsmith.Extension(), HostOptions{
			OnCall: func(method string, _ map[string]any) (map[string]any, string) {
				if method == "getSessionID" {
					return map[string]any{"sessionId": "s"}, ""
				}
				return map[string]any{}, ""
			},
		})
		h.Fire("before_agent_start", withPromptOptions("before_agent_start", map[string]any{"prompt": "p"}))
		h.Fire("agent_end", map[string]any{"messages": []any{}})
		calls := h.CallsTo("ui.setStatus")
		if got := calls[len(calls)-1].Args["text"]; got != c.wantStatus {
			t.Errorf("host info %q: status %v, want %v", c.hostInfo, got, c.wantStatus)
		}
		h.Fire("session_shutdown", map[string]any{"reason": "quit"})
		if len(mock.Calls()) == 0 {
			t.Errorf("host info %q: the batched runs were not sent before shutdown returned", c.hostInfo)
		}
	}
}
