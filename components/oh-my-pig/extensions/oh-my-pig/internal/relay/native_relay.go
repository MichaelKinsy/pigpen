package relay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// upstream: packages/coding-agent/src/tools/browser/relay/native-relay.ts:1-190

const (
	relinkMinDelay = 250 * time.Millisecond
	relinkMaxDelay = 5 * time.Second
	restartGrace   = 100 * time.Millisecond
)

// RelayLinkPort is what ServeRelayLink needs from the native host's stdio port.
// upstream: native-relay.ts:28-41
type RelayLinkPort struct {
	Port        int
	ExtensionID string
	Next        func() (*ToNativeHost, error)
	Send        func(FromNativeHost)
	RelayLine   func(string)
	Log         func(string)
}

type relayUpstream struct {
	send  func(line string)
	close func()
	ended chan string
}

// ServeRelayLink keeps this browser linked to a relay until the extension disconnects,
// ctx cancels, or this host (as leader) accepts a restart request.
// upstream: native-relay.ts:47-190
func ServeRelayLink(ctx context.Context, port RelayLinkPort) {
	bl := &browserLink{
		port: port,
		left: make(chan struct{}),
	}
	bl.run(ctx)
}

type browserLink struct {
	port RelayLinkPort
	left chan struct{}

	mu         sync.Mutex
	server     *RelayServer
	upstream   *relayUpstream
	gone       bool
	restarting bool
}

func (bl *browserLink) run(ctx context.Context) {
	go bl.pump()

	go func() {
		<-ctx.Done()
		bl.markGone()
	}()

	pause := relinkMinDelay
	for {
		bl.mu.Lock()
		stop := bl.gone || bl.restarting
		bl.mu.Unlock()
		if stop {
			break
		}

		var up *relayUpstream
		if bl.port.Port == 0 {
			// Dynamic port mode (production): follow an existing live leader under EndpointsDir()
			// first so a second Chrome instance joins the first leader and promotes on exit;
			// if none answers, bind a fresh OS-assigned port and lead.
			up = bl.followDiscovered(ctx)
			if up == nil {
				up = bl.lead()
			}
		} else {
			// Fixed port mode (tests): first to bind leads, later ones follow on that port.
			up = bl.lead()
			if up == nil {
				up = bl.followPort(ctx, bl.port.Port, "")
			}
		}
		if up == nil {
			timer := time.NewTimer(pause)
			select {
			case <-timer.C:
			case <-bl.left:
				timer.Stop()
			}
			pause *= 2
			if pause > relinkMaxDelay {
				pause = relinkMaxDelay
			}
			continue
		}

		bl.mu.Lock()
		if bl.gone {
			bl.mu.Unlock()
			up.close()
			break
		}
		bl.upstream = up
		srv := bl.server
		bl.mu.Unlock()

		pause = relinkMinDelay
		var unpublish func() error
		if srv != nil {
			fn, err := PublishRelayEndpoint(srv.Port, srv.AuthToken)
			if err != nil {
				bl.port.Log(fmt.Sprintf("cannot publish relay endpoint: %v", err))
			} else {
				unpublish = fn
			}
		}

		bl.port.Send(FromNativeHost{T: "linked"})
		reason := <-up.ended
		if unpublish != nil {
			_ = unpublish()
		}

		bl.mu.Lock()
		bl.upstream = nil
		stop = bl.gone || bl.restarting
		bl.mu.Unlock()
		if stop {
			break
		}
		bl.port.Send(FromNativeHost{T: "unlinked", Reason: reason})
	}

	bl.mu.Lock()
	srv := bl.server
	bl.server = nil
	bl.mu.Unlock()
	if srv != nil {
		srv.Stop()
	}
}
func (bl *browserLink) markGone() {
	bl.mu.Lock()
	if bl.gone {
		bl.mu.Unlock()
		return
	}
	bl.gone = true
	up := bl.upstream
	close(bl.left)
	bl.mu.Unlock()
	if up != nil {
		up.close()
	}
}

func (bl *browserLink) pump() {
	for {
		msg, err := bl.port.Next()
		if err != nil || msg == nil {
			break
		}
		if msg.T == "relay" && msg.Line != nil {
			bl.mu.Lock()
			up := bl.upstream
			bl.mu.Unlock()
			if up != nil {
				up.send(*msg.Line)
			}
		} else {
			bl.port.Log(fmt.Sprintf("ignoring %s on a relay link", msg.T))
		}
	}
	bl.markGone()
}

