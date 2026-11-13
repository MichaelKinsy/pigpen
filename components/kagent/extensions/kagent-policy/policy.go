package kagentpolicy

// The policy file is the one input the harness adapter hands the extension. It is untrusted configuration
// (it is compiled from a Kubernetes spec), so it is validated strictly: an unknown key, a duplicate key, a
// null, a wrong type or an out-of-range value is an error that names the field, never a default.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const (
	// maxPolicyBytes bounds the policy file read.
	maxPolicyBytes = 1 << 20
	maxServers     = 64
	maxHeaders     = 32
	maxTools       = 256
	maxNameLength  = 128
	maxURLLength   = 2048
	// maxTimeoutSeconds is the largest per-request timeout a server may ask for.
	maxTimeoutSeconds = 3600
	// maxServerNameLength bounds a server name; see serverNamePattern.
	maxServerNameLength = 40
)

var (
	// serverNamePattern bounds a server name at 40 characters. PiG cuts a tool name longer than 64 characters to 55 and
	// a hash suffix, and with a server name of 49 or more characters the cut removes the end of the `mcp__<server>__`
	// prefix the gate and the default deny match on.
	//
	// A name is alphanumeric words joined by single `-` or `_`: no `__` and no leading or trailing separator. PiG writes
	// the tool name as `mcp__<server>__<tool>` with `-` as `_`, so such a name never contains `__` and the prefix of one
	// policy server is never the start of another policy server's prefix (`a` against `a__b` or `a_`).
	serverNamePattern = regexp.MustCompile(`^[A-Za-z0-9]+([_-][A-Za-z0-9]+)*$`)
	headerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	// headerValuePattern is the only header value the extension accepts: a reference to one environment variable.
	// PiG expands `$VAR` anywhere in a header value and runs a value that starts with `!` as a shell command, so a
	// literal from the compiled configuration is never handed to PiG. A `${NAME}` whole-value reference is expanded
	// from pig's environment and its content is delivered literally.
	headerValuePattern = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
)

// Server is one MCP server the policy binds.
type Server struct {
	Name string
	URL  string
	// Headers maps a header name to the environment variable that holds its value, in file order sorted by name.
	Headers []Header
	// TimeoutSeconds is the per-request timeout; zero keeps PiG's default.
	TimeoutSeconds float64
	// Tools is the allowlist of tool names; empty exposes every tool of the server.
	Tools []string
	// RequireApproval gates every call of a tool of this server.
	RequireApproval bool
}

// Header is one request header whose value is `${Var}`.
type Header struct {
	Name string
	Var  string
}

// Policy is a validated policy file.
type Policy struct {
	Servers []Server
	AskUser bool
}

type rawPolicy struct {
	MCPServers []rawServer `json:"mcp_servers"`
	AskUser    *bool       `json:"ask_user"`
}

type rawServer struct {
	Name            *string           `json:"name"`
	URL             *string           `json:"url"`
	Headers         map[string]string `json:"headers"`
	TimeoutSeconds  *float64          `json:"timeout_seconds"`
	Tools           []string          `json:"tools"`
	RequireApproval *bool             `json:"require_approval"`
}

// ParsePolicy validates the policy JSON. lookupEnv reports whether an environment variable is set, so a header
// that references a missing variable is an error rather than a request that carries the literal `${NAME}`.
func ParsePolicy(data []byte, lookupEnv func(string) (string, bool)) (Policy, error) {
	if err := checkJSONShape(data); err != nil {
		return Policy{}, err
	}
	var raw rawPolicy
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Policy{}, fmt.Errorf("policy: %w", err)
	}
	if len(raw.MCPServers) > maxServers {
		return Policy{}, fmt.Errorf("policy: mcp_servers has %d entries, the limit is %d", len(raw.MCPServers), maxServers)
	}
	var p Policy
	if raw.AskUser != nil {
		p.AskUser = *raw.AskUser
	}
	seen := map[string]string{}
	for i, rs := range raw.MCPServers {
		where := fmt.Sprintf("mcp_servers[%d]", i)
		s, err := validateServer(where, rs, lookupEnv)
		if err != nil {
			return Policy{}, err
		}
		// PiG treats `-` and `_` in a server name as the same character, so two names that differ only there collide.
		key := strings.ReplaceAll(s.Name, "-", "_")
		if first, dup := seen[key]; dup {
			return Policy{}, fmt.Errorf("policy: %s.name %q collides with %q (PiG treats - and _ as the same character)", where, s.Name, first)
		}
		seen[key] = s.Name
		p.Servers = append(p.Servers, s)
	}
	return p, nil
}

