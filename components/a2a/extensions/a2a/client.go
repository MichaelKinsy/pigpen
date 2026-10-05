package a2aext

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
)

// Remotes calls configured A2A agents over protocol 1.0 (JSON-RPC binding).
type Remotes struct {
	cfg    map[string]RemoteAgent
	getenv func(string) string

	mu      sync.Mutex
	clients map[string]*a2aclient.Client
	cards   map[string]*a2a.AgentCard
}

// SendArgs are the inputs of Send.
type SendArgs struct {
	Agent, Message, ContextID, TaskID string
	// OnTask, if set, is called once, as soon as the remote reports the task's ids.
	OnTask func(taskID, contextID string)
}

// TaskSummary is what a remote task looks like to the model.
type TaskSummary struct {
	TaskID    string
	ContextID string
	// State is lower-case with hyphens: submitted, working, input-required, completed, canceled, failed, rejected, auth-required.
	State    string
	Text     string
	Terminal bool
}

// NewRemotes builds the client set. Nothing is dialled until a call is made.
func NewRemotes(cfg map[string]RemoteAgent, getenv func(string) string) *Remotes {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	withDefaults := make(map[string]RemoteAgent, len(cfg))
	for n, ra := range cfg {
		if ra.TimeoutSeconds <= 0 {
			ra.TimeoutSeconds = defaultRemoteTimeout
		}
		withDefaults[n] = ra
	}
	cfg = withDefaults
	return &Remotes{cfg: cfg, getenv: getenv, clients: map[string]*a2aclient.Client{}, cards: map[string]*a2a.AgentCard{}}
}

