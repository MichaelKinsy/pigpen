# pi-typesafe-api (typed Go API)

The library half of the [pi-typesafe](../pi-typesafe/README.md) port: what an extension author imports to ask Jev typed
questions safely. Go module `github.com/MichaelKinsy/pigpen/components/pi-typesafe-api`, package `pitypesafe`, over the shared
client `components/typesafe`. A consumer extension's `go.mod` requires it at `v0.0.0` (no `replace`) and its directory has a
`go.work` with `use ( . ../../../pi-typesafe-api ../../../typesafe )`.

```go
client, err := pitypesafe.New(pitypesafe.Options{MaxRequests: 5, MaxUSDPerDay: 1}) // key: TYPESAFE_API_KEY, else the login store
answer := pitypesafe.Ask(ctx, client, typesafe.SystemOneRequest{
	State: typesafe.Text("I was charged twice."),
	Questions: typesafe.Questions{typesafe.Ask("billing", typesafe.Noul("Is this about billing?"))},
}, pitypesafe.AskOptions{Timeout: 5 * time.Second})
if !answer.OK { /* answer.ErrorCode == pitypesafe.CodeBudget: stop asking; never an error value */ }
```

| Piece | Where |
|---|---|
| Admission (`PrepareEvaluationRequest`: normalize model near-misses, validate, byte budget), `EvaluationSchema` | `schema.go`, `evaluation_schema.json` |
| Client with request budget, day caps, ledger, auth recording (`New`, `Evaluate`, `EvaluateRaw`, `ListModels`, `GetSpend`) | `client.go`, `usage.go`, `auth.go` |
| Backends: `typesafe`, `openrouter`, `commandcode`, a caller-supplied endpoint, and `ownmodel` | `backends.go` |
| Key store, key situations, `AgentDir` (PiG's directory) | `credentials.go` |
| `Ask`, `EvaluateMany`, `EvaluateAll`, `ChunkRequest`, `FanOut` | `ask.go`, `batch.go` |
| Calibration kit (`Calibrate`, `Replay`, `AUC`, ...) | `calibrate.go` |
| Own model on the PiG SDK's model access | `hostmodel/` |
| Hidden-input key prompt, `LoginWithPrompt`, `EnsureAPIKey` | `ui/` |

**Where content goes.** TypeSafe, OpenRouter, Command Code and custom-endpoint backends send it to that host
(`BackendHost` names it for consent text); the `ownmodel` backend sends it to the provider of the model PiG is configured with. A
key for one backend is never sent to another. Credit and license: [CREDITS.md](CREDITS.md). Mapping: [port/PORT.md](port/PORT.md).
