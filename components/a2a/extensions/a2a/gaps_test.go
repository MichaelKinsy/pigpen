package a2aext

import "testing"

// Every gap between the A2A 1.0 specification / a2a-go and this adapter is a named, skipped
// test (Skill step 3: "document every gap as a named skipped test, never silently").
// port/PORT.md lists them with the count.

func TestGap_GRPCTransportBinding(t *testing.T) {
	t.Skip("gap: only the JSON-RPC binding is served. a2a-go has gRPC and REST bindings; each is another authenticated surface to secure and test. Owner decision to add.")
}

func TestGap_RESTTransportBinding(t *testing.T) {
	t.Skip("gap: the HTTP+JSON (REST) binding is not served, only JSON-RPC. See GRPC gap.")
}

func TestGap_PushNotifications(t *testing.T) {
	t.Skip("gap: push notification configs are not supported (the card says pushNotifications=false). Webhook delivery needs SSRF policy for peer-supplied URLs.")
}

func TestGap_ProtocolV03Compatibility(t *testing.T) {
	t.Skip("gap: A2A 0.3 peers are refused with VERSION_NOT_SUPPORTED (server) and no 0.3 client transport is enabled. a2a-go ships a2acompat/a2av0; enabling it needs an owner decision because 0.3 has different task-state and auth semantics.")
}

func TestGap_ExtendedAgentCard(t *testing.T) {
	t.Skip("gap: GetExtendedAgentCard is not supported; the card is the same for every caller.")
}

func TestGap_PersistentTaskStore(t *testing.T) {
	t.Skip("gap: the task store is in memory. Tasks and their status are lost when the listener restarts, although each context's PiG session file persists on disk and a new task in the same contextId continues it.")
}

func TestGap_InputRequiredFromPiG(t *testing.T) {
	t.Skip("gap: a PiG turn never ends in input-required; the worker runs headless and extension UI requests are not surfaced to the peer. Peers see completed, failed or canceled.")
}

func TestGap_FileAndDataParts(t *testing.T) {
	t.Skip("gap: only text parts are accepted and produced. File, raw and data parts are refused, not dropped.")
}

func TestGap_OAuth2AndMutualTLSAuthentication(t *testing.T) {
	t.Skip("gap: server authentication is static bearer tokens named by environment variable, mapped to a principal and tenant. OAuth2/OIDC validation and mTLS client certificates are not implemented; the client sends a bearer token or custom headers only.")
}

func TestGap_SignedAgentCards(t *testing.T) {
	t.Skip("gap: agent card signatures are neither produced nor verified.")
}

func TestGap_SubscribeToTaskAfterRestart(t *testing.T) {
	t.Skip("gap: SubscribeToTask works only while the task is live in this process (see the persistent task store gap).")
}

func TestGap_ClientStreamingProgressToTheModel(t *testing.T) {
	t.Skip("gap: a2a_send waits for a terminal or input-required state and reports progress through the tool update channel only; there is no long-poll or resubscribe tool for tasks that outlive the call.")
}

func TestGap_ClientResubscribeTool(t *testing.T) {
	t.Skip("gap: the client tools do not resubscribe to a remote task's stream; a2a_task get/cancel and a2a_send (which streams) cover the model's needs. The server side does support SubscribeToTask for a live task (TestSubscribeToALiveTaskDeliversTheRest).")
}
