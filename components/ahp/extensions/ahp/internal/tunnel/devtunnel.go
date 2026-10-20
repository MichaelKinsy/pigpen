package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Error is a failure of the devtunnel CLI or of what it printed; its message is meant for the user.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func errorf(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

// CommandResult is how one devtunnel run ended. Status is nil when the process did not exit
// normally (then Signal may name why).
type CommandResult struct {
	Status *int
	Signal string
	Stdout string
	Stderr string
}

// Runner runs `devtunnel` with args. It returns an error only when the process could not be run
// at all.
type Runner func(args []string) (CommandResult, error)

// Tunnel is a tunnel as `devtunnel list` reports it.
type Tunnel struct {
	TunnelID string
	Labels   []string
}

type portDetails struct {
	portNumber    int
	protocol      string
	accessControl any
}

// ── process boundary ─────────────────────────────────────────────────────

func missingCLIError() error {
	return errorf("%s", strings.Join([]string{
		"`devtunnel` not found on PATH.",
		"",
		"Install it from https://aka.ms/devtunnels/download, then log in:",
		"",
		"    devtunnel user login -g      GitHub account",
		"    devtunnel user login         Microsoft account (the default)",
	}, "\n"))
}

// Spawn is the real runner: `devtunnel` found on PATH, run to completion with captured output.
func Spawn(args []string) (CommandResult, error) { return SpawnContext(context.Background(), args) }

// SpawnContext is [Spawn] that stops the process when ctx ends.
func SpawnContext(ctx context.Context, args []string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, "devtunnel", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		zero := 0
		result.Status = &zero
		return result, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			result.Signal = signalName(status.Signal())
			return result, nil
		}
		code := exit.ExitCode()
		result.Status = &code
		return result, nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return CommandResult{}, missingCLIError()
	}
	return CommandResult{}, errorf("failed to start devtunnel:\n%s", err.Error())
}

func failureDetail(result CommandResult) string {
	var termination string
	switch {
	case result.Signal != "":
		termination = "signal " + result.Signal
	case result.Status == nil:
		termination = "no exit status"
	default:
		termination = "exit status " + strconv.Itoa(*result.Status)
	}
	parts := []string{termination}
	for _, output := range []string{jsTrim(result.Stderr), jsTrim(result.Stdout)} {
		if output != "" {
			parts = append(parts, output)
		}
	}
	return strings.Join(parts, "\n")
}

func payload(stdout string) string {
	if trimmed := jsTrim(stdout); trimmed != "" {
		return trimmed
	}
	return "<empty output>"
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
			return true
		}
		return r >= 0x2000 && r <= 0x200a
	})
}

func parseObject(command, stdout string) (map[string]any, error) {
	var value any
	if err := json.Unmarshal([]byte(stdout), &value); err != nil {
		return nil, errorf("devtunnel %s returned invalid JSON:\n%s", command, payload(stdout))
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errorf("devtunnel %s returned an unexpected JSON value:\n%s", command, payload(stdout))
	}
	return object, nil
}

// ── command-specific JSON decoders ───────────────────────────────────────

func unexpectedResponse(command, stdout string) error {
	return errorf("devtunnel %s returned an unexpected response:\n%s", command, payload(stdout))
}

func decodeLoginStatus(value map[string]any, stdout string) (loggedIn bool, err error) {
	status, ok := value["status"].(string)
	if !ok {
		return false, unexpectedResponse("user show", stdout)
	}
	switch strings.ToLower(jsTrim(status)) {
	case "logged in":
		return true, nil
	case "not logged in":
		return false, nil
	}
	return false, unexpectedResponse("user show", stdout)
}

var (
	noTunnelsFound = regexp.MustCompile(`(?i)\bno tunnels? found\b`)
	noPortsFound   = regexp.MustCompile(`(?i)\bno (?:tunnel )?ports? found\b`)
)

