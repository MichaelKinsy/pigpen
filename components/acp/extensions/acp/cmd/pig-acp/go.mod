module github.com/MichaelKinsy/pigpen/acp/cmd/pig-acp

go 1.26

// The companion executable talks to `pig --mode rpc` over a pipe and to the editor over
// stdio. It imports no PiG package (public or internal) and no third-party module.
