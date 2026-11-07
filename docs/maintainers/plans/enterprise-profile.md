# Enterprise profile plan

Status: plan, step 1. No code in this change. The owner reviews this document before any Package changes.

## Goal

Pigpen Packages run unchanged on a developer machine. A headless hosted runtime needs more. This plan adds an opt-in
enterprise profile: six flags, one shared Go library, one new `otel` Package, and a release manifest per Package.

With every flag off and no profile file, each Package behaves exactly like its latest 0.1.x release. A flag that is on and
misconfigured fails closed with a typed error.

## Hosting requirements

The plan targets a generic downstream host with these properties.

- The host runs PiG 0.4.1 as `pig --mode rpc`, with one executable extension per `-e`, a Piglet path, offline mode, and a
  writable per-session agent directory.
- The host drains stderr. It treats a prompt response as admission only. It cannot answer a dialog.
- The root filesystem is read-only and the process is not root. No Go toolchain, Node, or shell exists at run time.
- The network is closed except for destinations the operator lists (a proxy, an identity provider, a collector, model and
  search APIs). Those destinations often have private addresses.
- Extensions are static binaries built with `CGO_ENABLED=0 go build -trimpath`. `pig install` cannot build from source here.
- Only the agent directory and the session directory are writable. The home directory is never written.
- The shared agent directory is read-only and reached through symlinks.
- Credentials arrive as a PiG `auth.json`-shaped file. The host rotates the file between turns by an atomic rename.

Consequences for every Package:

- No work at start-up or load that needs the network, the home directory, or a dialog.
- Every file read follows symlinks and tolerates a read-only target.
- Every failure is a typed, non-secret message on a channel the host can read (tool result, audit event, or stderr line).

## Flags

All flags default to off. The profile does not change any default.

| Flag | Contract |
|---|---|
| `credentialFile` | Read a credential from a named file on every outbound request. Keep nothing between requests. Accept the PiG `auth.json` entry shapes `{"type":"oauth","access":"...","expires":<ms>}` and `{"type":"api_key","key":"..."}` under a provider key (confirm both shapes against PiG 0.4.1 auth storage in PR 1). Never use or refresh a `refresh` value; the host rotates the file. Attach the credential only to the configured origin. A missing, unreadable, malformed, or expired credential fails the request. |
| `egressPolicy` | Allow only listed destinations (exact host or `.suffix` on a label boundary). Check every IP address at connect time, inside the dialer, on the address actually dialled. Deny non-public addresses unless an operator CIDR list exempts them. Use only the proxy and CA bundle the profile names. Ignore any proxy that a tool argument or another config file supplies. Make no network call at start-up or load. |
| `audit` | Emit structured lifecycle events with a closed field set: event, Package, tool, outcome, duration, reason code. Each value comes from a closed set or a validated identifier. Never emit tokens, claims, prompts, tool arguments, or paths, unless a separate named opt-in lists them. |
| `resourceServer` | a2a only. Admit a task only with a valid JWT access token. Verify the signature against a JWKS with a pinned algorithm list. Match `iss` and `aud` exactly. Check `exp` and `nbf` with a bounded leeway. Fail closed when no usable key is cached and the JWKS is unreachable. Map the subject and tenant claims to the a2a principal. Never log a token or claim. |
| `policyFailClosed` | permissions and warden deny every call when their policy cannot be read, parsed, or evaluated. warden and jev block a judged call when the judge is unavailable. Give a typed, non-secret reason. Deny wins over allow. |
| `headless` | Never call a dialog. Take the Package's existing no-UI path in its blocking variant. Fail fast with a typed error where no such path exists. Honour cancellation. |

### Config surface

Each Package reads one profile file, `<agent dir>/pigpen-enterprise/<package>.json`. The agent directory is
`PIG_CODING_AGENT_DIR`, else `$PIG_HOME/agent`. The profile never falls back to Pi directories, and never reads a file from
the working directory or a project `.pig/` directory.

A separate file leaves the original config schemas untouched (permissions and a2a reject unknown keys). A separate
directory avoids a name clash: websearch (`web-search.json`), jev (`pi-jev.json`), and a2a (`a2a.json`) all keep their
config in the agent directory itself, so one `enterprise.json` "next to the existing config" would be shared by three
Packages.

The environment variable `PIGPEN_<PACKAGE>_ENTERPRISE` names another file and overrides the default path. Each flag also
has an environment variable of the form `PIGPEN_<PACKAGE>_ENTERPRISE_<FLAG>`. An environment variable can turn a flag on.
It cannot turn off a flag that the file turns on.

