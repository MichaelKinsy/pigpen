package a2aext

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

// ProtocolVersion is the only A2A protocol version served and spoken.
const ProtocolVersion = a2a.Version

const (
	maxBodyBytes    = 1 << 20
	agentVersion    = "0.1.0"
	shutdownTimeout = 20 * time.Second
)

// Server is the A2A listener.
type Server struct {
	cfg     Config
	auth    *Authenticator
	exec    *executor
	handler http.Handler
	cancel  context.CancelFunc

	mu     sync.Mutex
	srv    *http.Server
	ln     net.Listener
	served chan struct{} // closed when the Serve goroutine has returned
	logs   []string

	// The Agent Card is the same document for every request, so its handler (which holds the encoded card) is
	// kept for as long as the advertised URL is the same.
	cardMu      sync.Mutex
	cardURL     string
	cardHandler http.Handler
}

// NewServer builds the server. It does not listen until Start.
func NewServer(cfg Config, worker Worker, getenv func(string) string) (*Server, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	cfg.applyDefaults()
	if err := cfg.validate(getenv); err != nil {
		return nil, err
	}
	if worker == nil {
		return nil, errors.New("a2a: server needs a worker")
	}
	auth, err := NewAuthenticator(cfg.Tokens, getenv, cfg.InsecureNoAuth)
	if err != nil {
		return nil, err
	}
	base, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, auth: auth, cancel: cancel}
	s.exec = newExecutor(base, worker, cfg.MaxConcurrentTasks, time.Duration(cfg.TaskTimeoutSeconds)*time.Second, s.logf)

	// The store is ours so that guard can ask it who owns a task: a2a-go's SubscribeToTask attaches to a live
	// task's event queue without consulting the store, so without this check any authenticated caller who
	// knew a task id could read another tenant's live stream (found by TestSubscribeToALiveTaskDeliversTheRest).
	store := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{Authenticator: a2asrv.NewTaskStoreAuthenticator()})
	handler := a2asrv.NewHandler(s.exec,
		a2asrv.WithTaskStore(store),
		a2asrv.WithCallInterceptors(guard{store: store}),
		// The a2a-go logger writes to stderr, which is PiG's terminal when the extension is fused.
		a2asrv.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	mux := http.NewServeMux()
	mux.Handle("POST /{$}", http.MaxBytesHandler(a2asrv.NewJSONRPCHandler(handler), maxBodyBytes))
	mux.HandleFunc(a2asrv.WellKnownAgentCardPath, s.serveCard)
	s.handler = auth.Wrap(mux, func(r *http.Request) bool { return r.URL.Path == a2asrv.WellKnownAgentCardPath })
	return s, nil
}

func (s *Server) logf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, fmt.Sprintf(format, args...))
	if len(s.logs) > 50 {
		s.logs = s.logs[len(s.logs)-50:]
	}
}

// RecentLogs returns the last server messages (task failures, listener errors).
func (s *Server) RecentLogs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logs...)
}

// Handler returns the HTTP handler (authentication included).
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) baseURL() string {
	if s.cfg.ExternalURL != "" {
		return strings.TrimRight(s.cfg.ExternalURL, "/")
	}
	scheme := "http"
	if s.cfg.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + s.Addr()
}

func (s *Server) card() *a2a.AgentCard {
	return &a2a.AgentCard{
		Name: s.cfg.Name, Description: s.cfg.Description, Version: agentVersion,
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(s.baseURL(), a2a.TransportProtocolJSONRPC)},
		Capabilities:        a2a.AgentCapabilities{Streaming: true},
		DefaultInputModes:   []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"},
		Skills: []a2a.AgentSkill{{
			ID: "pig-task", Name: "PiG task",
			Description: "Runs a task with a PiG coding agent. Each contextId is one PiG session; a later task in the same context continues it.",
			Tags:        []string{"coding", "pig"}, Examples: []string{"Summarise the layout of the repository."},
			InputModes: []string{"text/plain"}, OutputModes: []string{"text/plain"},
		}},
		SecuritySchemes: a2a.NamedSecuritySchemes{"bearer": a2a.HTTPAuthSecurityScheme{Scheme: "Bearer", Description: "Static bearer token issued by the operator."}},
		SecurityRequirements: a2a.SecurityRequirementsOptions{
			a2a.SecurityRequirements{"bearer": a2a.SecuritySchemeScopes{}},
		},
	}
}

