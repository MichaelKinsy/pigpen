# a2a: port record

**Kind of port (Skill, "What kind of port is this?"): 3, new code with no original.** a2a is an *original adapter* on the upstream
Go SDK [a2a-go](https://github.com/a2aproject/a2a-go) v2.6.0 (commit `ebf17c56ef7e63c72883a45454a538bbc0df66b8`, Apache-2.0). There
is no TypeScript or Pi original to twin, so there is **no Pi oracle and no equivalence claim**. The golden traces in `golden/` are
**self-recorded** regression baselines (lane `pig-go-self`): the port was recorded from itself and reviewed by hand, and `pigeq check`
labels them that way. What replaces the oracle for protocol facts is a2a-go's own contract plus an independent A2A peer (below).

## Identities

| Item | Value |
|---|---|
| Package | `components/a2a` (module `github.com/MichaelKinsy/pigpen/a2a`, Go extension `a2a`), Piglet `piglets/a2a` |
| Upstream SDK | `github.com/a2aproject/a2a-go/v2` **v2.6.0**, A2A protocol **1.0** (JSON-RPC binding), Apache-2.0, `port/a2a-go-LICENSE` |
| Host | PiG 0.3.0 (commit `63c6ba456`), upstream Pi 0.87.1, go1.27.1 |
| Interop peer | kagent upstream `main` at `e4516302` (its Go ADK server package `go/adk/pkg/a2a/server`, which pins a2a-go v2.6.0) |
| Gap check | `pig docs list` / `pig docs show built-in-extensions` in 0.3.0: PiG ships no A2A. The older port map lists A2A remote sessions as "not ported (spec 482)". `pigeq gaps --go`: PASS (`port-gaps`), public SDK only, no process-global calls |

## Gap check against the SDK surface

Everything the extension uses is fully provided by the 0.3.0 Go SDK: `sdk.New`, `RegisterTool`, `RegisterCommand`, `RegisterFlag`,
`OnEvent("session_start"|"session_shutdown")`, `Context.Notify`, `sdk.NewToolError`. No `PARTIAL` or `MISSING` (`pigeq gaps --go`).
Fused-extension rule found in `pig docs show extensions`: a fused extension shares PiG's process, so a2a discards the HTTP server's and
a2a-go's logs (`ErrorLog` and slog to `io.Discard`, `RecentLogs` ring instead) rather than writing to PiG's terminal.

## Contract

Every behaviour is a row; each has a test and, where a mutation applies, a named mutant that the tests kill (`mutations.json`).

| # | Contract | Tests | Mutants |
|---|---|---|---|
| C01 | Listener is off unless configured; installing opens no port | `TestListenerIsOffUnlessConfigured`, `TestNoConfigurationOpensNoListener`, scenario `listener-off` | `listener-on-by-default` |
| C02 | Config: `a2a.json` in the agent dir, `PIG_A2A_CONFIG`, env `PIG_A2A_LISTEN`, flag `--a2a-listen` (flag > env > file); strict JSON | `TestConfigFileEnablesListener`, `TestPrecedenceFlagOverEnvOverFile`, `TestConfigPathFromEnv`, `TestConfigIsReadFromTheAgentDirectory`, `TestFlagOverridesTheConfigurationFile` | precedence, agent-dir, flag mutants |
| C03 | Secrets are named by env var, never inline; never echoed | `TestInlineSecretsAreRejected`, `TestTokenEnvMustBeSetAndIsNotEchoed`, `TestRemoteAuthFailureDoesNotEchoTheToken` | inline-secret, echo mutants |
| C04 | A listener needs authentication; `insecureNoAuth` only on loopback; not together with tokens; tokens ≥16 chars, unique names and values | `TestListenerNeedsAuthentication`, `TestInsecureNoAuthOnlyOnLoopback`, `TestTokenConfigValidation`, `TestTwoVariablesWithOneValueAreRejected`, `TestInsecureNoAuthWithTokensIsAmbiguous`, scenario `flag-without-auth-refused` | auth/loopback/duplicate mutants |
| C05 | Bearer tokens compared by digest, constant time; token in a query string ignored; 401 + `WWW-Authenticate: Bearer`; agent card public | `TestAuthenticateBearer`, `TestAuthenticateRejects`, `TestTokenInQueryStringIsIgnored`, `TestWrapRejectsWith401AndChallenge`, `TestWrapPassesPrincipalAndPublicPaths`, `TestUnauthenticatedRequestIsRejectedBeforeTheWorker` | compare, query, 401 mutants |
| C06 | Protocol pinned to A2A 1.0: `A2A-Version: 1.0` required (an absent header, which the specification reads as 0.3, is refused), other versions refused, repeated `1.0, 1.0` accepted (kagent's client), `1.0, 0.3` refused | `TestProtocolVersionIsPinned`, `TestMixedVersionHeaderIsRefused`, `TestAgentCardIsPublicAndPinnedToProtocol10` | version mutants |
| C07 | Agent card: name, Bearer scheme, streaming true, push false, one 1.0 JSON-RPC interface, external URL | `TestAgentCardIsPublicAndPinnedToProtocol10`, `TestAgentCardAdvertisesExternalURL` | card mutants |
| C08 | Task identity: one message is one task on one worker turn; result is a completed task with the reply as artifact | `TestSendMessageRunsATurnAndReturnsACompletedTask`, `TestE2E_TaskRunsRealPiGWithAToolCall` | `worker-keeps-extensions`, `worker-not-marked`, `tool-updates-dropped` |
| C09 | Context identity: a `contextId` is a PiG session; the same id continues it, across tasks | `TestSameContextIDContinuesTheSameWorkerContext`, `TestE2E_ContextContinuesAcrossTasksAndTenantsAreIsolated`, `TestSessionIDIsStableScopedAndSafe` | session-id mutants |
| C10 | Tenant isolation: contexts, tasks and sessions are scoped to the caller's principal; another caller cannot read, attach to or subscribe to a task | `TestTenantsDoNotShareContextsOrTasks`, `TestBobCannotAttachToAliceTaskWithAMessage`, `TestSubscribeToALiveTaskDeliversTheRest`, `TestPrincipalKeysIsolate`, `TestExecutorRefusesAForgedUser` | `principal-not-checked`, key mutants |
| C11 | A request's tenant must match the token's | `TestRequestTenantMustMatchTheTokensTenant` | `tenant-not-enforced` |
| C12 | Cancellation aborts the worker (RPC abort, grace, SIGTERM, SIGKILL of the process group), ends `canceled`, reaps the child | `TestCancelTaskAbortsTheWorkerAndEndsCanceled`, `TestWorkerCancelAbortsThenReaps`, `TestWorkerKillsAChildThatIgnoresAbort`, `TestWorkerAsksForTermBeforeKill`, `TestWorkerEscalatesToKillWhenTermIsIgnored`, `TestE2E_CancelAbortsRealPiG` | `no-terminate`, `no-kill-after-grace`, `abort-not-sent`, `cancel-does-not-stop-worker` |
| C13 | Streaming: artifact chunks arrive before completion; streamed text equals the final text | `TestStreamingDeliversArtifactChunksBeforeCompletion`, `TestWorkerStreamsUpdatesAndFinalText…` | stream mutants |
| C14 | Failures end `failed` without provider detail sent to the peer | `TestWorkerFailureBecomesAFailedTask`, `TestWorkerErrorDetailIsNotSentToThePeer`, `TestModelFailureMessageIsExactlyGeneric`, `TestWorkerModelFailureIsAFailureWithoutProviderDetail` | `model-error-text-copied` |
| C15 | Limits: concurrent tasks, one task at a time per context, task timeout, prompt 256 KiB, body 1 MiB | `TestConcurrencyLimitQueuesTasks`, `TestTasksInOneContextRunOneAtATime`, `TestTaskTimeoutFailsTheTask`, `TestOversizedPromptIsRefusedBelowTheBodyLimit`, `TestBodyLimitAppliesEvenWithASmallPrompt`, `TestOversizedRequestBodyIsRefused` | limit mutants |
| C16 | Only text parts; empty message refused; attachments refused, not dropped | `TestNonTextPartsAreRejected`, `TestEmptyMessageIsRejected`, `TestTextPlusFilePartIsRefusedNotTrimmed` | `file-parts-dropped` |
| C17 | Worker has no tools by default (`--no-tools`; PiG's file tools take absolute paths, so even `read` reaches every file the account can), configured tools are the allowlist, no extensions, no context files, own session dir, allowlisted env; the worker never opens a listener | `TestWorkerDefaultsHaveNoTools`, `TestReview_WorkerHasNoToolsUnlessConfigured`, `TestE2E_DefaultWorkerCannotReadFilesOutsideItsWorkspace`, `TestWorkerPassesFlagsSessionAndCwd`, `TestWorkerEnvironmentIsAllowlisted`, `TestWorkerProcessDoesNotOpenAListener`, `TestWorkerChildNeverListens` | tool/env mutants |
| C18 | A turn that needs interactive input or a rejected prompt fails, never hangs | `TestWorkerFailsATurnThatNeedsInteractiveInput`, `TestWorkerReportsARejectedPromptWithoutTheReason` | `ui-request-hangs`, `rejected-prompt-ignored` |
| C27 | A cancel that arrives before the worker starts (queued behind the concurrency limit, or behind another task of the same context) ends the task `canceled`, never starts a worker, and leaves the task ahead untouched | `TestCancelBeforeTheWorkerStartsBehindTheConcurrencyLimit`, `TestCancelBeforeTheWorkerStartsBehindTheSameContext` | `queued-cancel-ignored-at-slot`, `queued-cancel-ignored-at-context-lock` |
| C28 | An `insecureNoAuth` listener answers only a loopback `Host` (DNS rebinding); a token listener serves any `Host` (proxies) | `TestReview_InsecureLoopbackListenerRefusesAForeignHostHeader`, `TestReview_TokenListenerServesAnyHostHeader` | `insecure-host-unchecked` |
| C29 | The per-context lock is per principal (two tenants with one `contextId` do not queue behind each other); a process a turn left behind is killed | `TestReview_SameContextIDInTwoTenantsRunsConcurrently`, `TestReview_WorkerKillsWhatATurnLeftBehind` | `context-lock-shared-across-tenants`, `left-behind-child-survives` |
| C19 | Shutdown cancels running tasks, waits, and closes the listener; a second start and a busy port are errors; TLS load failure is an error | `TestShutdownCancelsRunningTasksAndStopsListening`, `TestStartFailsOnAnAddressInUse`, `TestSecondStartIsAnError`, `TestShutdownClosesThePortEvenRightAfterStart`, `TestTLSListener`, `TestUnreadableTLSKeyPairFailsStart` | lifecycle mutants |
| C20 | Client: named remotes, card resolution, direct endpoint (`skipCard`), pinned 1.0 JSON-RPC interface only, `A2A-Version: 1.0` sent | `TestCardResolvesAndSummarises`, `TestSkipCardUsesTheEndpointDirectly`, `TestCardWithoutAProtocol10InterfaceIsRefused`, `TestSendReusesContextAndSendsProtocolVersion` | client mutants |
| C21 | Client credentials go only to the configured origin, scheme and host (transport check and card pinning, two independent defences; https is never downgraded to http); redirects are not followed | `TestReview_CredentialsAreNotDowngradedToPlainHTTP`, `TestCredentialsAreNotSentToAHostTheCardNamed`, `TestCredentialTransportRefusesAnotherHost`, `TestRemoteRedirectsAreNotFollowed`, `TestInterop_KagentCardHostIsNotTrustedWithCredentials` | `transport-host-not-checked`, `transport-scheme-not-checked`, `card-scheme-downgrade-accepted`, `redirects-followed-with-credentials` |
| C22 | Client: cancelling the call (or its timeout) cancels the remote task; states failed/input-required reported; terminal task reported | `TestCancellingTheCallCancelsTheRemoteTask`, `TestSendHonoursTheConfiguredTimeout`, `TestFailedAndInputRequiredStates`, `TestSummaryOfAnInputRequiredTaskIsNotTerminal`, `TestSendToATerminalTaskStillReportsItsState`, `TestGetAndCancelTask` | client-cancel mutants |
| C23 | Tools `a2a_agents`, `a2a_send`, `a2a_task`, command `/a2a`; argument errors name the argument; no remotes says how to configure | `TestRegistersToolsCommandAndLifecycleHandlers`, `TestToolsRejectBadArguments`, `TestToolsWithoutRemotesSayWhy`, `TestSendToolPassesTheContextIDThrough`, `TestStatusListsRemotes`, scenarios `tools-visible-to-model`, `send-missing-message`, `send-without-remotes`, `task-unknown-action` | tool mutants |
| C24 | Lifecycle: new/resume/fork keep the listener, other reasons stop it; reload re-reads the configuration; invalid config is reported, not fatal | `TestSessionSwitchKeepsTheListenerReloadRestartsIt`, `TestReloadAppliesChangedConfiguration`, `TestToolAfterShutdownUsesTheRewrittenConfiguration`, `TestInvalidConfigurationIsReportedNotFatal`, `TestListenFailureIsReportedToTheUser` | lifecycle mutants |
| C26 | The Piglet Binary hosts the listener itself: configured, it serves the card publicly, refuses unauthenticated calls, runs a real worker (the same Binary) on a local model, isolates tenants; unconfigured or flag-only it stays off | `TestBinary_*` (3, `PIG_A2A_BINARY`) | (external) |
| C25 | Interop with kagent's A2A server package (as a remote for PiG) and a kagent-shaped client (a2a-go v2, repeated version header) against PiG's server | `TestInterop_*` (4), `scripts/interop-kagent.mjs` | (external) |

## Named gaps (skipped tests, `gaps_test.go`)

Each is a `t.Skip` with a reason; none is silent. 13 gaps: `TestGap_GRPCTransportBinding`, `TestGap_RESTTransportBinding`, `TestGap_PushNotifications`,
`TestGap_ProtocolV03Compatibility`, `TestGap_ExtendedAgentCard`, `TestGap_PersistentTaskStore`, `TestGap_InputRequiredFromPiG`,
`TestGap_FileAndDataParts`, `TestGap_OAuth2AndMutualTLSAuthentication`, `TestGap_SignedAgentCards`, `TestGap_SubscribeToTaskAfterRestart`,
`TestGap_ClientStreamingProgressToTheModel`, `TestGap_ClientResubscribeTool`. Conditional skips (not gaps), each with a named reason: three kagent tests unless `KAGENT_A2A_ECHO` is set (`scripts/interop-kagent.mjs`), four e2e tests unless `PIG_A2A_E2E_BIN` is set, four Binary tests unless `PIG_A2A_BINARY` is set.

## Deviations from the Skill (recorded, not hidden)

- **Red-green.** RED commit `16ddb5c` (74 tests fail on a signature stub; `red.txt`), then GREEN. Two RED tests were fixed in GREEN because the *test double* was wrong, not
  the expectation: the fake remote lacked an authenticated user for a2a-go's task store, and one cancel test raced the stream's first event (it now waits for it). Tests were added
  after the first mutation run (survivors below); they only add assertions.
- **No twins.** There is no upstream suite; a2a-go's conformance behaviour is exercised through its own client against the server, and the fake remote (an independent
  `a2asrv` server) against the client.
- **Golden traces** are self-recorded (`pig-go-self`), six scenarios, hand reviewed.

## Host findings (for the porter and PiG)

- The fake-host template's `Command` sent the wrong request shape (fixed in the porter branch); a tool's own argument check runs, since the host does not validate schemas in the fake.
- A stale `go.mod` passes workspace tests but fails PiG's packed build (missing `go.sum` entries): always `go mod tidy` with the full source, then build a Binary.
- kagent's ADK server hard-codes its readiness port `:8081`; kagent's client repeats the `A2A-Version` header (`1.0, 1.0`); a2a-go's in-memory task store lists tasks only for an authenticated user.
- **a2a-go v2.6.0 defect (reported here, not fixed upstream):** `SubscribeToTask` on a *live* task attaches to the task's event queue without an ownership check
  (`a2asrv/handler.go`, `execManager.Resubscribe` before `taskStore.Get`), so any authenticated caller who knows a task id can read another tenant's live stream. `GetTask`,
  `CancelTask` and `ListTasks` are masked. The port owns the task store and checks ownership in its call interceptor (`guard`); `TestSubscribeToALiveTaskDeliversTheRest`
  and mutant `subscribe-ownership-unchecked` pin it. Found only by writing the roadmap's "stream resubscription" item with a second principal.
- a2a-go's client ends the event stream with an error on cancel, so a "cancel after clean end" branch would be dead code (removed).
- `go.work` written by `pigeq` uses `go 1.26`, which is below `go 1.26.0`; needed a local fix.
- PiG 0.3.0's `read`, `grep`, `find` and `ls` resolve absolute paths and `~` outside the working directory
  (`internal/codingagent/tools/path_utils.go` `resolveToCwd`), so a tool allowlist is not a file-access boundary. The worker
  therefore defaults to `--no-tools`; a PiG option that confines file tools to the working directory would let a served
  worker have read tools safely.

## What PiG 0.4.0 (Pi 0.99.1) will change

Not built or run against 0.4.0. Expected: the SDK module path and `sdk.New` surface are the public API this port already uses, so a rebuild is the test; the worker's RPC
protocol (`--mode rpc` JSONL: `prompt`, `abort`, `message_update`, `agent_end`, `extension_ui_request`) and flags (`--session-dir`, `--session-id`, `--tools`, `--no-*`) are read by
`worker.go` and are the surface to re-check. The kagent interop does not depend on a PiG version (it speaks A2A only).

## Proof results

Host: PiG 0.3.0+0.87.1 built from `63c6ba456`; Piglet Binary `piglets/a2a` built with `pig piglet build --format binary` from the staged tree
(58.9 MB, fused, a2a-go linked; the Binary is not committed). Lane build: sha256 `17eff7afd824d1b5…`; review rebuild after the fixes: sha256 `8e0f00bd44bbdab7…`.

| Layer | Result |
|---|---|
| Unit tests, `go test -race -count=3` | 122 pass, 24 skip (13 named gaps, 11 conditional), 0 fail (review re-run; the lane's run was 116 pass, 22 skip); also `GOMAXPROCS=4 taskset -c 0-3 -race -count=24`: pass (a first 24-run found a real race: `Server.Shutdown` could return before the Serve goroutine had closed the port; fixed, pinned by `TestShutdownClosesThePortEvenRightAfterStart`); `-count=8` under 6 CPU burners: pass |
| Real PiG worker end to end (`PIG_A2A_E2E_BIN`) | 4 pass with `pig` 0.3.0 and with the Binary: tool call (tools named), context continuity, tenant isolation, cancel reaps the child, and a worker configured only by `a2a.json` cannot read a file outside its workspace (this last test failed on the lane's code: the default `read` tool returned the file to the peer) |
| The Binary hosts the listener (`PIG_A2A_BINARY`) | 4 pass: configured (card public, 401, task on a real worker that is the same Binary, the tools named in `a2a.json` and no others, tenant isolation), off without configuration, flag without tokens refused, and its default worker cannot read outside its workspace (fails against the lane's Binary, passes against the review's) |
| kagent interop (`KAGENT_A2A_ECHO`, `scripts/interop-kagent.mjs`, kagent upstream `e4516302`) | 4 pass: kagent's own A2A server package as a remote for PiG (send, stream, cancel; credentials not sent to the card's host), a kagent-shaped client (a2a-go v2, repeated version header) against PiG's server. **No live kagent cluster**: `docker` and `kubectl` exist here, `kind` does not |
| Host scenarios, `pigeq check` (self-recorded, `pig-go-self`) | 6 pass + `port-gaps` + `exec-coverage` |
| Mutation (`mutation-run.txt`) | **109 mutants, 109 killed, 0 survived** (review re-run with an exact find/replace runner; the review replaced `default-tools-not-read-only` with `worker-gets-pig-default-tools` and added six: `insecure-host-unchecked`, `transport-scheme-not-checked`, `card-scheme-downgrade-accepted`, `context-lock-shared-across-tenants` and `left-behind-child-survives`, the last two surviving the lane's tests). Lane's run, `pigeq mutate --unit-only --jobs 4`: 104/104. First run: 65 killed of 102; after adding tests (`survivors_test.go` and additions) and fixing 6 mutants that did not compile: 96/100; the last 5 (plus one new mutant for the SubscribeToTask defect) killed. One mutant judged equivalent and dropped (`cancel-does-not-wait`: a2a-go's cancel already waits for the executor). Two dropped as dead code (`cancel-after-stream-end-missed`) or equivalent (`shutdown-leaves-workers`) |
| `go vet` | linux, windows, darwin |
| Repository | `npm run generate`/`check`, `npm test` (62), `npm run validate`, `npm run test:go-ports -- -race -count=3`: run against the porter-driver's scripts (this branch keeps the shared scripts at base, so merge the porter-driver branch first), all pass. `pig install components/a2a/extensions/a2a --validate-only`: packable |

### Mutants (109)

`listener-starts-without-config`, `worker-child-may-listen`, `flag-not-highest-precedence`, `env-not-over-file`, `unknown-keys-and-inline-secrets-accepted`, `listener-without-tokens-allowed`, `insecure-no-auth-off-loopback`, `insecure-and-tokens-both`, `short-tokens-allowed`, `duplicate-token-values-allowed`, `duplicate-token-names-allowed`, `tenant-not-validated`, `missing-token-env-allowed`, `listen-not-host-port`, `worker-gets-pig-default-tools`, `remote-url-scheme-unchecked`, `remote-name-unchecked`, `basic-scheme-accepted`, `extra-fields-accepted`, `token-comparison-inverted`, `insecure-flag-ignored`, `tenant-key-uses-token-name`, `key-namespaces-collide`, `everything-public`, `no-bearer-challenge`, `empty-authenticator-allowed`, `file-parts-dropped`, `empty-prompt-accepted`, `prompt-size-unbounded`, `timeout-never-fires`, `worker-error-detail-leaks`, `context-not-serialised`, `concurrency-limit-ignored`, `cancel-does-not-stop-worker`, `failure-reason-dropped`, `principal-not-checked`, `version-not-enforced`, `absent-version-accepted`, `minor-versions-accepted`, `mixed-version-values-accepted`, `tenant-not-enforced`, `user-not-attached`, `body-size-unbounded`, `card-requires-auth`, `card-drops-streaming`, `card-claims-push`, `card-ignores-external-url`, `card-without-security-requirement`, `plain-http-on-tls`, `second-start-allowed`, `worker-keeps-extensions`, `worker-not-marked`, `worker-inherits-secrets`, `passenv-ignored`, `session-id-ignores-principal`, `session-id-ignores-context`, `session-dir-shared`, `worker-cwd-not-set`, `tools-allowlist-dropped`, `session-not-kept`, `abort-not-sent`, `no-kill-after-grace`, `no-terminate`, `model-error-text-copied`, `model-error-not-a-failure`, `streamed-and-final-text-duplicated`, `empty-prompt-sent`, `tool-updates-dropped`, `ui-request-hangs`, `rejected-prompt-ignored`, `any-protocol-version-accepted`, `credentials-follow-the-card`, `transport-host-not-checked`, `redirects-followed-with-credentials`, `bearer-not-sent`, `missing-bearer-env-tolerated`, `custom-headers-dropped`, `remote-not-cancelled-on-abort`, `timeout-ignored`, `unknown-agent-unnamed`, `names-unsorted`, `empty-message-sent`, `input-required-called-terminal`, `failure-text-hidden`, `auth-error-shows-token-context`, `switch-drops-listener`, `shutdown-keeps-listener`, `config-error-silent`, `start-failure-silent`, `flag-not-read`, `agent-dir-ignored`, `stop-does-not-reload-config`, `status-hides-remotes`, `tool-required-not-enforced`, `tool-empty-message-allowed`, `tool-errors-not-errors`, `unfinished-task-not-flagged`, `task-action-unchecked`, `send-drops-context`, `tls-load-failure-ignored`, `subscribe-ownership-unchecked`, `queued-cancel-ignored-at-slot`, `queued-cancel-ignored-at-context-lock`, `shutdown-does-not-close-the-port`, `insecure-host-unchecked`, `transport-scheme-not-checked`, `card-scheme-downgrade-accepted`, `context-lock-shared-across-tenants`, `left-behind-child-survives`


## Re-verified on PiG 0.4.1 (porter-verify)

The golden traces were recorded again from the port itself (`--self`, a regression baseline), with PiG 0.4.1 content (`5f948f86a`, `pig --version`
`0.3.1+1.0.1`) and normalizer v3, because Pi 1.0.x changed the trace format: a `prompt` response now carries
`data.disposition` (Pi 0.99.0, #9098). An event-by-event diff against the previous traces shows no other
difference, and `pigeq check` passes on the Go port. Details: `docs/plan/progress/porter-verify.md`.
