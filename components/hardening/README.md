# hardening

The shared Go library behind the opt-in **enterprise profile** of Pigpen Packages. A Package runs unchanged on a developer
machine. A headless hosted runtime needs more: credentials from a rotated file, a closed set of destinations, audit
events, a policy that denies on any failure, no dialogs. This library holds those pieces once. Each Package adopts the
flags it supports in its own change.

**With no profile file and no `PIGPEN_<PACKAGE>_ENTERPRISE*` environment variable the library does nothing.** Every flag is
off, no file is written, no goroutine is started, no listener is opened and no connection is made. A flag that is on and
misconfigured fails closed with a typed error; it never degrades to the flag-off behaviour.

This Package has no extension or command of its own. It is Go only (`CGO_ENABLED=0`), MIT, and credits what it links in
[`CREDITS.md`](CREDITS.md). It targets PiG 0.4.1; [`PIG-0.4.1.md`](PIG-0.4.1.md) records the PiG behaviour it relies on.

## Packages

| Import path (under `github.com/MichaelKinsy/pigpen/components/hardening`) | What it does |
|---|---|
| `profile` | Loads `<agent dir>/pigpen-enterprise/<package>.json` and the environment; `Flags`, `Status`, the typed `Error` and its closed `Code` set |
| `credfile` | Reads one provider's credential from an `auth.json`-shaped file on every request; a `RoundTripper` that binds it to one origin |
| `egress` | An allow-listed, address-checked `http.Transport` and `http.Client`; the profile's proxy and CA bundle only |
| `audit` | Structured lifecycle events with a closed field set, one JSON line per `Write` |
| `headless` | What a Package does instead of opening a dialog |
| `policy` | The `Decider` interface for tool-call decisions, `FailClosed`, `Deny` |
| `policy/cedar` | A `Decider` on Cedar policies (cedar-go v1.8.0). A **nested module**, so a Package that does not use Cedar does not carry cedar-go |
| `resourceserver` | JWT access-token verification against a configured JWKS (go-jose v4) |

## Using it from an extension

Like `typesafe`, the library is reached through a `go.work` `use` entry from the extension directory
(a `replace` in `go.mod` is ignored by `pig piglet build`):

```
use (
	.
	../../../hardening
	../../../hardening/policy/cedar   // only when the extension uses Cedar
)
```

The extension's `go.mod` requires `github.com/MichaelKinsy/pigpen/components/hardening v0.0.0` (and, for Cedar,
`.../hardening/policy/cedar v0.0.0`). The `go.work` in this directory lets `go test ./...` run here; its `replace` line
resolves the nested module's `v0.0.0` requirement the way the repository's test scripts do.

```go
flags, err := profile.Load("websearch", profile.AgentDir(os.Getenv), os.Getenv) // no network work
if err != nil { /* profile_misconfigured: refuse to load; err is typed and carries no path */ }
if err := flags.Require(profile.FlagCredentialFile, profile.FlagEgressPolicy, profile.FlagAudit, profile.FlagHeadless); err != nil { ... }

emitter := audit.New(flags.Audit, os.Stderr)          // nil Audit => nil *Logger => every method is a no-op
if flags.EgressPolicy != nil {
	policy, _ := egress.New("websearch", flags.EgressPolicy) // reads the CA bundle; no network
	client, _ := policy.Client(30 * time.Second)
	...
}
```

## The profile file

One file per Package: `<agent dir>/pigpen-enterprise/<package>.json`. The agent directory is `PIG_CODING_AGENT_DIR`, else
`$PIG_HOME/agent` (`profile.AgentDir`). It never falls back to a Pi directory and never reads a file from the working
directory or a project `.pig/` directory: a relative agent directory or profile path is refused. A symlink at the path is
followed (a shared agent directory is reached through them); the checks are on the opened descriptor.

* No file: every flag off, no error. A file that exists and cannot be read, is larger than 64 KiB, is not a regular file,
  does not parse, repeats a key, has an unknown key (keys match exactly: `"Headless"` is not `"headless"`), or has the
  wrong `schema`: `profile_misconfigured`.
