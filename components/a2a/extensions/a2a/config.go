package a2aext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Config is the a2a extension's configuration. The listener is off unless Listen is set.
//
// Secrets are never stored here: tokens and remote credentials are named by the
// environment variable that holds them.
type Config struct {
	// Listen is the address of the A2A server ("127.0.0.1:8787"). Empty means off.
	Listen string `json:"listen"`
	// ExternalURL is the base URL peers reach the listener at (behind a proxy). Default: derived from Listen.
	ExternalURL string `json:"externalUrl"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// StateDir holds per-principal PiG session files. Default: <config home>/a2a.
	StateDir string `json:"stateDir"`
	// InsecureNoAuth serves without tokens; only allowed on a loopback address.
	InsecureNoAuth     bool                   `json:"insecureNoAuth"`
	Tokens             []TokenConfig          `json:"tokens"`
	TLS                *TLSConfig             `json:"tls"`
	Worker             WorkerConfig           `json:"worker"`
	MaxConcurrentTasks int                    `json:"maxConcurrentTasks"`
	TaskTimeoutSeconds int                    `json:"taskTimeoutSeconds"`
	Remotes            map[string]RemoteAgent `json:"remotes"`
}

// TokenConfig names a bearer token by the environment variable that holds it.
type TokenConfig struct {
	// Name identifies the caller in logs and in task ownership.
	Name string `json:"name"`
	// TokenEnv is the environment variable holding the token (at least 16 characters).
	TokenEnv string `json:"tokenEnv"`
	// Tenant, when set, is the tenant boundary this token belongs to. Tokens of one tenant share tasks and contexts.
	Tenant string `json:"tenant"`
}

// TLSConfig holds certificate file paths for the listener.
type TLSConfig struct {
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
}

// WorkerConfig says how a task turn runs PiG.
type WorkerConfig struct {
	// Command is the pig executable. Default: $PIG_A2A_PIG, then "pig" on PATH.
	Command string `json:"command"`
	// Args are extra arguments appended to the fixed ones.
	Args []string `json:"args"`
	// Cwd is the worker's working directory. Default: the session's directory.
	Cwd string `json:"cwd"`
	// Tools is the tool allowlist. Default: none (pig --no-tools). PiG's file tools take absolute paths, so any
	// tool named here reaches every file the account can, not only Cwd.
	Tools    []string `json:"tools"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	// PassEnv names extra environment variables to hand to the worker (for example a provider API key).
	PassEnv      []string `json:"passEnv"`
	GraceSeconds int      `json:"graceSeconds"`
}

// RemoteAgent is an A2A agent this extension can call.
type RemoteAgent struct {
	URL string `json:"url"`
	// BearerTokenEnv names the environment variable holding a bearer token.
	BearerTokenEnv string `json:"bearerTokenEnv"`
	// HeaderEnv maps a header name to the environment variable holding its value.
	HeaderEnv map[string]string `json:"headerEnv"`
	// SkipCard uses URL as the JSON-RPC endpoint without fetching the agent card (kagent's card URL is in-cluster).
	SkipCard       bool `json:"skipCard"`
	TimeoutSeconds int  `json:"timeoutSeconds"`
}

// LoadOptions are the inputs of LoadConfig.
type LoadOptions struct {
	ConfigHome string
	Getenv     func(string) string
	FlagListen string
}

const (
	defaultMaxConcurrent = 4
	defaultTaskTimeout   = 900
	defaultRemoteTimeout = 300
	defaultGraceSeconds  = 5
	minTokenLength       = 16
)

var (
	identRE  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	envRE    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	headerRE = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

// Enabled reports whether the listener is configured.
func (c Config) Enabled() bool { return c.Listen != "" }

// LoadConfig reads the configuration file, then the environment and the flag
// (flag over environment over file). It validates the result.
//
// Environment: PIG_A2A_CONFIG (file path), PIG_A2A_LISTEN, PIG_A2A_WORKER=1
// (set on worker children, which never listen).
func LoadConfig(o LoadOptions) (Config, error) {
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	path, explicit := getenv("PIG_A2A_CONFIG"), true
	if path == "" {
		explicit = false
		if o.ConfigHome != "" {
			path = filepath.Join(o.ConfigHome, "a2a.json")
		}
	}
	var cfg Config
	if path != "" {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&cfg); err != nil {
				return Config{}, fmt.Errorf("a2a: %s: %w (secrets belong in environment variables named by tokenEnv, bearerTokenEnv or headerEnv)", path, err)
			}
		case errors.Is(err, os.ErrNotExist) && !explicit:
		default:
			return Config{}, fmt.Errorf("a2a: read %s: %w", path, err)
		}
	}
	if v := getenv("PIG_A2A_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if o.FlagListen != "" {
		cfg.Listen = o.FlagListen
	}
	if getenv("PIG_A2A_WORKER") == "1" {
		// A worker child is a PiG the server started for one task; it must not start a second server.
		cfg.Listen = ""
	}
	if cfg.StateDir == "" && o.ConfigHome != "" {
		cfg.StateDir = filepath.Join(o.ConfigHome, "a2a")
	}
	cfg.applyDefaults()
	if err := cfg.validate(getenv); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Name == "" {
		c.Name = "PiG"
	}
	if c.Description == "" {
		c.Description = "A PiG coding agent serving tasks over A2A."
	}
	if c.MaxConcurrentTasks <= 0 {
		c.MaxConcurrentTasks = defaultMaxConcurrent
	}
	if c.TaskTimeoutSeconds <= 0 {
		c.TaskTimeoutSeconds = defaultTaskTimeout
	}
	if c.Worker.GraceSeconds <= 0 {
		c.Worker.GraceSeconds = defaultGraceSeconds
	}
	for name, r := range c.Remotes {
		if r.TimeoutSeconds <= 0 {
			r.TimeoutSeconds = defaultRemoteTimeout
		}
		c.Remotes[name] = r
	}
}

