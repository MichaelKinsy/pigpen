package websearch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Twins of test/credential-source.test.mjs (upstream 9a734ed).

// TestMain lets the test binary act as the fake external credential tool: the port's
// answer to the upstream test that spawns a real `node fake-op.mjs` child.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__fake-op" {
		if strings.Join(os.Args[2:], " ") != "read op://Synthetic/Search/credential" {
			os.Exit(2)
		}
		if os.Getenv("OP_SERVICE_ACCOUNT_TOKEN") != "synthetic-service-account-token" {
			os.Exit(3)
		}
		if os.Getenv("UNRELATED_SECRET") != "" {
			os.Exit(4)
		}
		_, _ = os.Stdout.WriteString("synthetic-provider-key")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func credOptions(configured, environment any) CredentialOptions {
	return CredentialOptions{Provider: "Synthetic", ConfiguredValue: configured, EnvironmentValue: environment}
}

func fakeRunner(stdout string) CredentialCommandRunner {
	return func(context.Context, string, CredentialCommandOptions) (string, error) { return stdout, nil }
}

func mustResolve(t *testing.T, o CredentialOptions) string {
	t.Helper()
	v, err := ResolveCredential(bg(), o)
	noErr(t, err)
	return v
}

func credCategory(err error) string {
	var ce *CredentialResolutionError
	if errors.As(err, &ce) {
		return ce.Category
	}
	return ""
}