```json
{
  "headless": { "enabled": true },
  "audit": { "enabled": true, "sink": "stderr" },
  "policyFailClosed": { "enabled": true }
}
```

A flag key that is absent means off. A file that exists and does not parse is `profile_misconfigured`, because the Package
cannot tell which flags were meant. An unknown key is `profile_misconfigured`. The Package reports its profile state in
`/doctor` (pig-doctor) and in its own status command: each flag, on or off, whether the file was found, and the last error
code. The report never prints a secret or a path.

## Flag matrix per Package

`yes` means the flag applies. `n/a` means the flag has no effect, with the reason. Each entry was checked against the
Package source at commit `86a2cff`.

| Package | credentialFile | egressPolicy | audit | resourceServer | policyFailClosed | headless |
|---|---|---|---|---|---|---|
| permissions | n/a: no outbound request | n/a: no network | yes | n/a: not a server | yes: the Cedar path; see note | n/a: the port has no dialog; `ask` already blocks the call |
| websearch | yes: search provider keys | yes: provider API calls and content fetches; see note | yes | n/a: not a server | n/a: no policy file; the SSRF guard is always on and stays on | yes: the stored-results command calls `Select` |
| a2a | yes: remote agent credentials (today `bearerTokenEnv` and `headerEnv`) | yes: remote agent calls, agent card fetch, JWKS fetch | yes | yes: replaces static `tokens` when on | n/a: no policy file | n/a: no dialog found on the task path; re-check at PR time |
| warden | yes: TypeSafe backend key only | yes: TypeSafe backend traffic only | yes | n/a: not a server | yes: guard patterns, config, and `failOpen` | yes: tool-call confirm, setup confirm and select |
| jev | yes: TypeSafe backend key only | yes: TypeSafe backend traffic only | yes | n/a: not a server | yes: an unavailable judge blocks in enforce mode | yes: gate confirm and the enable confirm |
| typesafe | hook: a per-request credential source | hook: the existing `HTTPClient` field | n/a: a library; its callers emit events | n/a | n/a | n/a: no UI |
| session-ingest | n/a: reads local files | n/a: no network | yes | n/a | n/a: no policy | n/a: no dialog |

Notes:

- permissions: a config that cannot be read or fails the schema already becomes an empty config. The default `*` rule is then
  `ask`, and the port blocks every `ask`. So the legacy path already fails closed. `policyFailClosed` adds the Cedar path, a
  typed reason, and an audit event. Today's warning text includes the config path; the profile replaces it with a code.
- websearch: it already has a domain policy, an `ssrf.allowRanges` exemption list, a `trustEnvProxy` switch, and a dial-time
  address check on content fetches. Its provider API client has no address check. Its tools take a `proxy` argument, so the
  model can choose a proxy, including `socks5h`. Under `egressPolicy`, the tool argument and the `proxy` key in
  `web-search.json` are ignored, `trustEnvProxy` has no effect, and the profile's proxy and CIDR list apply to both clients.
- warden and jev: each has two backends. The own-model backend calls the model through PiG's provider layer
  (`typesafe/libraries/pigmodel`). Its credentials and egress belong to PiG, not to the Package. The flags cover the
  TypeSafe backend only.
- warden: `failOpen` defaults to true. Under `policyFailClosed`, `failOpen` is forced to false.
- jev: an unavailable judge fails open today, with no setting to change it. Under `policyFailClosed` in enforce mode, an
  unavailable judge blocks the call. Under `headless`, a flagged call blocks (the `gate.blockWithoutUI` path).
- headless: PiG's RPC mode can forward dialogs to the client, as Pi's RPC mode does, so `HasUI()` can be true on a host that
  never answers. Confirm the 0.4.1 behaviour in PR 1. Under `headless`, a Package treats `HasUI()` as false.
- typesafe is a library with no extension. `Config.HTTPClient` already injects a transport. `Config.APIKey` is a static
  string, so a per-request credential source is new. typesafe has no profile file.
- a2a: task workers run as `pig --mode rpc --no-extensions` children. permissions and warden do not guard a worker's tool
  calls, and the profile does not reach into the worker. See open question 13.
- session-ingest gets `audit` only. It reads any path the model names, so the audit rule (no paths) matters for it. See open
  question 10.
- Each Package states in its README which flags it supports and links this plan.

## Failure behaviour

Every failure returns a typed error with a closed `Code`, the `Flag`, the `Package`, and a non-secret `Reason`.
No message contains a credential, token, claim, prompt, argument, or path unless the named opt-in lists it.