* A flag key that is absent, or has `"enabled": false` (or no `enabled`), is off.
* `PIGPEN_<PACKAGE>_ENTERPRISE` names another file (an absolute path; named and missing is an error).
  `PIGPEN_<PACKAGE>_ENTERPRISE_<FLAG>` (`CREDENTIAL_FILE`, `EGRESS_POLICY`, `AUDIT`, `RESOURCE_SERVER`,
  `POLICY_FAIL_CLOSED`, `HEADLESS`) set to `1` or `true` turns a flag **on**. Nothing in the environment turns a flag off:
  `0`, `false`, `no`, `off` or an empty value leave the file's setting, and any other value is `profile_misconfigured`.
  A flag that needs configuration and is turned on by the environment alone is `profile_misconfigured`.

```json
{
  "schema": 1,
  "credentialFile": {
    "enabled": true,
    "path": "/run/agent/auth.json",
    "provider": "search",
    "origin": "https://api.example.com",
    "header": "Authorization",
    "scheme": "Bearer",
    "expiryMarginSeconds": 30
  },
  "egressPolicy": {
    "enabled": true,
    "allow": ["api.example.com", ".search.example", "proxy.internal"],
    "allowCIDRs": ["10.20.0.0/16"],
    "proxy": "http://proxy.internal:3128",
    "noProxy": [".internal"],
    "caBundle": "/etc/pki/ca.pem"
  },
  "audit": { "enabled": true, "sink": "stderr", "required": false },
  "resourceServer": {
    "enabled": true,
    "issuer": "https://idp.example/",
    "audience": "a2a",
    "jwksURL": "https://idp.example/jwks.json",
    "algorithms": ["RS256"],
    "leewaySeconds": 60,
    "subjectClaim": "sub",
    "tenantClaim": "tenant"
  },
  "policyFailClosed": { "enabled": true, "policyFile": "/etc/agent/policy.cedar" },
  "headless": { "enabled": true }
}
```

`profile.Flags.Status()` reports, for `/doctor` and a Package's status command, each flag on or off, whether the file was
found, and the last error code. It holds no secret and no path.

## Failure codes

Every failure is an `*profile.Error` with a closed `Code`, the `Flag`, the `Package` and a fixed `Reason` per code. No
message contains a credential, token, claim, prompt, argument or path. Match with `errors.Is(err, &profile.Error{Code: ...})`,
`errors.As`, or `profile.CodeOf`.