type inProcSocket struct {
	sendFn  func(string)
	closeFn func()
}

func (s *inProcSocket) Send(text string) { s.sendFn(text) }
func (s *inProcSocket) Close()           { s.closeFn() }

func (bl *browserLink) lead() *relayUpstream {
	bl.mu.Lock()
	srv := bl.server
	bl.mu.Unlock()
	if srv == nil {
		srv = bl.serve()
		if srv == nil {
			return nil
		}
	}
	bridge := srv.Bridge
	ended := make(chan string, 1)
	var endOnce sync.Once
	finish := func(reason string) {
		endOnce.Do(func() {
			ended <- reason
		})
	}
	var sock *inProcSocket
	sock = &inProcSocket{
		sendFn: func(line string) {
			bl.port.RelayLine(line)
		},
		closeFn: func() {
			go bridge.ExtClosed(sock)
			finish("the relay dropped this browser")
		},
	}
	bridge.ExtConnected(sock, bl.port.ExtensionID)
	return &relayUpstream{
		send: func(line string) {
			bridge.ExtMessage(sock, line)
		},
		close: func() {
			bridge.ExtClosed(sock)
			finish("the extension left")
		},
		ended: ended,
	}
}

func (bl *browserLink) serve() *RelayServer {
	srv, err := StartRelayServer(RelayServerOptions{
		Port:      bl.port.Port,
		OnRestart: bl.restart,
	})
	if err != nil {
		if !errors.Is(err, syscall.EADDRINUSE) {
			bl.port.Log(fmt.Sprintf("cannot serve the relay: %v", err))
		}
		return nil
	}
	bl.mu.Lock()
	bl.server = srv
	bl.mu.Unlock()
	return srv
}

func (bl *browserLink) followDiscovered(ctx context.Context) *relayUpstream {
	// Skip our own server if we are already leading.
	bl.mu.Lock()
	ownSrv := bl.server
	bl.mu.Unlock()
	if ownSrv != nil {
		return nil
	}
	for _, ep := range DiscoverRelayEndpointEntries() {
		if up := bl.followPort(ctx, ep.Port, ep.Token); up != nil {
			return up
		}
	}
	return nil
}

func (bl *browserLink) followPort(ctx context.Context, port int, token string) *relayUpstream {
	if token == "" {
		var err error
		token, err = RelayTokenForURL(fmt.Sprintf("http://127.0.0.1:%d", port))
		if err != nil || token == "" {
			return nil
		}
	}
	u := url.URL{
		Scheme: "ws",
		Host:   "127.0.0.1:" + strconv.Itoa(port),
		Path:   "/ext",
	}
	hdr := http.Header{}
	hdr.Set("Origin", "chrome-extension://"+bl.port.ExtensionID)
	hdr.Set("Authorization", "Bearer "+token)
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ws, err := DialWS(dialCtx, u.String(), &WSDialOptions{
		Headers:    hdr,
		MaxMsgSize: DefaultMaxWSMessageBytes,
	})
	if err != nil {
		return nil
	}
	ended := make(chan string, 1)
	var endOnce sync.Once
	finish := func(reason string) {
		endOnce.Do(func() {
			ended <- reason
		})
	}
	go func() {
		defer func() {
			_ = ws.Close()
			finish("the relay went away")
		}()
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			bl.port.RelayLine(string(msg))
		}
	}()
	return &relayUpstream{
		send: func(line string) {
			_ = ws.WriteText([]byte(line))
		},
		close: func() {
			_ = ws.Close()
		},
		ended: ended,
	}
}

func (bl *browserLink) restart(req RestartRequest) bool {
	bl.mu.Lock()
	if bl.restarting || !IsOlderOmpVersion(RelayVersion, req.Version) {
		bl.mu.Unlock()
		return false
	}
	if req.IdleOnly && bl.server != nil && bl.server.Bridge.CDPClientCount() > 0 {
		bl.mu.Unlock()
		return false
	}
	bl.restarting = true
	bl.mu.Unlock()
	time.AfterFunc(restartGrace, func() {
		bl.mu.Lock()
		up := bl.upstream
		bl.mu.Unlock()
		if up != nil {
			up.close()
		}
	})
	return true
}
