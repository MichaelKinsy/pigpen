// Package gamemcptest is a minimal MCP client for tests of the game servers: it speaks
// the JSON-RPC over HTTP that the host speaks, with the bearer token, and nothing else.
package gamemcptest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// Client calls one game server.
type Client struct {
	URL   string
	Token string

	next atomic.Int64
	http http.Client
}

// New returns a client for the server at url.
func New(url, token string) *Client {
	return &Client{URL: url, Token: token, http: http.Client{Timeout: 10 * time.Second}}
}

// Request sends one JSON-RPC request and returns the HTTP status and the decoded result. A
// JSON-RPC error is returned as an error.
func (c *Client) Request(method string, params any) (int, map[string]any, error) {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.next.Add(1), "method": method, "params": params})
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, fmt.Errorf("%s: HTTP %d: %s", method, resp.StatusCode, bytes.TrimSpace(data))
	}
	var reply struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return resp.StatusCode, nil, fmt.Errorf("%s: %w: %s", method, err, data)
	}
	if reply.Error != nil {
		return resp.StatusCode, nil, fmt.Errorf("%s: %s", method, reply.Error.Message)
	}
	return resp.StatusCode, reply.Result, nil
}

// Tools lists the tool names.
func (c *Client) Tools() ([]string, error) {
	_, result, err := c.Request("tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, tool := range result["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	return names, nil
}

// Call calls a tool and returns its text content decoded as JSON. A tool error is an error.
func (c *Client) Call(tool string, args map[string]any) (any, error) {
	_, result, err := c.Request("tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return nil, err
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return nil, fmt.Errorf("%s: no content", tool)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if result["isError"] == true {
		return nil, fmt.Errorf("%s: %s", tool, text)
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", tool, err, text)
	}
	return value, nil
}

// CallObject is Call for a tool that returns an object.
func (c *Client) CallObject(tool string, args map[string]any) (map[string]any, error) {
	value, err := c.Call(tool, args)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: result is %T, not an object", tool, value)
	}
	return object, nil
}
