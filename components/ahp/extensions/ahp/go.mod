module github.com/MichaelKinsy/pigpen/ahp

go 1.26

// PiG resolves this requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1

// The Microsoft Agent Host Protocol Go types and reducers (MIT), vendored byte for byte.
// See third_party/agent-host-protocol-go/NOTICE-PIGPEN.md.
require github.com/microsoft/agent-host-protocol/clients/go v0.9.0

replace github.com/microsoft/agent-host-protocol/clients/go => ./third_party/agent-host-protocol-go