func decodeTunnels(value map[string]any, stdout string) ([]Tunnel, error) {
	if list, ok := value["tunnels"].([]any); ok {
		tunnels := make([]Tunnel, 0, len(list))
		for _, item := range list {
			tunnel, ok := item.(map[string]any)
			if !ok {
				return nil, unexpectedResponse("list", stdout)
			}
			id, idOK := tunnel["tunnelId"].(string)
			labelItems, labelsOK := tunnel["labels"].([]any)
			if !idOK || id == "" || !labelsOK {
				return nil, unexpectedResponse("list", stdout)
			}
			labels := make([]string, 0, len(labelItems))
			for _, label := range labelItems {
				text, ok := label.(string)
				if !ok {
					return nil, unexpectedResponse("list", stdout)
				}
				labels = append(labels, text)
			}
			tunnels = append(tunnels, Tunnel{TunnelID: id, Labels: labels})
		}
		return tunnels, nil
	}
	if warning, ok := value["warning"].(string); ok && noTunnelsFound.MatchString(warning) {
		return nil, nil
	}
	return nil, unexpectedResponse("list", stdout)
}

func decodeCreatedTunnelID(value map[string]any, stdout string) (string, error) {
	tunnel, ok := value["tunnel"].(map[string]any)
	if !ok {
		return "", unexpectedResponse("create", stdout)
	}
	id, ok := tunnel["tunnelId"].(string)
	if !ok || id == "" {
		return "", unexpectedResponse("create", stdout)
	}
	return id, nil
}

func decodePort(value any, command, stdout string) (portDetails, error) {
	port, ok := value.(map[string]any)
	if !ok {
		return portDetails{}, unexpectedResponse(command, stdout)
	}
	protocol := ""
	if text, ok := port["protocol"].(string); ok {
		protocol = strings.ToLower(jsTrim(text))
	}
	number, ok := port["portNumber"].(float64)
	if !ok || number != math.Trunc(number) || number < 1 || number > 65535 || protocol == "" {
		return portDetails{}, unexpectedResponse(command, stdout)
	}
	return portDetails{portNumber: int(number), protocol: protocol, accessControl: port["accessControl"]}, nil
}

func decodePortList(value map[string]any, stdout string) ([]portDetails, error) {
	if list, ok := value["ports"].([]any); ok {
		ports := make([]portDetails, 0, len(list))
		for _, item := range list {
			port, err := decodePort(item, "port list", stdout)
			if err != nil {
				return nil, err
			}
			ports = append(ports, port)
		}
		return ports, nil
	}
	if warning, ok := value["warning"].(string); ok && noPortsFound.MatchString(warning) {
		return nil, nil
	}
	return nil, unexpectedResponse("port list", stdout)
}

// ── domain adapter ───────────────────────────────────────────────────────

// CLI is the devtunnel operations the host needs. Its decoders accept additive fields but reject
// missing or malformed fields that the host relies on.
type CLI struct{ runner Runner }

// NewCLI returns a CLI over runner; nil runs the real `devtunnel`.
func NewCLI(runner Runner) *CLI {
	if runner == nil {
		runner = Spawn
	}
	return &CLI{runner: runner}
}

func (c *CLI) run(command string, args ...string) (CommandResult, error) {
	result, err := c.runner(args)
	if err != nil {
		return CommandResult{}, err
	}
	if result.Status == nil || *result.Status != 0 {
		return CommandResult{}, errorf("devtunnel %s failed:\n%s", command, failureDetail(result))
	}
	return result, nil
}

func (c *CLI) runJSON(command string, args ...string) (map[string]any, string, error) {
	result, err := c.run(command, args...)
	if err != nil {
		return nil, "", err
	}
	value, err := parseObject(command, result.Stdout)
	return value, result.Stdout, err
}