func validateServer(where string, rs rawServer, lookupEnv func(string) (string, bool)) (Server, error) {
	var s Server
	if rs.Name == nil || len(*rs.Name) > maxServerNameLength || !serverNamePattern.MatchString(*rs.Name) {
		return s, fmt.Errorf("policy: %s.name is required, 1 to %d characters of words made of letters and digits joined by single - or _", where, maxServerNameLength)
	}
	s.Name = *rs.Name
	where = fmt.Sprintf("%s (%s)", where, s.Name)
	if rs.URL == nil {
		return s, fmt.Errorf("policy: %s.url is required", where)
	}
	if err := validateURL(*rs.URL); err != nil {
		return s, fmt.Errorf("policy: %s.url: %w", where, err)
	}
	s.URL = *rs.URL
	if len(rs.Headers) > maxHeaders {
		return s, fmt.Errorf("policy: %s.headers has %d entries, the limit is %d", where, len(rs.Headers), maxHeaders)
	}
	names := make([]string, 0, len(rs.Headers))
	for name := range rs.Headers {
		names = append(names, name)
	}
	slices.Sort(names)
	lower := map[string]string{}
	for _, name := range names {
		if !headerNamePattern.MatchString(name) {
			return s, fmt.Errorf("policy: %s.headers has an invalid header name %q", where, name)
		}
		if first, dup := lower[strings.ToLower(name)]; dup {
			return s, fmt.Errorf("policy: %s.headers names %q and %q are the same header", where, first, name)
		}
		lower[strings.ToLower(name)] = name
		m := headerValuePattern.FindStringSubmatch(rs.Headers[name])
		if m == nil {
			// The value is not echoed: it may be a secret.
			return s, fmt.Errorf("policy: %s.headers[%q] must be a single ${VAR} environment reference", where, name)
		}
		if _, set := lookupEnv(m[1]); !set {
			return s, fmt.Errorf("policy: %s.headers[%q] references the environment variable %s, which is not set", where, name, m[1])
		}
		s.Headers = append(s.Headers, Header{Name: name, Var: m[1]})
	}
	if rs.TimeoutSeconds != nil {
		t := *rs.TimeoutSeconds
		if !(t > 0 && t <= maxTimeoutSeconds) {
			return s, fmt.Errorf("policy: %s.timeout_seconds must be greater than 0 and at most %d", where, maxTimeoutSeconds)
		}
		s.TimeoutSeconds = t
	}
	if len(rs.Tools) > maxTools {
		return s, fmt.Errorf("policy: %s.tools has %d entries, the limit is %d", where, len(rs.Tools), maxTools)
	}
	seenTool := map[string]bool{}
	for i, tool := range rs.Tools {
		if err := validateToolName(tool); err != nil {
			return s, fmt.Errorf("policy: %s.tools[%d]: %w", where, i, err)
		}
		if seenTool[tool] {
			return s, fmt.Errorf("policy: %s.tools lists %q twice", where, tool)
		}
		seenTool[tool] = true
	}
	s.Tools = slices.Clone(rs.Tools)
	if rs.RequireApproval != nil {
		s.RequireApproval = *rs.RequireApproval
	}
	return s, nil
}

// validateURL accepts an absolute http or https URL without credentials. A `$` is refused because PiG expands
// environment variables in an MCP url, and a compiled URL must not read pig's environment.
func validateURL(raw string) error {
	if len(raw) == 0 || len(raw) > maxURLLength {
		return fmt.Errorf("must be 1 to %d bytes", maxURLLength)
	}
	if strings.ContainsRune(raw, '$') {
		return errors.New("must not contain $ (PiG expands environment variables in an MCP url)")
	}
	for _, r := range raw {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("must not contain whitespace or control characters")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("must use http or https")
	}
	if u.Hostname() == "" {
		return errors.New("must name a host")
	}
	if u.User != nil {
		return errors.New("must not carry credentials; use a header")
	}
	return nil
}

// validateToolName refuses a name PiG would read as a pattern: `*` in a `toolExposure` key matches any characters,
// so an allowlist entry `*` would expose every tool.
func validateToolName(name string) error {
	if name == "" || len(name) > maxNameLength {
		return fmt.Errorf("must be 1 to %d bytes", maxNameLength)
	}
	if strings.ContainsRune(name, '*') {
		return errors.New("must not contain * (PiG reads it as a wildcard)")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.New("must not contain control characters")
		}
	}
	return nil
}

// checkJSONShape rejects what encoding/json would accept silently: a second document after the first, a
// duplicate object key (the last would win) and a null (a null decodes to the zero value, so
// "require_approval": null would turn the gate off).
// schema lists the keys an object of the policy may have. A nil schema means the object's keys are data (header names).
type schema struct {
	fields map[string]*schema
	// elem is the schema of the elements of an array.
	elem *schema
}

var policySchema = &schema{fields: map[string]*schema{
	"ask_user": {},
	"mcp_servers": {elem: &schema{fields: map[string]*schema{
		"name": {}, "url": {}, "headers": nil, "timeout_seconds": {}, "tools": {}, "require_approval": {},
	}}},
}}

// checkKey rejects a key that is not exactly one of the schema's keys. encoding/json matches field names without
// regard to case, so a later "REQUIRE_APPROVAL" would silently override "require_approval" if it got that far.
func (sc *schema) checkKey(path, key string) error {
	if sc == nil || sc.fields == nil {
		return nil
	}
	if _, ok := sc.fields[key]; ok {
		return nil
	}
	for k := range sc.fields {
		if strings.EqualFold(k, key) {
			return fmt.Errorf("%s has the key %q, which differs from %q only in case; keys are case-sensitive", path, key, k)
		}
	}
	return nil
}

func checkJSONShape(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := walkValue(dec, "policy", policySchema); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("policy: unexpected data after the JSON document")
	}
	return nil
}

func walkValue(dec *json.Decoder, path string, sc *schema) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	switch v := tok.(type) {
	case nil:
		return fmt.Errorf("%s must not be null", path)
	case json.Delim:
		switch v {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return fmt.Errorf("policy: %w", err)
				}
				key := keyTok.(string)
				if seen[key] {
					return fmt.Errorf("%s has the key %q twice", path, key)
				}
				seen[key] = true
				if err := sc.checkKey(path, key); err != nil {
					return err
				}
				var child *schema
				if sc != nil {
					child = sc.fields[key]
				}
				if err := walkValue(dec, path+"."+key, child); err != nil {
					return err
				}
			}
		case '[':
			for i := 0; dec.More(); i++ {
				var elem *schema
				if sc != nil {
					elem = sc.elem
				}
				if err := walkValue(dec, fmt.Sprintf("%s[%d]", path, i), elem); err != nil {
					return err
				}
			}
		}
		if _, err := dec.Token(); err != nil { // the closing delimiter
			return fmt.Errorf("policy: %w", err)
		}
	}
	return nil
}
