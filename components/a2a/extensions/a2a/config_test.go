package a2aext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "a2a.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestListenerIsOffUnlessConfigured(t *testing.T) {
	cfg, err := LoadConfig(LoadOptions{ConfigHome: t.TempDir(), Getenv: envFrom(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled() || cfg.Listen != "" {
		t.Fatalf("listener must be off by default, got %+v", cfg)
	}
}

func TestConfigFileEnablesListener(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:9911","tokens":[{"name":"ci","tokenEnv":"CI_TOKEN"}]}`)
	cfg, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(map[string]string{"CI_TOKEN": "s3cret-token-value"})})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9911" || !cfg.Enabled() {
		t.Fatalf("listen = %q", cfg.Listen)
	}
}

func TestPrecedenceFlagOverEnvOverFile(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:1111","insecureNoAuth":true}`)
	env := envFrom(map[string]string{"PIG_A2A_LISTEN": "127.0.0.1:2222"})
	cfg, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: env})
	if err != nil || cfg.Listen != "127.0.0.1:2222" {
		t.Fatalf("env over file: %q %v", cfg.Listen, err)
	}
	cfg, err = LoadConfig(LoadOptions{ConfigHome: dir, Getenv: env, FlagListen: "127.0.0.1:3333"})
	if err != nil || cfg.Listen != "127.0.0.1:3333" {
		t.Fatalf("flag over env: %q %v", cfg.Listen, err)
	}
}

func TestConfigPathFromEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "elsewhere.json")
	if err := os.WriteFile(path, []byte(`{"listen":"127.0.0.1:4444","insecureNoAuth":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(LoadOptions{ConfigHome: t.TempDir(), Getenv: envFrom(map[string]string{"PIG_A2A_CONFIG": path})})
	if err != nil || cfg.Listen != "127.0.0.1:4444" {
		t.Fatalf("got %q, %v", cfg.Listen, err)
	}
}

func TestInlineSecretsAreRejected(t *testing.T) {
	for name, body := range map[string]string{
		"token":       `{"listen":"127.0.0.1:1","tokens":[{"name":"a","token":"inline"}]}`,
		"bearerToken": `{"remotes":{"x":{"url":"http://h","bearerToken":"inline"}}}`,
		"unknown":     `{"listenn":"127.0.0.1:1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, body)
			_, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)})
			if err == nil {
				t.Fatal("want an error: secrets are named by environment variable, unknown keys are typos")
			}
			if strings.Contains(err.Error(), "inline") {
				t.Fatalf("error must not echo the secret: %v", err)
			}
		})
	}
}

func TestListenerNeedsAuthentication(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:5555"}`)
	if _, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)}); err == nil {
		t.Fatal("a listener without tokens must be refused unless insecureNoAuth is set")
	}
}

func TestInsecureNoAuthOnlyOnLoopback(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:5555","insecureNoAuth":true}`)
	if _, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)}); err != nil {
		t.Fatalf("loopback insecureNoAuth: %v", err)
	}
	for _, addr := range []string{"0.0.0.0:5555", ":5555", "192.168.1.5:5555", "example.com:5555"} {
		writeConfig(t, dir, `{"listen":"`+addr+`","insecureNoAuth":true}`)
		if _, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)}); err == nil {
			t.Fatalf("insecureNoAuth on %s must be refused", addr)
		}
	}
}

func TestTokenEnvMustBeSetAndIsNotEchoed(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:5555","tokens":[{"name":"a","tokenEnv":"A2A_TOKEN_A"}]}`)
	_, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)})
	if err == nil || !strings.Contains(err.Error(), "A2A_TOKEN_A") {
		t.Fatalf("want an error naming the missing variable, got %v", err)
	}
}

func TestTokenConfigValidation(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate name":  `{"listen":"127.0.0.1:1","tokens":[{"name":"a","tokenEnv":"T"},{"name":"a","tokenEnv":"U"}]}`,
		"duplicate value": `{"listen":"127.0.0.1:1","tokens":[{"name":"a","tokenEnv":"T"},{"name":"b","tokenEnv":"T"}]}`,
		"short token":     `{"listen":"127.0.0.1:1","tokens":[{"name":"a","tokenEnv":"SHORT"}]}`,
		"bad tenant":      `{"listen":"127.0.0.1:1","tokens":[{"name":"a","tokenEnv":"T","tenant":"a/b"}]}`,
		"no name":         `{"listen":"127.0.0.1:1","tokens":[{"tokenEnv":"T"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, body)
			env := envFrom(map[string]string{"T": "0123456789abcdef0123456789abcdef", "U": "fedcba9876543210fedcba9876543210", "SHORT": "abc"})
			if _, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: env}); err == nil {
				t.Fatal("want a validation error")
			}
		})
	}
}

func TestListenAddressMustBeHostPort(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"not-an-address","insecureNoAuth":true}`)
	if _, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)}); err == nil {
		t.Fatal("want an error for a malformed listen address")
	}
}

func TestRemoteDefaultsAndValidation(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"remotes":{"kagent":{"url":"http://kagent.local/api/a2a/ns/agent","bearerTokenEnv":"KAGENT_TOKEN","skipCard":true}}}`)
	cfg, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)})
	if err != nil {
		t.Fatalf("remotes without a listener need no server auth: %v", err)
	}
	if cfg.Enabled() || cfg.Remotes["kagent"].URL == "" || !cfg.Remotes["kagent"].SkipCard {
		t.Fatalf("%+v", cfg)
	}
	writeConfig(t, dir, `{"remotes":{"bad name!":{"url":"http://x"}}}`)
	if _, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)}); err == nil {
		t.Fatal("remote names are identifiers")
	}
	writeConfig(t, dir, `{"remotes":{"x":{"url":"ftp://x"}}}`)
	if _, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)}); err == nil {
		t.Fatal("remote URL must be http or https")
	}
}

func TestWorkerDefaultsHaveNoTools(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:5555","insecureNoAuth":true}`)
	cfg, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(nil)})
	if err != nil {
		t.Fatal(err)
	}
	// PiG's read, grep, find and ls take absolute paths, so even "read-only" tools reach every file the account can
	// read (credentials, other tenants' sessions). The operator names tools explicitly (review finding H1).
	if got := strings.Join(cfg.Worker.Tools, ","); got != "" {
		t.Fatalf("default worker tools = %q, want none", got)
	}
	if cfg.MaxConcurrentTasks < 1 || cfg.TaskTimeoutSeconds < 1 {
		t.Fatalf("limits must default to positive values: %+v", cfg)
	}
}

func TestWorkerProcessDoesNotOpenAListener(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"listen":"127.0.0.1:5555","insecureNoAuth":true}`)
	cfg, err := LoadConfig(LoadOptions{ConfigHome: dir, Getenv: envFrom(map[string]string{"PIG_A2A_WORKER": "1"})})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled() {
		t.Fatal("a worker child (PIG_A2A_WORKER=1) must never listen, or a task would start another server")
	}
}
