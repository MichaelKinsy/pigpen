// Package mockls is a loopback stand-in for the LangSmith API: it answers what the SDK's tracing path calls and
// records every request, byte for byte, as JSONL (see package reqlog).
package mockls

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lstools/internal/reqlog"
)

// Server is the mock endpoint. It is an http.Handler.
type Server struct {
	cfg  Config
	info []byte

	mu    sync.Mutex
	log   io.Writer
	seq   int
	calls map[string]int
	// Body byte counters (as received, after Content-Encoding was undone), kept without -log.
	bytes        int64
	decodedBytes int64
	routeBytes   map[string]int64
	routeDecoded map[string]int64
	base         string
	logErr       error
}

// New builds a server that writes its request log to log (nil: no log). Call SetBase once the listen address is
// known, before serving.
func New(cfg *Config, log io.Writer) (*Server, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	s := &Server{cfg: *cfg, log: log, calls: map[string]int{}, routeBytes: map[string]int64{}, routeDecoded: map[string]int64{}}
	if s.cfg.MaxBodyBytes == 0 {
		s.cfg.MaxBodyBytes = 64 << 20
	}
	return s, nil
}

// SetBase sets the URL "{{base}}" expands to (for example http://127.0.0.1:34567).
func (s *Server) SetBase(base string) {
	s.mu.Lock()
	s.base = strings.TrimRight(base, "/")
	s.mu.Unlock()
}

// LogError is the first error writing the request log, if any.
func (s *Server) LogError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logErr
}

// Stats is what GET /__mockls/stats answers.
type Stats struct {
	Requests int            `json:"requests"`
	ByRoute  map[string]int `json:"byRoute"`
	// Bytes is the sum of the request bodies as received (still gzip-compressed when they were), ByRouteBytes the same per
	// route; DecodedBytes and ByRouteDecodedBytes count them after Content-Encoding was undone.
	Bytes               int64            `json:"bytes"`
	ByRouteBytes        map[string]int64 `json:"byRouteBytes"`
	DecodedBytes        int64            `json:"decodedBytes"`
	ByRouteDecodedBytes map[string]int64 `json:"byRouteDecodedBytes"`
}

// Stats returns the request counts so far.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{Requests: s.seq, ByRoute: map[string]int{}, Bytes: s.bytes, DecodedBytes: s.decodedBytes,
		ByRouteBytes: map[string]int64{}, ByRouteDecodedBytes: map[string]int64{}}
	for k, v := range s.calls {
		st.ByRoute[k] = v
	}
	for k, v := range s.routeBytes {
		st.ByRouteBytes[k] = v
	}
	for k, v := range s.routeDecoded {
		st.ByRouteDecodedBytes[k] = v
	}
	return st
}

func classify(method, path string) string {
	switch {
	case method == http.MethodGet && path == "/info":
		return "info"
	case method == http.MethodPost && path == "/runs/multipart":
		return "multipart"
	case method == http.MethodPost && path == "/runs/batch":
		return "batch"
	case method == http.MethodPost && path == "/runs":
		return "runs"
	case method == http.MethodPatch && strings.HasPrefix(path, "/runs/") && len(path) > len("/runs/"):
		return "patch"
	case method == http.MethodGet && strings.Contains(path, "/.well-known/oauth-authorization-server"):
		return "oauth-metadata"
	case method == http.MethodPost && strings.HasSuffix(path, "/oauth/token"):
		return "token"
	}
	return "other"
}

func (s *Server) expand(raw []byte) []byte {
	s.mu.Lock()
	base := s.base
	s.mu.Unlock()
	// A JSON-safe base never needs escaping: it is a URL without quotes or backslashes.
	return bytes.ReplaceAll(raw, []byte("{{base}}"), []byte(base))
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/__mockls/") {
		s.admin(w, r)
		return
	}
	e := &reqlog.Entry{
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   r.URL.RawQuery,
		Headers: map[string]string{"host": r.Host},
		Route:   classify(r.Method, r.URL.Path),
	}
	for k, v := range r.Header {
		e.Headers[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	switch {
	case r.ContentLength < 0 || containsFold(r.TransferEncoding, "chunked"):
		e.Transfer, e.ContentLength = "chunked", -1
	case r.Header.Get("Content-Length") != "":
		e.Transfer, e.ContentLength = "content-length", r.ContentLength
	default:
		e.Transfer, e.ContentLength = "none", -1
	}
	tooBig := false
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, s.cfg.MaxBodyBytes+1))
		if int64(len(body)) > s.cfg.MaxBodyBytes {
			tooBig, body = true, body[:s.cfg.MaxBodyBytes]
		}
		e.Body = body
		if err != nil {
			e.ParseError = "reading the body: " + err.Error()
		}
	}
	s.analyse(e)

	s.mu.Lock()
	s.seq++
	e.Seq = s.seq
	s.calls[e.Route]++
	s.bytes += int64(len(e.Body))
	s.routeBytes[e.Route] += int64(len(e.Body))
	s.decodedBytes += int64(len(e.Payload()))
	s.routeDecoded[e.Route] += int64(len(e.Payload()))
	e.Call = s.calls[e.Route]
	e.Time = time.Now().UTC().Format(time.RFC3339Nano)
	fault := -1
	for i := range s.cfg.Faults {
		if s.cfg.Faults[i].matches(e.Route, e.Call) {
			fault = i
			break
		}
	}
	e.Reply.Fault = fault
	var f *Fault
	if fault >= 0 {
		f = &s.cfg.Faults[fault]
		e.Reply.DelayMs = f.DelayMs
		e.Reply.Dropped = f.Drop
	}
	status, ctype, payload := s.answer(e, f, tooBig)
	if !e.Reply.Dropped {
		e.Reply.Status = status
	}
	s.writeLog(e)
	s.mu.Unlock()

	if f != nil && f.DelayMs > 0 {
		select {
		case <-time.After(time.Duration(f.DelayMs) * time.Millisecond):
		case <-r.Context().Done():
			return
		}
	}
	if e.Reply.Dropped {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		return
	}
	if f != nil {
		for k, v := range f.Headers {
			w.Header().Set(k, v)
		}
	}
	if ctype != "" && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", ctype)
	}
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