| Code | Raised when |
|---|---|
| `profile_misconfigured` | the profile is unreadable or invalid, or a flag that is on lacks its configuration |
| `credential_unavailable` | the credential file is missing, unreadable, not a regular file (FIFO, device, directory), or over 64 KiB |
| `credential_malformed` | it does not parse, has no entry for the provider, an unknown `type`, or an unusable value |
| `credential_expired` | an `oauth` entry expires at or before now plus the margin |
| `egress_denied` | the destination host is not on the allow-list (first request or any redirect) |
| `egress_address_denied` | a dialled address is not permitted |
| `egress_proxy_ignored` | a proxy named outside the profile was ignored (an audit reason) |
| `audit_unavailable` | the audit sink cannot be written (`audit.required` fails the call) |
| `token_invalid` | no token, bad signature, wrong `iss`/`aud`, expired, not yet valid, bad claims, oversize |
| `token_alg_denied` | `alg` is `none`, `HS*`, outside the list, or does not match the key |
| `jwks_unavailable` | the JWKS is unreachable and no cached key can verify the token |
| `policy_unavailable` | the policy cannot be read or parsed (every call is denied) |
| `policy_error` | a policy errored while evaluating, or the request cannot be mapped |
| `policy_forbidden` | a forbid matched |
| `policy_denied` | no policy permits the call (default deny; the plan's table has no code for it) |
| `judge_unavailable` | reserved for warden and jev |
| `ui_unavailable` | a dialog is needed under `headless` |
| `cancelled` | the context was cancelled |

## What each flag guarantees

**credentialFile.** One `open` per request, then `fstat` on the descriptor, then a bounded read (64 KiB), with `O_NONBLOCK`
so a FIFO cannot hang. Nothing is cached: a rotated file is read by the next request. Entries are the PiG `auth.json`
shapes `{"type":"oauth","access":…,"expires":<ms>}` and `{"type":"api_key","key":…}`. A `refresh` value is never decoded or
used. A `key` that starts with `!` (PiG runs it as a command) is refused, and no key is resolved from the environment. The
`RoundTripper` adds the credential only to requests for the bound scheme, host and port, and removes its header from every
other request, so a cross-origin redirect never carries it. A credential over `http` is allowed only for a loopback origin.

**egressPolicy.** Host names are lower-cased, stripped of a trailing dot and converted to IDNA ASCII, then matched exactly or
by `.suffix` on a label boundary. Every address the dialler tries is checked in `net.Dialer.ControlContext`, on the address
actually dialled (no separate resolve step). Addresses are unmapped (`::ffff:a.b.c.d`), and NAT64 (`64:ff9b::/96`) and
6to4 (`2002::/16`) addresses are checked on their embedded IPv4 address. Denied unless an operator range exempts them:
`0.0.0.0/8`, `10.0.0.0/8`, `100.64.0.0/10`, `127.0.0.0/8`, `169.254.0.0/16`, `172.16.0.0/12`, `192.0.0.0/24`,
`192.168.0.0/16`, `198.18.0.0/15`, `224.0.0.0/4`, `240.0.0.0/4`, `::/128`, `::1/128`, `fc00::/7`, `fe80::/10`, `ff00::/8`, and
(beyond the plan's list, because they carry or hide an IPv4 address or hold no global address) `::/8` (IETF-reserved; it
covers the IPv4-compatible `::/96`, the SIIT form `::ffff:0:a.b.c.d` and local-use NAT64 `64:ff9b:1::/48`), `2001::/32`,
`fec0::/10` and `100::/64`.
**Never dialled, even inside an operator range:** `169.254.0.0/16`, `fe80::/10`, `fd00:ec2::254`, `100.100.100.200` and
`168.63.129.16` (cloud metadata and link-local). An operator range that covers every address (`/0`) is refused. Only the
profile's proxy and CA bundle are used (`HTTP_PROXY`, `ALL_PROXY`, a proxy on a base transport and `SSL_CERT_FILE` are
ignored; a named bundle replaces the system roots). The proxy's host must be on the allow-list. With a proxy the dialler
connects only to the proxy (its address passes the same check), the target host is still checked against the allow-list
on every request and redirect, and an IP-literal target is checked against the address rules; the proxy owns address
policy on its far side. `noProxy` targets are dialled
directly with the full check. The library does no network work at construction.

