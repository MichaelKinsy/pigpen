package a2aext

// Capabilities the listener does not offer are advertised false and refused with one exact sentence, never silently
// accepted (PORT.md "Named gaps": push notifications, extended agent card, A2A 0.3, gRPC and REST bindings).

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const refusalPrefix = "@pi-in-go/pigpen-a2a does not support "

func fetchCard(t *testing.T, base string) *a2a.AgentCard {
	t.Helper()
	resp, err := http.Get(base + "/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var card a2a.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	return &card
}

func rpcError(t *testing.T, out map[string]any) (int, string) {
	t.Helper()
	e, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error response, got %v", out)
	}
	code, _ := e["code"].(float64)
	msg, _ := e["message"].(string)
	return int(code), msg
}

func TestCardAdvertisesWhatTheListenerDoesNotOfferAsFalse(t *testing.T) {
	s := startServer(t, serverConfig(), &scriptedWorker{})
	card := fetchCard(t, "http://"+s.Addr())
	if card.Capabilities.PushNotifications || card.Capabilities.ExtendedAgentCard {
		t.Fatalf("capabilities %+v", card.Capabilities)
	}
	if !card.Capabilities.Streaming {
		t.Fatal("streaming is offered")
	}
	if len(card.SupportedInterfaces) != 1 || card.SupportedInterfaces[0].ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Fatalf("only the JSON-RPC binding is advertised: %+v", card.SupportedInterfaces)
	}
	if len(card.Signatures) != 0 {
		t.Fatalf("the card is not signed: %+v", card.Signatures)
	}
}

func TestPushNotificationMethodsAreRefusedWithTheExactSentence(t *testing.T) {
	s := startServer(t, serverConfig(), &scriptedWorker{})
	base := "http://" + s.Addr()
	for method, params := range map[string]any{
		"CreateTaskPushNotificationConfig": map[string]any{"taskId": "t1", "url": "https://hook.example.test/a2a"},
		"GetTaskPushNotificationConfig":    map[string]any{"taskId": "t1", "id": "p1"},
		"ListTaskPushNotificationConfigs":  map[string]any{"taskId": "t1"},
		"DeleteTaskPushNotificationConfig": map[string]any{"taskId": "t1", "id": "p1"},
	} {
		_, out := rawRPC(t, base, tokenA, "1.0", method, params)
		code, msg := rpcError(t, out)
		if code != -32003 || !strings.HasPrefix(msg, refusalPrefix+"push notifications yet.") {
			t.Errorf("%s: code %d message %q", method, code, msg)
		}
	}
}

func TestSendMessageWithAPushConfigIsRefusedNotIgnored(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	params := map[string]any{
		"message":       map[string]any{"messageId": "m1", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "hello there"}}},
		"configuration": map[string]any{"taskPushNotificationConfig": map[string]any{"url": "https://hook.example.test/a2a"}},
	}
	for _, method := range []string{"SendMessage", "SendStreamingMessage"} {
		status, out := rawRPC(t, "http://"+s.Addr(), tokenA, "1.0", method, params)
		_ = status
		code, msg := rpcError(t, out)
		if code != -32003 || !strings.HasPrefix(msg, refusalPrefix+"push notifications yet.") {
			t.Errorf("%s: code %d message %q", method, code, msg)
		}
	}
	if len(w.Turns()) != 0 {
		t.Fatal("a task ran although its push configuration was refused")
	}
}

func TestExtendedAgentCardIsRefusedWithTheExactSentence(t *testing.T) {
	s := startServer(t, serverConfig(), &scriptedWorker{})
	_, out := rawRPC(t, "http://"+s.Addr(), tokenA, "1.0", "GetExtendedAgentCard", map[string]any{})
	code, msg := rpcError(t, out)
	if code != -32007 || !strings.HasPrefix(msg, refusalPrefix+"an extended agent card yet.") {
		t.Fatalf("code %d message %q", code, msg)
	}
}

func TestOldProtocolVersionsAreRefusedWithTheExactSentence(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	params := map[string]any{"message": map[string]any{"messageId": "m1", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "hello there"}}}}
	for version, feature := range map[string]string{
		"0.3": `A2A protocol version 0.3 yet.`,
		"":    `requests without an A2A-Version header (protocol 0.3) yet.`,
	} {
		_, out := rawRPC(t, "http://"+s.Addr(), tokenA, version, "SendMessage", params)
		code, msg := rpcError(t, out)
		if code != -32009 || !strings.HasPrefix(msg, refusalPrefix+feature) || !strings.Contains(msg, "1.0") {
			t.Errorf("version %q: code %d message %q", version, code, msg)
		}
	}
	if len(w.Turns()) != 0 {
		t.Fatal("a worker ran for a refused version")
	}
}

func TestRESTBindingIsNotServed(t *testing.T) {
	w := &scriptedWorker{}
	s := startServer(t, serverConfig(), w)
	for _, path := range []string{"/message:send", "/v1/message:send", "/tasks", "/extendedAgentCard"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req, _ := http.NewRequest(method, "http://"+s.Addr()+path, strings.NewReader(`{"message":{"messageId":"m","role":"ROLE_USER","parts":[{"text":"hi there"}]}}`))
			req.Header.Set("Authorization", "Bearer "+tokenA)
			req.Header.Set("A2A-Version", "1.0")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status %d", method, path, resp.StatusCode)
			}
		}
	}
	if len(w.Turns()) != 0 {
		t.Fatal("a REST call ran a task")
	}
}