| Flag | Condition | Behaviour | Code |
|---|---|---|---|
| any | profile file exists but does not parse, or has an unknown key | The Package refuses to load. | `profile_misconfigured` |
| credentialFile | file missing or unreadable | The request fails. No fallback to an environment key. | `credential_unavailable` |
| credentialFile | not a regular file after symlinks (FIFO, device, directory), or larger than the cap (64 KiB) | The request fails. | `credential_unavailable` |
| credentialFile | file malformed, no entry for the provider, or an unknown `type` | The request fails. | `credential_malformed` |
| credentialFile | `oauth` entry with `expires` at or before now plus the margin (default 30 s) | The request fails. | `credential_expired` |
| credentialFile | on, but no file name or provider is configured | The Package refuses to load. | `profile_misconfigured` |
| egressPolicy | destination host not listed, on the first request or any redirect | The request fails before the dial. | `egress_denied` |
| egressPolicy | a dialled address is non-public and outside the operator CIDR list | The dial fails in the dialer control hook. | `egress_address_denied` |
| egressPolicy | a dialled address is link-local or a cloud metadata address | The dial fails, even inside the CIDR list. | `egress_address_denied` |
| egressPolicy | proxy or CA bundle unreadable or invalid | The Package refuses to load. | `profile_misconfigured` |
| egressPolicy | a tool argument or another config file names a proxy | The value is ignored and an audit event records the code. | `egress_proxy_ignored` |
| audit | sink unwritable | The Package keeps working. It writes at most one `audit_unavailable` line to stderr per minute. With `audit.required`, the call fails instead. | `audit_unavailable` |
| resourceServer | no token, bad signature, wrong `iss` or `aud`, expired, not yet valid | The task is rejected (401 with `WWW-Authenticate: Bearer`) and never starts. | `token_invalid` |
| resourceServer | `alg` is `none`, HS*, or outside the configured list, or does not match the key | The task is rejected (401). | `token_alg_denied` |
| resourceServer | subject or tenant claim missing, or fails the a2a identifier rule `^[A-Za-z0-9._-]{1,64}$` | The task is rejected (401). | `token_invalid` |
| resourceServer | JWKS unreachable and no cached key is within its lifetime | The task is rejected (503). | `jwks_unavailable` |
| policyFailClosed | policy file missing, unreadable, or fails to parse | Every call is denied. | `policy_unavailable` |
| policyFailClosed | any policy returns an evaluation error | The call is denied. | `policy_error` |
| policyFailClosed | a forbid matches | The call is denied, whatever permits match. | `policy_forbidden` |
| policyFailClosed | the judge is unavailable (warden, jev enforce) | The call is denied. | `judge_unavailable` |
| headless | a dialog would be needed and no blocking no-UI path exists | The request fails at once. | `ui_unavailable` |
| headless | the context is cancelled | The call returns the cancellation error and takes no action. | `cancelled` |

Rules that hold for all flags:

- A flag that is on and misconfigured never degrades to the flag-off behaviour.
- Typed errors are values in the shared library, matched with `errors.Is` or `errors.As`.
- A tool result that carries a typed error uses a fixed text per `Code`. The text never echoes input.
- No flag does network work at start-up or load. Credential, CA, and JWKS reads happen on first use.

### Security details per flag

credentialFile:

- One `open` per request, then `fstat` on the descriptor, then a bounded read. No `stat`-then-`open` sequence. Open with
  `O_NONBLOCK` so a FIFO cannot hang the read.
- Symlinks are followed, because the shared directory uses them. The check is on the opened descriptor, not the path.
- The host replaces the file by an atomic rename. A partial read is a parse error and fails the request. There is no retry.
- The `RoundTripper` binds the credential to one configured origin (scheme, host, port). It never adds the credential to a
  request for another origin, so a cross-origin redirect never carries it.
- Expiry uses the local clock. The margin makes a token count as expired slightly early, never late.

egressPolicy:

- The address check runs in `net.Dialer.ControlContext`. It sees each address the dialler tries, including every A and AAAA
  record and Happy Eyeballs fallbacks. There is no separate pre-resolve step whose answer could change before the dial.
- Addresses are parsed with `net/netip` and unmapped first (`::ffff:a.b.c.d` becomes `a.b.c.d`). NAT64 (`64:ff9b::/96`) and
  6to4 (`2002::/16`) addresses are checked on their embedded IPv4 address.
- Denied by default: `0.0.0.0/8`, `10.0.0.0/8`, `100.64.0.0/10`, `127.0.0.0/8`, `169.254.0.0/16`, `172.16.0.0/12`,
  `192.0.0.0/24`, `192.168.0.0/16`, `198.18.0.0/15`, `224.0.0.0/4`, `240.0.0.0/4`, `::/128`, `::1/128`, `fc00::/7`,
  `fe80::/10`, `ff00::/8`. `169.254.0.0/16` and `fd00:ec2::254` stay denied even inside the operator CIDR list.
