package a2aext

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// fakeRemote is an A2A 1.0 agent built directly on the upstream server SDK (not on this
// package's Server), so the client is tested against an independent implementation.
type fakeRemote struct {
	srv         *httptest.Server
	mu          sync.Mutex
	headers     []http.Header
	cancels     atomic.Int32
	execute     func(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool)
	cardPath    bool
	interface_  []*a2a.AgentInterface
	requireAuth string
}

// remoteUser makes every caller the same authenticated user: the upstream in-memory task store
// (and so ListTasks) needs one.
type remoteUser struct {
	a2asrv.PassthroughCallInterceptor
}

func (remoteUser) Before(ctx context.Context, cc *a2asrv.CallContext, _ *a2asrv.Request) (context.Context, any, error) {
	cc.User = a2asrv.NewAuthenticatedUser("fake-user", nil)
	return ctx, nil, nil
}

type remoteExec struct{ r *fakeRemote }

func (e remoteExec) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) { e.r.execute(ctx, ec, yield) }
}

func (e remoteExec) Cancel(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	e.r.cancels.Add(1)
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

func completeWith(text string) func(context.Context, *a2asrv.ExecutorContext, func(a2a.Event, error) bool) {
	return func(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) {
		if ec.StoredTask == nil && !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
			return
		}
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}
		if !yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart(text)), nil) {
			return
		}
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, nil), nil)
	}
}

func newFakeRemote(t *testing.T) *fakeRemote {
	t.Helper()
	r := &fakeRemote{execute: completeWith("remote says hi")}
	handler := a2asrv.NewHandler(remoteExec{r}, a2asrv.WithCallInterceptors(remoteUser{}))
	mux := http.NewServeMux()
	mux.Handle("/rpc", a2asrv.NewJSONRPCHandler(handler))
	mux.HandleFunc(a2asrv.WellKnownAgentCardPath, func(w http.ResponseWriter, req *http.Request) {
		card := &a2a.AgentCard{
			Name: "fake-remote", Description: "a fake remote agent", Version: "9",
			SupportedInterfaces: r.interface_, DefaultInputModes: []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"},
			Skills:       []a2a.AgentSkill{{ID: "chat", Name: "Chat", Description: "chats", Tags: []string{"chat"}}},
			Capabilities: a2a.AgentCapabilities{Streaming: true},
		}
		a2asrv.NewStaticAgentCardHandler(card).ServeHTTP(w, req)
	})
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.headers = append(r.headers, req.Header.Clone())
		r.mu.Unlock()
		if r.requireAuth != "" && req.URL.Path == "/rpc" && req.Header.Get("Authorization") != "Bearer "+r.requireAuth {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, req)
	}))
	r.interface_ = []*a2a.AgentInterface{a2a.NewAgentInterface(r.srv.URL+"/rpc", a2a.TransportProtocolJSONRPC)}
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeRemote) lastRPCHeader() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.headers) - 1; i >= 0; i-- {
		if r.headers[i].Get("Content-Type") != "" && strings.Contains(r.headers[i].Get("Content-Type"), "json") && r.headers[i].Get("A2A-Version") != "" {
			return r.headers[i]
		}
	}
	return nil
}

func remotes(t *testing.T, r *fakeRemote, extra func(*RemoteAgent), env map[string]string) *Remotes {
	t.Helper()
	ra := RemoteAgent{URL: r.srv.URL}
	if extra != nil {
		extra(&ra)
	}
	return NewRemotes(map[string]RemoteAgent{"peer": ra}, envFrom(env))
}

func TestRemotesNamesAreSorted(t *testing.T) {
	rs := NewRemotes(map[string]RemoteAgent{"b": {URL: "http://b"}, "a": {URL: "http://a"}}, envFrom(nil))
	if got := strings.Join(rs.Names(), ","); got != "a,b" {
		t.Fatalf("names %q", got)
	}
}

func TestCardResolvesAndSummarises(t *testing.T) {
	r := newFakeRemote(t)
	card, err := remotes(t, r, nil, nil).Card(context.Background(), "peer")
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != "fake-remote" || len(card.Skills) != 1 {
		t.Fatalf("%+v", card)
	}
}

func TestSendReturnsCompletedTaskText(t *testing.T) {
	r := newFakeRemote(t)
	var progress []string
	sum, err := remotes(t, r, nil, nil).Send(context.Background(), SendArgs{Agent: "peer", Message: "hello"}, func(s string) { progress = append(progress, s) })
	if err != nil {
		t.Fatal(err)
	}
	if sum.State != "completed" || !sum.Terminal || sum.Text != "remote says hi" || sum.TaskID == "" || sum.ContextID == "" {
		t.Fatalf("%+v", sum)
	}
	if strings.Join(progress, "") != "remote says hi" {
		t.Fatalf("progress %q", progress)
	}
}

