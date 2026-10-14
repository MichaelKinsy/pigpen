package acp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	acpext "github.com/MichaelKinsy/pigpen/acp"
)

// The extension is the in-session half of the ACP Package (the protocol entrypoint is the
// companion executable, see cmd/pig-acp). Layer-1 cases through the fake host.

// runCommand runs a slash command through the fake host (the template's Host.Command).
func runCommand(h *Host, name, args string) string {
	h.t.Helper()
	return h.Command(name, args)
}

func notifications(h *Host) []HostCall { return h.CallsTo("ui.notify") }

func TestRegistersOnlyTheAcpCommand(t *testing.T) {
	h := StartHost(t, acpext.Extension(), HostOptions{})
	if h.Registered("session_start") || h.Registered("session_shutdown") || h.Registered("tool_call") {
		t.Error("registered an event handler: the extension must not touch the session")
	}
	if code := runCommand(h, "acp", ""); code != "" {
		t.Fatalf("/acp failed: %s", code)
	}
}

func TestAcpCommandExplainsSetup(t *testing.T) {
	h := StartHost(t, acpext.Extension(), HostOptions{})
	if code := runCommand(h, "acp", ""); code != "" {
		t.Fatalf("/acp failed: %s", code)
	}
	notes := notifications(h)
	if len(notes) != 1 {
		t.Fatalf("%d notifications, want 1", len(notes))
	}
	msg, _ := notes[0].Args["message"].(string)
	for _, want := range []string{
		"pig-acp", "--pig", `"agent_servers"`, "Agent Client Protocol",
		"Supported:", "Not supported:", "file system and terminal delegation", "MCP",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if notes[0].Args["level"] != "info" {
		t.Errorf("level = %v", notes[0].Args["level"])
	}
}

func TestAcpCommandNamesThisExecutable(t *testing.T) {
	h := StartHost(t, acpext.Extension(), HostOptions{})
	runCommand(h, "acp", "")
	msg, _ := notifications(h)[0].Args["message"].(string)
	exe := acpext.Executable()
	if exe == "" || !strings.Contains(msg, exe) {
		t.Errorf("message does not name the running executable %q:\n%s", exe, msg)
	}
}

// Installed as a Package (not fused into a Piglet Binary), the extension runs in its own process: an
// extension cell under PiG's cache, which is not an agent and cannot be pig-acp's --pig target. PiG hands
// every extension process the executable of the agent that started it in PIG_HARNESS_BINARY; /acp must
// name that one. (Found by review: `pig -e components/acp/extensions/acp` printed the cell runner.)
func TestAcpCommandNamesTheHostAgentWhenItRunsAsASubprocess(t *testing.T) {
	t.Setenv("PIG_HARNESS_BINARY", "/opt/pig/bin/pig")
	if got := acpext.Executable(); got != "/opt/pig/bin/pig" {
		t.Errorf("Executable() = %q, want the host agent /opt/pig/bin/pig", got)
	}
	h := StartHost(t, acpext.Extension(), HostOptions{})
	runCommand(h, "acp", "zed")
	msg, _ := notifications(h)[0].Args["message"].(string)
	if !strings.Contains(msg, `"/opt/pig/bin/pig"`) || !strings.Contains(msg, "pig-acp --pig /opt/pig/bin/pig") {
		t.Errorf("the snippet does not name the host agent:\n%s", msg)
	}
}

func TestAcpCommandRejectsUnknownArguments(t *testing.T) {
	h := StartHost(t, acpext.Extension(), HostOptions{})
	runCommand(h, "acp", "bogus")
	notes := notifications(h)
	if len(notes) != 1 || notes[0].Args["level"] != "warning" || !strings.Contains(notes[0].Args["message"].(string), "Usage: /acp") {
		t.Errorf("notifications = %+v", notes)
	}
}

func TestAcpCommandZedSnippetOnly(t *testing.T) {
	h := StartHost(t, acpext.Extension(), HostOptions{})
	runCommand(h, "acp", "zed")
	msg, _ := notifications(h)[0].Args["message"].(string)
	if !strings.Contains(msg, `"agent_servers"`) || strings.Contains(msg, "Not supported:") {
		t.Errorf("message:\n%s", msg)
	}
	// The snippet names this executable as the --pig target (a JSON string, so escaped as JSON).
	quoted, _ := json.Marshal(acpext.Executable())
	if !strings.Contains(msg, string(quoted)) {
		t.Errorf("the snippet does not name the executable %s:\n%s", quoted, msg)
	}
}

func TestAcpCommandStaysQuietWithoutUI(t *testing.T) {
	no := false
	h := StartHost(t, acpext.Extension(), HostOptions{Mode: "print", HasUI: &no})
	if code := runCommand(h, "acp", ""); code != "" {
		t.Fatalf("/acp failed headless: %s", code)
	}
}

func acpMessage(t *testing.T) string {
	t.Helper()
	h := StartHost(t, acpext.Extension(), HostOptions{})
	if code := runCommand(h, "acp", ""); code != "" {
		t.Fatalf("/acp failed: %s", code)
	}
	msg, _ := notifications(h)[0].Args["message"].(string)
	return msg
}

func TestAcpCommandSaysWhenCompanionIsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	msg := acpMessage(t)
	for _, want := range []string{"was not found on your PATH", "pig install npm:@pi-in-go/pigpen-acp", "pig acp install", "Go 1.26", "https://pi-in-go.dev/docs/packages#editors-and-acp-pig-acp"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
}

func TestAcpCommandOmitsMissingNoticeWhenCompanionIsOnPath(t *testing.T) {
	dir := t.TempDir()
	name := "pig-acp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if msg := acpMessage(t); strings.Contains(msg, "was not found") {
		t.Errorf("unexpected missing notice:\n%s", msg)
	}
}
