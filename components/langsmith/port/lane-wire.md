# Lane LsWire: time, uuid, xxh3, sampling, runtime env

Spec: langsmith JS SDK 0.10.5 (`run_trees.js:21-35`, `utils/_uuid.js`, `utils/uuid/src/v7.js`, `utils/xxhash/xxhash.js`,
`utils/sampling.js`, `utils/env.js`). Files: `timefmt.go uuid.go xxh3.go sampling.go runtime_env.go jstext.go` (+ `*_test.go`,
`wire_vectors_test.go`), vectors `testdata/wire-{xxh3,uuid,sampling,time,env}.json`, generators
`port/tools/gen/wire-{common,xxh3,uuid,sampling,time,env}.mjs`.

Regenerate (the vectors are produced by the real vendored JS, deterministic seeds, byte-identical on rerun):

    ls run "cd components/langsmith/port/tools/gen && for f in xxh3 uuid sampling time env; do node wire-$f.mjs; done"

then copy `testdata/wire-*.json` from xd6702 back to the worktree. `LS_SDK_DIST` overrides the SDK `dist` path.

## Contract table

| ID | Upstream | Go | Test |
|---|---|---|---|
| W-T1 | run_trees.js:24-28 getMicrosecondPrecisionDatestring | `getMicrosecondPrecisionDatestring` | `TestDottedOrderVectors` |
| W-T2 | run_trees.js:21-35 convertToDottedOrderFormat / stripNonAlphanumeric | `convertToDottedOrderFormat` | `TestDottedOrderVectors` |
| W-T3 | `new Date(ms).toISOString()` (incl. extended years, RangeError) | `jsISOString`, `appendJSISOString` | `TestISOStringVectors` |
| W-U1 | _uuid.js uuid7FromTime + v7.js v7Bytes (seq 0) | `uuid7FromTime` | `TestUUID7FromTime`, `TestUUID7FromTimeRandomBytes` |
| W-U2 | _uuid.js nonCryptographicUuid7Deterministic | `nonCryptographicUuid7Deterministic` | `TestNonCryptographicUuid7Deterministic` |
| W-U3 | _uuid.js assertUuid / getUuidVersion / warnIfNotUuidV7 | `assertUUID`, `assertUUIDFor`, `uuidVersion`, `warnIfNotUUIDv7` | `TestAssertUUID`, `TestUUIDVersion`, `TestWarnIfNotUUIDv7` |
| W-X1 | xxhash.js XXH3_128 / xxh128ToBytes, all length classes | `xxh3_128`, `xxh128ToBytes` | `TestXXH3ByLength` (0..700), `TestXXH3Long`, `TestXXH3RandomAndExhaustive`, `TestXXH3Unicode` |
| W-S1 | sampling.js isSampledById | `isSampledByID`, `samplingBucket` | `TestIsSampledByID` |
| W-S2 | JS `toLowerCase` + `TextEncoder.encode` | `jsUTF8Lower`, `jsValidUTF8` | `TestJSLowerCase`, `TestJSValidUTF8` |
| W-E1 | env.js getRuntimeEnvironment / getShas | `getRuntimeEnvironment`, `runtimeEnvFrom`, `shasFrom` | `TestRuntimeEnvironmentVectors` |
| W-E2 | env.js getLangSmithEnvVarsMetadata / getLangSmithEnvironmentVariables | `getLangSmithEnvVarsMetadata`, `langSmithEnvVarsMetadataFrom`, `getLangSmithEnvironmentVariables`, `langSmithEnvFrom` | `TestLangSmithEnvVarsMetadataVectors` |
| W-E3 | env.js getEnvironmentVariable / getLangSmithEnvironmentVariable / getOtelEnabled / resolveTracingMode | same names | `TestEnvAccessorsAndTracingModeVectors` |

Benchmarks (allocs reported): `BenchmarkXXH3`, `BenchmarkUUID7FromTime`, `BenchmarkNonCryptographicUuid7Deterministic`,
`BenchmarkIsSampledByID`, `BenchmarkConvertToDottedOrderFormat`, `BenchmarkGetLangSmithEnvVarsMetadata`. Zero-allocation tests
for xxh3 and sampling; deterministic uuid allocates only its result string.

