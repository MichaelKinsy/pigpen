# PiG 0.4.1: what this library relies on

The enterprise-profile plan asks the first change to confirm two PiG behaviours against 0.4.1. This page records them, read
from the PiG source at tag `v0.4.1` (commit `3ee745c8cda3c1a9a8d71112c140790c4d64d78d`). Paths are in that tree.

## `auth.json` entry shapes

`ai/auth.go:47-57` documents the `"type"` discriminator, which matches upstream Pi's `auth-storage`:

* `"api_key"` with the field `"key"` (`Credential.Key`, line 77).
* `"oauth"` with `"access"`, `"refresh"` and `"expires"` (`Credential.Access`, `Refresh`, `Expires`, lines 87-91). `expires` is
  milliseconds since the epoch; PiG keeps Pi's exact value, so it may carry a fraction or be absent (lines 89-90).
* `"api"` is the old, pre-parity discriminator, rewritten on read (lines 59-61). **This library does not accept it.**

The file is a JSON object keyed by provider. Both shapes in the plan are confirmed. Consequences in `credfile`:

* `refresh` is never decoded, so it can never be used; the host rotates the file.
* An absent or non-numeric `expires` on an `oauth` entry is `credential_malformed` (the credential cannot be judged), and a
  fraction is read as a number of milliseconds.
* PiG resolves an `api_key` `key` that starts with `!` by running a command, and may resolve other forms from the
  environment. This library does neither: the key is the literal text, and a leading `!` is `credential_malformed`.

## RPC mode and dialogs

* `cmd/pig/rpc_mode.go:272-306` creates the RPC UI context (`newRPCUIContext(writeRPC)`) and binds it to the host.
* `coding/extension/host/subprocess/ui_bridge.go:483` reports `HasUI: uiCtx != nil && uiCtx != extension.NoopUIContext`, so it
  is `true` in RPC mode. `extensions/sdk/context.go:1772-1780` documents it: "Print and JSON modes have no UI; interactive
  and RPC modes do."
* A dialog (`Select`, `Confirm`, `Input`, `Editor`) writes an `extension_ui_request` to the client and **waits for a response**
  (`cmd/pig/rpc_ui.go:58-117`). It returns only on a response, a cancellation, the context, or the dialog's own timeout, which
  resolves as a cancelled dialog (a `false` confirm). A host that never answers leaves the call blocked until then.

So `HasUI()` is true on a host that never answers, as the plan feared. Under the `headless` flag a Package therefore
treats `HasUI` as false (`headless.UI`), takes its no-UI path, and where none exists returns `ui_unavailable`
(`headless.Guard`). Nothing in this library answers a dialog.