// Names lists configured remote names in order.
func (r *Remotes) Names() []string {
	names := make([]string, 0, len(r.cfg))
	for n := range r.cfg {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (r *Remotes) remote(name string) (RemoteAgent, error) {
	ra, ok := r.cfg[name]
	if !ok {
		if len(r.cfg) == 0 {
			return RemoteAgent{}, errors.New("a2a: no remotes are configured (add a \"remotes\" object to a2a.json)")
		}
		return RemoteAgent{}, fmt.Errorf("a2a: unknown agent %q; configured remotes: %s", name, strings.Join(r.Names(), ", "))
	}
	return ra, nil
}

func (ra RemoteAgent) hasCredentials() bool { return ra.BearerTokenEnv != "" || len(ra.HeaderEnv) > 0 }

func (r *Remotes) headers(name string, ra RemoteAgent) (http.Header, error) {
	h := http.Header{}
	if ra.BearerTokenEnv != "" {
		v := r.getenv(ra.BearerTokenEnv)
		if v == "" {
			return nil, fmt.Errorf("a2a: remote %q needs the environment variable %s (bearerTokenEnv), which is not set", name, ra.BearerTokenEnv)
		}
		h.Set("Authorization", "Bearer "+v)
	}
	for header, env := range ra.HeaderEnv {
		v := r.getenv(env)
		if v == "" {
			return nil, fmt.Errorf("a2a: remote %q needs the environment variable %s (headerEnv %s), which is not set", name, env, header)
		}
		h.Set(header, v)
	}
	return h, nil
}

type headerTransport struct {
	h      http.Header
	host   string // credentials go to this host only
	scheme string // and over this scheme only (an https remote's token never goes over http)
	base   http.RoundTripper
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if req.URL.Host != t.host {
		return nil, fmt.Errorf("a2a: refusing to send credentials to %s (the remote is configured at %s)", req.URL.Host, t.host)
	}
	if req.URL.Scheme != t.scheme {
		return nil, fmt.Errorf("a2a: refusing to send credentials over %s (the remote is configured with %s)", req.URL.Scheme, t.scheme)
	}
	for k, v := range t.h {
		req.Header[k] = v
	}
	return t.base.RoundTrip(req)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

func schemeOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme
}

// originOf is scheme://host, the unit credentials are pinned to.
func originOf(raw string) string { return schemeOf(raw) + "://" + hostOf(raw) }

func (r *Remotes) httpClient(name string, ra RemoteAgent) (*http.Client, error) {
	h, err := r.headers(name, ra)
	if err != nil {
		return nil, err
	}
	var rt http.RoundTripper = http.DefaultTransport
	if len(h) > 0 {
		rt = headerTransport{h: h, host: hostOf(ra.URL), scheme: schemeOf(ra.URL), base: http.DefaultTransport}
	}
	return &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func (r *Remotes) call(ctx context.Context, ra RemoteAgent) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(ra.TimeoutSeconds)*time.Second)
}

// Card resolves a remote's agent card.
func (r *Remotes) Card(ctx context.Context, name string) (*a2a.AgentCard, error) {
	ra, err := r.remote(name)
	if err != nil {
		return nil, err
	}
	if ra.SkipCard {
		return nil, fmt.Errorf("a2a: remote %q is configured with skipCard; it has no card to show", name)
	}
	return r.card(ctx, name, ra)
}

func (r *Remotes) card(ctx context.Context, name string, ra RemoteAgent) (*a2a.AgentCard, error) {
	r.mu.Lock()
	if c := r.cards[name]; c != nil {
		r.mu.Unlock()
		return c, nil
	}
	r.mu.Unlock()
	hc, err := r.httpClient(name, ra)
	if err != nil {
		return nil, err
	}
	ctx, cancel := r.call(ctx, ra)
	defer cancel()
	card, err := agentcard.NewResolver(hc).Resolve(ctx, ra.URL)
	if err != nil {
		return nil, fmt.Errorf("a2a: fetch the agent card of %q: %w", name, err)
	}
	r.mu.Lock()
	r.cards[name] = card
	r.mu.Unlock()
	return card, nil
}

func (r *Remotes) client(ctx context.Context, name string, ra RemoteAgent) (*a2aclient.Client, error) {
	r.mu.Lock()
	if c := r.clients[name]; c != nil {
		r.mu.Unlock()
		return c, nil
	}
	r.mu.Unlock()
	hc, err := r.httpClient(name, ra)
	if err != nil {
		return nil, err
	}
	// Defaults are disabled so nothing but the pinned JSON-RPC 1.0 transport can be selected.
	opts := []a2aclient.FactoryOption{a2aclient.WithDefaultsDisabled(), a2aclient.WithJSONRPCTransport(hc)}
	var c *a2aclient.Client
	if ra.SkipCard {
		c, err = a2aclient.NewFromEndpoints(ctx, []*a2a.AgentInterface{a2a.NewAgentInterface(ra.URL, a2a.TransportProtocolJSONRPC)}, opts...)
	} else {
		card, cerr := r.card(ctx, name, ra)
		if cerr != nil {
			return nil, cerr
		}
		iface, ierr := pinnedInterface(name, ra, card)
		if ierr != nil {
			return nil, ierr
		}
		c, err = a2aclient.NewFromEndpoints(ctx, []*a2a.AgentInterface{iface}, opts...)
	}
	if err != nil {
		return nil, fmt.Errorf("a2a: connect to %q: %w", name, err)
	}
	r.mu.Lock()
	r.clients[name] = c
	r.mu.Unlock()
	return c, nil
}

// pinnedInterface picks the card's A2A 1.0 JSON-RPC interface. Credentials only go to the
// configured origin (scheme and host): a card is not trusted to redirect or downgrade them.
func pinnedInterface(name string, ra RemoteAgent, card *a2a.AgentCard) (*a2a.AgentInterface, error) {
	var offered []string
	for _, i := range card.SupportedInterfaces {
		offered = append(offered, string(i.ProtocolBinding)+" "+string(i.ProtocolVersion))
		if i.ProtocolBinding != a2a.TransportProtocolJSONRPC || !versionOK(string(i.ProtocolVersion)) {
			continue
		}
		if ra.hasCredentials() && originOf(i.URL) != originOf(ra.URL) {
			return nil, fmt.Errorf("a2a: the card of %q names %s, not the configured %s; not sending credentials there (set skipCard to use the configured URL as the endpoint)", name, originOf(i.URL), originOf(ra.URL))
		}
		return i, nil
	}
	return nil, fmt.Errorf("a2a: %q offers no A2A %s JSON-RPC interface (offers: %s); this adapter is pinned to protocol %s", name, ProtocolVersion, strings.Join(offered, ", "), ProtocolVersion)
}

func stateName(s a2a.TaskState) string {
	n := strings.TrimPrefix(string(s), "TASK_STATE_")
	return strings.ToLower(strings.ReplaceAll(n, "_", "-"))
}

func textOf(parts a2a.ContentParts) string {
	var b strings.Builder
	for _, p := range parts {
		if t, ok := p.Content.(a2a.Text); ok {
			b.WriteString(string(t))
		}
	}
	return b.String()
}

func messageText(m *a2a.Message) string {
	if m == nil {
		return ""
	}
	return textOf(m.Parts)
}

func summarize(t *a2a.Task) TaskSummary {
	var arts []string
	for _, a := range t.Artifacts {
		if s := textOf(a.Parts); s != "" {
			arts = append(arts, s)
		}
	}
	return finish(TaskSummary{TaskID: string(t.ID), ContextID: t.ContextID, State: stateName(t.Status.State), Terminal: t.Status.State.Terminal()},
		strings.Join(arts, "\n\n"), messageText(t.Status.Message), t.Status.State)
}

func finish(s TaskSummary, artifactText, statusText string, state a2a.TaskState) TaskSummary {
	var parts []string
	if artifactText != "" {
		parts = append(parts, artifactText)
	}
	if statusText != "" && state != a2a.TaskStateCompleted {
		parts = append(parts, statusText)
	}
	s.Text = strings.Join(parts, "\n\n")
	return s
}

// Send sends a message and returns when the task is terminal or needs input. onProgress
// receives assistant text as it streams. Cancelling ctx (or the timeout) cancels the remote task.
func (r *Remotes) Send(ctx context.Context, args SendArgs, onProgress func(string)) (TaskSummary, error) {
	ra, err := r.remote(args.Agent)
	if err != nil {
		return TaskSummary{}, err
	}
	if strings.TrimSpace(args.Message) == "" {
		return TaskSummary{}, errors.New("a2a: message is empty")
	}
	callCtx, cancelCall := r.call(ctx, ra)
	defer cancelCall()
	c, err := r.client(callCtx, args.Agent, ra)
	if err != nil {
		return TaskSummary{}, err
	}
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(args.Message))
	msg.ContextID, msg.TaskID = args.ContextID, a2a.TaskID(args.TaskID)
	req := &a2a.SendMessageRequest{Message: msg}

	streaming := ra.SkipCard
	if !ra.SkipCard {
		if card, err := r.card(callCtx, args.Agent, ra); err == nil {
			streaming = card.Capabilities.Streaming
		}
	}
	var events func(func(a2a.Event, error) bool)
	if streaming {
		events = c.SendStreamingMessage(callCtx, req)
	} else {
		events = func(yield func(a2a.Event, error) bool) {
			res, err := c.SendMessage(callCtx, req)
			if err != nil {
				yield(nil, err)
				return
			}
			yield(res, nil)
		}
	}

	var (
		sum        TaskSummary
		artifacts  []a2a.ArtifactID
		artText    = map[a2a.ArtifactID]*strings.Builder{}
		statusText string
		state      a2a.TaskState
		final      *TaskSummary
	)
	assembled := func() string {
		var parts []string
		for _, id := range artifacts {
			if s := artText[id].String(); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	announced := false
	for ev, err := range events {
		if err != nil {
			return r.failedSend(ctx, callCtx, args.Agent, ra, sum, err)
		}
		switch e := ev.(type) {
		case *a2a.Task:
			s := summarize(e)
			final = &s
			sum.TaskID, sum.ContextID = s.TaskID, s.ContextID
			state, statusText = e.Status.State, messageText(e.Status.Message)
			for _, a := range e.Artifacts {
				if artText[a.ID] == nil {
					artText[a.ID] = &strings.Builder{}
					artifacts = append(artifacts, a.ID)
				}
				artText[a.ID].Reset()
				artText[a.ID].WriteString(textOf(a.Parts))
			}
		case *a2a.Message:
			s := TaskSummary{TaskID: string(e.TaskID), ContextID: e.ContextID, State: "message", Text: messageText(e), Terminal: true}
			return s, nil
		case *a2a.TaskStatusUpdateEvent:
			sum.TaskID, sum.ContextID = string(e.TaskID), e.ContextID
			state, statusText = e.Status.State, messageText(e.Status.Message)
			final = nil
		case *a2a.TaskArtifactUpdateEvent:
			sum.TaskID, sum.ContextID = string(e.TaskID), e.ContextID
			id := e.Artifact.ID
			if artText[id] == nil {
				artText[id] = &strings.Builder{}
				artifacts = append(artifacts, id)
			}
			if !e.Append {
				artText[id].Reset()
			}
			chunk := textOf(e.Artifact.Parts)
			artText[id].WriteString(chunk)
			if chunk != "" && onProgress != nil {
				onProgress(chunk)
			}
			final = nil
		}
		if !announced && sum.TaskID != "" {
			announced = true
			if args.OnTask != nil {
				args.OnTask(sum.TaskID, sum.ContextID)
			}
		}
	}
	if final != nil {
		return *final, nil
	}
	if state == a2a.TaskStateUnspecified {
		return sum, fmt.Errorf("a2a: %q ended the stream without a task state", args.Agent)
	}
	sum.State, sum.Terminal = stateName(state), state.Terminal()
	return finish(sum, assembled(), statusText, state), nil
}

// failedSend reports a broken or cancelled call and, when a remote task was started and is
// still going, asks the remote to cancel it.
func (r *Remotes) failedSend(caller, call context.Context, name string, ra RemoteAgent, sum TaskSummary, err error) (TaskSummary, error) {
	if sum.TaskID != "" && (caller.Err() != nil || call.Err() != nil) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(caller), 10*time.Second)
		defer cancel()
		if c, cerr := r.client(cctx, name, ra); cerr == nil {
			_, _ = c.CancelTask(cctx, &a2a.CancelTaskRequest{ID: a2a.TaskID(sum.TaskID)})
		}
	}
	switch {
	case caller.Err() != nil:
		return sum, caller.Err()
	case call.Err() != nil:
		return sum, fmt.Errorf("a2a: %q did not finish within %ds: %w", name, ra.TimeoutSeconds, call.Err())
	}
	return sum, fmt.Errorf("a2a: %q: %w", name, describe(err))
}