func TestUpstream_credential_source(t *testing.T) {
	const f = "credential-source"

	tw(t, f, "literal and legacy environment credentials preserve existing precedence", func(t *testing.T) {
		if mustResolve(t, credOptions("literal-value", "environment-value")) != "environment-value" ||
			mustResolve(t, credOptions("literal-value", nil)) != "literal-value" ||
			mustResolve(t, credOptions(nil, "environment-value")) != "environment-value" ||
			mustResolve(t, credOptions(nil, nil)) != "" {
			t.Fatal("precedence")
		}
	})

	tw(t, f, "explicit environment sources use only the named variable", func(t *testing.T) {
		o := credOptions("${SCOPED_SYNTHETIC_KEY}", "stale-legacy-value")
		o.Environment = map[string]string{"SCOPED_SYNTHETIC_KEY": "scoped-value"}
		if mustResolve(t, o) != "scoped-value" {
			t.Fatal()
		}
		o = credOptions("$SCOPED_SYNTHETIC_KEY", "stale-legacy-value")
		o.Environment = map[string]string{}
		_, err := ResolveCredential(bg(), o)
		if credCategory(err) != "environment-empty" {
			t.Fatalf("%v", err)
		}
	})

	tw(t, f, "command sources override stale values, remain lazy, and rotate per resolution", func(t *testing.T) {
		calls := 0
		want := map[string]string{
			"HOME": "/synthetic/home", "PATH": "/usr/bin:/bin",
			"OP_SERVICE_ACCOUNT_TOKEN": "synthetic-service-account-token", "OP_SESSION_my": "synthetic-password-manager-session",
		}
		o := credOptions("!/trusted/read synthetic", "stale-environment-value")
		o.RunCommand = func(_ context.Context, command string, ro CredentialCommandOptions) (string, error) {
			calls++
			if command != "/trusted/read synthetic" {
				t.Errorf("command %q", command)
			}
			if len(ro.Environment) != len(want) {
				t.Errorf("environment %v", ro.Environment)
			}
			for k, v := range want {
				if ro.Environment[k] != v {
					t.Errorf("environment %s=%q", k, ro.Environment[k])
				}
			}
			if calls == 1 {
				return "first-value\n", nil
			}
			return "second-value\n", nil
		}
		o.Environment = map[string]string{
			"HOME": "/synthetic/home", "PATH": "/usr/bin:/bin", "EXA_API_KEY": "stale-provider-key-must-not-reach-command",
			"OP_SESSION_my": "synthetic-password-manager-session", "OP_SERVICE_ACCOUNT_TOKEN": "synthetic-service-account-token",
			"NODE_OPTIONS": "--require=untrusted.js",
		}
		if !HasCredentialSource(o) || calls != 0 {
			t.Fatal("hasCredentialSource must not run the command")
		}
		if mustResolve(t, o) != "first-value" || mustResolve(t, o) != "second-value" || calls != 2 {
			t.Fatalf("calls=%d", calls)
		}
	})

	tw(t, f, "service-account tokens remain absent unless inherited by Pi", func(t *testing.T) {
		var env map[string]string
		o := credOptions("!/trusted/read synthetic", nil)
		o.RunCommand = func(_ context.Context, _ string, ro CredentialCommandOptions) (string, error) {
			env = ro.Environment
			return "value", nil
		}
		o.Environment = map[string]string{"HOME": "/synthetic/home"}
		mustResolve(t, o)
		if _, ok := env["OP_SERVICE_ACCOUNT_TOKEN"]; ok {
			t.Fatal("token must be absent")
		}
	})

	tw(t, f, "a real resolver child can authenticate with an inherited service-account token", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the fake tool is launched through a POSIX shell")
		}
		exe, err := os.Executable()
		noErr(t, err)
		dir := t.TempDir()
		command := "'" + exe + "' __fake-op read op://Synthetic/Search/credential"
		o := credOptions("!"+command, nil)
		o.Environment = map[string]string{"HOME": dir, "PATH": os.Getenv("PATH"),
			"OP_SERVICE_ACCOUNT_TOKEN": "synthetic-service-account-token", "UNRELATED_SECRET": "must-not-reach-command"}
		if got := mustResolve(t, o); got != "synthetic-provider-key" {
			t.Fatalf("%q", got)
		}
		_ = exec.Command
		_ = filepath.Join
	})

	tw(t, f, "command output must be one non-empty bounded value", func(t *testing.T) {
		for _, c := range []struct{ stdout, category string }{
			{"", "command-empty"}, {"one\ntwo\n", "command-invalid-output"}, {strings.Repeat("x", 16385), "command-output-too-large"},
		} {
			o := credOptions("!ignored", nil)
			o.RunCommand = fakeRunner(c.stdout)
			_, err := ResolveCredential(bg(), o)
			if credCategory(err) != c.category {
				t.Fatalf("%q: %v", c.category, err)
			}
		}
	})

	tw(t, f, "command failures are categorized and redact command output", func(t *testing.T) {
		const secret = "SYNTHETIC_SECRET_MUST_NOT_ESCAPE"
		for _, c := range []struct {
			failure  *CommandError
			category string
		}{
			{&CommandError{Code: "ENOENT", Message: "spawn failed " + secret, Stderr: secret}, "command-failed"},
			{&CommandError{Killed: true, Message: "timed out " + secret, Stderr: secret}, "command-timeout"},
			{&CommandError{Code: "ERR_CHILD_PROCESS_STDIO_MAXBUFFER", Message: "too large " + secret, Stderr: secret}, "command-output-too-large"},
		} {
			o := credOptions("!/trusted/read synthetic", "stale-value")
			failure := c.failure
			o.RunCommand = func(context.Context, string, CredentialCommandOptions) (string, error) { return "", failure }
			_, err := ResolveCredential(bg(), o)
			if credCategory(err) != c.category || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "/trusted/read") {
				t.Fatalf("%s: %v", c.category, err)
			}
		}
	})

	tw(t, f, "escaped source prefixes remain literal and override legacy environment values", func(t *testing.T) {
		if mustResolve(t, credOptions("$$OPENAI_API_KEY", "legacy-value")) != "$OPENAI_API_KEY" ||
			mustResolve(t, credOptions("$!literal-command", "legacy-value")) != "!literal-command" ||
			!HasCredentialSource(credOptions("$$OPENAI_API_KEY", nil)) {
			t.Fatal("escaped prefixes")
		}
	})

	tw(t, f, "malformed explicit sources fail closed instead of becoming literals", func(t *testing.T) {
		for _, source := range []string{"!", "$BAD-NAME", "${UNCLOSED"} {
			_, err := ResolveCredential(bg(), credOptions(source, "stale-value"))
			if credCategory(err) != "invalid-source" {
				t.Fatalf("%s: %v", source, err)
			}
		}
	})
}
