package pi_subagents

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeHC is a herdr-core stand-in: one JSON-RPC line in, one line out.
type fakeHC struct {
	socket string
	mu     sync.Mutex
	log    []fakeCall
}

type fakeCall struct {
	method string
	params map[string]any
	raw    string
}

// startFakeHC serves handle on a unix socket. A handler returning (nil, nil) closes the connection without answering.
func startFakeHC(t *testing.T, handle func(method string, params map[string]any) (any, *hcError)) *fakeHC {
	t.Helper()
	dir, err := os.MkdirTemp("", "hc") // macOS limits socket paths to 104 bytes; t.TempDir() is longer
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fakeHC{socket: filepath.Join(dir, "s")}
	ln, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c, handle)
		}
	}()
	return f
}

func (f *fakeHC) serve(c net.Conn, handle func(string, map[string]any) (any, *hcError)) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(line, &req) != nil {
			return
		}
		f.mu.Lock()
		f.log = append(f.log, fakeCall{req.Method, req.Params, string(line)})
		f.mu.Unlock()
		res, herr := handle(req.Method, req.Params)
		if res == nil && herr == nil {
			return
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if herr != nil {
			resp["error"] = map[string]any{"code": -32000, "message": herr.Message, "data": map[string]any{"kind": herr.Kind}}
		} else {
			resp["result"] = res
		}
		b, _ := json.Marshal(resp)
		c.Write(append(b, '\n'))
	}
}

// calls returns the params of every request for method, in order.
func (f *fakeHC) calls(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.log {
		if c.method == method {
			out = append(out, c.params)
		}
	}
	return out
}

// methods returns the method of every request, in call order.
func (f *fakeHC) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.log {
		out = append(out, c.method)
	}
	return out
}

// lines returns every raw request line, in call order.
func (f *fakeHC) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.log {
		out = append(out, c.raw)
	}
	return out
}
