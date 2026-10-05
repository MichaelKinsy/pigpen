package a2aext

// Interoperability with kagent. kagent speaks A2A 1.0 through a2a-go v2 (kagent's go.mod
// requires github.com/a2aproject/a2a-go/v2 v2.6.0, the version pinned here). The test drives a
// real kagent A2A server: kagent's own adk/pkg/a2a/server package with an echo executor
// (port/interop/kagent, built by scripts/interop-kagent.mjs from a kagent checkout).
// Without KAGENT_A2A_ECHO the tests are skipped, by name, and the report says so.

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
)

func startKagentEcho(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("KAGENT_A2A_ECHO")
	if bin == "" {
		t.Skip("no kagent-compatible A2A endpoint: set KAGENT_A2A_ECHO to the binary built by scripts/interop-kagent.mjs (needs a kagent checkout, KAGENT_GO_DIR)")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	cmd := exec.Command(bin, "-port", port)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	out, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { // keep the pipe drained
		sc := bufio.NewScanner(out)
		for sc.Scan() {
		}
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	base := "http://127.0.0.1:" + port
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := http.Get(base + "/.well-known/agent-card.json")
		if err == nil {
			resp.Body.Close()
			return base
		}
		if time.Now().After(deadline) {
			t.Fatalf("kagent echo did not start: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestInterop_KagentServerAsRemote(t *testing.T) {
	base := startKagentEcho(t)
	// kagent's card advertises the in-cluster URL, so callers use the configured URL as the endpoint (skipCard),
	// exactly as kagent's own CLI does.
	rs := NewRemotes(map[string]RemoteAgent{"kagent": {URL: base, SkipCard: true, TimeoutSeconds: 30}}, envFrom(nil))
	var progress strings.Builder
	sum, err := rs.Send(context.Background(), SendArgs{Agent: "kagent", Message: "hello kagent"}, func(s string) { progress.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if sum.State != "completed" || sum.Text != "kagent echo: hello kagent" || sum.TaskID == "" || sum.ContextID == "" {
		t.Fatalf("%+v", sum)
	}
	got, err := rs.GetTask(context.Background(), "kagent", sum.TaskID)
	if err == nil && got.State != "completed" {
		t.Fatalf("get: %+v", got)
	}
	// Continue the context.
	next, err := rs.Send(context.Background(), SendArgs{Agent: "kagent", Message: "again", ContextID: sum.ContextID}, nil)
	if err != nil || next.ContextID != sum.ContextID || next.TaskID == sum.TaskID {
		t.Fatalf("context continuation: %+v %v", next, err)
	}
}

func TestInterop_KagentCardHostIsNotTrustedWithCredentials(t *testing.T) {
	base := startKagentEcho(t)
	rs := NewRemotes(map[string]RemoteAgent{"kagent": {URL: base, BearerTokenEnv: "KAGENT_TOKEN", TimeoutSeconds: 30}}, envFrom(map[string]string{"KAGENT_TOKEN": "kagent-token-value"}))
	_, err := rs.Send(context.Background(), SendArgs{Agent: "kagent", Message: "hello"}, nil)
	if err == nil || !strings.Contains(err.Error(), "skipCard") {
		t.Fatalf("kagent's card names an in-cluster host; the client must not send a token there: %v", err)
	}
}

func TestInterop_KagentCancelReachesTheRemoteTask(t *testing.T) {
	base := startKagentEcho(t)
	rs := NewRemotes(map[string]RemoteAgent{"kagent": {URL: base, SkipCard: true, TimeoutSeconds: 30}}, envFrom(nil))
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	ids := make(chan string, 1)
	go func() {
		_, err := rs.Send(ctx, SendArgs{Agent: "kagent", Message: "sleep", OnTask: func(id, _ string) { ids <- id }}, nil)
		errc <- err
	}()
	var id string
	select {
	case id = <-ids:
	case <-time.After(10 * time.Second):
		t.Fatal("kagent never reported the task")
	}
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("cancelled Send must fail")
	}
	waitFor(t, func() bool {
		s, err := rs.GetTask(context.Background(), "kagent", id)
		return err == nil && s.State == "canceled"
	}, "the kagent task to be canceled")
}

// kagentHeaders reproduces kagent's own CLI client (go/core/cli/internal/a2a/client.go): an A2A v1
// client with the JSON-RPC transport, no card resolution, and static headers including A2A-Version.
type kagentHeaders map[string]string

func (h kagentHeaders) Before(ctx context.Context, req *a2aclient.Request) (context.Context, any, error) {
	for k, v := range h {
		req.ServiceParams.Append(k, v)
	}
	return ctx, nil, nil
}

func (kagentHeaders) After(context.Context, *a2aclient.Response) error { return nil }

func TestInterop_KagentClientShapeAgainstThisServer(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	hc := &http.Client{Transport: bearerTransport{token: tokenA}, Timeout: 30 * time.Second}
	c, err := a2aclient.NewFromEndpoints(context.Background(),
		[]*a2a.AgentInterface{{URL: "http://" + s.Addr(), ProtocolVersion: a2a.Version, ProtocolBinding: a2a.TransportProtocolJSONRPC}},
		a2aclient.WithJSONRPCTransport(hc),
		a2aclient.WithCallInterceptors(kagentHeaders{a2a.SvcParamVersion: string(a2a.Version)}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("from a kagent-shaped client")})
	task := mustTask(t, res, err)
	if task.Status.State != a2a.TaskStateCompleted || taskText(task) != "echo: from a kagent-shaped client" {
		t.Fatalf("%s %q", task.Status.State, taskText(task))
	}
	var events int
	for ev, err := range c.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: textMessage("stream")}) {
		if err != nil {
			t.Fatal(err)
		}
		_ = ev
		events++
	}
	if events < 3 {
		t.Fatalf("a stream has a task, chunks and a final status; got %d events", events)
	}
}
