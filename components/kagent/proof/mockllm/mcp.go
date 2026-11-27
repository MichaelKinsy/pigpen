package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Minimal stateless MCP server over Streamable HTTP (POST /mcp, JSON responses).

var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

var mcpTools = []any{
	map[string]any{
		"name":        "kagent_probe",
		"title":       "kagent probe",
		"description": "Returns probe:<note>. Used by the kagent harness proof to show MCP wiring.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"note": map[string]any{"type": "string", "description": "free text"}}, "required": []string{"note"}},
	},
	map[string]any{
		"name":        "echo",
		"title":       "echo",
		"description": "Returns its text argument unchanged.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}},
	},
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func rpcErr(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

func handleRPC(q rpcReq) (any, bool) {
	if len(q.ID) == 0 { // notification
		return nil, false
	}
	res := func(v any) (any, bool) { return map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": v}, true }
	switch q.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(q.Params, &p)
		ver := mcpVersions[0]
		for _, v := range mcpVersions {
			if v == p.ProtocolVersion {
				ver = v
			}
		}
		return res(map[string]any{"protocolVersion": ver, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo": map[string]any{"name": "kagent-mock-mcp", "version": "1.0.0"}})
	case "ping":
		return res(map[string]any{})
	case "tools/list":
		return res(map[string]any{"tools": mcpTools})
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.Unmarshal(q.Params, &p) != nil {
			return rpcErr(q.ID, -32602, "invalid params"), true
		}
		var text string
		switch p.Name {
		case "kagent_probe":
			note, _ := p.Arguments["note"].(string)
			text = "probe:" + note
		case "echo":
			text, _ = p.Arguments["text"].(string)
		default:
			return rpcErr(q.ID, -32602, "unknown tool "+p.Name), true
		}
		return res(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": false})
	}
	return rpcErr(q.ID, -32601, "method not found: "+q.Method), true
}

func mcpHandler(w http.ResponseWriter, r *http.Request) {
	entry := map[string]any{"path": r.URL.Path, "method": r.Method, "api": "mcp", "auth": authKinds(r.Header), "headers": headerNames(r.Header)}
	defer func() { logJSON(entry) }()
	if r.Method != http.MethodPost {
		entry["status"] = 405
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", 405)
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	entry["body_bytes"] = len(raw)
	trim := strings.TrimSpace(string(raw))
	var out any
	if strings.HasPrefix(trim, "[") {
		var batch []rpcReq
		if json.Unmarshal(raw, &batch) != nil {
			entry["status"] = 400
			writeJSON(w, 400, rpcErr(nil, -32700, "parse error"))
			return
		}
		var resps []any
		for _, q := range batch {
			if v, ok := handleRPC(q); ok {
				resps = append(resps, v)
			}
		}
		if len(resps) == 0 {
			entry["status"] = 202
			w.WriteHeader(202)
			return
		}
		out = resps
	} else {
		var q rpcReq
		if json.Unmarshal(raw, &q) != nil {
			entry["status"] = 400
			writeJSON(w, 400, rpcErr(nil, -32700, "parse error"))
			return
		}
		entry["rpc"] = q.Method
		v, ok := handleRPC(q)
		if !ok {
			entry["status"] = 202
			w.WriteHeader(202)
			return
		}
		out = v
	}
	entry["status"] = 200
	writeJSON(w, 200, out)
}
