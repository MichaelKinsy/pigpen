# Port record: ahp

(This directory is `proof/`, not the Skill's usual `port/`: the repository's port-layout test and
`test:port` treat every `port/` as a scenario/golden-trace port recorded with `pigeq`, which needs the
original to be a Pi extension. pi-ahp is a standalone host, so its proof is the twin gate, the real-pig
scenarios and the mutation list `proof/mutations.json`.)

| Input | Identity |
|---|---|
| Original | pi-ahp (`src/`, `test/`), https://github.com/Qusic/pi-ahp, commit `4065e98309b03c1d2d916300856c3c21a9c10674`, MIT (Bang Lee) |
| Protocol | Microsoft Agent Host Protocol 0.9.0, https://github.com/microsoft/agent-host-protocol, commit `296b25e7b698a4a84a0ee5a28d9573e70048a0bf`, MIT. Types and reducers vendored byte for byte (`extensions/ahp/third_party/agent-host-protocol-go`) |
| Oracle | pi-ahp under Node 24 with Pi 0.87.1: 420 of its 430 leaf cases pass here; the 10 `resource-watch` "lifetime" cases time out in this environment (they mock `setTimeout`) and are red upstream too |
| Target | PiG 0.3.0 + Pi 0.87.1 (a pre-release build, `63c6ba456`), go1.27.1, public Go SDK only |

## What kind of port this is

pi-ahp is not a Pi extension: it is a standalone Node host that embeds Pi (`createAgentSession`) and
runs many sessions itself. The Go port is an **extension inside one PiG session**, so the
protocol host, the mapping from Pi events to AHP actions, the catalogue and the services are ported
faithfully, and the agent side is an adapter over the Go SDK that serves exactly the running
session. The equivalence method is therefore three layers, not the scenario harness alone:

1. **Twins.** Every one of the 430 upstream test cases has a Go test named after it
   (`internal/twin`; the gate `extensions/ahp/twins_test.go` fails when a case has neither a test nor
   a named skip). **375 run, 55 are named skips** (below). Upstream inputs and expectations are kept;
   where the Go form had to differ the test says why.
2. **Real host.** `extensions/ahp/realpig_test.go` builds the extension with a real `pig`, serves a real PiG
   session backed by a scripted local model (`tools/fakellm`), and drives it with a WebSocket AHP
   client: token and Origin refusal, nothing exposed by default, catalogue, remote turn, local
   prompt mirrored, opt-in filesystem and terminal, resumed session history, remote cancel,
   non-loopback without token, listener off unless asked, session replacement. The same eight
   scenarios pass against a Piglet Binary with the extension fused in (`PIGPEN_AHP_FUSED=1`).
3. **Mutation.** `proof/mutations.json` (run with `pigeq mutate --unit-only`) lists mutants of the
   security and reconciliation checks. Each one is killed by a unit test. **No protocol differential
   against pi-ahp under Pi, and no interop run with Microsoft's Go client, has been done.** Both are
   open.

## Mapping

| Upstream | Go | Twins |
|---|---|---|
| `src/protocol/*`, `core/channels.ts`, `core/uri.ts` | `internal/wire` | handshake, uri, protocol-surface |
| `core/host.ts`, `sequencer.ts`, `client-workarounds.ts` | `internal/host` (+ `internal/ws`, an RFC 6455 server on `net/http` Hijack) | handshake 14, subscriptions 9, reconnect 9, client-workarounds 14, schema 4, upstream-workarounds 2 |
| `pi/event-mapper.ts`, `activity.ts`, `user-message.ts`, `message-input.ts`, `session-title.ts` | `internal/mapper` | event-mapper 27, activity 14, message-input 12, mapper-fixtures 25 |
| `channels/*` | `internal/channels` | session-summary 13 |
| `pi/chat-driver.ts` | `internal/pi/chatdriver.go` | chat-driver 13 |
| `pi/session-registry.ts`, `session-catalogue.ts`, `session-hydrator.ts`, `history.ts`, `turn-paging`, `delete-session.ts` | `internal/pi` | session-lifecycle 17, disposal 6, client-actions 6, catalogue 11, hydration 8, hydrated lifecycle 9, fetch-turns 4, truncate 5, delete-session 6, active-turn-reconnect 3, session-storage 1, reconnect-after-restart 1 |
| `pi/models.ts`, `session-config.ts`, `completions.ts`, `project-trust.ts` | `internal/pi` | models 10, session-config 8, completions 22, project-trust 6 |
| Pi `SessionManager` (JSONL v3) | `internal/pisession` | used by the above |
| `pi/resource-*.ts` | `internal/svc` (`resource.go`, `watch.go`, `glob.go`, `watchpolicy.go`) | resource 23, resource-watch 28, policy 9, watch-events 4 |
| `host/terminal-service.ts` | `internal/svc` (`terminal.go`, `pty*.go`) | terminal-service 14, pty 2 |
| `pi/image-input.ts` | `internal/pi/imageinput.go` | image-input 2 |
| `host/pi-host.ts` | `internal/compose` | pi-host 1 |
| `host/direct-settings.ts` | `internal/settings` | direct-settings 3 |
| `pi/in-process-backend.ts` | `internal/live` (adapter over the SDK; not a port) | real-pig scenarios |
| (none) | root package: flags, `/ahp`, listener lifecycle | real-pig scenarios |

## Named skipped twins (55)

