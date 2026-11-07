# Lane LsProfile: CLI profile, OAuth refresh, refresh lock

Spec: langsmith JS SDK 0.10.5 `utils/profiles.js`, `utils/profile-lock.js`, `utils/fs.js` (file primitives), and the two
callers in `client.js` (`_fetch` 336-346, `getDefaultClientConfig` 913-948). Files: `profile.go profile_lock.go profile_json.go
profile_url.go` (+ `profile_*_test.go`), vectors `testdata/profile_vectors.json` and `testdata/profile_differential.json`,
generators `port/tools/gen/profile-vectors.mjs` and `port/tools/gen/profile-differential.mjs`.

Regenerate (real vendored JS; the differential pins `Date.now()`, the rest is deterministic):

    ls run "cd components/langsmith/port/tools/gen && LS_SCRATCH=/home/kinsy/ls-work/scratch/<you> node profile-vectors.mjs && LS_SCRATCH=/home/kinsy/ls-work/scratch/<you> node profile-differential.mjs"

then copy `testdata/profile_*.json` from xd6702 back to the worktree. `LS_SDK_DIST` overrides the SDK `dist` path.

## Contract with LsClient (signatures differ from the stubs: the original throws here)

    loadProfileClientConfig() (profileClientConfig, error)
    (*profileAuth).currentAuthHeader() (*authHeader, error)
    (*profileAuth).getAuthHeader(ctx context.Context, hc *http.Client) (*authHeader, error)
    (*profileAuth).isProfileAuthorizationHeader(value string) bool
    (*profileAuth).close()                       // client shutdown: cancels and waits for a refresh in flight
    hasValue(*string) bool

`getAuthHeader`'s `ctx` plays `init.signal`: if it ends while a refresh is awaited the error is `context.Cause(ctx)` and the
refresh keeps running for the other callers (as in the original; the refresh is not tied to any caller). A context that is
already done is ignored when no refresh is needed (also as in the original). All callers share one refresh (`refreshPromise`).
A nil `hc` means `http.DefaultClient`. The refresh runs on its own goroutine, bounded by the 10 s deadline and by `close()`.

## Contract table

