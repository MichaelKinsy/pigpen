package host_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/protocol-surface.test.ts: the AHP 0.9 command surface this product
// exposes. Upstream makes the policy table exhaustive at compile time over the client library's
// CommandMap, so an AHP upgrade forces an explicit decision; here the same table is checked against
// the vendored spec's command list in TestCommandPolicyCoversTheSpec (an addition, not a twin).

// rootMethods are the connection-level commands routed on the root channel.
var rootMethods = []string{
	"ping", "createResourceWatch", "listSessions", "resourceRead", "resourceWrite", "resourceList",
	"resourceCopy", "resourceDelete", "resourceMove", "resourceResolve", "resourceMkdir", "resourceRequest",
	"resolveSessionConfig", "sessionConfigCompletions",
}

// unsupportedRequests are commands this host deliberately does not implement.
var unsupportedRequests = map[string]map[string]any{
	"createChat":                       {"channel": "ahp-session:/session", "chat": "ahp-chat:/chat"},
	"disposeChat":                      {"channel": "ahp-chat:/chat"},
	"authenticate":                     {"channel": wire.RootChannel, "resource": "https://example.com", "token": "opaque"},
	"invokeChangesetOperation":         {"channel": "ahp-changeset:/changes", "operationId": "apply"},
	"listAutomationTriggerDefinitions": {"channel": wire.RootChannel},
	"runAutomation":                    {"channel": "ahp-automations://", "automation": "ahp-automation:/job", "requestId": "request"},
	"fetchAutomationRuns":              {"channel": "ahp-automations://", "automation": "ahp-automation:/job"},
}

func TestAHPCommandSurface(t *testing.T) {
	h := testkit.NewHost(host.Options{})
	client := testkit.Connect(t, h)
	t.Cleanup(client.Close)
	client.Initialize("surface-client", nil)

	twin.Run(t, "protocol-surface", "returns MethodNotFound for every deliberately unsupported command", func(t *testing.T) {
		for method, params := range unsupportedRequests {
			client.ExpectError(method, params, wire.CodeMethodNotFound)
		}
	})

	twin.Run(t, "protocol-surface", "requires the root routing channel on every connection-level command", func(t *testing.T) {
		wrongChannel := "ahp-session:/wrong-channel"
		for _, method := range rootMethods {
			client.ExpectError(method, map[string]any{"channel": wrongChannel}, wire.CodeInvalidParams)
		}
		initializing := testkit.Connect(t, h)
		t.Cleanup(initializing.Close)
		initializing.ExpectError("initialize", map[string]any{"channel": wrongChannel, "clientId": "wrong-channel-client", "protocolVersions": testkit.SupportedVersions()}, wire.CodeInvalidParams)
		reconnecting := testkit.Connect(t, h)
		t.Cleanup(reconnecting.Close)
		reconnecting.ExpectError("reconnect", map[string]any{"channel": wrongChannel, "clientId": "wrong-channel-reconnect", "lastSeenServerSeq": 0, "subscriptions": []string{}}, wire.CodeInvalidParams)
	})
}

// TestCommandPolicyCoversTheSpec is the port's counterpart of upstream's compile-time exhaustiveness:
// every command of the vendored spec must be an explicit decision, implemented or refused.
func TestCommandPolicyCoversTheSpec(t *testing.T) {
	raw, err := os.ReadFile("../testkit/schema/commands.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	implemented := map[string]bool{
		"initialize": true, "ping": true, "reconnect": true, "subscribe": true, "createSession": true, "disposeSession": true,
		"createTerminal": true, "disposeTerminal": true, "createResourceWatch": true, "listSessions": true,
		"resourceRead": true, "resourceWrite": true, "resourceList": true, "resourceCopy": true, "resourceDelete": true,
		"resourceMove": true, "resourceResolve": true, "resourceMkdir": true, "resourceRequest": true, "fetchTurns": true,
		"resolveSessionConfig": true, "sessionConfigCompletions": true, "completions": true,
	}
	// unsubscribe and dispatchAction are notifications, not commands.
	notCommands := map[string]bool{"BaseParams": true, "PaginatedParams": true, "UnsubscribeParams": true, "DispatchActionParams": true}
	for def := range schema.Defs {
		if !strings.HasSuffix(def, "Params") || notCommands[def] {
			continue
		}
		method := strings.ToLower(def[:1]) + strings.TrimSuffix(def[1:], "Params")
		if _, refused := unsupportedRequests[method]; !implemented[method] && !refused {
			t.Errorf("spec command %q has no explicit policy: implement it or refuse it in unsupportedRequests", method)
		}
	}
}
