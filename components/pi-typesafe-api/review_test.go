package pitypesafe

import "testing"

// Cases added by the review (rev-pigpen-typesafe).

// Plain http: is allowed only for a loopback address. The original's WHATWG URL parser rejects a dotted
// address with an octet above 255 (new URL throws), so 127.999.0.1 never passes its 127.0.0.0/8 rule; Go's
// url.Parse accepts it as a host name, which the resolver would then look up in DNS and reach over plain
// http: with the endpoint's key.
func TestLoopbackHTTPNeedsARealLoopbackAddress(t *testing.T) {
	for _, host := range []string{"http://127.999.0.1", "http://127.0.0.256:8080"} {
		_, err := ResolveBackend(map[string]any{"label": "Local", "host": host, "keyEnv": "GATEWAY_JEV_KEY"})
		if !hasCode(err, CodeConfiguration) || err.Error() != hostMessage {
			t.Errorf("%s: %v", host, err)
		}
	}
	for _, host := range []string{"http://127.0.0.1:8787", "http://127.255.255.254", "http://localhost:1", "http://[::1]:9"} {
		if _, err := ResolveBackend(map[string]any{"label": "Local", "host": host, "keyEnv": "GATEWAY_JEV_KEY"}); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
}
