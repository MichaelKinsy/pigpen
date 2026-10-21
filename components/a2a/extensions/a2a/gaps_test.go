package a2aext

import "testing"

// Every gap between the A2A 1.0 specification / a2a-go and this adapter is a named, skipped test (Skill step 3:
// "document every gap as a named skipped test, never silently"). port/PORT.md lists them with the count and, for
// each, who decides: an owner decision (recommendation recorded), or a PiG SDK capability (core-items). A gap
// that a caller can reach is refused with `@pi-in-go/pigpen-a2a does not support <feature> yet.` (refusal_test.go),
// advertised false on the agent card, and never accepted and ignored.

func TestGap_GRPCTransportBinding(t *testing.T) {
	t.Skip("tracked follow-up (opt-in seam in capability.go, off by default): only the JSON-RPC binding is served and advertised. a2a-go has a gRPC binding; it is another authenticated surface to secure and test. Not served (TestRESTBindingIsNotServed covers the HTTP side). Recommendation: do not add until a peer needs it.")
}

func TestGap_RESTTransportBinding(t *testing.T) {
	t.Skip("tracked follow-up (opt-in seam in capability.go, off by default): the HTTP+JSON (REST) binding is not served, only JSON-RPC (TestRESTBindingIsNotServed). Recommendation: same as gRPC.")
}

func TestGap_PushNotifications(t *testing.T) {
	t.Skip("tracked follow-up (opt-in seam in capability.go, off by default): push notifications are refused with the exact sentence (TestPushNotificationMethodsAreRefusedWithTheExactSentence, TestSendMessageWithAPushConfigIsRefusedNotIgnored) and the card says pushNotifications=false. A security design (SSRF policy for peer-supplied webhook URLs, an allowlist of origins in a2a.json with no default, webhook credentials, ownership of push configs) is required before enabling.")
}

func TestGap_ProtocolV03Compatibility(t *testing.T) {
	t.Skip("tracked follow-up (opt-in seam in capability.go, off by default): A2A 0.3 requests, and requests without an A2A-Version header, are refused with the exact sentence (TestOldProtocolVersionsAreRefusedWithTheExactSentence); the client uses 1.0 interfaces only. a2a-go ships a2acompat/a2av0, but 0.3 has other task-state and auth semantics. Recommendation: serve 1.0 only; kagent speaks 1.0.")
}

func TestGap_ExtendedAgentCard(t *testing.T) {
	t.Skip("tracked follow-up (opt-in seam in capability.go, off by default): GetExtendedAgentCard is refused with the exact sentence (TestExtendedAgentCardIsRefusedWithTheExactSentence) and the card says extendedAgentCard=false. Nothing in this listener differs by caller, so there is nothing to put in it. Recommendation: leave refused.")
}

func TestGap_URLFileParts(t *testing.T) {
	t.Skip("tracked follow-up (opt-in seam in capability.go, off by default): a file part that names a URL is refused (TestServerRefusesAURLPartWithoutRunningTheWorker); the worker would have to fetch a peer-supplied URL, an SSRF decision. Raw and data parts are served. Recommendation: leave refused; the peer can send the bytes.")
}

func TestGap_InputRequiredFromPiG(t *testing.T) {
	t.Skip("needs-SDK: a PiG turn never ends in input-required; the worker runs headless and fails a turn that needs an extension UI request (C18). Surfacing the request as input-required needs the host to expose Pi's extension_ui_request / extension_ui_response over a program-driven session to a Go extension. See status/a2a.md and core-items.md.")
}

func TestGap_WorkerFileToolsAreNotConfined(t *testing.T) {
	t.Skip("needs-SDK/host: the worker has no tools unless worker.tools names some, because PiG's file tools take absolute paths and ~ outside the working directory (C17). Safe read tools for served workers need a host option that confines file tools to the working directory. See status/a2a.md and core-items.md.")
}

func TestGap_OAuth2AndMutualTLSAuthentication(t *testing.T) {
	t.Skip("tracked follow-up (opt-in seam in capability.go, off by default): server authentication is static bearer tokens named by environment variable, mapped to a principal and tenant; an unknown configuration key is an error, so nothing is silently ignored. OAuth2/OIDC validation and mTLS client certificates are not implemented; the client sends a bearer token or custom headers only. A security design (token validation and key rotation for OAuth2; client-certificate trust roots for mTLS; mapping both to a principal and tenant) is required before enabling; an authenticating proxy covers it meanwhile.")
}

func TestGap_SignedAgentCards(t *testing.T) {
	t.Skip("tracked follow-up (opt-in seam in capability.go, off by default): agent card signatures are neither produced nor verified, and the card says so by carrying none (TestCardAdvertisesWhatTheListenerDoesNotOfferAsFalse). A security design (signing key storage and rotation, what a verifier trusts) is required before enabling; credentials are pinned to the configured origin instead of trusting a card.")
}
