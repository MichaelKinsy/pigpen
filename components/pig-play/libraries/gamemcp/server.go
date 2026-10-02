package gamemcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Path is the one URL path the server answers on.
const Path = "/mcp"

// maxBody bounds one request body. Tool arguments are a few bytes.
const maxBody = 64 << 10

// supportedVersions are the MCP protocol versions the server speaks, newest first. The server
// echoes the client's version when it is listed and answers the newest otherwise.
var supportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// Tool is one MCP tool. Call receives the raw JSON arguments and returns the value the agent
// reads, or an error that reaches the agent as a tool error (isError) rather than a protocol
// error.
type Tool struct {
	Name         string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
	Call         func(args json.RawMessage) (Result, error)
}

// Result is the typed value of one tool call. Value is what the agent reads as the text
// content; Structured, when set, is sent as structuredContent (an MCP object).
type Result struct {
	Value      any
	Structured map[string]any
}

// Server is the listener. It binds 127.0.0.1 only, so nothing off the machine can reach it,
// and it answers only requests that carry its bearer token.
type Server struct {
	name  string
	token string
	// session is the Mcp-Session-Id. It is random on its own: it is no part of the token, so
	// a client or a log that shows the session id shows nothing of the secret.
	session  string
	tools    []Tool
	listener net.Listener
	http     *http.Server
	addr     string

	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// StartServer listens on 127.0.0.1 with a random port and a random token and starts serving
// tools. Close stops it.
func StartServer(name, instructions string, tools []Tool) (*Server, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	session, err := randomToken()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen on 127.0.0.1: %w", err)
	}
	s := &Server{name: name, token: token, session: session[:32], tools: slices.Clone(tools), listener: listener, addr: listener.Addr().String(), done: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc(Path, func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, instructions) })
	s.http = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		defer close(s.done)
		_ = s.http.Serve(listener)
	}()
	return s, nil
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("make a random id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// URL is the endpoint the host connects to.
func (s *Server) URL() string { return "http://" + s.addr + Path }

// Addr is the listener's host:port.
func (s *Server) Addr() string { return s.addr }

// Token is the bearer token every request must carry.
func (s *Server) Token() string { return s.token }

// Close stops listening, drops open connections and waits for the serve loop. It is safe to
// call more than once.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.http.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, s.http.Close())
	}
	<-s.done
	return err
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const (
	codeParse          = -32700
	codeInvalid        = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

func (s *Server) serve(w http.ResponseWriter, r *http.Request, instructions string) {
	if !s.localRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="pig-games"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	default:
		// No server-to-client stream: the tools have no notifications to push.
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: codeParse, Message: "empty body"}})
		return
	}
	var batch []json.RawMessage
	isBatch := body[0] == '['
	if isBatch {
		if err := json.Unmarshal(body, &batch); err != nil || len(batch) == 0 {
			writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: codeInvalid, Message: "invalid batch"}})
			return
		}
	} else {
		batch = []json.RawMessage{body}
	}
	var replies []rpcResponse
	for _, raw := range batch {
		if reply, ok := s.handle(raw, instructions, w); ok {
			replies = append(replies, reply)
		}
	}
	if len(replies) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if isBatch {
		writeJSON(w, http.StatusOK, replies)
		return
	}
	writeJSON(w, http.StatusOK, replies[0])
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// localRequest rejects a request whose peer is not loopback or whose Host is not this
// listener's own address, so a web page cannot reach the server by rebinding a DNS name.
func (s *Server) localRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return false
	}
	_, port, _ := net.SplitHostPort(s.addr)
	return slices.Contains([]string{s.addr, "localhost:" + port}, strings.ToLower(r.Host))
}

func (s *Server) authorized(r *http.Request) bool {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(s.token)) == 1
}

// handle answers one JSON-RPC message. A notification or a response yields no reply.
func (s *Server) handle(raw json.RawMessage, instructions string, w http.ResponseWriter) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: codeParse, Message: "invalid JSON"}}, true
	}
	isRequest := len(req.ID) > 0 && string(req.ID) != "null"
	if req.Method == "" {
		// A response from the client to a server request: the server sends none.
		return rpcResponse{}, false
	}
	reply := func(result any) (rpcResponse, bool) {
		if !isRequest {
			return rpcResponse{}, false
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	}
	fail := func(code int, message string) (rpcResponse, bool) {
		if !isRequest {
			return rpcResponse{}, false
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: message}}, true
	}
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := supportedVersions[0]
		if slices.Contains(supportedVersions, params.ProtocolVersion) {
			version = params.ProtocolVersion
		}
		result := map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.name, "version": "0.1.0"},
		}
		if instructions != "" {
			result["instructions"] = instructions
		}
		w.Header().Set("Mcp-Session-Id", s.session)
		return reply(result)
	case "ping":
		return reply(map[string]any{})
	case "tools/list":
		return reply(map[string]any{"tools": s.toolList()})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
			return fail(codeInvalidParams, "tools/call needs a tool name")
		}
		i := slices.IndexFunc(s.tools, func(t Tool) bool { return t.Name == params.Name })
		if i < 0 {
			return fail(codeInvalidParams, "unknown tool "+params.Name)
		}
		return reply(s.call(s.tools[i], params.Arguments))
	default:
		if strings.HasPrefix(req.Method, "notifications/") {
			return rpcResponse{}, false
		}
		return fail(codeMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) toolList() []map[string]any {
	list := make([]map[string]any, 0, len(s.tools))
	for _, tool := range s.tools {
		entry := map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": tool.InputSchema}
		if tool.OutputSchema != nil {
			entry["outputSchema"] = tool.OutputSchema
		}
		list = append(list, entry)
	}
	return list
}

func (s *Server) call(tool Tool, args json.RawMessage) map[string]any {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	result, err := tool.Call(args)
	if err != nil {
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true}
	}
	text, err := json.Marshal(result.Value)
	if err != nil {
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": "encode result: " + err.Error()}}, "isError": true}
	}
	out := map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}, "isError": false}
	if result.Structured != nil {
		out["structuredContent"] = result.Structured
	}
	return out
}