func TestSendReusesContextAndSendsProtocolVersion(t *testing.T) {
	r := newFakeRemote(t)
	var seen atomic.Value
	r.execute = func(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) {
		seen.Store(ec.ContextID)
		completeWith("ok")(ctx, ec, yield)
	}
	rs := remotes(t, r, nil, nil)
	if _, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "hello", ContextID: "ctx-77"}, nil); err != nil {
		t.Fatal(err)
	}
	if seen.Load() != "ctx-77" {
		t.Fatalf("remote saw context %v", seen.Load())
	}
	if v := r.lastRPCHeader().Get("A2A-Version"); v != "1.0" {
		t.Fatalf("A2A-Version %q: the protocol version is pinned to 1.0", v)
	}
}

func TestBearerAndCustomHeadersComeFromEnvironment(t *testing.T) {
	r := newFakeRemote(t)
	r.requireAuth = "remote-secret-value"
	rs := remotes(t, r, func(ra *RemoteAgent) {
		ra.BearerTokenEnv = "REMOTE_TOKEN"
		ra.HeaderEnv = map[string]string{"X-Api-Key": "REMOTE_KEY"}
	}, map[string]string{"REMOTE_TOKEN": "remote-secret-value", "REMOTE_KEY": "key-123"})
	if _, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "hello"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := r.lastRPCHeader().Get("X-Api-Key"); got != "key-123" {
		t.Fatalf("custom header %q", got)
	}
}

func TestMissingTokenEnvironmentIsAnErrorBeforeAnyRequest(t *testing.T) {
	r := newFakeRemote(t)
	rs := remotes(t, r, func(ra *RemoteAgent) { ra.BearerTokenEnv = "REMOTE_TOKEN" }, nil)
	_, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "hello"}, nil)
	if err == nil || !strings.Contains(err.Error(), "REMOTE_TOKEN") {
		t.Fatalf("want an error naming the variable: %v", err)
	}
	r.mu.Lock()
	n := len(r.headers)
	r.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d requests were sent without credentials", n)
	}
}

func TestRemoteAuthFailureDoesNotEchoTheToken(t *testing.T) {
	r := newFakeRemote(t)
	r.requireAuth = "the-right-one"
	rs := remotes(t, r, func(ra *RemoteAgent) { ra.BearerTokenEnv = "REMOTE_TOKEN" }, map[string]string{"REMOTE_TOKEN": "wrong-secret-token"})
	_, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "hello"}, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "wrong-secret-token") || strings.Contains(err.Error(), "the-right-one") {
		t.Fatalf("token in error: %v", err)
	}
}

func TestCardWithoutAProtocol10InterfaceIsRefused(t *testing.T) {
	r := newFakeRemote(t)
	old := a2a.NewAgentInterface(r.srv.URL+"/rpc", a2a.TransportProtocolJSONRPC)
	old.ProtocolVersion = "0.3"
	r.interface_ = []*a2a.AgentInterface{old}
	_, err := remotes(t, r, nil, nil).Send(context.Background(), SendArgs{Agent: "peer", Message: "hello"}, nil)
	if err == nil || !strings.Contains(err.Error(), "1.0") {
		t.Fatalf("want an error naming the pinned version: %v", err)
	}
}

func TestSkipCardUsesTheEndpointDirectly(t *testing.T) {
	r := newFakeRemote(t)
	r.interface_ = nil // the card is unusable, like kagent's in-cluster URL seen from outside
	rs := remotes(t, r, func(ra *RemoteAgent) { ra.URL, ra.SkipCard = r.srv.URL+"/rpc", true }, nil)
	sum, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "hello"}, nil)
	if err != nil || sum.State != "completed" {
		t.Fatalf("%+v %v", sum, err)
	}
}

func TestUnknownAgentNamesTheKnownOnes(t *testing.T) {
	r := newFakeRemote(t)
	_, err := remotes(t, r, nil, nil).Send(context.Background(), SendArgs{Agent: "nope", Message: "hello"}, nil)
	if err == nil || !strings.Contains(err.Error(), "peer") {
		t.Fatalf("%v", err)
	}
}

func TestFailedAndInputRequiredStates(t *testing.T) {
	r := newFakeRemote(t)
	r.execute = func(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) {
		yield(a2a.NewSubmittedTask(ec, ec.Message), nil)
		msg := a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart("which file?"))
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateInputRequired, msg), nil)
	}
	sum, err := remotes(t, r, nil, nil).Send(context.Background(), SendArgs{Agent: "peer", Message: "do it"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.State != "input-required" || sum.Terminal || sum.Text != "which file?" {
		t.Fatalf("%+v", sum)
	}
	r.execute = func(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) {
		yield(a2a.NewSubmittedTask(ec, ec.Message), nil)
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateFailed, a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart("it broke"))), nil)
	}
	sum, err = remotes(t, r, nil, nil).Send(context.Background(), SendArgs{Agent: "peer", Message: "do it"}, nil)
	if err != nil || sum.State != "failed" || !sum.Terminal || sum.Text != "it broke" {
		t.Fatalf("%+v %v", sum, err)
	}
}