| Family | Count | Why |
|---|---|---|
| `tunnel` | 20 | The dev tunnel (Microsoft `devtunnel` CLI management) is not bundled: owner decision, ACP, AHP and A2A are separate extensions |
| `pi-replay` | 15 | Replays provider streams through a live embedded Pi agent loop. Covered by the recorded-stream twins (`mapper-fixtures`) and the real-pig scenarios |
| `changeset`, `changeset-lifecycle`, `changeset-uri` | 15 | The git changeset service (`git-changes.ts`, `changeset-service.ts`) is not ported in this release; the host advertises no changeset channels. Documented gap, candidate for the next release |
| `image-session` | 1 | Needs Pi's `InProcessPiBackend` and a faux model |
| `model-discovery` | 2 | Needs an embedded Pi model registry with extension providers; the adapter reads models through the SDK |
| `live-turn` | 1 | Upstream skips it itself without a real model |
| `project-trust` | 1 | "applies the decision before pi loads project extensions": the extension cannot gate what PiG loaded |

## Deviations from upstream (deliberate)

1. **One session.** Only the running PiG session has an agent. Other sessions in the catalogue are
   read from their files (browse, history); starting a turn on one fails with a clear error.
   Clients cannot create sessions, and `chat/truncated`, fork and tree navigation are refused: the
   SDK offers `NewSession`, `Fork` and `NavigateTree` to commands only.
2. **Session replacement.** Contexts retained from `session_start` belong to their runtime. On
   `new`, `resume`, `fork` and `reload`, PiG emits `session_shutdown` and runs the extension factory
   again, so the listener is stopped. With `--ahp` the new session's `session_start` opens it again
   on the same port. After `/ahp start` it stays closed, and a warning tells the user
   (`TestRealPigSessionReplacement`, review fix).
3. **Nothing on by default.** Upstream's standalone process is the listener; the extension opens none
   without `--ahp` or `/ahp start`. Upstream's resource service is unrestricted without roots; here
   the filesystem is off, then confined to the working directory, and only `unrestricted: true`
   lifts that. Terminals and session deletion are opt-in settings. Non-loopback needs a token.
4. **Foreign turns.** A prompt typed into the session locally is mirrored into the chat
   (`AdoptForeignTurns`); upstream owns its agent and never sees one.
5. **Steering.** PiG has no `queue_update` event; the adapter synthesises it from the delivered user
   message so pending-steering reconciliation works.
6. **Newer spec, older client library.** The pinned spec has `chat/isArchivedChanged` and
   `session/mcpServerBackgroundRequested`, which npm 0.9.0 lacks; both are refused and the
   client-actions twin expects that. The spec has no `Conflict` error code; -32011 is kept from npm 0.9.0.
7. **Polling watches.** No portable file notification exists in the standard library, so a resource
   watch diffs a snapshot (default every second, excluded directories pruned) where chokidar uses
   native events. Batching, filters, grace and disposal are upstream's.
8. **PTY.** A stdlib-only PTY (`/dev/ptmx` + ioctl) on linux and darwin. Windows has none; the terminal
   service refuses to start there. A signalled child reports `128+signal`.
9. **Images.** Pi's `convertToPng`/`resizeImage` are not in the SDK: PNG, JPEG, GIF decode with the
   standard library, BMP with a small decoder, resizing is a box filter, WebP passes through unvalidated
   beyond its header. PNG, JPEG and GIF declaring more than 64 megapixels are refused from their header,
   before decoding (Go's decoders allocate the whole image from the header). A
   panic while a prompt or steering message is handed to the agent fails that turn or message.
10. **MIME table** is the subset of the `mime` package that matters for source trees.
11. **Async side effects.** Registry and driver effects run on goroutines where upstream runs them in
    the same tick; a few twins wait for the backend call before the next step. `host.drain` defers
    re-entrant listener callbacks; the driver serialises events (`evMu`).
12. **JS artefacts.** Echoed tool arguments have alphabetical key order (events arrive decoded); UTF-16
    cuts at a lone surrogate yield U+FFFD; `localeCompare` is approximated (`localeLess`); terminal trimming counts runes.
13. **Vendored client library.** Its reconnect results marshal without the `type` discriminator, so the
    host has its own structs; its `SnapshotState` cannot decode a terminal snapshot (tests read the raw result).
14. **Tests written with the code.** From the third layer on, tests and implementation were committed
    together (failing runs observed before fixes) rather than as a separate red commit.
15. **403 for a bad token.** A missing or wrong `token`/`tkn` is refused at the upgrade with 403 Forbidden, as VS Code's
    agent host (the AHP reference host, `src/vs/platform/agentHost/node/webSocketTransport.ts`) answers a bad `tkn`; pi-ahp
    writes 401. The AHP spec leaves endpoint access to the transport, upstream's twin only asserts the refusal, and no
    client tells the two apart (a browser WebSocket reports either as one error); a 401 would also owe a `WWW-Authenticate`
    challenge that a query token has no scheme for. A browser `Origin` that is not allowed is 403 as before.

## Build notes

- A fused Piglet Binary ignores `replace` directives of the extension's `go.mod`; a `go.work` beside it
  (`use . ./third_party/agent-host-protocol-go`) is how the vendored module is found.
- PiG 0.4.0 (Pi 0.99.1): re-check `session_start` retained contexts across session replacement, whether
  `Fork`/`NewSession` become callable from events, any `queue_update` event, and the SDK's image helpers.
