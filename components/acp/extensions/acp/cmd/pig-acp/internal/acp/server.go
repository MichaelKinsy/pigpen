package acp

import (
	"encoding/json"
	"io"
	"time"

	"github.com/MichaelKinsy/pigpen/acp/cmd/pig-acp/internal/jsonrpc"
)

// ServeOptions configure Serve.
type ServeOptions struct {
	// NewAgent builds the agent for a connection; the default is NewAgent.
	NewAgent func(Conn) *Agent
	// DrainTimeout bounds how long Serve waits, after the client closed its output, for requests in
	// flight to be answered. Zero means three seconds.
	DrainTimeout time.Duration
	// Done ends Serve early (SIGINT, SIGTERM); the agent is disposed as for a closed input.
	Done <-chan struct{}
}

// wireConn is the agent's view of the client connection.
type wireConn struct{ c *jsonrpc.Conn }

func (w wireConn) SessionUpdate(sessionID string, update Update) error {
	return w.c.Notify("session/update", map[string]any{"sessionId": sessionID, "update": update})
}

func (w wireConn) RequestPermission(req PermissionRequest) (PermissionResponse, error) {
	var resp PermissionResponse
	err := w.c.Call("session/request_permission", req, &resp)
	return resp, err
}

// Serve speaks ACP on r and w until the client closes r or w fails. It disposes the agent
// (killing every pig child) before it returns. Nothing but protocol messages is written to w.
func Serve(r io.Reader, w io.Writer, opts ServeOptions) error {
	newAgent := opts.NewAgent
	if newAgent == nil {
		newAgent = NewAgent
	}
	var agent *Agent
	ready := make(chan struct{})
	conn := jsonrpc.New(r, w, func(req *jsonrpc.Request) (any, error) {
		<-ready
		return dispatch(agent, req)
	})
	agent = newAgent(wireConn{conn})
	close(ready)
	select {
	case <-conn.Done():
	case <-opts.Done:
	}
	d := opts.DrainTimeout
	if d == 0 {
		d = 3 * time.Second
	}
	conn.Drain(d)
	agent.Dispose()
	return nil
}

// decode fills v from the request params; a missing or malformed body is invalid params.
func decode(req *jsonrpc.Request, v any) error {
	raw := req.Params
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return ErrInvalidParams(nil, err.Error())
	}
	return nil
}

func need(cond bool, what string) error {
	if !cond {
		return ErrInvalidParams(nil, what)
	}
	return nil
}

// dispatch routes one request. Methods the adapter does not implement, among them every method
// that would delegate to the client (fs/*, terminal/*) or that the adapter did not advertise
// (session/fork, session/resume, session/close), are Method not found.
func dispatch(base *Agent, req *jsonrpc.Request) (any, error) {
	// Deferred work (available commands, usage) runs once this request's response is written.
	a := base.Scoped(req.AfterResponse)
	switch req.Method {
	case "initialize":
		var p struct {
			InitializeRequest
			ProtocolVersion *int `json:"protocolVersion"`
		}
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		if err := need(p.ProtocolVersion != nil, "protocolVersion is required"); err != nil {
			return nil, err
		}
		p.InitializeRequest.ProtocolVersion = *p.ProtocolVersion
		return a.Initialize(p.InitializeRequest)
	case "authenticate":
		var p AuthenticateRequest
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		if err := a.Authenticate(p); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	case "session/new":
		var p struct {
			Cwd        *string `json:"cwd"`
			McpServers []any   `json:"mcpServers"`
		}
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		if err := need(p.Cwd != nil, "cwd is required"); err != nil {
			return nil, err
		}
		return a.NewSession(NewSessionRequest{Cwd: *p.Cwd, McpServers: orEmpty(p.McpServers)})
	case "session/load":
		var p struct {
			SessionID *string `json:"sessionId"`
			Cwd       *string `json:"cwd"`
			Mcp       []any   `json:"mcpServers"`
		}
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		if err := need(p.SessionID != nil && p.Cwd != nil, "sessionId and cwd are required"); err != nil {
			return nil, err
		}
		return a.LoadSession(LoadSessionRequest{SessionID: *p.SessionID, Cwd: *p.Cwd, McpServers: orEmpty(p.Mcp)})
	case "session/list":
		var p ListSessionsRequest
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		return a.ListSessions(p)
	case "session/delete":
		var p struct {
			SessionID *string `json:"sessionId"`
		}
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		if err := need(p.SessionID != nil, "sessionId is required"); err != nil {
			return nil, err
		}
		return a.DeleteSession(DeleteSessionRequest{SessionID: *p.SessionID})
	case "session/prompt":
		var p struct {
			SessionID *string        `json:"sessionId"`
			Prompt    []ContentBlock `json:"prompt"`
		}
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		if err := need(p.SessionID != nil && p.Prompt != nil, "sessionId and prompt are required"); err != nil {
			return nil, err
		}
		return a.Prompt(PromptRequest{SessionID: *p.SessionID, Prompt: p.Prompt})
	case "session/cancel":
		var p struct {
			SessionID *string `json:"sessionId"`
		}
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		if err := need(p.SessionID != nil, "sessionId is required"); err != nil {
			return nil, err
		}
		if err := a.Cancel(*p.SessionID); err != nil {
			return nil, err
		}
		return nil, nil
	case "session/set_mode":
		var p SetSessionModeRequest
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		if err := need(p.SessionID != "", "sessionId is required"); err != nil {
			return nil, err
		}
		return a.SetSessionMode(p)
	case "session/set_config_option":
		var p SetSessionConfigOptionRequest
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		if err := need(p.SessionID != "" && p.ConfigID != "", "sessionId and configId are required"); err != nil {
			return nil, err
		}
		return a.SetSessionConfigOption(p)
	case "session/set_model":
		var p SetSessionModelRequest
		if err := decode(req, &p); err != nil {
			return nil, err
		}
		if err := need(p.SessionID != "" && p.ModelID != "", "sessionId and modelId are required"); err != nil {
			return nil, err
		}
		if err := a.UnstableSetSessionModel(p); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	}
	return nil, ErrMethodNotFound(req.Method)
}

func orEmpty(v []any) []any {
	if v == nil {
		return []any{}
	}
	return v
}
