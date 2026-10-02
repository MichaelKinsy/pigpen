package typesafe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// captureStderr runs fn and returns what it wrote to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	noErr(t, err)
	old := os.Stderr
	os.Stderr = w
	var out []byte
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); out, _ = io.ReadAll(r) }()
	func() {
		defer func() { os.Stderr = old; _ = w.Close() }()
		fn()
	}()
	wg.Wait()
	return string(out)
}

func modelsOK() *mockDoer {
	return always(func() *http.Response { return jsonResp(200, map[string]any{"models": []any{}}) })
}

func TestDefaultConsoleLogger_IsSilentAtTheDefaultLevelForASuccessfulRequest(t *testing.T) {
	twin(t, "logging.test.ts | default console logger is silent at the default level for a successful request")
	out := captureStderr(t, func() {
		c, err := NewClient(Config{APIKey: "k", HTTPClient: modelsOK(), Getenv: noEnv})
		noErr(t, err)
		_, err = c.Models().List(ctxBG(), nil)
		noErr(t, err)
	})
	eq(t, out, "")
}

func TestDefaultConsoleLogger_WritesPrefixedLinesToConsoleAtDebugLevel(t *testing.T) {
	twin(t, "logging.test.ts | default console logger writes prefixed lines to console at debug level")
	out := captureStderr(t, func() {
		c, err := NewClient(Config{APIKey: "k", HTTPClient: modelsOK(), LogLevel: LogDebug, Getenv: noEnv})
		noErr(t, err)
		_, err = c.Models().List(ctxBG(), nil)
		noErr(t, err)
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("want request and response lines, got %q", out)
	}
	re := regexp.MustCompile(`^\[typesafe-sdk\] #1 GET /v1/models`)
	for _, l := range lines {
		if !re.MatchString(l) {
			t.Fatalf("unprefixed line %q", l)
		}
	}
}

func TestDefaultConsoleLogger_RoutesEachLevelToTheMatchingConsoleMethod(t *testing.T) {
	// Adapted: Go has one stream (stderr); the message keeps the prefix and the values follow it.
	twin(t, "logging.test.ts | default console logger routes each level to the matching console method")
	out := captureStderr(t, func() { NewStderrLogger().Warn("careful", map[string]any{"a": 1}) })
	eq(t, out, "[typesafe-sdk] careful {\"a\":1}\n")
	out = captureStderr(t, func() { NewStderrLogger().Error("bad") })
	eq(t, out, "[typesafe-sdk] bad\n")
}

func loggedClient(t *testing.T, logger Logger, level LogLevel) *Client {
	return newClient(t, always(func() *http.Response {
		return jsonResp(200, map[string]any{"models": []any{}}, "x-typesafe-request-id", "req_9")
	}), func(c *Config) {
		c.APIKey = "sk_live_0123456789abcdef"
		c.BaseURL = "https://x.test"
		c.Logger = logger
		c.LogLevel = level
	})
}

func TestCustomLogger_ExposesTheLevelFilteredLoggerOnTheClient(t *testing.T) {
	twin(t, "logging.test.ts | custom logger and level filtering exposes the level-filtered logger on the client")
	l := &recordingLogger{}
	c := loggedClient(t, l, LogWarn)
	c.Logger().Info("dropped")
	c.Logger().Warn("kept")
	eq(t, l.messages(""), []string{"kept"})
}

func TestCustomLogger_DropsEverythingAtLevelOff(t *testing.T) {
	twin(t, "logging.test.ts | custom logger and level filtering drops everything at level off")
	l := &recordingLogger{}
	_, err := loggedClient(t, l, LogOff).Models().List(ctxBG(), nil)
	noErr(t, err)
	eq(t, len(l.snapshot()), 0)
}

func TestCustomLogger_LogsAOneLineSummaryAtInfoAndNothingAtDebug(t *testing.T) {
	twin(t, "logging.test.ts | custom logger and level filtering logs a one-line summary at info and nothing at debug")
	l := &recordingLogger{}
	_, err := loggedClient(t, l, LogInfo).Models().List(ctxBG(), nil)
	noErr(t, err)
	eq(t, len(l.messages("debug")), 0)
	got := l.messages("info")
	eq(t, len(got), 1)
	if !regexp.MustCompile(`^#1 GET /v1/models <- 200 in \d+ms \(request req_9\)$`).MatchString(got[0]) {
		t.Fatalf("summary %q", got[0])
	}
}

func TestCustomLogger_LogsRequestHeadersRequestBodyAndResponseBodyAtDebug(t *testing.T) {
	twin(t, "logging.test.ts | custom logger and level filtering logs request headers, request body, and response body at debug")
	l := &recordingLogger{}
	_, err := loggedClient(t, l, LogDebug).Models().List(ctxBG(), nil)
	noErr(t, err)
	calls := l.snapshot()
	first := calls[0]
	eq(t, first.Level, "debug")
	eq(t, first.Message, "#1 GET /v1/models -> https://x.test/v1/models")
	detail := first.Args[0].(map[string]any)
	headers := detail["headers"].(map[string]string)
	runtimeHeader := headers["X-TypeSafe-Runtime"]
	if !strings.HasPrefix(runtimeHeader, "go/") {
		t.Fatalf("runtime %q", runtimeHeader)
	}
	eq(t, headers, map[string]string{
		"Authorization": "Bearer ***cdef", "Accept": "application/json",
		"User-Agent": "typesafe-sdk-go/" + Version, "X-TypeSafe-SDK": "typesafe-sdk-go/" + Version, "X-TypeSafe-Runtime": runtimeHeader,
	})
	if detail["body"] != nil {
		t.Fatalf("a GET has no body, got %v", detail["body"])
	}
	last := calls[len(calls)-1]
	eq(t, last.Level, "debug")
	eq(t, last.Message, "#1 GET /v1/models <- body")
	eq(t, last.Args[0], any(map[string]any{"models": []any{}}))
}

func TestCustomLogger_NeverLogsTheRawAPIKey(t *testing.T) {
	twin(t, "logging.test.ts | custom logger and level filtering never logs the raw API key")
	l := &recordingLogger{}
	_, err := loggedClient(t, l, LogDebug).Models().List(ctxBG(), nil)
	noErr(t, err)
	for _, c := range l.snapshot() {
		if strings.Contains(c.Message, "sk_live_0123456789abcdef") || strings.Contains(strings.ReplaceAll(strings.TrimSpace(fmtAny(c.Args)), "\n", " "), "sk_live_0123456789abcdef") {
			t.Fatalf("raw key in log call %+v", c)
		}
	}
}

func TestCustomLogger_NumbersRequestsSoConcurrentCallsCanBeToldApart(t *testing.T) {
	twin(t, "logging.test.ts | custom logger and level filtering numbers requests so concurrent calls can be told apart")
	l := &recordingLogger{}
	c := loggedClient(t, l, LogInfo)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.Models().List(ctxBG(), nil) }()
	}
	wg.Wait()
	var tags []string
	for _, m := range l.messages("info") {
		tags = append(tags, m[:2])
	}
	if len(tags) != 2 || !((tags[0] == "#1" && tags[1] == "#2") || (tags[0] == "#2" && tags[1] == "#1")) {
		t.Fatalf("tags %v", tags)
	}
}