// Validate checks the configuration without reading the environment for secrets.
func (c Config) Validate() error { return c.validate(func(string) string { return "" }) }

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c Config) validate(getenv func(string) string) error {
	if c.Listen != "" {
		host, port, err := net.SplitHostPort(c.Listen)
		if err != nil {
			return fmt.Errorf("a2a: listen %q is not host:port: %w", c.Listen, err)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
			return fmt.Errorf("a2a: listen port %q is not a port number", port)
		}
		switch {
		case c.InsecureNoAuth && len(c.Tokens) > 0:
			return errors.New("a2a: insecureNoAuth and tokens are both set; remove one")
		case c.InsecureNoAuth && !isLoopbackHost(host):
			return fmt.Errorf("a2a: insecureNoAuth is only allowed on a loopback address, not %q", host)
		case !c.InsecureNoAuth && len(c.Tokens) == 0:
			return errors.New("a2a: a listener needs at least one entry in tokens (or insecureNoAuth on a loopback address)")
		}
		if err := c.validateTokens(getenv); err != nil {
			return err
		}
		if c.TLS != nil && (c.TLS.CertFile == "" || c.TLS.KeyFile == "") {
			return errors.New("a2a: tls needs both certFile and keyFile")
		}
		if c.ExternalURL != "" {
			if err := checkHTTPURL(c.ExternalURL); err != nil {
				return fmt.Errorf("a2a: externalUrl: %w", err)
			}
		}
	}
	for name, r := range c.Remotes {
		if !identRE.MatchString(name) {
			return fmt.Errorf("a2a: remote name %q must match %s", name, identRE)
		}
		if err := checkHTTPURL(r.URL); err != nil {
			return fmt.Errorf("a2a: remote %q: %w", name, err)
		}
		if r.BearerTokenEnv != "" && !envRE.MatchString(r.BearerTokenEnv) {
			return fmt.Errorf("a2a: remote %q: bearerTokenEnv %q is not an environment variable name", name, r.BearerTokenEnv)
		}
		for header, env := range r.HeaderEnv {
			if !headerRE.MatchString(header) || !envRE.MatchString(env) {
				return fmt.Errorf("a2a: remote %q: headerEnv entry %q: %q is not a valid header name or variable name", name, header, env)
			}
		}
	}
	return nil
}

func (c Config) validateTokens(getenv func(string) string) error {
	names, envs, values := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, t := range c.Tokens {
		if !identRE.MatchString(t.Name) {
			return fmt.Errorf("a2a: tokens[%d].name %q must match %s", i, t.Name, identRE)
		}
		if t.Tenant != "" && !identRE.MatchString(t.Tenant) {
			return fmt.Errorf("a2a: tokens[%d].tenant %q must match %s", i, t.Tenant, identRE)
		}
		if !envRE.MatchString(t.TokenEnv) {
			return fmt.Errorf("a2a: tokens[%d].tokenEnv %q is not an environment variable name", i, t.TokenEnv)
		}
		if names[t.Name] || envs[t.TokenEnv] {
			return fmt.Errorf("a2a: tokens[%d]: name %q or variable %s is used twice", i, t.Name, t.TokenEnv)
		}
		names[t.Name], envs[t.TokenEnv] = true, true
		v := getenv(t.TokenEnv)
		if v == "" {
			return fmt.Errorf("a2a: token %q: environment variable %s is not set", t.Name, t.TokenEnv)
		}
		if len(v) < minTokenLength {
			return fmt.Errorf("a2a: token %q (%s) is shorter than %d characters", t.Name, t.TokenEnv, minTokenLength)
		}
		if values[v] {
			return fmt.Errorf("a2a: token %q has the same value as another token", t.Name)
		}
		values[v] = true
	}
	return nil
}

func checkHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%q must be an http or https URL", raw)
	}
	return nil
}
