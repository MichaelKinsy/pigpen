# Port record: hardening

This Package is original work (see `provenance.json`: no upstream). There is no original to compare with, so the proof
is the hostile test suite and the mutation check below. It is a library Package: no extension, so no golden traces.

## Evidence

| Check | Command | Result |
|---|---|---|
| Unit and hostile tests | `go test -race -count=1 ./... ./policy/cedar/...` (the root module and the nested module, through `go.work`) | pass |
| Vet, all platforms | `GOOS=linux\|windows\|darwin\|freebsd go vet ./... ./policy/cedar/...`, `GOOS=android GOARCH=arm64` (`CGO_ENABLED=0`) | clean |
| Vulnerabilities | `govulncheck ./...` in the root module and in `policy/cedar` | none found |
| Mutation check | `python3 port/mutate.py` | see `mutation-results.txt` |
| Flag-off differential | `TestFlagOffDoesNothing` (module root), `TestFlagOffLoadOpensTheProfileOnceAndStartsNothing` (`profile`) | no file written, no goroutine, one open of the profile path |

## Plan rule to test

The rules are those of the plan's library section (`docs/plan/enterprise-profile.md` as merged in #12, commit `335e637c`). Each rule has a test
derived from the plan's wording, not from the code. The mutation check is what shows each test bites: a first run left
survivors, and each became a new or a stronger test (for example the exact-cap file and JWKS sizes, a valid signature on an
oversize token, a key without a `kid` against a malformed `kid`) before the final run.

* **Profile.** `profile/load_test.go`: no file, empty file, parse error, unknown and repeated keys, trailing data, oversize,
  directory, unreadable, symlink, never from the working directory or a project directory, relative paths refused, env on
  only, env path, per-flag configuration and its misconfigurations, status without secrets or paths, `Require`.
* **credfile.** `credfile/*_test.go`: missing file; unreadable; malformed JSON; wrong provider key; unknown `type`; expired
  token; token inside the margin; rotation between two requests; symlink to a read-only target; FIFO and device at the path;
  oversize file; partial read; cross-origin redirect (credential not sent, with `Authorization` and with a header net/http
  does not strip); one open per call; no refresh value used; no path or token in any error.
* **egress.** `egress/*_test.go`, `internal/netrange`, `internal/hostname`: unlisted host; look-alike suffix (`evil-example.com`
  vs `.example.com`); trailing dot and mixed case; IDNA; a public name that resolves to a private address; a name with one
  public and one private record (every address the dialler tries is checked; the private one is never connected to); a
  rebind after the static check; IPv4-mapped IPv6; NAT64; 6to4; `169.254.169.254` and `fd00:ec2::254` inside an allowed
  range; redirect to a private address and to an unlisted host; proxy in use with an unlisted target; environment proxies and
  a base transport's proxy ignored; invalid CA bundle; a named bundle replacing the system roots.
* **audit.** `audit/*_test.go`: tokens, claims, prompts, arguments and paths placed in every input never appear in the
  output; an unknown event name becomes `invalid` and is counted; one write per event, under 4 KiB; concurrent writers do not
  interleave; an unwritable sink costs one `audit_unavailable` line a minute and fails the call under `required`.
* **resourceserver.** `resourceserver/*_test.go`: no token; bad signature; wrong `aud`; `aud` array with and without the
  audience; wrong `iss`; `iss` with a trailing slash; expired; `exp` missing; `nbf` in the future; just inside and outside the
  leeway; `alg: none`; HS256 signed with the public key; `alg` that does not match the key; unknown `kid` (one refresh, then
  reject; a burst triggers one refresh only); `jku` and embedded `jwk` ignored; unknown `crit`; oversize token; tenant claim
  with a `/`; JWKS unreachable with no cached key; JWKS unreachable with a cached key inside its lifetime.
* **policy and Cedar.** `policy/policy_test.go`, `policy/cedar/*_test.go`: unreadable policy; unparsable policy; empty
  policy; a forbid that errors on a missing attribute; a float argument; forbid and permit both match; a panic, a nil and a
  typed-nil decider; a finished context; the argument mapping table.
* **headless.** `headless/headless_test.go`: the dialog site, a cancelled context, the flag-off pass-through.

## Mutation check

`mutations.json` has one entry per guard (`name`, `pkg`, `file`, `find`, `replace`; `pkg` is relative to this module).
`mutate.py` applies each to a copy of the module and runs the tests of that package (the whole module for `internal/`).
All must be KILLED.

These apparent mutants are **equivalent** (another check reaches the same behaviour), so they are not listed:

* the userinfo check in `resourceserver`'s JWKS URL parser: `hostname.OriginOf` refuses userinfo too;
* an invalid `netip.Addr` allowed: the final `IsGlobalUnicast` case denies it;
* the allow-list check in `egress`'s wrapper `RoundTripper`: the transport's own proxy hook refuses the same request first;
* `-9223372036854775807.0` for `-9223372036854775808.0` as the Long range bound: the same `float64`;
* an empty `alg` in `resourceserver`: it is never in the allow-list;
* a second signature, a negative `NumericDate`, the second clamp of the JWKS lifetime, a cancelled context before the fetch,
  `HS256` in go-jose's list, whitespace in a bearer token, a repeated header key: each is also refused by a later check.