func (s *Server) serveCard(w http.ResponseWriter, r *http.Request) {
	url := s.baseURL()
	s.cardMu.Lock()
	if s.cardHandler == nil || s.cardURL != url {
		s.cardHandler, s.cardURL = a2asrv.NewStaticAgentCardHandler(s.card()), url
	}
	h := s.cardHandler
	s.cardMu.Unlock()
	h.ServeHTTP(w, r)
}

// Start listens and serves in the background.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		return errors.New("a2a: server already started")
	}
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("a2a: listen %s: %w", s.cfg.Listen, err)
	}
	// The default error log writes to stderr, which is PiG's terminal when the extension is fused.
	srv := &http.Server{Handler: s.handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		ErrorLog: log.New(io.Discard, "", 0)}
	if s.cfg.TLS != nil {
		cert, err := tls.LoadX509KeyPair(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("a2a: load TLS key pair: %w", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		ln = tls.NewListener(ln, srv.TLSConfig)
	}
	s.srv, s.ln = srv, ln
	s.served = make(chan struct{})
	go func() {
		defer close(s.served)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logf("listener stopped: %v", err)
		}
	}()
	return nil
}

// Addr is the bound address, or "" before Start.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// ActiveTasks counts tasks that are queued or running.
func (s *Server) ActiveTasks() int { return int(s.exec.active.Load()) }

// Shutdown stops accepting connections, cancels running turns and waits for their workers.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	srv, ln, served := s.srv, s.ln, s.served
	s.mu.Unlock()
	s.cancel()
	var errs []error
	if err := s.exec.shutdown(ctx); err != nil {
		errs = append(errs, err)
	}
	if srv != nil {
		// Streams end once their tasks stop; close whatever is left.
		sctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			_ = srv.Close()
		}
		// http.Server closes only the listeners its Serve goroutine has already registered. If Shutdown wins the
		// race with that goroutine's start, the port would stay open until it ran, so close it here as well
		// (a second close is harmless) and wait for Serve to return.
		_ = ln.Close()
		<-served
	}
	return errors.Join(errs...)
}

// guard is the call interceptor that enforces the pinned protocol version, attaches the
// authenticated principal and keeps callers inside their tenant.
type guard struct {
	store taskstore.Store
	a2asrv.PassthroughCallInterceptor
}

func (g guard) Before(ctx context.Context, callCtx *a2asrv.CallContext, req *a2asrv.Request) (context.Context, any, error) {
	if v, ok := callCtx.ServiceParams().Get(a2a.SvcParamVersion); !ok || !versionsOK(v) {
		return ctx, nil, a2a.ErrVersionNotSupported
	}
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return ctx, nil, a2a.ErrUnauthenticated
	}
	if t := callCtx.Tenant(); t != "" && t != p.Tenant {
		return ctx, nil, a2a.ErrUnauthorized
	}
	callCtx.User = a2asrv.NewAuthenticatedUser(p.Key(), map[string]any{"name": p.Name, "tenant": p.Tenant})
	if sub, ok := req.Payload.(*a2a.SubscribeToTaskRequest); ok && sub != nil && g.store != nil {
		// Ownership is masked as "not found", as the spec's task lookups are (§3.3.2).
		if _, err := g.store.Get(ctx, sub.ID); err != nil {
			return ctx, nil, a2a.ErrTaskNotFound
		}
	}
	return ctx, nil, nil
}

// versionsOK accepts the header when every value it carries is 1.0. kagent's client sends the
// header twice (the SDK's own and its static-header interceptor's), which HTTP folds into "1.0, 1.0".
func versionsOK(values []string) bool {
	n := 0
	for _, v := range values {
		for _, one := range strings.Split(v, ",") {
			if !versionOK(strings.TrimSpace(one)) {
				return false
			}
			n++
		}
	}
	return n > 0
}

func versionOK(v string) bool {
	return v == string(ProtocolVersion) || strings.HasPrefix(v, string(ProtocolVersion)+".")
}
