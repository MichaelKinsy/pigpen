package ahp

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
)

func TestLoopbackHosts(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "localhost": true, "127.1.2.3": true,
		"0.0.0.0": false, "::": false, "192.168.1.5": false, "example.com": false, "": false,
	} {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestRemoteHostNeedsAToken(t *testing.T) {
	if err := requireTokenOffLoopback("s.json", "0.0.0.0", ""); err == nil || !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("a public host without a token was accepted: %v", err)
	}
	for _, c := range []struct{ host, token string }{{"0.0.0.0", "secret"}, {"127.0.0.1", ""}, {"localhost", ""}} {
		if err := requireTokenOffLoopback("s.json", c.host, c.token); err != nil {
			t.Errorf("%+v refused: %v", c, err)
		}
	}
}

func TestSessionDeletionIsGuarded(t *testing.T) {
	var removed []string
	remove := func(p string) (pi.SessionFileDeletionResult, error) {
		removed = append(removed, p)
		return pi.SessionFileDeletionResult{OK: true}, nil
	}
	live := func() string { return "/sessions/./live.jsonl" }

	off := guardedDelete(false, live, remove)
	if r, _ := off("/sessions/other.jsonl"); r.OK || r.Error == "" {
		t.Fatalf("deletion is off by default, got %+v", r)
	}
	on := guardedDelete(true, live, remove)
	if r, _ := on("/sessions/live.jsonl"); r.OK || !strings.Contains(r.Error, "running") {
		t.Fatalf("the running session's file must never be deleted, got %+v", r)
	}
	if len(removed) != 0 {
		t.Fatalf("removed %v before any allowed deletion", removed)
	}
	if r, _ := on("/sessions/other.jsonl"); !r.OK {
		t.Fatalf("an allowed deletion failed: %+v", r)
	}
	if len(removed) != 1 || removed[0] != "/sessions/other.jsonl" {
		t.Fatalf("removed = %v", removed)
	}
	if r, _ := guardedDelete(true, func() string { return "" }, remove)("/sessions/x.jsonl"); !r.OK {
		t.Fatalf("with no live file everything else may go: %+v", r)
	}
}

// VS Code's "Agents: Add Remote Agent Host..." takes ws://<host>:<port>?tkn=<token>; the notice prints that form first and the
// generic one after it, and prints no token form for a listener without a token.
func TestListenNoticePrintsTheVSCodeAddress(t *testing.T) {
	got := listenNotice("127.0.0.1:7117", "s3cret")
	for _, want := range []string{"AHP listening on ws://127.0.0.1:7117?tkn=s3cret ", "VS Code", "Agents: Add Remote Agent Host...", "ws://127.0.0.1:7117/?token=s3cret"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice %q lacks %q", got, want)
		}
	}
	if got := listenNotice("127.0.0.1:7117", ""); got != "AHP listening on ws://127.0.0.1:7117" {
		t.Errorf("a tokenless notice = %q", got)
	}
}

func TestListenNoticeEscapesAToken(t *testing.T) {
	got := listenNotice("127.0.0.1:1", "a b&c")
	if !strings.Contains(got, "?token=a+b%26c") || !strings.Contains(got, "?tkn=a+b%26c") {
		t.Errorf("a token with reserved characters must be query-escaped: %q", got)
	}
}

// VS Code's SSH launcher (chat.sshRemoteAgentHostCommand) scrapes the command's output with this expression and takes the
// first match: microsoft/vscode src/vs/platform/agentHost/node/sshRemoteAgentHostHelpers.ts (AGENT_HOST_WS_URL_RE,
// extractAgentHostWebSocketURL). The token it captures is used as is.
var vsCodeSSHListeningURL = regexp.MustCompile(`ws://(?:127\.0\.0\.1|localhost):(\d+)(?:\?tkn=([^\s&]+))?`)

func TestListenNoticeIsReadByVSCodeSSHLauncher(t *testing.T) {
	m := vsCodeSSHListeningURL.FindStringSubmatch(listenNotice("127.0.0.1:7117", "s3cret"))
	if m == nil || m[1] != "7117" || m[2] != "s3cret" {
		t.Fatalf("VS Code's SSH launcher reads port and token %q from the notice, want 7117 and s3cret", m)
	}
}

// "Agents: Add Remote Agent Host..." takes the first ws:// URL in what is pasted, drops trailing ),.;] and moves `tkn` out
// of the query into the connection token: parseRemoteAgentHostInput in microsoft/vscode
// src/vs/platform/agentHost/common/remoteAgentHostService.ts. Pasting the whole notice must give the right token.
func TestListenNoticeIsReadByAddRemoteAgentHost(t *testing.T) {
	notice := listenNotice("127.0.0.1:7117", "s3cret")
	candidate := strings.TrimRight(regexp.MustCompile(`(?i)(?:https?|wss?)://\S+`).FindString(notice), "),.;]")
	u, err := url.Parse(candidate)
	if err != nil {
		t.Fatalf("%q: %v", candidate, err)
	}
	if got := u.Query().Get("tkn"); got != "s3cret" || u.Host != "127.0.0.1:7117" {
		t.Fatalf("pasting the notice gives host %q and tkn %q, want 127.0.0.1:7117 and s3cret (from %q)", u.Host, got, candidate)
	}
}