func TestCustomLogger_LogsErrorResponsesAsASummaryPlusTheBodyAtDebugWithoutAWarn(t *testing.T) {
	twin(t, "logging.test.ts | custom logger and level filtering logs error responses as a summary plus the body at debug, without a warn")
	l := &recordingLogger{}
	c := newClient(t, always(func() *http.Response { return jsonResp(404, map[string]any{"message": "nope"}) }), func(c *Config) { c.Logger, c.LogLevel = l, LogDebug })
	_, err := c.Models().List(ctxBG(), nil)
	if err == nil || !strings.Contains(err.Error(), "404 nope") {
		t.Fatalf("got %v", err)
	}
	info := l.messages("info")
	eq(t, len(info), 1)
	if !regexp.MustCompile(`^#1 GET /v1/models <- 404 in \d+ms$`).MatchString(info[0]) {
		t.Fatalf("summary %q", info[0])
	}
	calls := l.snapshot()
	last := calls[len(calls)-1]
	eq(t, last, logCall{"debug", "#1 GET /v1/models <- error body", []any{map[string]any{"message": "nope"}}})
	eq(t, len(l.messages("warn")), 0)
	eq(t, len(l.messages("error")), 0)
}

func TestCustomLogger_LogsConnectionFailuresWithTheUnderlyingError(t *testing.T) {
	twin(t, "logging.test.ts | custom logger and level filtering logs connection failures with the underlying error")
	l := &recordingLogger{}
	boom := errors.New("fetch failed")
	c := newClient(t, newMock(func(*recordedRequest) (*http.Response, error) { return nil, boom }), func(c *Config) {
		c.Logger, c.LogLevel = l, LogInfo
		c.Retry = RetryOverrides{MaxRetries: Ptr(0)}
	})
	_, err := c.Models().List(ctxBG(), nil)
	if err == nil || !strings.Contains(err.Error(), "fetch failed") {
		t.Fatalf("got %v", err)
	}
	calls := l.snapshot()
	eq(t, len(calls), 1)
	eq(t, calls[0].Level, "info")
	if !regexp.MustCompile(`^#1 GET /v1/models connection error after \d+ms$`).MatchString(calls[0].Message) {
		t.Fatalf("message %q", calls[0].Message)
	}
	if len(calls[0].Args) != 1 || calls[0].Args[0] != error(boom) {
		t.Fatalf("args %v", calls[0].Args)
	}
}