| ID | Upstream | Go | Test |
|---|---|---|---|
| P-1 | profiles.js:14-26 getProfileConfigPath (`LANGSMITH_CONFIG_FILE`, `HOME ?? USERPROFILE`) | `getProfileConfigPath` | `TestProfileConfigPathResolution` |
| P-2 | profiles.js:27-39 resolveProfileName | `resolveProfileName`, `profileByName` | `TestProfileNameResolution`, differential `env-profile-*`, `current-profile-*`, `profiles-array*` |
| P-3 | profiles.js:40-58 loadProfileState | `loadProfileState` | differential `config-*`, `profile-*` |
| P-4 | profiles.js:59-61 hasValue | `hasValue` | `TestProfileHasValueVectors` (554 vectors, every unit 0-255 plus the Unicode space set) |
| P-5 | profiles.js:62-64 trimConfigValue | `trimConfigValue` | `TestProfileTrimVectors` (297), differential `api-key-*` |
| P-6 | profiles.js:65-83 shouldRefreshProfileToken (60 s leeway) | `shouldRefreshProfileToken` | differential `leeway-*`, `expires-at-*` |
| P-7 | profiles.js:84-111 normalizeConfigUrl, oauthDiscoveryCandidates | `normalizeConfigURL`, `oauthDiscoveryCandidates` | `TestProfileDiscoveryVectors` (40 api URLs) |
| P-8 | profiles.js:119-148 isTrustedOAuthMetadata (RFC 8414) | `isTrustedOAuthMetadata` | `TestProfileTrustVectors` (1008 documents + 9 odd bodies), differential `discovery-*` |
| P-9 | profiles.js:158-169 oauthMetadataUrls | `oauthMetadataUrls` | `TestProfileDiscoveryVectors` |
| P-10 | profiles.js:170-210 fetchOAuthMetadata, resolveTokenEndpoint | `fetchOAuthMetadata`, `resolveTokenEndpoint` | vectors above, `TestProfileDefaultAPIURL` |
| P-11 | profiles.js:211-221 applyTokenResponse | `applyTokenResponse` | differential `response-*` |
| P-12 | profiles.js:222-251 abort race (`waitForAbortSignal`) | `getAuthHeader` select on `ctx` | `TestProfileContextEndsWhileRefreshing`, `TestProfileFreshTokenNeedsNoRefreshAndIgnoresCancelledContext` |
| P-13 | profiles.js:252-273 loadProfileClientConfig | `loadProfileClientConfig` | differential `load` block of every scenario |
| P-14 | profiles.js:274-289 reloadProfile | `(*profileAuth).reloadProfile` | differential `reload-*` |
| P-15 | profiles.js:290-346 refreshOAuthToken (lock, reload, form body, POST, atomic rewrite, 10 s deadline) | `(*profileAuth).refreshOAuthToken` | differential (158 scenarios), `TestProfileRefreshDeadline`, `TestProfileTwoInstancesSerialiseOnTheLock`, `TestProfileRefreshFailureKeepsOldToken` |
| P-16 | profiles.js:275-300 getAuthHeader single flight, rememberProfileAuthHeader, isProfileAuthorizationHeader | `getAuthHeader`, `rememberProfileAuthHeader`, `isProfileAuthorizationHeader` | `TestProfileConcurrentCallersShareOneRefresh`, `TestProfileManagedHeaderTracksRefreshAndAPIKey` |
| P-17 | profiles.js:355-376 currentAuthHeaderFromProfile / authHeaderFromProfile | same names | differential `AUTH` ops, `TestProfileHeaderTypeErrors` |
| P-18 | profile-lock.js:60-98 acquireOAuthRefreshLock (mkdir lock, created_at + owner) | `acquireOAuthRefreshLock` | `TestLock*`, `TestLockMutualExclusion{Goroutines,Processes}`, `TestLockOfKilledProcessIsBroken` |
| P-19 | profile-lock.js:13-49 stale break (created_at, mtime fallback, 10 s) | `lockMetadataLines`, `lockCreatedAtMs`, `lockOwner`, `removeStaleLock` | `TestLockStaleBreak` (15 cases), differential `stale-lock-*` |
| P-20 | profile-lock.js:99-104 owner-checked release | `(*oauthRefreshLock).release` | `TestLockReleaseChecksOwner` |
| P-21 | fs.js writeFileAtomic / mkdirExclusive / statMtimeMs / rmRecursive / readFileSync("utf-8") / existsSync | `writeFileAtomic`, `mkdirExclusive`, `statMtimeMs`, `rmRecursive`, `readFileUTF8`, `pathExists` | `TestWriteFileAtomic`, differential `write-failure-keeps-token-in-memory` |
| P-22 | `JSON.parse` + `JSON.stringify(v, null, 2)` (config rewrite) | `parseJSONOrdered`, `stringifyIndent2` | `TestProfileJSONRoundTripVectors` (403 documents incl. every prefix of three), differential rewrite scenarios |
| P-23 | WHATWG `new URL` / `fetch` URL rules | `parseWhatwgURL`, `fetchRequestURL` | `TestProfileURLVectors` (193), `TestProfileFetchRequestURL` |
| P-24 | URLSearchParams serializer | `formURLEncode` | `TestProfileFormEncodingVectors` (122) |
| P-25 | UTF-8 decode (readFileSync / `response.json()`) | `decodeUTF8Replacing` | `TestProfileUTF8DecodeVectors` (458) |

Tests: 54 top-level test functions in `profile*_test.go` (one, `TestProfileLockHelper`, is the child process of the cross-process lock tests); `TestProfileDifferential` runs the 158 scenarios as subtests (real
ProfileAuth against the same loopback server, Date.now pinned: same requests incl. Accept/Content-Type/body, same headers
and the same config bytes, same files left in the directory); the vector files add ~3,000 individual comparisons.
Benchmarks: `BenchmarkProfileGetAuthHeaderNoRefresh` (393 ns, 48 B, 2 allocs), `BenchmarkProfileLoadClientConfig` (15 us, 43 allocs),
`BenchmarkProfileJSONRoundTrip` (3.5 us, 25 allocs).

## Findings (defects/quirks of the SDK, ported)

* **P1** `trimConfigValue` throws `TypeError: value?.trim is not a function` for a configured `api_key` / `oauth.access_token` /
  `oauth.refresh_token` that is a number, boolean, object or array. `loadProfileClientConfig` does not catch it, so
  `new Client()` throws for such a config. Ported: `loadProfileClientConfig` returns that error (the same text); so do
  `currentAuthHeader` / `getAuthHeader` when a refresh writes a non-string `access_token` (the original stores whatever
  truthy `access_token` the server sent, a number included, and every later header build throws).
* **P2** `api_url` / `workspace_id` of another type than string or null pass through `loadProfileClientConfig` untouched and
  fail later in client code. A Go `*string` cannot carry them: `loadProfileClientConfig` refuses them by name
  (`unsupported langsmith profile: api_url must be a string`). Decision for the lead: keep the refusal.
* **P3** `refreshOAuthToken` trims `profile.api_url` outside its try block: after a reload brought in a non-string `api_url`,
  `getAuthHeader` rejects with the TypeError. Ported (`TestProfileRefreshWithNonStringAPIURLAfterReloadFails`).
* **P4** The lock release runs in `finally`: a failing `rm` rejects the whole refresh and `getAuthHeader`, even after the new
  token was stored in memory and on disk. Ported (`TestProfileLockReleaseFailureIsReturned`).
