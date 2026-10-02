// Command kagent-a2a-echo serves an A2A agent with kagent's own A2A server package
// (github.com/kagent-dev/kagent/go/adk/pkg/a2a/server, Apache-2.0), the code kagent agents use to
// speak A2A. scripts/interop-kagent.mjs copies that package from a kagent checkout and changes one
// thing: its hard-coded readiness listener (":8081") binds an ephemeral port, so it can run next to
// another service. Every A2A handler is unmodified. The executor echoes the message; the message "sleep" waits for cancellation.
// It exists for the pigpen a2a interop test.
package main

import (
	"context"
	"flag"
	"iter"
	"log/slog"
	"os"
	"strings"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	a2aserver "kagentinterop/server"
)

type echo struct{}

func (echo) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2atype.Event, error] {
	return func(yield func(a2atype.Event, error) bool) {
		var text strings.Builder
		for _, p := range ec.Message.Parts {
			text.WriteString(p.Text())
		}
		if ec.StoredTask == nil && !yield(a2atype.NewSubmittedTask(ec, ec.Message), nil) {
			return
		}
		if !yield(a2atype.NewStatusUpdateEvent(ec, a2atype.TaskStateWorking, nil), nil) {
			return
		}
		if text.String() == "sleep" {
			<-ctx.Done()
			return
		}
		if !yield(a2atype.NewArtifactEvent(ec, a2atype.NewTextPart("kagent echo: "+text.String())), nil) {
			return
		}
		yield(a2atype.NewStatusUpdateEvent(ec, a2atype.TaskStateCompleted, nil), nil)
	}
}

func (echo) Cancel(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2atype.Event, error] {
	return func(yield func(a2atype.Event, error) bool) {
		yield(a2atype.NewStatusUpdateEvent(ec, a2atype.TaskStateCanceled, nil), nil)
	}
}

func main() {
	port := flag.String("port", "8083", "port")
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	card := a2atype.AgentCard{
		Name: "kagent-echo", Description: "kagent A2A server package with an echo executor", Version: "1",
		SupportedInterfaces: []*a2atype.AgentInterface{a2atype.NewAgentInterface("http://kagent-controller.kagent.svc.cluster.local:8083/api/a2a/kagent/echo", a2atype.TransportProtocolJSONRPC)},
		Capabilities:        a2atype.AgentCapabilities{Streaming: true},
		DefaultInputModes:   []string{"text"}, DefaultOutputModes: []string{"text"},
		Skills: []a2atype.AgentSkill{{ID: "echo", Name: "Echo", Description: "echoes", Tags: []string{"echo"}}},
	}
	srv, err := a2aserver.NewA2AServer(card, echo{}, logger, a2aserver.ServerConfig{Host: "127.0.0.1", Port: *port})
	if err != nil {
		logger.Error("new server", "error", err)
		os.Exit(1)
	}
	if err := srv.Run(); err != nil {
		logger.Error("run", "error", err)
		os.Exit(1)
	}
}
