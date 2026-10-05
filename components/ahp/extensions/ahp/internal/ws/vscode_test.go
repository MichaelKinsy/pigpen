package ws_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
	"github.com/MichaelKinsy/pigpen/ahp/internal/ws"
)

// VS Code's Agents window connects to a WebSocket AHP host given as ws://<host>:<port>?tkn=<token>
// (command "Agents: Add Remote Agent Host...", or the chat.remoteAgentHosts setting). These tests send what VS Code sends, as
// read from microsoft/vscode main; nothing here was run against a live VS Code. Files are under src/vs/platform/agentHost/.

// vsCodeInitialize is VS Code's first message, field for field (browser/agentHostProtocolClient.ts, connect): request id 1
// (_nextRequestId starts at 1), the root channel, every protocol version it can speak newest first with 0.8.0 left out
// (CLIENT_SUPPORTED_PROTOCOL_VERSIONS, from common/state/protocol/version/registry.ts SUPPORTED_PROTOCOL_VERSIONS), a UUID
// client id, the Agents window's clientInfo (common/agentHostClientInfo.ts), the _meta of toAgentHostClientMeta
// (common/agentHostTelemetry.ts) for a direct WebSocket, and the root as the one initial subscription.
func vsCodeInitialize(t *testing.T) []byte { return vsCodeInitializeOffering(t, vsCodeOffers["main"]) }

// vsCodeOffers is the protocolVersions list each VS Code sends: 1.140.0 speaks 0.9.0 itself (PROTOCOL_VERSION), release/1.141
// and main speak 0.10.0 and still offer 0.9.0.
var vsCodeOffers = map[string][]string{
	"1.140.0": {"0.9.0", "0.7.0", "0.6.0", "0.5.2", "0.5.1"},
	"main":    {"0.10.0", "0.9.0", "0.7.0", "0.6.0", "0.5.2", "0.5.1"},
}

