package a2aext

import (
	"net/http"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// Optional capabilities. Every one of them is off by default, advertised false on the Agent Card and refused when a
// caller asks for it. Each is a tracked follow-up (port/PORT.md, "Capabilities"): the seam below is where its
// implementation plugs in, so adding one later changes no caller.
//
// A capability is switched on in a2a.json (`"capabilities": {"pushNotifications": true}`). Switching one on while
// no implementation is registered is a configuration error, reported when the configuration is loaded with the
// exact sentence `@pi-in-go/pigpen-a2a does not support <feature> yet.`, never at call time.

// capSpec names one optional capability.
type capSpec struct {
	// key is its name in the capabilities object of a2a.json and in the registry.
	key string
	// feature completes the refusal sentence.
	feature string
	on      func(CapabilitiesConfig) bool
}

// capSpecs is every optional capability this adapter knows. None has an implementation yet.
var capSpecs = []capSpec{
	{"grpc", "the gRPC binding", func(c CapabilitiesConfig) bool { return c.GRPC }},
	{"rest", "the REST (HTTP+JSON) binding", func(c CapabilitiesConfig) bool { return c.REST }},
	{"pushNotifications", "push notifications", func(c CapabilitiesConfig) bool { return c.PushNotifications }},
	{"protocol03", "A2A protocol version 0.3", func(c CapabilitiesConfig) bool { return c.Protocol03 }},
	{"extendedAgentCard", "an extended agent card", func(c CapabilitiesConfig) bool { return c.ExtendedAgentCard }},
	{"urlFileParts", "URL file parts", func(c CapabilitiesConfig) bool { return c.URLFileParts }},
	{"oauth2", "OAuth2 authentication", func(c CapabilitiesConfig) bool { return c.OAuth2 }},
	{"mtls", "mutual TLS authentication", func(c CapabilitiesConfig) bool { return c.MTLS }},
	{"signedCards", "signed agent cards", func(c CapabilitiesConfig) bool { return c.SignedCards }},
}

// CapabilitiesConfig is the capabilities object of a2a.json. Every field defaults to false.
type CapabilitiesConfig struct {
	GRPC              bool `json:"grpc"`
	REST              bool `json:"rest"`
	PushNotifications bool `json:"pushNotifications"`
	Protocol03        bool `json:"protocol03"`
	ExtendedAgentCard bool `json:"extendedAgentCard"`
	URLFileParts      bool `json:"urlFileParts"`
	OAuth2            bool `json:"oauth2"`
	MTLS              bool `json:"mtls"`
	SignedCards       bool `json:"signedCards"`
}

// Capability is an implementation of one optional capability.
type Capability interface {
	// Key is the capability's key in capSpecs.
	Key() string
	// Advertise sets what the capability adds to the Agent Card (a capability flag, an interface, a security
	// scheme, signatures).
	Advertise(card *a2a.AgentCard)
	// Mount serves the capability: a binding's routes, a webhook sender's callbacks. h is the server's request
	// handler, behind authentication and the version guard.
	Mount(mux *http.ServeMux, h a2asrv.RequestHandler) error
}

var (
	capMu       sync.Mutex
	capRegistry = map[string]Capability{}
)

// registerCapability makes an implementation available to servers built afterwards. It returns the function that
// removes it (tests).
func registerCapability(c Capability) (remove func()) {
	capMu.Lock()
	capRegistry[c.Key()] = c
	capMu.Unlock()
	return func() {
		capMu.Lock()
		delete(capRegistry, c.Key())
		capMu.Unlock()
	}
}

// resolveCapabilities returns the implementation of every capability the configuration switches on, or the
// refusal for the first one that has none.
func (c CapabilitiesConfig) resolve() ([]Capability, error) {
	capMu.Lock()
	defer capMu.Unlock()
	var out []Capability
	for _, spec := range capSpecs {
		if !spec.on(c) {
			continue
		}
		impl := capRegistry[spec.key]
		if impl == nil {
			return nil, refuse(nil, spec.feature, "")
		}
		out = append(out, impl)
	}
	return out, nil
}

// enabledSet names the capabilities the configuration switches on.
func (c CapabilitiesConfig) enabledSet() map[string]bool {
	set := map[string]bool{}
	for _, spec := range capSpecs {
		if spec.on(c) {
			set[spec.key] = true
		}
	}
	return set
}