* **P5** `expires_in` large enough that `Date.now() + expires_in * 1000 > 8.64e15` makes `toISOString` throw a RangeError after
  `access_token` / `refresh_token` were already assigned: the in-memory profile has the new token, the file is not rewritten
  (differential `response-expires-in-out-of-range`).
* **P6** The 10 s stale-lock heuristic has the classic race: a breaker can delete a lock another process has just created
  (both read `created_at`, one removes and recreates, the other then removes the new one). Ported unchanged.
* **P7** The rewrite normalises the user's file: 2-space indent, every number through `Number#toString` (integers beyond 2^53
  lose digits, `1.0` becomes `1`, `-0` becomes `0`), duplicate keys collapse, integer-like profile names move to the front of
  `profiles`, comments and a BOM make the file unreadable (the SDK then ignores the profile). The new file has mode 0600 whatever
  the old one had; a failed rename leaves `config.json.tmp` behind.
* **P8** The metadata trust check compares issuer, protocol and host only; `token_endpoint` may be any path (or an `http:` URL
  for an `http:` issuer, sending the refresh token in clear). `device_authorization_endpoint` is required although unused.
* **P9** `created_at` of the lock is `new Date().toISOString()` (real clock) while staleness compares `Date.now()`: identical
  in production.
* **P10** A refresh is not cancelled when the caller that started it is aborted; the original and this port both let it run
  to completion for later callers.

## Deliberate differences (named, with reason)

* **D-P1 response bodies are capped at 8 MiB** (`profileMaxBodyBytes`): the original reads them unbounded. A larger body is
  treated as unparseable (token ignored / metadata untrusted). `TestProfileOversizedTokenResponseIsIgnored`.
* **D-P2 JSON nesting is capped at 5000** (V8 parses iteratively); a deeper config is treated as unparseable.
* **D-P3 `api_url` / `workspace_id` refusal**: see P2.
* **D-P4 Request headers**: fetch's `Accept: */*` is sent; Node's other defaults (`user-agent: node`, `accept-language`,
  `sec-fetch-mode`, `accept-encoding`) are not copied: Go sends its own `User-Agent` and `Accept-Encoding`. These requests carry
  no SDK `User-Agent` in the original either. (Wire divergence D1 applies.)
* **D-P5 `Date.parse` is the host's**: `sdk.ParseDate` answers the ISO form locally and asks the PiG host (V8's parser) for any
  other text (`expires_at` such as `2020-01-02`, an RFC 2822 date, a number). Without a host connection the error is
  returned (from `getAuthHeader`, or `acquireOAuthRefreshLock` for the lock file) instead of guessing.
* **D-P6 Unreachable branches of the original are not ported**: `profile.oauth ??= {}`, `config.profiles ??= {}` and
  `config.profiles[name] = profile` are no-ops once a profile with a refresh token was found (the object is already there);
  `oauth?.refresh_token ?? refreshToken` never takes its right side. Commented at the sites.

## Limitations (named; nothing silently approximated)

* **L1 URL parser**: `profile_url.go` is a WHATWG URL parser (special and opaque schemes, credentials, ports, IPv4 number forms,
  IPv6 serialisation, dot segments, the percent-encode sets), checked against Node 24 on 193 inputs plus 1008 trust and 40
  discovery cases. Not ported: the UTS #46 mapping tables (non-ASCII hosts are lower-cased, soft hyphens and full-width dots
  mapped, then punycoded; exotic mappings such as `ß` or Unicode normalisation forms are not applied) and the Windows
  drive-letter quirks of `file:` URLs. Neither matters for a `https://` LangSmith endpoint.
* **L2 invalid percent escapes** in the token endpoint or API URL path (`/%zz`): fetch sends them as they are, Go's `net/url`
  refuses the request, so the refresh (or the discovery probe) is skipped as for any other URL fetch rejects
  (`TestProfileURLWithBrokenEscapeIsNotRequested`).
* **L3 Windows**: `go vet` passes for windows; the tests were run on linux only. The permission tests skip as root and on windows.
* **L4 Environment**: `HOME` / `USERPROFILE` come from `getEnvironmentVariable` (`os.LookupEnv`); Node's case-insensitive
  `process.env` on Windows is the same through `os.LookupEnv`.

## Process note

The red-first log (`port/red/`) was not produced for this lane: the implementation and its golden tests were written
together and the stubs were replaced before the first run, so there is no genuine failing run to record. The tests were
checked for sensitivity afterwards through the branch coverage of the lane files (uncovered statements: error returns that
cannot occur: `crypto/rand` failure x2, `appendValue` errors x2, `newProfileAuth` error inside `loadProfileClientConfig`,
`stringify` error in the refresh).
