// Package ssh implements omp's `ssh://` internal URL protocol handler, SSH host
// discovery (`ssh.json` and `~/.ssh/config`), ControlMaster connection manager,
// and byte-preserving remote POSIX file I/O.
//
// upstream: packages/coding-agent/src/ssh/{utils,config-writer,connection-manager,file-transfer}.ts,
//
//	packages/coding-agent/src/internal-urls/ssh-protocol.ts,
//	packages/coding-agent/src/discovery/{ssh,ssh-config}.ts,
//	packages/coding-agent/src/utils/{shell-quote,owner-private-dir}.ts
package ssh

import (
	"fmt"
	"regexp"
	"strings"
)

var sanitizeHostRE = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// QuotePosixArgument quotes a single argument for a POSIX shell command.
// upstream: packages/coding-agent/src/utils/shell-quote.ts:4-6
func QuotePosixArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// SanitizeHostName converts a host name into a safe filename token.
// upstream: packages/coding-agent/src/ssh/utils.ts:3-6
func SanitizeHostName(name string) string {
	sanitized := sanitizeHostRE.ReplaceAllString(name, "_")
	if len(sanitized) > 0 {
		return sanitized
	}
	return "remote"
}

// BuildSSHTarget validates username and host and formats the OpenSSH destination
// (`user@host` or `host`). Rejects leading `-`, whitespace, control characters,
// and empty hosts before anything can reach `ssh` argv.
// upstream: packages/coding-agent/src/ssh/utils.ts:8-23
func BuildSSHTarget(username, host string) (string, error) {
	if host == "" {
		return "", fmt.Errorf("Invalid SSH host %q: host cannot be empty", host)
	}
	// SSH treats a destination starting with "-" as an option, so a host/user of
	// `-oProxyCommand=...` becomes local command execution. Reject before this
	// string reaches any `ssh` argv (single render chokepoint).
	if strings.HasPrefix(host, "-") {
		return "", fmt.Errorf("Invalid SSH host %q: an SSH destination must not begin with \"-\" (argument-injection guard)", host)
	}
	if err := checkDestinationChars("host", host); err != nil {
		return "", err
	}
	if username != "" {
		if strings.HasPrefix(username, "-") {
			return "", fmt.Errorf("Invalid SSH username %q: an SSH username must not begin with \"-\" (argument-injection guard)", username)
		}
		if err := checkDestinationChars("username", username); err != nil {
			return "", err
		}
		return username + "@" + host, nil
	}
	return host, nil
}

func checkDestinationChars(kind, value string) error {
	for i := range len(value) {
		c := value[i]
		if c <= 0x20 || c == 0x7f {
			return fmt.Errorf("Invalid SSH %s %q: must not contain whitespace or control characters", kind, value)
		}
		switch c {
		case '`', '$', ';', '|', '&', '<', '>', '\'', '"', '\\', '(', ')', '{', '}', '*', '?', '!':
			return fmt.Errorf("Invalid SSH %s %q: must not contain shell metacharacters", kind, value)
		}
	}
	return nil
}

// WrapInPosixShell wraps a POSIX command snippet in `<shell> -c '<command>'` so
// OpenSSH executes it under the verified POSIX shell rather than a non-POSIX
// login shell (fish/csh/tcsh).
// upstream: packages/coding-agent/src/ssh/utils.ts:39-41
func WrapInPosixShell(shell, command string) string {
	return shell + " -c " + QuotePosixArgument(command)
}