// RequireLogin fails unless an account is logged in.
func (c *CLI) RequireLogin() error {
	value, stdout, err := c.runJSON("user show", "user", "show", "--json")
	if err != nil {
		return err
	}
	loggedIn, err := decodeLoginStatus(value, stdout)
	if err != nil {
		return err
	}
	if !loggedIn {
		return errorf("%s", strings.Join([]string{
			"`devtunnel` is not logged in. Run one of:",
			"",
			"    devtunnel user login -g      GitHub account",
			"    devtunnel user login         Microsoft account (the default)",
			"",
			"Add -d for device-code auth when there is no browser.",
		}, "\n"))
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// FindTunnel returns this host's first labelled tunnel, nil if a previous run made none.
func (c *CLI) FindTunnel() (*Tunnel, error) {
	value, stdout, err := c.runJSON("list", "list", "--all-labels", IdentityLabel, "--all-labels", LauncherLabel, "--json")
	if err != nil {
		return nil, err
	}
	tunnels, err := decodeTunnels(value, stdout)
	if err != nil {
		return nil, err
	}
	if len(tunnels) == 0 {
		return nil, nil
	}
	found := tunnels[0]
	if !contains(found.Labels, IdentityLabel) || !contains(found.Labels, LauncherLabel) {
		return nil, unexpectedResponse("list", stdout)
	}
	return &found, nil
}

// CreateTunnel makes a tunnel carrying the reserved labels and, if name is not empty, a display
// label, and returns its id.
func (c *CLI) CreateTunnel(name string) (string, error) {
	labels := []string{LauncherLabel, ProtocolLabel, IdentityLabel}
	if name != "" {
		labels = append(labels, name)
	}
	args := []string{"create", "--json"}
	for _, label := range labels {
		args = append(args, "-l", label)
	}
	value, stdout, err := c.runJSON("create", args...)
	if err != nil {
		return "", err
	}
	return decodeCreatedTunnelID(value, stdout)
}

func (c *CLI) createPort(id string) error {
	_, err := c.run("port create", "port", "create", id, "-p", strconv.Itoa(Port), "--protocol", "http", "--json")
	return err
}

// EnsurePort makes sure the fixed port uses an HTTP origin. The relay terminates TLS, while the
// local listener speaks HTTP/WebSocket. A wrong-protocol port is replaced only after confirming
// that it has no port-specific ACL.
func (c *CLI) EnsurePort(id string) error {
	value, stdout, err := c.runJSON("port list", "port", "list", id, "--json")
	if err != nil {
		return err
	}
	ports, err := decodePortList(value, stdout)
	if err != nil {
		return err
	}
	var current *portDetails
	for i := range ports {
		if ports[i].portNumber == Port {
			current = &ports[i]
			break
		}
	}
	if current == nil {
		return c.createPort(id)
	}
	if current.protocol == "http" {
		return nil
	}

	// List output does not carry port-specific ACLs. Re-read the details before a destructive
	// protocol migration, and trust the newer response if the port changed concurrently.
	shown, shownStdout, err := c.runJSON("port show", "port", "show", id, "-p", strconv.Itoa(Port), "--json")
	if err != nil {
		return err
	}
	details, err := decodePort(shown["port"], "port show", shownStdout)
	if err != nil {
		return err
	}
	if details.portNumber != Port {
		return unexpectedResponse("port show", shownStdout)
	}
	if details.protocol == "http" {
		return nil
	}
	hasPortACL := details.accessControl != nil
	if list, ok := details.accessControl.([]any); ok {
		hasPortACL = len(list) > 0
	}
	if hasPortACL {
		return errorf("devtunnel port %d uses %s and has port-specific access control; replace it manually", Port, details.protocol)
	}
	if _, err := c.run("port delete", "port", "delete", id, "-p", strconv.Itoa(Port), "--json"); err != nil {
		return err
	}
	return c.createPort(id)
}

// Rename swaps the display label, leaving the identity and protocol labels alone. from is empty
// when the tunnel has no display label yet.
func (c *CLI) Rename(id, from, to string) error {
	args := []string{"update", id, "--add-labels", to}
	if from != "" {
		args = append(args, "--remove-labels", from)
	}
	_, err := c.run("update", args...)
	return err
}
