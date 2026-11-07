package mockls

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
)

// Config is the mock endpoint's behaviour (JSON, unknown fields rejected).
type Config struct {
	// Info is the whole JSON document GET /info answers (the SDK reads batch_ingest_config.{size_limit,
	// size_limit_bytes,use_multipart_endpoint,...} and instance_flags.gzip_body_enabled from it). "{{base}}" in
	// any string is replaced by the server's base URL. Empty means DefaultInfo.
	Info json.RawMessage `json:"info,omitempty"`
	// OAuthMetadata is the RFC 8414 document served at /.well-known/oauth-authorization-server (and below it).
	// Empty means 404, which makes the SDK fall back to <mount>/oauth/token. "{{base}}" is replaced.
	OAuthMetadata json.RawMessage `json:"oauthMetadata,omitempty"`
	// Token is the JSON answered to a POST to a path ending in /oauth/token. Empty means DefaultToken.
	Token json.RawMessage `json:"token,omitempty"`
	// Faults are checked in order for every request; the first that matches decides the answer.
	Faults []Fault `json:"faults,omitempty"`
	// MaxBodyBytes caps what is read from one request (default 64 MiB); a larger body answers 413.
	MaxBodyBytes int64 `json:"maxBodyBytes,omitempty"`
}

// Fault changes the answer to some requests of some route. A fault with only DelayMs delays the normal answer.
type Fault struct {
	// Route is one of info, multipart, batch, runs, patch, token, oauth-metadata, other, or "*"/"" for all.
	Route string `json:"route,omitempty"`
	// Calls lists the 1-based per-route call indexes the fault applies to; From/To (inclusive, 0 = open) give a
	// range instead. With none of them the fault applies to every call of the route.
	Calls []int `json:"calls,omitempty"`
	From  int   `json:"from,omitempty"`
	To    int   `json:"to,omitempty"`

	// Status is the answer's HTTP status (0 keeps the normal status, or 200 when Body or JSON is given).
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	JSON    json.RawMessage   `json:"json,omitempty"`
	// DelayMs waits before answering (the request is already logged).
	DelayMs int `json:"delayMs,omitempty"`
	// Drop closes the connection after the body was read, without any answer.
	Drop bool `json:"drop,omitempty"`
}

// Routes are the route names a fault can address.
var Routes = []string{"info", "multipart", "batch", "runs", "patch", "token", "oauth-metadata", "other"}

// DefaultInfo is the /info document without a config: multipart ingestion on, no gzip, the SDK's own defaults for
// the batch limits.
const DefaultInfo = `{"version":"mockls","license_expiration_time":null,"batch_ingest_config":{"use_multipart_endpoint":true,"scale_up_qsize_trigger":1000,"scale_up_nthreads_limit":16,"scale_down_nempty_trigger":4,"size_limit":100,"size_limit_bytes":20971520},"instance_flags":{"gzip_body_enabled":false}}`

// DefaultToken is the token endpoint's answer without a config.
const DefaultToken = `{"access_token":"mock-access-token","refresh_token":"mock-refresh-token","expires_in":3600}`

func (f *Fault) matches(route string, call int) bool {
	if f.Route != "" && f.Route != "*" && f.Route != route {
		return false
	}
	if len(f.Calls) > 0 {
		return slices.Contains(f.Calls, call)
	}
	if f.From > 0 && call < f.From {
		return false
	}
	if f.To > 0 && call > f.To {
		return false
	}
	return true
}

// Validate rejects a config that cannot work (unknown route names, bad JSON).
func (c *Config) Validate() error {
	for _, raw := range []struct {
		name string
		v    json.RawMessage
	}{{"info", c.Info}, {"oauthMetadata", c.OAuthMetadata}, {"token", c.Token}} {
		if len(raw.v) > 0 && !json.Valid(raw.v) {
			return fmt.Errorf("config %s is not valid JSON", raw.name)
		}
	}
	for i, f := range c.Faults {
		if f.Route != "" && f.Route != "*" && !slices.Contains(Routes, f.Route) {
			return fmt.Errorf("fault %d: unknown route %q (routes: %v)", i, f.Route, Routes)
		}
		if f.Status != 0 && (f.Status < 100 || f.Status > 599) {
			return fmt.Errorf("fault %d: status %d", i, f.Status)
		}
		if f.DelayMs < 0 || f.From < 0 || f.To < 0 {
			return fmt.Errorf("fault %d: negative delayMs, from or to", i)
		}
		if f.Body != "" && len(f.JSON) > 0 {
			return fmt.Errorf("fault %d: set body or json, not both", i)
		}
		if len(f.JSON) > 0 && !json.Valid(f.JSON) {
			return fmt.Errorf("fault %d: json is not valid JSON", i)
		}
		for _, c := range f.Calls {
			if c < 1 {
				return fmt.Errorf("fault %d: calls are 1-based, got %d", i, c)
			}
		}
	}
	return nil
}

// ParseConfig decodes and validates a config document.
func ParseConfig(data []byte) (*Config, error) {
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// LoadConfig reads a config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := ParseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}