- Host names are lower-cased, stripped of a trailing dot, and converted to IDNA ASCII before the allow-list match.
  `.example.com` matches `a.example.com` and not `evil-example.com` or `example.com`.
- With a proxy, the dialler connects only to the proxy. The proxy address must be listed, and it may be private if the CIDR
  list covers it. The target address is resolved by the proxy and cannot be checked locally. The target host is still checked
  against the allow-list on every request and redirect, and an IP-literal target is checked against the address rules. The
  proxy is responsible for address policy on the far side. `NO_PROXY` targets are dialled directly and get the full check.

resourceServer:

- The config names the issuer, the audience, the JWKS URL, and the allowed algorithms (default `RS256`; `ES256` optional).
  The JWKS URL is configured, never discovered from the token or from `iss`.
- The verifier selects the key by `kid`. A token without `kid` is accepted only when the JWKS has exactly one key. The key's
  `kty`, and its `use` and `alg` when present, must agree with the token's `alg`. HS* and `none` are never accepted, so a public key cannot be
  used as an HMAC secret.
- The header fields `jku`, `x5u`, `jwk`, and `x5c` are ignored. A `crit` header the verifier does not know rejects the token.
- `iss` matches the configured string exactly. `aud` matches when it equals the configured audience, or, as an array,
  contains it exactly.
- `exp` is required. `nbf` and `iat` are checked when present. The leeway defaults to 60 s and is capped at 300 s.
- The JWKS cache lifetime follows `Cache-Control: max-age`, bounded to 5 minutes to 24 hours (default 1 hour). An unknown
  `kid` triggers at most one refresh per minute. A failed refresh keeps the cached keys until their lifetime ends.
- The token size is capped (8 KiB). The subject and tenant claims become the a2a `Principal`. They do not enter the model
  context.
- `insecureNoAuth` and `resourceServer` together are `profile_misconfigured`.

audit:

- `Event`, `Outcome`, and `Reason` are named types with unexported constructors. `Emit` replaces any value outside the
  closed set with `invalid` and counts it. `Tool` must match the identifier rule, else it becomes `invalid`.
- One event is one JSON line under 4 KiB, written with one `Write` call, so lines from concurrent writers do not interleave
  on a pipe.

headless:

- permissions: no change. The port already blocks every `ask`.
- jev: a flagged call blocks, as with `gate.blockWithoutUI: true`. The enable command fails with `ui_unavailable`.
- warden: confirm mode blocks the call. The setup command fails with `ui_unavailable`.
- websearch: the stored-results command fails with `ui_unavailable`.
- The profile never answers a dialog with allow. A host that wants to allow a tool class does it in policy, not in dialog
  answers.

## Shared library API sketch

One Package, `components/hardening`, with one Go module, reached from each extension through a `go.work` `use` entry as
CONTRIBUTING.md describes (the same layout as `components/typesafe`). warden and jev already have a `go.work`. websearch,
a2a, and permissions get one in their PRs. The library uses the standard library only. `policy/cedar` is a nested module, so
a Package that does not use Cedar does not carry cedar-go in its module graph.

