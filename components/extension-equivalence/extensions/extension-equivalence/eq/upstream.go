package eq

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ServerSpec declares one fake HTTP upstream: an API the extension calls (a search provider, a
// judge model, a remote agent). Every lane gets its own instance, with the same routes, on a
// loopback port; {{server:NAME}} in the scenario's env, files and args is that server's base URL.
type ServerSpec struct {
	Routes []Route `json:"routes"`
	// RecordHeaders are the request headers copied into the trace, lower-case, in addition to
	// content-type. Others (user agent, accept-encoding, connection) differ between the Node and
	// Go runtimes and would make every trace differ.
	RecordHeaders []string `json:"recordHeaders,omitempty"`
}

// Route is one canned answer. The first route that matches and still has uses left answers.
type Route struct {
	Method  string            `json:"method,omitempty"` // any method when empty
	Path    string            `json:"path"`             // exact, or a prefix when it ends in *
	Query   map[string]string `json:"query,omitempty"`  // these query parameters must have exactly these values
	Status  int               `json:"status,omitempty"` // default 200
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	JSON    any               `json:"json,omitempty"` // an alternative to Body, sent as application/json
	DelayMs int               `json:"delayMs,omitempty"`
	Times   int               `json:"times,omitempty"` // uses before the route is spent; 0 is unlimited
	Drop    bool              `json:"drop,omitempty"`  // close the connection without answering
}

var serverRefRE = regexp.MustCompile(`\{\{server:([^}]*)\}\}`)

type upstream struct {
	name   string
	spec   ServerSpec
	srv    *http.Server
	ln     net.Listener
	record func(ch string, data any)

	mu   sync.Mutex
	used []int
}

type upstreams struct {
	byName map[string]*upstream
}

func startUpstreams(specs map[string]ServerSpec, record func(ch string, data any)) (*upstreams, error) {
	u := &upstreams{byName: map[string]*upstream{}}
	for name, spec := range specs {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			u.close()
			return nil, err
		}
		s := &upstream{name: name, spec: spec, ln: ln, record: record, used: make([]int, len(spec.Routes))}
		s.srv = &http.Server{Handler: http.HandlerFunc(s.handle)}
		go func() { _ = s.srv.Serve(ln) }()
		u.byName[name] = s
	}
	return u, nil
}

func (u *upstreams) close() {
	for _, s := range u.byName {
		_ = s.srv.Close()
	}
}

// URL is the base URL of a server, without a trailing slash.
func (u *upstreams) URL(name string) string { return "http://" + u.byName[name].ln.Addr().String() }

// expand replaces {{server:NAME}} with the server's base URL.
func (u *upstreams) expand(s string) string {
	return serverRefRE.ReplaceAllStringFunc(s, func(m string) string {
		name := serverRefRE.FindStringSubmatch(m)[1]
		if _, ok := u.byName[name]; !ok {
			return m
		}
		return u.URL(name)
	})
}

// normalize teaches the normalizer that a server's host:port is <server:NAME>, so a URL an
// extension echoes back compares equal across lanes (each lane's ports differ).
func (u *upstreams) normalize(n *Normalizer) {
	for name, s := range u.byName {
		n.AddReplacement("http://"+s.ln.Addr().String(), "<server:"+name+">")
		n.AddReplacement(s.ln.Addr().String(), "<server:"+name+">")
	}
}

func (s *upstream) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	route := s.match(r)
	s.record(ChHTTP, s.describe(r, body, route != nil))
	if route == nil {
		http.NotFound(w, r)
		return
	}
	if route.DelayMs > 0 {
		time.Sleep(time.Duration(route.DelayMs) * time.Millisecond)
	}
	if route.Drop {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		return
	}
	payload := []byte(route.Body)
	if route.JSON != nil {
		payload, _ = json.Marshal(route.JSON)
		w.Header().Set("Content-Type", "application/json")
	}
	for k, v := range route.Headers {
		w.Header().Set(k, v)
	}
	status := route.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func (s *upstream) match(r *http.Request) *Route {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := r.URL.Query()
	for i := range s.spec.Routes {
		rt := &s.spec.Routes[i]
		if rt.Method != "" && !strings.EqualFold(rt.Method, r.Method) {
			continue
		}
		if strings.HasSuffix(rt.Path, "*") {
			if !strings.HasPrefix(r.URL.Path, strings.TrimSuffix(rt.Path, "*")) {
				continue
			}
		} else if r.URL.Path != rt.Path {
			continue
		}
		ok := true
		for k, v := range rt.Query {
			if q.Get(k) != v {
				ok = false
			}
		}
		if !ok || (rt.Times > 0 && s.used[i] >= rt.Times) {
			continue
		}
		s.used[i]++
		return rt
	}
	return nil
}

