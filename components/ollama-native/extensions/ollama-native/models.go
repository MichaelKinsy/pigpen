package ollamanative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	defaultHost          = "http://127.0.0.1:11434"
	defaultPort          = "11434"
	defaultContextWindow = 32768
	defaultMaxTokens     = 8192
)

// client talks to one Ollama server. host is read on every use, so a changed
// OLLAMA_HOST applies without restarting pig.
type client struct {
	host  func() string
	http  *http.Client
	newID func() string
}

// transport bounds connecting, not the response: a large model can take a long
// time to load before the first token, and a long answer is a long stream.
func transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	return t
}

func (c *client) baseURL() string { return normalizeHost(c.host()) }

// normalizeHost turns an OLLAMA_HOST value into a base URL the way Ollama's own
// client reads it: a bare host or host:port means http on port 11434, ":port"
// means this machine, and an explicit scheme without a port means that scheme's
// default port.
func normalizeHost(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultHost
	}
	scheme, rest := "http", value
	explicit := false
	if s, r, ok := strings.Cut(value, "://"); ok {
		scheme, rest, explicit = strings.ToLower(s), r, true
	}
	u, err := url.Parse(scheme + "://" + rest)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return defaultHost
	}
	host, port := u.Hostname(), u.Port()
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		switch {
		case !explicit:
			port = defaultPort
		case scheme == "https":
			port = "443"
		default:
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port) + strings.TrimRight(u.Path, "/")
}

// unreachable is the error for an Ollama that is not running (or not there).
func unreachable(base string, err error) error {
	return fmt.Errorf("Ollama is not reachable at %s (%v). Start it with \"ollama serve\", or set OLLAMA_HOST to where it listens.", base, rootCause(err))
}

func rootCause(err error) error {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return fmt.Errorf("%s: %w", op.Op, op.Err)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// isDialFailure reports a request that never reached a server.
func isDialFailure(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

func (c *client) do(ctx context.Context, method, path string, body any, timeout time.Duration) (*http.Response, context.CancelFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL()+path, reader)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return resp, cancel, nil
}

// apiError reads Ollama's {"error": "..."} body, or falls back to the raw text.
func apiError(resp *http.Response) string {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	if text := strings.TrimSpace(string(raw)); text != "" {
		return text
	}
	return http.StatusText(resp.StatusCode)
}

// discover lists the models Ollama has installed. It reads /api/tags, then asks
// /api/show about each model for its context length and capabilities. A model
// /api/show cannot describe is still listed, with defaults.
func (c *client) discover(ctx context.Context) ([]map[string]any, error) {
	resp, cancel, err := c.do(ctx, http.MethodGet, "/api/tags", nil, 10*time.Second)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, unreachable(c.baseURL(), err)
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama at %s answered %d to /api/tags: %s", c.baseURL(), resp.StatusCode, apiError(resp))
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("Ollama at %s sent a model list that is not JSON: %w", c.baseURL(), err)
	}
	models := make([]map[string]any, 0, len(tags.Models))
	for _, m := range tags.Models {
		if m.Name == "" {
			continue
		}
		info := c.show(ctx, m.Name)
		if info.capabilities != nil && !slices.Contains(info.capabilities, "completion") {
			continue // embedding-only models cannot chat
		}
		input := []string{"text"}
		if slices.Contains(info.capabilities, "vision") {
			input = append(input, "image")
		}
		window := info.contextLength
		if window <= 0 {
			window = defaultContextWindow
		}
		models = append(models, map[string]any{
			"id":            m.Name,
			"name":          m.Name,
			"api":           ProviderID,
			"reasoning":     slices.Contains(info.capabilities, "thinking"),
			"input":         input,
			"contextWindow": window,
			"maxTokens":     min(window, defaultMaxTokens),
			"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
		})
	}
	return models, nil
}

type modelInfo struct {
	contextLength int
	capabilities  []string
}

// show reads a model's details. Failure is not an error: the zero value means
// "unknown", and discover then uses defaults.
func (c *client) show(ctx context.Context, name string) modelInfo {
	resp, cancel, err := c.do(ctx, http.MethodPost, "/api/show", map[string]any{"model": name}, 5*time.Second)
	if err != nil {
		return modelInfo{}
	}
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return modelInfo{}
	}
	var out struct {
		ModelInfo    map[string]any `json:"model_info"`
		Capabilities []string       `json:"capabilities"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return modelInfo{}
	}
	info := modelInfo{capabilities: out.Capabilities}
	if arch, _ := out.ModelInfo["general.architecture"].(string); arch != "" {
		if n, ok := out.ModelInfo[arch+".context_length"].(float64); ok {
			info.contextLength = int(n)
		}
	}
	return info
}