// describe keeps the protocol error but never a credential: errors from the transport
// carry status lines and bodies, not request headers.
func describe(err error) error {
	switch {
	case errors.Is(err, a2a.ErrUnauthenticated):
		return errors.New("authentication failed (the remote rejected the credentials)")
	case strings.Contains(err.Error(), "401"):
		return errors.New("authentication failed (HTTP 401; check the remote's bearerTokenEnv)")
	}
	return err
}

func (r *Remotes) list(ctx context.Context, name string) ([]TaskSummary, error) {
	ra, err := r.remote(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := r.call(ctx, ra)
	defer cancel()
	c, err := r.client(ctx, name, ra)
	if err != nil {
		return nil, err
	}
	res, err := c.ListTasks(ctx, &a2a.ListTasksRequest{IncludeArtifacts: true})
	if err != nil {
		return nil, fmt.Errorf("a2a: %q: %w", name, describe(err))
	}
	out := make([]TaskSummary, 0, len(res.Tasks))
	for _, t := range res.Tasks {
		out = append(out, summarize(t))
	}
	return out, nil
}

// GetTask fetches a remote task.
func (r *Remotes) GetTask(ctx context.Context, name, taskID string) (TaskSummary, error) {
	ra, err := r.remote(name)
	if err != nil {
		return TaskSummary{}, err
	}
	ctx, cancel := r.call(ctx, ra)
	defer cancel()
	c, err := r.client(ctx, name, ra)
	if err != nil {
		return TaskSummary{}, err
	}
	t, err := c.GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(taskID)})
	if err != nil {
		return TaskSummary{}, fmt.Errorf("a2a: %q: %w", name, describe(err))
	}
	return summarize(t), nil
}

// CancelTask cancels a remote task.
func (r *Remotes) CancelTask(ctx context.Context, name, taskID string) (TaskSummary, error) {
	ra, err := r.remote(name)
	if err != nil {
		return TaskSummary{}, err
	}
	ctx, cancel := r.call(ctx, ra)
	defer cancel()
	c, err := r.client(ctx, name, ra)
	if err != nil {
		return TaskSummary{}, err
	}
	t, err := c.CancelTask(ctx, &a2a.CancelTaskRequest{ID: a2a.TaskID(taskID)})
	if err != nil {
		return TaskSummary{}, fmt.Errorf("a2a: %q: %w", name, describe(err))
	}
	return summarize(t), nil
}