```go
package profile // flag loading

type Flags struct {
    CredentialFile   *CredentialFile
    EgressPolicy     *EgressPolicy
    Audit            *Audit
    ResourceServer   *ResourceServer
    PolicyFailClosed bool
    Headless         bool
}

// Load reads <agent dir>/pigpen-enterprise/<pkg>.json and the environment. It does no network work.
// A missing file means every flag is off. A file that exists and does not parse returns *Error.
func Load(pkg, agentDir string, getenv func(string) string) (Flags, error)
func (f Flags) Status() Status // for /doctor and status commands; holds no secrets and no paths

type Error struct {
    Code    Code   // closed set, see the failure table
    Flag    string
    Package string
    Reason  string // fixed text per Code; never input
}

package credfile

type Source struct{ /* path, provider, origin, margin, now func() time.Time */ }

// Token opens, checks, and parses the file on every call. It keeps nothing.
func (s Source) Token(ctx context.Context) (string, error)
func (s Source) Header(ctx context.Context) (name, value string, err error)
// RoundTripper adds the credential to requests for the bound origin only, reading the file once per request.
func (s Source) RoundTripper(next http.RoundTripper) http.RoundTripper

package egress

type Policy struct{ Allow []string /* host or .suffix */; AllowCIDRs []netip.Prefix /* proxy, CA bundle */ }

// Transport returns a transport that checks the allow-list per request, uses only the profile proxy and CA bundle,
// and rejects denied addresses in net.Dialer.ControlContext on the address actually dialled.
func (p Policy) Transport(base *http.Transport) (*http.Transport, error)
func (p Policy) Client(timeout time.Duration) (*http.Client, error) // re-checks each redirect
func (p Policy) Check(host string) error // static allow-list check, no DNS

package audit

type EventName struct{ s string } // values only from package constants
type Outcome struct{ s string }   // ok, denied, error
type Reason struct{ s string }    // a Code
type Event struct {
    Event    EventName
    Package  string
    Tool     string // optional; must match the identifier rule
    Outcome  Outcome
    Duration time.Duration
    Reason   Reason
}
type Emitter interface{ Emit(Event) }
func New(cfg Audit, w io.Writer) Emitter // JSON lines, one Write per event

package policy

type Request struct {
    Principal string         // the session or caller subject
    Action    string         // tool name
    Resource  string         // what the tool acts on, if known
    Context   map[string]any // tool arguments
}
type Decision struct { Allow bool; Reason string /* code */ ; Matched []string }
type Decider interface { Decide(ctx context.Context, r Request) (Decision, error) }
// FailClosed wraps a Decider: any error, panic, or nil decider becomes a deny.
func FailClosed(d Decider) Decider

package cedar // module components/hardening/policy/cedar; wraps github.com/cedar-policy/cedar-go
func Load(path string) (policy.Decider, error)

package headless

// Blocking reports the typed error a Package returns instead of opening a dialog.
func Blocking(pkg, what string) error
```

The `Decider` interface is the only policy contract that permissions and warden see. OPA/Rego can plug in later as
another `Decider` without a change to either Package.

## Engine and standard choices

### Tool policy: Cedar behind a `Decider`

- Engine: [cedar-go](https://github.com/cedar-policy/cedar-go), the Go implementation in the `cedar-policy` organisation.
  Apache-2.0. The latest release is v1.8.0, tagged 2026-06-01. Pin it and check its sum in the sum database in PR 1.
- Semantics fit tool calls: default deny, forbid wins over permit, no side effects in evaluation.
- Cedar is the language of [Amazon Bedrock AgentCore Policy](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/policy-understanding-cedar.html).
  That page states default deny and forbid-overrides-permit for agent tool calls. This is prior art for the exact use.
- Gaps in cedar-go, quoted from its README: "CLI applications; the schema validator (experimental support is provided in
  x/exp/schema ...); the formatter; partial evaluation; support for policy templates".
  Mitigations: run schema checks as a test-time tool only; write static policies only; do not depend on `x/exp` at run time.
- Plug-in point: Microsoft's [Agent Governance Toolkit](https://github.com/microsoft/agent-governance-toolkit/tree/main/policy-engine)
  (MIT, a Rust policy engine) reads YAML manifests and offers `rego` and `cedar` policy types behind optional features.
  The `Decider` interface follows the same split. An OPA `Decider` is future work.

Recommended Cedar model for a tool call:

| Cedar element | Value |
|---|---|
| principal | `Session::"<session id>"`, with attributes for subject and tenant when `resourceServer` supplies them |
| action | `Action::"<tool name>"` (one action per tool; an `Action` group per tool class, such as `Shell`, `FileWrite`, `Network`) |
| resource | `Target::"<normalised target>"`: the cleaned absolute file path, the URL host, or `none` |
| context | the tool arguments as a record (strings, longs, booleans, sets, records); command text under `context.command` |

Rules for the mapping:

- Evaluation is deterministic and has no side effects.
- A tool without a matching permit is denied. A matching forbid always denies.
- Cedar skips a policy whose evaluation errors, so an erroring forbid would not deny. The Decider therefore denies the call
  when any policy reports an error (`policy_error`).
- JSON numbers that are not integers in the Cedar long range become strings. `null` values are omitted. The mapping is
  fixed in one function and has its own tests.
- The path target is cleaned, not resolved through symlinks. A path rule is therefore not a sandbox; the read-only root and
  the writable directories are the hard boundary.
- Arguments reach the engine only; the audit emitter never records them.
- permissions maps its existing allow, ask, and deny rules onto the same `Decider` for the flag-on path. With
  `policyFailClosed` on and a Cedar file configured, Cedar decides; the legacy rule format stays valid for flag-off use.
  A policy file that cannot be read or parsed denies every call.
- `ask` has no meaning without a person. It stays a block, as in the port today.

### Screening and redaction: extend warden and jev

No new Package. warden already redacts what it sends out, and jev already judges tool output for secrets. The work adds
pure-Go, rule-based detectors to those two Packages. They run offline and need no model.

