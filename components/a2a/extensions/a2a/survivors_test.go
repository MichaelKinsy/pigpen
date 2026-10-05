package a2aext

// Tests added after the first mutation run (pigeq mutate --unit): each one kills a mutant that the
// first suite let survive. The mutant's name is in the test's name or comment.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

func TestTokenEnvNotSetIsSaidPlainly(t *testing.T) { // missing-token-env-allowed
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:5555","tokens":[{"name":"a","tokenEnv":"A2A_TOKEN_A"}]}`)
	_, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)})
	if err == nil || !strings.Contains(err.Error(), "not set") {
		t.Fatalf("%v", err)
	}
}

func TestTwoVariablesWithOneValueAreRejected(t *testing.T) { // duplicate-token-values-allowed
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:1","tokens":[{"name":"a","tokenEnv":"V1"},{"name":"b","tokenEnv":"V2"}]}`)
	same := "0123456789abcdef0123456789abcdef"
	_, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(map[string]string{"V1": same, "V2": same})})
	if err == nil || !strings.Contains(err.Error(), "same value") {
		t.Fatalf("two callers with one token cannot be told apart: %v", err)
	}
}

func TestInsecureNoAuthWithTokensIsAmbiguous(t *testing.T) { // insecure-and-tokens-both
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:1","insecureNoAuth":true,"tokens":[{"name":"a","tokenEnv":"T"}]}`)
	_, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(map[string]string{"T": "0123456789abcdef0123456789abcdef"})})
	if err == nil || !strings.Contains(err.Error(), "insecureNoAuth") {
		t.Fatalf("%v", err)
	}
}