**audit.** `EventName`, `Outcome` and `Reason` are structs with an unexported field and no constructor from free text;
`Reason` comes from the closed `profile.Code` set. `Emit` replaces any value outside its set with `invalid` and counts it
(`Logger.Invalid`). The Package must match `^[a-z][a-z0-9-]{0,31}$` and the tool `^[A-Za-z0-9._-]{1,64}$`; a Package that
registers its tools calls `WithTools(...)` so that nothing else is accepted (an identifier-shaped token is otherwise
indistinguishable from a tool name), and `Open` pins the Package. The line has these keys and no others: `ts` (UTC, a
field beyond the plan's list, because an audit record needs a time), `event`, `package`, `tool`, `outcome`, `duration_ms`,
`reason`. One event is one line under 4 KiB, one `Write`. The default sink is stderr; `session-file` appends to
`pigpen-audit.jsonl` (mode 0600) in the session directory, opened on the first event; a symbolic link at that path is
not followed (the session directory is writable by the agent's tools). A sink failure writes at most one
`audit_unavailable` line to stderr per minute and, with `audit.required`, fails the call through `Record`.

**resourceServer.** The configuration names issuer, audience, JWKS URL and algorithms (RS256, RS384, RS512, PS*, ES*, EdDSA;
default RS256). The JWKS URL is configured and never discovered from a token or `iss`; it is fetched on first use, over the
egress client when egressPolicy is on, without following redirects by default. Keys are selected by `kid`; without a `kid`
the JWKS must hold exactly one key; two keys under one `kid` are ambiguous. The key type, curve (ES256 P-256, ES384 P-384,
ES512 P-521), `use` and `alg` must agree with the token's `alg`; RSA keys under 2048 bits and non-public keys are refused.
`HS*` and `none` can never be configured or accepted. `jku`, `x5u`, `jwk` and `x5c` are ignored; a `crit` header or `b64`
rejects the token. `iss` and `aud` match exactly; `exp` is required; `nbf` and `iat` are checked when present; leeway
defaults to 60 s and is capped at 300 s. Tokens are capped at 8 KiB. The cache follows `Cache-Control: max-age` bounded to
5 minutes to 24 hours (default 1 hour); an unknown `kid` refreshes at most once a minute; a failed refresh keeps the
cached keys until their lifetime ends; after a failed fetch the next requests fail at once for 5 seconds. A caller that
abandons its request stops waiting but does not cancel the fetch, so it cannot leave the cache empty for others. A key in
the set that go-jose cannot decode (an X25519 encryption key, say) is skipped, not the whole set. Subject and
tenant claims must match `^[A-Za-z0-9._-]{1,64}$` and become the `Principal`. `StatusFor`/`WriteError` map errors to 401
(with `WWW-Authenticate: Bearer`) or 503 (`jwks_unavailable`, `cancelled`).

**policyFailClosed.** `policy.FailClosed(d)` turns any error, panic, nil or typed-nil decider, or finished context into a
deny with a typed reason, and never returns an error. `cedar.Load(path)` returns a deny-everything `Decider` together with
a `policy_unavailable` error when the file is missing, unreadable, over 1 MiB, or does not parse. Cedar semantics: default
deny, forbid wins over permit, and a call is also denied when any policy reports an evaluation error (Cedar itself skips
such a policy, so an erroring forbid would not deny). The request maps to `Session::"<principal>"` (attributes `subject`
and `tenant`), `Action::"<tool>"` (member of `Action::"<group>"` for each action group), `Target::"<cleaned absolute path |
URL host, normalised as for the egress allow-list | none>"` (a relative path or a host that cannot be normalised denies)
and a `context` record built from the arguments by `cedar.Record` (strings, longs, booleans, sets, records; integral
floats become longs, other numbers strings, `null` is omitted; anything else, nesting over 16 or a string over 1 MiB denies).
Only static policies are supported; nothing from cedar-go's `x/exp` packages is imported. A path rule is not a sandbox: the
path is cleaned, not resolved through symlinks.

**headless.** `headless.UI(flags, hostHasUI)` is `false` under the flag; `Guard`/`Dialog`/`Blocking` give the typed
`ui_unavailable` (or `cancelled`) error at a site that would open a dialog. The flag never answers a dialog with allow.

## Tests

```sh
cd components/hardening
go vet ./... ./policy/cedar/... && go test -race -count=1 ./... ./policy/cedar/...   # the root module and the nested module (through go.work)
GOOS=windows go vet ./... ./policy/cedar/... && GOOS=darwin go vet ./... ./policy/cedar/...
python3 port/mutate.py                               # every mutant in port/mutations.json must be KILLED
```

`port/mutations.json` lists each guard the hostile tests protect (drop the expiry check, skip the dial-time check, widen the
allow-list match, send the credential cross-origin, treat a parse error as allow, remove the audience check, skip the
erroring-policy deny, answer a dialog, and so on) in the format the other Packages use; `port/mutation-results.txt` is the
last run. A few apparent mutants are equivalent (another check reaches the same result) and are not listed.

## Not in this change

The per-Package adoption (permissions, websearch, a2a, warden, jev, otel), the release-manifest generator and
`npm run test:enterprise` are separate changes in the plan's sequence.