func vsCodeInitializeOffering(t *testing.T, versions []string) []byte {
	t.Helper()
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"channel":              wire.RootChannel,
		"protocolVersions":     []string{"0.10.0", "0.9.0", "0.7.0", "0.6.0", "0.5.2", "0.5.1"},
		"clientId":             "6f1c2d3e-4b5a-4c7d-8e9f-0a1b2c3d4e5f",
		"clientInfo":           map[string]any{"name": "vscode-agents-window", "title": "VS Code Agents Window"},
		"_meta":                map[string]any{"vscode.telemetryLevel": "all", "vscode.clientConnectionKind": "direct_websocket"},
		"initialSubscriptions": []string{wire.RootChannel},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// vsCodeRootConfig is one of the notifications VS Code sends right after initialize (_forwardClientConfig, which dispatches
// root/configChanged with client sequence 0 for each forwarded setting: _dispatchRootConfig).
func vsCodeRootConfig(t *testing.T, config map[string]any) []byte {
	t.Helper()
	msg, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "dispatchAction", "params": map[string]any{
		"channel": wire.RootChannel, "clientSeq": 0, "action": map[string]any{"type": "root/configChanged", "config": config},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

// rawAt upgrades by hand at the request target VS Code's address produces, so a test can see opcodes and the status line.
func rawAt(t *testing.T, s *ws.Server, target string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+itoa(s.Port()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	io.WriteString(conn, "GET "+target+" HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn, br, resp
}

func TestVSCodeHandshakeNegotiatesTheSupportedVersionOverTextFrames(t *testing.T) {
	_, s := serve(t, ws.Options{Token: "s3cret"})
	conn, br, resp := rawAt(t, s, "/?tkn=s3cret")
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade with the right tkn = %d", resp.StatusCode)
	}
	if _, err := conn.Write(maskedFrame(true, 1, vsCodeInitialize(t))); err != nil {
		t.Fatal(err)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	op, payload := readFrame(t, br)
	if op != 1 {
		t.Fatalf("the reply was opcode %d; VS Code drops every non-text frame and closes the connection (4002) after ten (browser/webSocketClientTransport.ts)", op)
	}
	var reply struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			Snapshots       []struct {
				Resource string `json:"resource"`
				State    struct {
					Agents []struct {
						Provider string `json:"provider"`
					} `json:"agents"`
				} `json:"state"`
			} `json:"snapshots"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &reply); err != nil {
		t.Fatalf("%s: %v", payload, err)
	}
	if reply.Result.ProtocolVersion != "0.9.0" {
		t.Fatalf("VS Code's offer (0.10.0, 0.9.0, 0.7.0, 0.6.0, 0.5.2, 0.5.1) negotiated %q, want the one version this host speaks, 0.9.0", reply.Result.ProtocolVersion)
	}
	if len(reply.Result.Snapshots) != 1 || reply.Result.Snapshots[0].Resource != wire.RootChannel {
		t.Fatalf("the initial root subscription returned %+v", reply.Result.Snapshots)
	}
	for _, agent := range reply.Result.Snapshots[0].State.Agents {
		if agent.Provider == "" || strings.Contains(agent.Provider, "-") {
			t.Errorf("agent provider id %q must be non-empty and dash-free", agent.Provider)
		}
	}

	// Then, as VS Code does, forward settings as root/configChanged notifications and keep using the connection (VS Code's
	// keep-alive ping is {"channel":"ahp-root://"}): the host must neither drop it nor answer with anything but text frames.
	for _, config := range []map[string]any{{"telemetryLevel": "all"}, {"terminalAutoApproveEnabled": true}} {
		if _, err := conn.Write(maskedFrame(true, 1, vsCodeRootConfig(t, config))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Write(maskedFrame(true, 1, []byte(`{"jsonrpc":"2.0","id":2,"method":"ping","params":{"channel":"ahp-root://"}}`))); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		op, payload := readFrame(t, br)
		if op != 1 {
			t.Fatalf("a later frame was opcode %d, want text", op)
		}
		var msg struct {
			ID    *int            `json:"id"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(payload, &msg); err != nil {
			t.Fatalf("%s: %v", payload, err)
		}
		if msg.ID != nil && *msg.ID == 2 {
			if msg.Error != nil {
				t.Fatalf("ping after the config notifications failed: %s", payload)
			}
			break
		}
	}
}

func TestVSCodeAddressFormIsAccepted(t *testing.T) {
	_, s := serve(t, ws.Options{Token: "s3cret"})
	for release, versions := range vsCodeOffers {
		// The address VS Code is given has no slash before the query.
		c, _, err := ws.Dial("ws://127.0.0.1:"+itoa(s.Port())+"?tkn=s3cret", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.WriteText(vsCodeInitializeOffering(t, versions)); err != nil {
			t.Fatal(err)
		}
		msg, err := c.ReadMessage(2 * time.Second)
		c.Close()
		if err != nil || !strings.Contains(string(msg), `"protocolVersion":"0.9.0"`) {
			t.Fatalf("VS Code %s: reply %s (%v)", release, msg, err)
		}
	}
}

func TestVSCodeWrongOrMissingTknIsRefusedAtTheUpgrade(t *testing.T) {
	_, s := serve(t, ws.Options{Token: "s3cret"})
	for _, target := range []string{"/?tkn=wrong", "/?tkn=", "/", "/?TKN=s3cret"} {
		_, _, resp := rawAt(t, s, target)
		// 403 Forbidden with no upgrade, as VS Code's own agent host answers a missing or wrong tkn
		// (microsoft/vscode src/vs/platform/agentHost/node/webSocketTransport.ts, verifyClient: cb(false, 403, 'Forbidden')).
		// The AHP spec leaves endpoint access to the transport (docs/specification/transport.md, "Authentication"), and
		// VS Code's client cannot see the status: a browser WebSocket reports any refused upgrade as one error.
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s = %d, want 403 and no upgrade", target, resp.StatusCode)
		}
	}
}

func TestProviderIDIsDashFree(t *testing.T) {
	// AHP session URIs are <provider>:/<id>; VS Code parses the scheme, so a dash in the provider breaks them.
	if pi.Provider == "" || strings.Contains(pi.Provider, "-") {
		t.Fatalf("provider id %q must be non-empty and dash-free", pi.Provider)
	}
	if got := pi.BuildAgentInfo(nil).Provider; got != pi.Provider {
		t.Fatalf("the agent advertises provider %q, not %q", got, pi.Provider)
	}
}
