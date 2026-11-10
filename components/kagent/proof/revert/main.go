// Command revert reads one kagent Session and, with -revert, calls SessionService.RevertSession on it. It is the client
// of the crash-and-revert check (crashrevert.sh): kagent v1.0.0 has no CLI or HTTP route for RevertSession.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
)

func main() {
	addr := flag.String("addr", "localhost:18083", "controller API address (host:port)")
	id := flag.String("session", "", "Session id (the A2A context id)")
	revert := flag.Bool("revert", false, "call RevertSession before reading the Session")
	flag.Parse()
	if *id == "" {
		fmt.Fprintln(os.Stderr, "revert: -session is required")
		os.Exit(2)
	}
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "revert:", err)
		os.Exit(1)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(context.Background(), "x-user-id", "admin@kagent.dev"), 3*time.Minute)
	defer cancel()
	sessions := apiv1.NewSessionServiceClient(conn)
	if *revert {
		reverted, err := sessions.RevertSession(ctx, &apiv1.RevertSessionRequest{SessionId: *id})
		if err != nil {
			fmt.Println("RevertSession error:", err)
			os.Exit(3)
		}
		fmt.Println("RevertSession ok:", reverted.GetSession().GetState(), reverted.GetSession().GetOperation())
	}
	got, err := sessions.GetSession(ctx, &apiv1.GetSessionRequest{SessionId: *id})
	if err != nil {
		fmt.Println("GetSession error:", err)
		os.Exit(1)
	}
	fmt.Println("GetSession:", got.GetSession().GetState(), got.GetSession().GetOperation())
}