func (s *upstream) describe(r *http.Request, body []byte, matched bool) map[string]any {
	headers := map[string]any{}
	for _, name := range append([]string{"content-type"}, s.spec.RecordHeaders...) {
		if v := r.Header.Get(name); v != "" {
			headers[strings.ToLower(name)] = v
		}
	}
	query := map[string]any{}
	keys := make([]string, 0)
	for k := range r.URL.Query() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vals := r.URL.Query()[k]
		list := make([]any, len(vals))
		for i, v := range vals {
			list[i] = v
		}
		query[k] = list
	}
	out := map[string]any{"server": s.name, "method": r.Method, "path": r.URL.Path, "query": query, "headers": headers, "matched": matched}
	if len(body) > 0 {
		var decoded any
		if json.Unmarshal(body, &decoded) == nil {
			out["body"] = decoded
		} else if form, err := url.ParseQuery(string(body)); err == nil && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			f := map[string]any{}
			for k, v := range form {
				f[k] = v[0]
			}
			out["body"] = f
		} else {
			out["body"] = string(body)
		}
	}
	return out
}

// writeAgentFiles writes the scenario's agentFiles under the lane's agent directory.
func writeAgentFiles(agent string, files map[string]string, ups *upstreams) error {
	for rel, content := range files {
		p := filepath.Join(agent, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(ups.expand(content)), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// validate checks the scenario's servers, env and {{server:...}} references.
func (s *Scenario) validateServers() error {
	for name, spec := range s.Servers {
		if !validName(name) {
			return fmt.Errorf("server name %q must be lowercase words joined by hyphens", name)
		}
		if len(spec.Routes) == 0 {
			return fmt.Errorf("server %q needs at least one route", name)
		}
		for i, r := range spec.Routes {
			switch {
			case !strings.HasPrefix(r.Path, "/"):
				return fmt.Errorf("server %q route %d: path %q must start with /", name, i, r.Path)
			case r.Body != "" && r.JSON != nil:
				return fmt.Errorf("server %q route %d: set body or json, not both", name, i)
			case r.Status != 0 && (r.Status < 100 || r.Status > 599):
				return fmt.Errorf("server %q route %d: status %d", name, i, r.Status)
			case r.Times < 0 || r.DelayMs < 0:
				return fmt.Errorf("server %q route %d: times and delayMs cannot be negative", name, i)
			}
		}
	}
	for k := range s.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") || harnessEnv(k) {
			return fmt.Errorf("env %q is set by the harness and cannot be overridden", k)
		}
	}
	check := func(where, text string) error {
		for _, m := range serverRefRE.FindAllStringSubmatch(text, -1) {
			if _, ok := s.Servers[m[1]]; !ok {
				return fmt.Errorf("%s refers to server %q, which the scenario does not declare", where, m[1])
			}
		}
		return nil
	}
	for k, v := range s.Env {
		if err := check("env "+k, v); err != nil {
			return err
		}
	}
	for k, v := range s.Files {
		if err := check("file "+k, v); err != nil {
			return err
		}
	}
	for k, v := range s.AgentFiles {
		clean := filepath.ToSlash(filepath.Clean(k))
		if filepath.IsAbs(k) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("agent file %q must stay inside the agent directory", k)
		}
		if len(s.LLM) > 0 && clean == "models.json" {
			return errors.New("agent file models.json is written by the harness when the scenario scripts the model")
		}
		if err := check("agent file "+k, v); err != nil {
			return err
		}
	}
	for i, v := range s.Args {
		if err := check(fmt.Sprintf("arg %d", i), v); err != nil {
			return err
		}
	}
	return nil
}