// answer is the normal answer of a route, replaced by the fault's where it says so. Called with s.mu held.
func (s *Server) answer(e *reqlog.Entry, f *Fault, tooBig bool) (int, string, []byte) {
	status, ctype := http.StatusOK, "application/json"
	var payload []byte
	switch e.Route {
	case "info":
		payload = []byte(DefaultInfo)
		if len(s.cfg.Info) > 0 {
			payload = s.cfg.Info
		}
		payload = bytes.ReplaceAll(payload, []byte("{{base}}"), []byte(s.base))
	case "oauth-metadata":
		if len(s.cfg.OAuthMetadata) == 0 {
			status, ctype, payload = http.StatusNotFound, "text/plain; charset=utf-8", []byte("404 page not found\n")
		} else {
			payload = bytes.ReplaceAll(s.cfg.OAuthMetadata, []byte("{{base}}"), []byte(s.base))
		}
	case "token":
		payload = []byte(DefaultToken)
		if len(s.cfg.Token) > 0 {
			payload = s.cfg.Token
		}
	case "multipart", "batch", "runs", "patch":
		payload = []byte("{}")
	default:
		status, ctype, payload = http.StatusNotFound, "text/plain; charset=utf-8", []byte("404 page not found\n")
	}
	if tooBig {
		status, ctype, payload = http.StatusRequestEntityTooLarge, "text/plain; charset=utf-8", []byte("body too large\n")
	}
	if f != nil {
		if f.Status != 0 {
			status = f.Status
		}
		switch {
		case f.Body != "":
			payload = []byte(f.Body)
			if f.Status == 0 {
				status = http.StatusOK
			}
			ctype = "text/plain; charset=utf-8"
		case len(f.JSON) > 0:
			payload = f.JSON
			if f.Status == 0 {
				status = http.StatusOK
			}
			ctype = "application/json"
		case f.Status != 0 && !isJSONStatus(f.Status):
			ctype = "text/plain; charset=utf-8"
			payload = []byte(http.StatusText(f.Status))
		}
	}
	return status, ctype, payload
}

func isJSONStatus(code int) bool { return code >= 200 && code < 300 }

// analyse decodes the body: Content-Encoding, then multipart parts or a form.
func (s *Server) analyse(e *reqlog.Entry) {
	enc := strings.ToLower(strings.TrimSpace(e.Headers["content-encoding"]))
	e.Encoding = enc
	payload := e.Body
	switch enc {
	case "", "identity":
		e.Encoding = ""
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(e.Body))
		if err == nil {
			var dec []byte
			dec, err = io.ReadAll(zr)
			e.Decoded = dec
			if err == nil {
				err = zr.Close()
			}
		}
		if err != nil {
			e.ParseError = "gzip: " + err.Error()
			return
		}
		payload = e.Decoded
	default:
		e.ParseError = "unsupported content-encoding " + enc
		return
	}
	if len(payload) == 0 {
		return
	}
	mt, params, err := mime.ParseMediaType(e.Headers["content-type"])
	if err != nil {
		return
	}
	switch mt {
	case "multipart/form-data":
		e.Boundary = params["boundary"]
		if e.Boundary == "" {
			e.ParseError = "multipart without a boundary"
			return
		}
		parts, perr := parseMultipart(payload, e.Boundary)
		e.Parts = parts
		if perr != nil {
			e.ParseError = "multipart: " + perr.Error()
		}
	case "application/x-www-form-urlencoded":
		form, ferr := url.ParseQuery(string(payload))
		if ferr != nil {
			e.ParseError = "form: " + ferr.Error()
			return
		}
		e.Form = form
	}
}

func (s *Server) writeLog(e *reqlog.Entry) {
	if s.log == nil {
		return
	}
	line, err := json.Marshal(e)
	if err == nil {
		line = append(line, '\n')
		_, err = s.log.Write(line)
	}
	if err != nil && s.logErr == nil {
		s.logErr = err
	}
}

func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/__mockls/health":
		_, _ = io.WriteString(w, "ok\n")
	case "/__mockls/stats":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Stats())
	default:
		http.NotFound(w, r)
	}
}

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
