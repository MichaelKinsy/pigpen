package websearch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	credentialCommandTimeout = 5 * time.Second
	maxCredentialBytes       = 16384
)

var (
	envSource     = regexp.MustCompile(`^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)\})$`)
	opSessionName = regexp.MustCompile(`^OP_SESSION_[A-Za-z0-9_]+$`)
	ctrlChars     = regexp.MustCompile("[\x00-\x1f\x7f]")
)

// commandEnvironmentNames is the allow-list a credential command inherits (upstream
// COMMAND_ENVIRONMENT_NAMES); nothing else, in particular no other provider's key, reaches it.
var commandEnvironmentNames = []string{"HOME", "USER", "LOGNAME", "OP_SERVICE_ACCOUNT_TOKEN", "PATH", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "TMPDIR",
	"XDG_CONFIG_HOME", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "SSH_AUTH_SOCK", "WSL_DISTRO_NAME", "WSL_INTEROP"}

// CredentialResolutionError never contains the command, its output or the credential.
type CredentialResolutionError struct {
	Provider string
	Category string
}

func (e *CredentialResolutionError) Error() string {
	suffix := e.Category
	switch e.Category {
	case "command-aborted":
		suffix = "aborted"
	case "oauth-credential-rejected":
		suffix = "OAuth token exchange rejected the credentials"
	}
	return fmt.Sprintf("%s credential resolution failed: %s", e.Provider, suffix)
}

// CommandError describes a failed credential command (the Node errors of the original).
type CommandError struct {
	Code    string
	Killed  bool
	Message string
	Stderr  string
}

func (e *CommandError) Error() string { return e.Message }

// CredentialCommandOptions are the limits and environment of one command run.
type CredentialCommandOptions struct {
	Timeout        time.Duration
	MaxOutputBytes int
	Environment    map[string]string
}

// CredentialCommandRunner runs a credential command and returns its stdout.
type CredentialCommandRunner func(ctx context.Context, command string, o CredentialCommandOptions) (string, error)

// CredentialOptions selects a credential: ConfiguredValue is the web-search.json value (a
// literal, "$NAME"/"${NAME}", "!command", or an escaped "$$..."/"$!..." literal),
// EnvironmentValue the legacy environment variable of the provider.
type CredentialOptions struct {
	Provider         string
	ConfiguredValue  any
	EnvironmentValue any
	// Environment is the variable set the source may read; nil means the process environment.
	Environment map[string]string
	RunCommand  CredentialCommandRunner
}

// RedactCredential removes every occurrence of a credential from text.
func RedactCredential(text, credential string) string {
	if credential == "" {
		return text
	}
	return strings.ReplaceAll(text, credential, "[redacted]")
}

func normalizeString(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	return s, s != ""
}

func processEnvironment() map[string]string {
	env := map[string]string{}
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			env[k] = v
		}
	}
	return env
}

func commandEnvironment(source map[string]string) map[string]string {
	env := map[string]string{}
	for _, name := range commandEnvironmentNames {
		if v, ok := source[name]; ok {
			env[name] = v
		}
	}
	for name, v := range source {
		if opSessionName.MatchString(name) {
			env[name] = v
		}
	}
	return env
}

func escapedSource(source string) (string, bool) {
	if strings.HasPrefix(source, "$$") || strings.HasPrefix(source, "$!") {
		return source[1:], true
	}
	return "", false
}

func explicitEnvironmentName(source string) string {
	m := envSource.FindStringSubmatch(source)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

func isMalformedExplicitSource(source string) bool {
	_, escaped := escapedSource(source)
	return strings.HasPrefix(source, "$") && !escaped && explicitEnvironmentName(source) == ""
}

// HasCredentialSource reports whether any source is configured, without running a command.
func HasCredentialSource(o CredentialOptions) bool {
	source, _ := normalizeString(o.ConfiguredValue)
	if strings.HasPrefix(source, "!") || strings.HasPrefix(source, "$") {
		return true
	}
	_, envSet := normalizeString(o.EnvironmentValue)
	return envSet || source != ""
}

func commandFailureCategory(err error, ctx context.Context) string {
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		return "command-aborted"
	}
	var ce *CommandError
	if errors.As(err, &ce) {
		if ce.Code == "ERR_CHILD_PROCESS_STDIO_MAXBUFFER" {
			return "command-output-too-large"
		}
		if ce.Killed || ce.Code == "ETIMEDOUT" {
			return "command-timeout"
		}
	}
	return "command-failed"
}

// ResolveCredential resolves a credential; "" means none is configured. A command source runs
// on every resolution (so rotated secrets are picked up) and never at availability checks.
func ResolveCredential(ctx context.Context, o CredentialOptions) (string, error) {
	source, _ := normalizeString(o.ConfiguredValue)
	if escaped, ok := escapedSource(source); ok && source != "" {
		return escaped, nil
	}
	if strings.HasPrefix(source, "!") {
		command := strings.TrimSpace(source[1:])
		if command == "" {
			return "", &CredentialResolutionError{o.Provider, "invalid-source"}
		}
		runner := o.RunCommand
		if runner == nil {
			runner = defaultRunCommand
		}
		env := o.Environment
		if env == nil {
			env = processEnvironment()
		}
		stdout, err := runner(ctx, command, CredentialCommandOptions{Timeout: credentialCommandTimeout, MaxOutputBytes: maxCredentialBytes, Environment: commandEnvironment(env)})
		if err != nil {
			return "", &CredentialResolutionError{o.Provider, commandFailureCategory(err, ctx)}
		}
		if len(stdout) > maxCredentialBytes {
			return "", &CredentialResolutionError{o.Provider, "command-output-too-large"}
		}
		value := strings.TrimSpace(stdout)
		if value == "" {
			return "", &CredentialResolutionError{o.Provider, "command-empty"}
		}
		if ctrlChars.MatchString(value) {
			return "", &CredentialResolutionError{o.Provider, "command-invalid-output"}
		}
		return value, nil
	}
	if source != "" && isMalformedExplicitSource(source) {
		return "", &CredentialResolutionError{o.Provider, "invalid-source"}
	}
	if strings.HasPrefix(source, "$") {
		name := explicitEnvironmentName(source)
		if name == "" {
			return "", &CredentialResolutionError{o.Provider, "invalid-source"}
		}
		env := o.Environment
		if env == nil {
			env = processEnvironment()
		}
		value, ok := normalizeString(env[name])
		if !ok {
			return "", &CredentialResolutionError{o.Provider, "environment-empty"}
		}
		return value, nil
	}
	if v, ok := normalizeString(o.EnvironmentValue); ok {
		return v, nil
	}
	return source, nil
}

func defaultRunCommand(ctx context.Context, command string, o CredentialCommandOptions) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
		hideWindow(cmd)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Env = nil
	for k, v := range o.Environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout = &limitWriter{buf: &out, limit: o.MaxOutputBytes + 1}
	cmd.Stderr = &stderr
	err := cmd.Run()
	if out.Len() > o.MaxOutputBytes {
		return "", &CommandError{Code: "ERR_CHILD_PROCESS_STDIO_MAXBUFFER", Message: "stdout maxBuffer length exceeded"}
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", &CommandError{Killed: true, Code: "ETIMEDOUT", Message: "credential command timed out"}
		}
		return "", &CommandError{Message: "credential command failed"}
	}
	return out.String(), nil
}

type limitWriter struct {
	buf   *bytes.Buffer
	limit int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if room := w.limit - w.buf.Len(); room > 0 {
		if len(p) > room {
			w.buf.Write(p[:room])
		} else {
			w.buf.Write(p)
		}
	}
	return len(p), nil
}