func TestCancellingTheCallCancelsTheRemoteTask(t *testing.T) {
	r := newFakeRemote(t)
	r.execute = func(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) {
		if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
			return
		}
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil)
		yield(a2a.NewArtifactEvent(ec, a2a.NewTextPart("partial")), nil)
		<-ctx.Done()
	}
	rs := remotes(t, r, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan struct{})
	var once sync.Once
	errc := make(chan error, 1)
	go func() {
		_, err := rs.Send(ctx, SendArgs{Agent: "peer", Message: "long job"}, func(string) { once.Do(func() { close(got) }) })
		errc <- err
	}()
	waitChan(t, got, "the first streamed chunk")
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Send did not return")
	}
	waitFor(t, func() bool { return r.cancels.Load() == 1 }, "the remote CancelTask call")
}

func TestGetAndCancelTask(t *testing.T) {
	r := newFakeRemote(t)
	rs := remotes(t, r, nil, nil)
	sum, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "hello"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rs.GetTask(context.Background(), "peer", sum.TaskID)
	if err != nil || got.State != "completed" || got.Text != "remote says hi" {
		t.Fatalf("%+v %v", got, err)
	}
	r.execute = func(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) {
		yield(a2a.NewSubmittedTask(ec, ec.Message), nil)
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil)
		<-ctx.Done()
	}
	// A task that is still running can be cancelled by id.
	idc := make(chan string, 1)
	go func() {
		_, _ = rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "again"}, func(string) {})
	}()
	go func() {
		for i := 0; i < 500; i++ {
			list, err := rs.list(context.Background(), "peer")
			if err == nil && len(list) > 0 {
				for _, s := range list {
					if s.State == "working" {
						idc <- s.TaskID
						return
					}
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	var id string
	select {
	case id = <-idc:
	case <-time.After(10 * time.Second):
		t.Fatal("no working task")
	}
	c, err := rs.CancelTask(context.Background(), "peer", id)
	if err != nil || c.State != "canceled" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestSendHonoursTheConfiguredTimeout(t *testing.T) {
	r := newFakeRemote(t)
	r.execute = func(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) {
		yield(a2a.NewSubmittedTask(ec, ec.Message), nil)
		<-ctx.Done()
	}
	rs := remotes(t, r, func(ra *RemoteAgent) { ra.TimeoutSeconds = 1 }, nil)
	start := time.Now()
	_, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "hang"}, nil)
	if err == nil || time.Since(start) > 8*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestCredentialsAreNotSentToAHostTheCardNamed(t *testing.T) {
	evil := newFakeRemote(t) // stands in for an attacker's server named in an untrusted card
	honest := newFakeRemote(t)
	honest.interface_ = []*a2a.AgentInterface{a2a.NewAgentInterface(evil.srv.URL+"/rpc", a2a.TransportProtocolJSONRPC)}
	rs := remotes(t, honest, func(ra *RemoteAgent) { ra.BearerTokenEnv = "REMOTE_TOKEN" }, map[string]string{"REMOTE_TOKEN": "remote-secret-value"})
	_, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "hello"}, nil)
	if err == nil || !strings.Contains(err.Error(), "skipCard") {
		t.Fatalf("want an error that points at skipCard: %v", err)
	}
	evil.mu.Lock()
	n := len(evil.headers)
	evil.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d requests reached the host the card named, with credentials configured", n)
	}
}

func TestSendToATerminalTaskStillReportsItsState(t *testing.T) {
	r := newFakeRemote(t)
	rs := remotes(t, r, nil, nil)
	first, err := rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "one"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A2A refuses a message for a task that is already terminal; the tool must surface that, not hide it.
	_, err = rs.Send(context.Background(), SendArgs{Agent: "peer", Message: "two", TaskID: first.TaskID, ContextID: first.ContextID}, nil)
	if err == nil {
		t.Fatal("want the remote's refusal")
	}
}

func TestRemotesNamesAreSortedForManyNames(t *testing.T) { // names-unsorted: two names can pass by luck
	cfg := map[string]RemoteAgent{}
	for _, n := range []string{"f", "c", "a", "e", "b", "d", "h", "g"} {
		cfg[n] = RemoteAgent{URL: "http://" + n}
	}
	if got := strings.Join(NewRemotes(cfg, envFrom(nil)).Names(), ""); got != "abcdefgh" {
		t.Fatalf("names %q", got)
	}
}