func TestCustomLogger_LogsCallerAborts(t *testing.T) {
	twin(t, "logging.test.ts | custom logger and level filtering logs caller aborts")
	l := &recordingLogger{}
	ctx, cancel := context.WithCancel(context.Background())
	c := newClient(t, newMock(func(*recordedRequest) (*http.Response, error) { cancel(); return nil, context.Canceled }), func(c *Config) { c.Logger, c.LogLevel = l, LogInfo })
	_, err := c.Models().List(ctx, nil)
	mustAs[*APIUserAbortError](t, err)
	info := l.messages("info")
	eq(t, len(info), 1)
	if !regexp.MustCompile(`^#1 GET /v1/models aborted by caller after \d+ms$`).MatchString(info[0]) {
		t.Fatalf("message %q", info[0])
	}
}

func TestRedactHeaders_MasksCredentialsKeepsTheSchemeAndTheLastFourCharacters(t *testing.T) {
	twin(t, "logging.test.ts | redactHeaders masks credentials, keeps the scheme and the last four characters")
	got := RedactHeaders(map[string]string{"Authorization": "Bearer sk_live_0123456789abcdef", "x-api-key": "0123456789abcdef", "Cookie": "session=abc", "Accept": "application/json"})
	eq(t, got, map[string]string{"Authorization": "Bearer ***cdef", "x-api-key": "***cdef", "Cookie": "***", "Accept": "application/json"})
}

func TestRedactHeaders_DoesNotLeakATailFromShortSecrets(t *testing.T) {
	twin(t, "logging.test.ts | redactHeaders does not leak a tail from short secrets")
	eq(t, RedactHeaders(map[string]string{"authorization": "Bearer abc"}), map[string]string{"authorization": "Bearer ***"})
}

func TestRedactHeaders_DoesNotMutateItsInput(t *testing.T) {
	twin(t, "logging.test.ts | redactHeaders does not mutate its input")
	in := map[string]string{"Authorization": "Bearer x"}
	RedactHeaders(in)
	eq(t, in["Authorization"], "Bearer x")
}

func TestParseLogLevel_RejectsUnknownValuesNamingTheSource(t *testing.T) {
	_, err := ParseLogLevel("loud", "somewhere")
	contains(t, err.Error(), `Invalid log level "loud" from somewhere. Expected one of: debug, info, warn, error, off.`)
	l, err := ParseLogLevel("info", "x")
	noErr(t, err)
	eq(t, l, LogInfo)
}