func TestMalformedListenAddressSaysHostPort(t *testing.T) { // listen-not-host-port
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"not-an-address","insecureNoAuth":true}`)
	_, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)})
	if err == nil || !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("%v", err)
	}
}

func TestOversizedPromptIsRefusedBelowTheBodyLimit(t *testing.T) { // prompt-size-unbounded
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	if _, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage(strings.Repeat("a", maxPromptBytes+1))}); err == nil {
		t.Fatal("a prompt over the limit must be refused")
	}
	if len(w.Turns()) != 0 {
		t.Fatal("worker ran")
	}
}

func TestBodyLimitAppliesEvenWithASmallPrompt(t *testing.T) { // body-size-unbounded
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "SendMessage", "params": map[string]any{
		"message": map[string]any{"messageId": "m", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "small prompt"}},
			"metadata": map[string]any{"padding": strings.Repeat("x", 2<<20)}}}})
	req, _ := http.NewRequest(http.MethodPost, "http://"+s.Addr(), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenA)
	req.Header.Set("A2A-Version", "1.0")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	if len(w.Turns()) != 0 {
		t.Fatal("a 2 MiB request with a small prompt reached the worker; the body cap is the first line of defence")
	}
}

func TestTextPlusFilePartIsRefusedNotTrimmed(t *testing.T) { // file-parts-dropped
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	m := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("summarise this"), a2a.NewRawPart([]byte("attachment")))
	if _, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: m}); err == nil {
		t.Fatal("an attachment the worker cannot see must not be silently dropped")
	}
	if len(w.Turns()) != 0 {
		t.Fatal("worker ran")
	}
}

func TestMixedVersionHeaderIsRefused(t *testing.T) { // mixed-version-values-accepted
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	params := map[string]any{"message": map[string]any{"messageId": "m1", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "hello there"}}}}
	_, out := rawRPC(t, "http://"+s.Addr(), tokenA, "1.0, 0.3", "SendMessage", params)
	if out["error"] == nil {
		t.Fatalf("a header that also names 0.3 is not 1.0: %v", out)
	}
	_, out = rawRPC(t, "http://"+s.Addr(), tokenA, "1.0, 1.0", "SendMessage", params)
	if out["error"] != nil {
		t.Fatalf("kagent's client repeats the header; that must work: %v", out)
	}
}

func TestExecutorRefusesAForgedUser(t *testing.T) { // principal-not-checked
	for name, u := range map[string]*a2asrv.User{
		"nil":             nil,
		"unauthenticated": {Name: "token:alice", Authenticated: false, Attributes: map[string]any{"name": "alice"}},
		"key mismatch":    {Name: "tenant:other", Authenticated: true, Attributes: map[string]any{"name": "alice", "tenant": "team-a"}},
	} {
		if _, err := principalOf(&a2asrv.ExecutorContext{User: u}); err == nil {
			t.Errorf("%s: a user the interceptor did not vouch for must not run tasks", name)
		}
	}
	p, err := principalOf(&a2asrv.ExecutorContext{User: a2asrv.NewAuthenticatedUser("tenant:team-a", map[string]any{"name": "alice", "tenant": "team-a"})})
	if err != nil || p.Name != "alice" || p.Tenant != "team-a" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestCancelReturnsOnlyAfterTheWorkerExited(t *testing.T) { // cancel-does-not-wait
	var exited, started = make(chan struct{}), make(chan struct{})
	w := &scriptedWorker{run: func(ctx context.Context, tn Turn, up func(Update)) (Result, error) {
		close(started)
		<-ctx.Done()
		time.Sleep(300 * time.Millisecond) // the worker takes its time to stop
		close(exited)
		return Result{}, ctx.Err()
	}}
	s := startServer(t, serverConfig(), w)
	c := a2aClient(t, "http://"+s.Addr(), tokenA)
	go func() {
		for range c.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("long job")}) {
		}
	}()
	waitChan(t, started, "the worker")
	var id a2a.TaskID
	waitFor(t, func() bool {
		list, err := c.ListTasks(context.Background(), &a2a.ListTasksRequest{})
		if err == nil && len(list.Tasks) == 1 {
			id = list.Tasks[0].ID
			return true
		}
		return false
	}, "the task to be listed")
	if _, err := c.CancelTask(context.Background(), &a2a.CancelTaskRequest{ID: id}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("CancelTask answered while the worker was still running: a session file would have two writers")
	}
}

func TestSecondStartIsAnError(t *testing.T) { // second-start-allowed
	s := startServer(t, serverConfig(), &scriptedWorker{})
	if err := s.Start(); err == nil {
		t.Fatal("starting a running server twice must fail, not leak a listener")
	}
}

func TestWorkerAsksForTermBeforeKill(t *testing.T) { // no-terminate
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals")
	}
	w, logPath, _ := newTestWorker(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = w.Run(ctx, Turn{Principal: alice, ContextID: "c", Prompt: "IGNORE_ABORT"}, func(Update) {})
		close(done)
	}()
	waitFakeLog(t, logPath, func(l fakeLog) bool { return l.Started })
	cancel()
	waitChan(t, done, "Run")
	if !readFakeLog(t, logPath).Terminated {
		t.Fatal("after the grace period the worker gets SIGTERM before SIGKILL")
	}
}

func TestWorkerEscalatesToKillWhenTermIsIgnored(t *testing.T) { // no-kill-after-grace
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals")
	}
	w, logPath, _ := newTestWorker(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = w.Run(ctx, Turn{Principal: alice, ContextID: "c", Prompt: "IGNORE_TERM"}, func(Update) {})
		close(done)
	}()
	l := waitFakeLog(t, logPath, func(l fakeLog) bool { return l.Started })
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("a worker that ignores abort and SIGTERM must still be killed")
	}
	if processAlive(l.PID) {
		t.Fatal("child survived")
	}
}

func TestWorkerFailsATurnThatNeedsInteractiveInput(t *testing.T) { // ui-request-hangs
	w, _, _ := newTestWorker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := w.Run(ctx, Turn{Principal: alice, ContextID: "c", Prompt: "NEEDUI now"}, func(Update) {})
	if err != nil || !strings.Contains(res.Failure, "interactive") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestWorkerReportsARejectedPromptWithoutTheReason(t *testing.T) { // rejected-prompt-ignored
	w, _, _ := newTestWorker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := w.Run(ctx, Turn{Principal: alice, ContextID: "c", Prompt: "REJECT me"}, func(Update) {})
	if err != nil || res.Failure != "PiG rejected the prompt" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestModelFailureMessageIsExactlyGeneric(t *testing.T) { // model-error-text-copied
	w, _, _ := newTestWorker(t)
	res, err := w.Run(context.Background(), Turn{Principal: alice, ContextID: "c", Prompt: "FAIL now"}, func(Update) {})
	if err != nil || res.Failure != "the model call failed" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestSendRefusesAnEmptyMessageBeforeAnyRequest(t *testing.T) { // empty-message-sent
	r := newFakeRemote(t)
	_, err := remotes(t, r, nil, nil).Send(context.Background(), SendArgs{Agent: "peer", Message: "  "}, nil)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("%v", err)
	}
	r.mu.Lock()
	n := len(r.headers)
	r.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d requests for an empty message", n)
	}
}

func TestAuthFailureIsExplainedWithoutTheToken(t *testing.T) { // auth-error-shows-token-context
	r := newFakeRemote(t)
	r.requireAuth = "the-right-one"
	rs := remotes(t, r, func(ra *RemoteAgent) { ra.BearerTokenEnv = "REMOTE_TOKEN" }, map[string]string{"REMOTE_TOKEN": "wrong-secret-token"})
	_, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "hello"}, nil)
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("the model needs to know the credentials were refused: %v", err)
	}
}

func TestCredentialTransportRefusesAnotherHost(t *testing.T) { // transport-host-not-checked
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer other.Close()
	// Same scheme as the other server, so only the host check can refuse it.
	tr := headerTransport{h: http.Header{"Authorization": {"Bearer secret-value"}}, host: "configured.example:443", scheme: "http", base: http.DefaultTransport}
	req, _ := http.NewRequest(http.MethodGet, other.URL, nil)
	if _, err := tr.RoundTrip(req); err == nil || hit {
		t.Fatalf("credentials went to %s (hit=%v, err=%v)", other.URL, hit, err)
	}
}

func TestRemoteRedirectsAreNotFollowed(t *testing.T) { // redirects-followed-with-credentials
	rs := NewRemotes(map[string]RemoteAgent{"peer": {URL: "http://127.0.0.1:1", BearerTokenEnv: "T"}}, envFrom(map[string]string{"T": "secret-value"}))
	hc, err := rs.httpClient("peer", rs.cfg["peer"])
	if err != nil {
		t.Fatal(err)
	}
	if got := hc.CheckRedirect(&http.Request{}, nil); got != http.ErrUseLastResponse {
		t.Fatalf("redirects must not be followed: %v", got)
	}
}

func TestSummaryOfAnInputRequiredTaskIsNotTerminal(t *testing.T) { // input-required-called-terminal
	task := &a2a.Task{ID: "t", ContextID: "c", Status: a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("which file?"))}}
	s := summarize(task)
	if s.Terminal || s.State != "input-required" || s.Text != "which file?" {
		t.Fatalf("%+v", s)
	}
	out := formatSummary(s) // unfinished-task-not-flagged
	if !strings.Contains(out, "not finished") || !strings.Contains(out, "which file?") {
		t.Fatalf("%s", out)
	}
	if strings.Contains(formatSummary(TaskSummary{State: "completed", Terminal: true, Text: "done"}), "not finished") {
		t.Fatal("a finished task is not flagged")
	}
}
