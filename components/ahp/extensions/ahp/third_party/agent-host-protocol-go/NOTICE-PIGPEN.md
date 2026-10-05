# Vendored: Microsoft Agent Host Protocol Go client (types and reducers)

Source: https://github.com/microsoft/agent-host-protocol, path `clients/go` (module
`github.com/microsoft/agent-host-protocol/clients/go`), protocol and module version 0.9.0, pinned at
commit `296b25e7b698a4a84a0ee5a28d9573e70048a0bf`. That commit is **not** the `v0.9.0` tag: the tag is a
different commit (`6070633`) whose files differ from these, so the pin is the commit, and `0.9.0` names the
protocol and module version (the `v0.9.0` in `go.mod` is only the label the `replace` directive stands in for).
License: MIT, Copyright (c) Microsoft
Corporation (see `LICENSE`).

`ahp/*.go` and `ahptypes/*.go` are copied **byte for byte**, tests excluded. Two files are
Pigpen's: this notice and `go.mod`, which declares the same module path with no
requirements (upstream's `go.mod` requires `github.com/coder/websocket` for the `ahpws`
transport, which is not vendored and which the extension does not use). The extension
selects this directory with a `replace` directive.

Used for the wire types and the pure reducers only. The host, the transport and the Pi
mapping are Pigpen's own code.