## Findings (defects/quirks of the SDK, ported)

* **W1** `xxhash.js` XXH3_len_1to3_128b: `readUInt8(data, len>>1) << 24` is an int32 shift. A middle byte >= 0x80 makes
  `combined` a negative BigInt, which sign-extends into the high 32 bits of the low half. The reference C (and the Python
  SDK's `xxhash.xxh3_128`) does not. Hashes of 1-3 byte inputs with such a byte differ between the JS and Python SDKs; ids
  derived by `nonCryptographicUuid7Deterministic` and sampling buckets of such short inputs are therefore not
  cross-SDK-identical. Run ids (36 bytes) are unaffected. Ported as is.
* **W2** `XXH3_len_9to16_128b`: `(len-1)<<54` is added to the low half without masking and OR-ed into the high half, so a
  carry sets bit 0 of the high half instead of wrapping. Differs from the reference for the inputs where the carry happens.
  Ported as is.
* **W3** `getLangSmithEnvironmentVariables`: the loop is inside one try/catch and masks a secret with
  `"*".repeat(value.length - 4)`. A secret-named variable (`*KEY*`, `*TOKEN*`, ...) with fewer than four UTF-16 units
  (including an empty `LANGSMITH_API_KEY=`) throws RangeError, the catch swallows it and the function returns what was
  collected so far: every variable after it in the environment block is silently missing from run metadata. Ported
  (including that the order of the environment block decides what is lost); vectors `short-secret-3-aborts`,
  `empty-secret-aborts`, `abort-first`, `utf16-secret-short`.
* **W4** `getLangSmithEnvVarsMetadata` filters sensitive names a second time after masking, so masked secrets are never
  emitted; the Go port therefore skips building the masked strings (same output, no allocation), but keeps the abort of W3.
* **W5** `getShas()` and `getRuntimeEnvironment()` are computed once per process (module-level cache), so commit-SHA
  variables set after the first run are never seen. Ported (`sync.Once`).
* **W6** `uuid7FromTime` with seq 0 fixes bytes 6-9 (`7000 8000`), so only 2 + 40 bits of a generated id are random and
  ids created in the same millisecond are not ordered by creation. Ported as is.

## Decisions / notes

* D1 (brief fact 6): `getRuntimeEnvironment` keeps the SDK keys with truthful values `runtime:"go"`,
  `sdk:"pigpen-langsmith-go"`, `sdk_version: portVersion`; `library:"langsmith"` and the commit-SHA variables are the SDK's.
  The test replaces exactly those three JS values and compares key order and everything else.
* `new Date(ms).toISOString()` throws RangeError for |ms| > 8.64e15; the Go functions panic with the string
  `RangeError: Invalid time value` (the signatures return no error). Real callers pass `Date.now()` or a parsed ISO
  timestamp, so the case cannot occur on the extension's path.
* `uuid7FromTime(timestamp)` takes the epoch in ms only; the JS string branch (`Date.parse`) is the caller's.
* `warnIfNotUUIDv7` and `resolveTracingMode` write the SDK's `console.warn` text to `wireWarnWriter` (stderr by default;
  tests swap it). The lead may rewire them to LsClient's `warnf`. The SDK's `warnOnce` also de-duplicates by message; the
  v7 warning has its own once flag as in `_uuid.js`.
* JS strings with lone surrogates reach Go as WTF-8; `jsValidUTF8` turns each into U+FFFD like `TextEncoder`.
  `jsUTF8Lower` is `toLowerCase` (U+0130 and Final_Sigma special cases) writing into a caller buffer; `jsutil.go`'s
  `isCased`/`isCaseIgnorable`/`utf16Len` (LsExt) are reused.
* `getEnv`/`isBrowser`/`isNode`... of env.js are the runtime detection; the port is always "go", so they have no Go form.
* Environment enumeration order is the order of `os.Environ()`, as `Object.entries(process.env)` follows the process
  environment block. On Windows Go and Node may enumerate in different orders; not testable here.

## Open questions

None blocking.