Detector categories follow these open projects (design input only; no code is copied):

- [Microsoft Presidio](https://github.com/data-privacy-stack/presidio) (MIT; the `microsoft/presidio` URL now redirects
  there): pattern recognizers with context words and a validation hook used for checksums.
- [Meta LlamaFirewall](https://github.com/meta-llama/PurpleLlama/tree/main/LlamaFirewall) (MIT): PromptGuard 2 (injection
  classifier), AlignmentCheck (goal-hijack audit by a model), CodeShield (static rules for generated code, MIT).
  The PromptGuard weights are under a Llama community licence, not MIT. The classifier and auditor are models. They enter
  only through the optional external hook below.
- [NVIDIA NeMo Guardrails](https://github.com/NVIDIA-NeMo/Guardrails) (Apache-2.0) has five rail types: input, dialog,
  retrieval, execution, and output. warden and jev use three of them: input (before the model sees data), execution
  (before a tool runs), and output (after a tool returns).

Plan:

- PII and secrets: rules for emails, phone numbers, payment cards (Luhn), national ID shapes, IP addresses, cloud and API key
  prefixes, private keys, and JWTs. Each rule has a category, a replacement token, and a test corpus.
- Injection screening: rules for instruction override phrases, role-change markers, hidden or zero-width text, encoded
  payloads, and tool-result text that addresses the agent. Output is a score band and a category, not a verdict by itself.
- Where the stage applies: tool results (output stage), outbound judge payloads (existing redaction), and tool calls
  (execution stage, with CodeShield-style rules for shell and code in `bash` and `write`).
- Write the rules in-tree by default. Candidate sources, each checked in PR 5:
  - [wuming](https://github.com/taoq-ai/wuming) (`github.com/taoq-ai/wuming`, MIT, v0.13.1). The repository and module
    path match; no other owner's copy was found. Its README claims 75+ detectors, 14 locales, compliance presets, and the
    standard library only. All commits and releases are dated 2026-03-20 and it has few stars, so treat it as
    unmaintained. Use it as a category and test-case reference, not as a dependency, unless open question 7 says otherwise.
  - [gitleaks](https://github.com/gitleaks/gitleaks) (MIT): its secret rule set as a reference for key prefixes.
    Unverified at this commit; check the rule file licence in PR 5.
  - trufflehog is AGPL-3.0 and is excluded.
- Model-based classifiers (PromptGuard-class or an LLM judge) are an optional external hook: a URL on the egress
  allow-list, off by default, with a typed error when it fails and `policyFailClosed` is on.
- CI red-teaming: use [Augustus](https://github.com/praetorian-inc/augustus) (Praetorian, Apache-2.0, single Go binary;
  "210+ probes" in its README, "190+" in the repository description) against a model-backed test host to measure screening
  gaps. It is a CI tool only. It does not ship in a Package.

### Tracing: one `otel` Package

- New Package `components/otel` on the official [OpenTelemetry Go SDK](https://github.com/open-telemetry/opentelemetry-go)
  (Apache-2.0). Pin the `go.opentelemetry.io/otel` family at one release. v1.47.0 (released 2026-10-02) is current, and
  `otlptracehttp` is also v1.47.0. Confirm at pin time. It uses `otlptracehttp`.
- Spans follow the GenAI semantic conventions: `invoke_agent`, `chat`, and `execute_tool {gen_ai.tool.name}`.
  `gen_ai.tool.name` is required. `gen_ai.tool.call.id` and `gen_ai.tool.type` are recommended.
- The conventions moved to their own repository,
  [semantic-conventions-genai](https://github.com/open-telemetry/semantic-conventions-genai), on 12 June 2026 (core
  semconv v1.42.0). It has no tagged release yet, and every GenAI convention is in Development status. The old registry page
  on opentelemetry.io is now a "moved" stub. Pin a commit of the new repository.
- So the Package declares attribute keys as local constants, as docker-agent does in
  [`pkg/telemetry/genai`](https://pkg.go.dev/github.com/docker/docker-agent/pkg/telemetry/genai) "to insulate callers from
  upstream reorganisations", and does not import `semconv`. One file holds the keys and a comment with the pinned commit.
  A bump is a deliberate one-file change.
- Custom keys use a `pigpen.` prefix so they never collide with `gen_ai.*`.
- Export goes through the `egressPolicy` allow-list and CIDR list (OTLP over HTTP), or to a file in the session directory.
  No export runs at load. A failed export never blocks a tool call.
- Tool arguments and tool results are off by default. A separate named opt-in lists each. The span attribute set follows the
  same closed-field rules as `audit`. Both use one shared field list in the library.
- Span `status` carries only the typed `Code`, never a message.

### MCP: no separate Package

PiG has native MCP from 0.4.0 (see the roadmap). Pigpen does not plan an MCP adapter Package. Any MCP proxy behaviour that
PiG's native MCP lacks is a PiG gap. File it against [PiG](https://github.com/MichaelKinsy/PiG/issues), not Pigpen.

Candidate gaps to check against PiG 0.4.1 and file if confirmed (generic, drawn from the hosting needs):

- A per-session allow-list of MCP servers and tools.
- Credential injection from a rotated file on each call.
- Egress restricted to a listed set of destinations.
- A call timeout and a result size cap per tool.
- A fixed prefix on exposed tool names to avoid collisions.
- A structured lifecycle event per MCP call.
- No child-process launch (stdio servers) under a read-only, no-shell root.

## Test plan

Each Package PR carries all of these for the flags it supports.

1. **Flag-off differential.** Build the Package from its latest `components/<name>/v0.1.x` tag and from the PR head. Run
   both on the Package's existing scenarios with no profile file and no profile environment variable. The traces, tool
   results, and files written match byte for byte after the golden harness's existing normalisation. The existing twin and
   golden-trace suites run unchanged. session-ingest has no golden suite; `npm run test:moved` is its differential. A second
   run with a profile file of `{}` gives the same result. A new test fails if the flag-off path does more than one open of
   the profile path, or starts a goroutine, a listener, an outbound connection, or a file write.
2. **Hostile case per flag.**
   - credentialFile: missing file; unreadable file; malformed JSON; wrong provider key; unknown `type`; expired token; token
     inside the margin; token rotated between two requests (the second request uses the new token); file replaced by a
     symlink to a read-only target; FIFO at the path; oversize file; cross-origin redirect (the credential is not sent).
   - egressPolicy: unlisted host; suffix that only looks like a match (`evil-example.com` vs `.example.com`); trailing dot
     and mixed case; a public name that resolves to a private address; a name with one public and one private record;
     DNS rebinding between check and dial; IPv4-mapped IPv6 (`::ffff:10.0.0.1`); NAT64; `169.254.169.254` and
     `fd00:ec2::254` inside an allowed CIDR; redirect to a private address and to an unlisted host; proxy in use with an
     unlisted target; a `proxy` tool argument (ignored); invalid CA bundle.
   - audit: a token, claim, prompt, argument, or path placed in every input; the scan of all emitted events finds none of
     them. An unknown event name becomes `invalid`.
   - resourceServer: no token; bad signature; wrong `aud`; `aud` array with and without the audience; wrong `iss`; `iss` with
     a trailing slash; expired; `exp` missing; `nbf` in the future; each just inside and outside the leeway; `alg: none`;
     HS256 signed with the public key; `alg` that does not match the key; unknown `kid` (one refresh, then reject; a burst
     triggers one refresh only); `jku` pointing elsewhere (ignored); unknown `crit`; oversize token; tenant claim with a
     `/`; JWKS unreachable with no cached key; JWKS unreachable with a cached key inside its lifetime.
   - policyFailClosed: unreadable policy; unparsable policy; empty policy; a forbid that errors on a missing attribute; a
     float argument; forbid and permit both match; judge unavailable in warden and in jev enforce mode.
   - headless: each dialog site in the Package; a cancelled context during a request.
3. **`npm run test:enterprise`.** A new script loads each Package under the pinned PiG 0.4.1 (`scripts/pig-requirement.json`)
   in `--mode rpc`. It runs a read-only, non-root container with no external network, with one `-e` per extension, a Piglet
   path, offline mode, and a writable agent directory and session directory only. The shared agent directory is a read-only
   mount reached by symlinks. It drains stderr, treats a prompt response as admission only, and fails if any dialog request
   appears. It uses fixture credentials, a fixture proxy and a JWKS fixture on loopback. The test profile lists their
   loopback addresses in the CIDR list; a second run without that entry must fail with `egress_address_denied`.
4. **Mutation checks.** For each flag, mutate the guard (drop the expiry check, skip the dial-time check, widen the
   allow-list match, send the credential cross-origin, log a token, treat a parse error as allow, remove the audience check,
   skip the erroring-policy deny, call a dialog). The hostile suite fails on every mutant. The mutants live in each Package's
   `port/mutations.json`, in the existing format, and run in CI.
5. **Supply chain.** CI runs `govulncheck` on each module and on each built executable (`govulncheck -mode=binary`). A
   finding fails the build unless the owner records an exception with a reason and an expiry. CI builds with
   `CGO_ENABLED=0 go build -trimpath` and fails if the result links a C library or reads the home directory.
6. **Screening corpus** (warden and jev). A labelled corpus per category with expected hits and expected non-hits. Augustus
   runs against a model-backed host in a scheduled CI job; results are a report, not a gate.

## Release manifest

Each Package release publishes one machine-readable manifest, `release-manifest.json`, so downstream hosts can pin. It
extends the existing release receipt (`releases/packages/<name>.json`: tag, commit, date). The release path generates it
from the tag; nobody edits it by hand. `npm run check` validates its schema and its agreement with the receipt.

```json
{
  "schema": 1,
  "name": "@pi-in-go/pigpen-websearch",
  "version": "0.2.0",
  "npm": { "integrity": "sha512-...", "tarball": "pi-in-go-pigpen-websearch-0.2.0.tgz" },
  "source": { "repository": "MichaelKinsy/pigpen", "tag": "components/websearch/v0.2.0", "commit": "<sha>", "digest": "sha256:<tree digest>" },
  "go": { "module": "<module path>", "toolchain": "go1.26", "deps": [{ "path": "<module>", "version": "<v>", "sum": "h1:..." }], "workspaceModules": [{ "path": "components/hardening", "digest": "sha256:..." }] },
  "license": "MIT",
  "upstreams": [{ "name": "<name>", "url": "<url>", "revision": "<full sha>", "license": "<spdx>" }],
  "pig": { "requirement": "0.4.1", "sdk": "v0.4.1" },
  "enterprise": { "flags": ["credentialFile", "egressPolicy", "audit", "headless"], "profileSchema": 1 }
}
```

Rules:

- `upstreams` copies the `upstreams` array of the Package's `provenance.json`. A Package with no upstream has an empty array.
- `source.digest` is computed over the Package directory and the workspace modules it uses.
- `go.deps` lists every module in the build, with its `go.sum` hash, including `cedar-go` and the OpenTelemetry modules.
- A downstream host pins `name`, `version`, and `npm.integrity`, and checks the manifest against the installed tree.

## PR sequence

One PR each, in this order. Each PR passes `npm run quality`, `npm run test:packages`, and its own enterprise tests.

1. **Shared library** `components/hardening`: `profile`, `credfile`, `egress`, `audit`, `headless`, `policy`, the nested
   `policy/cedar` module, the typed errors, the manifest generator, and `npm run test:enterprise` with a stub Package.
   Includes `provenance.json`, `CREDITS.md`, and licences. Confirms PiG 0.4.1 RPC dialog behaviour and auth file shapes.
2. **permissions**: `policyFailClosed`, `audit`; Cedar `Decider` path.
3. **websearch**: `credentialFile`, `egressPolicy`, `audit`, `headless`. The existing SSRF guard stays and runs first.
4. **a2a**: `resourceServer`, `credentialFile`, `egressPolicy`, `audit`.
5. **warden**: `policyFailClosed`, `headless`, `credentialFile`, `egressPolicy`, `audit`, plus the rule-based screening.
   The `typesafe` credential-source hook lands here.
6. **jev**, then **otel**: jev gets `policyFailClosed`, `headless`, `credentialFile`, `egressPolicy`, `audit`, and the output
   screening. `otel` follows as a new Package. session-ingest gets `audit` in the jev PR or its own small PR, at the owner's
   choice.

Each Package bumps its `package.json` version in its PR. The owner tags and publishes.

## Decisions

The owner reviewed the open questions on 2026-10-06 and decided:

1. The config surface is `<agent dir>/pigpen-enterprise/<package>.json`. No existing schema changes.
2. The shared library is named `hardening`.
3. Pin cedar-go v1.8.0. PR 1 checks the sum database.
4. `ask` is a block under `headless`. No dialog is ever answered with allow. Per-class answers wait until a host needs them.
5. `audit` writes to stderr by default. A file in the session directory is an opt-in for crash survival.
6. Security comes first: the library, then permissions, websearch, a2a, warden and jev, then `otel`.
7. wuming is a reference only. Detection rules live in the tree.
8. typesafe gets the injected hooks only.
9. Release manifests cover Packages and Piglets.
10. With the flag on, session-ingest confines `path` to the session directory and fails closed.
11. The manifest `pig` pin changes only on a rebuild. It records the PiG version used to build and test.
12. Environment variables can only turn a flag on. Profile variables use the `PIGPEN_<PACKAGE>_` prefix. Existing variables such as `PIG_A2A_*` stay unchanged.
13. Under the profile, a2a passes the guard extensions to its workers through an explicit worker-extensions list.
14. `resourceServer` uses `github.com/go-jose/go-jose/v4`.
