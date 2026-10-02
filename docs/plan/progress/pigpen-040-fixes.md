# pigpen-040-fixes progress

Base: the Pigpen release line (signed root 77b5a21). Built against a PiG 0.4.0 release-candidate build (`pig --version` 0.3.1+0.99.2) until the 0.4.0 SDK exists.

## Done (6 defects from the Mac verification)
1. Tests assumed a non-symlinked TMPDIR: a2a worker cwd, acp changelog and the pig-acp e2e fixture now compare/use `filepath.EvalSymlinks` paths. Red reproduced with TMPDIR pointing at a symlink (the three failures of the report), green with the fix.
2. typesafe cross-check: Go 1.26 passes U+2028/U+2029 raw through a Marshaler or json.RawMessage when HTML escaping is off (compact escapes them only with escapeHTML); Go 1.27 always escapes. A body, and its Content-Length, depended on the toolchain. `marshalPlain` now escapes them on every version (marshal_test.go red on go1.26.7, green on 1.26.7 and 1.27.1); the cross-check test is unchanged.
3+5. `scripts/pig-bin.mjs`: PIG_BIN is required (no PATH fallback), must match `scripts/pig-requirement.json` (same major.minor, patch >= required), clear error otherwise. Used by every script and test that runs a real pig (scripts/pig-bin.test.mjs).
4. test:pig and test:pig-snake pass GOCACHE/GOMODCACHE (`goCaches()` in go-modules.mjs) into the pane env. Mutation: without it the pig-with-batteries source case fails here too (cold module fetch into the isolated HOME).
6. `pig install --validate-only` validates one extension; on a Package root it follows Pi's `pi.extensions` rule and answers valid:false for every Pigpen Package. The Package manifests are correct (`pig package validate` passes, `pig install` loads the Go extension). Two docs showed the root form: fixed, and scripts/validate-only-form.test.mjs keeps it out. A clearer PiG diagnostic for the Package-root form is proposed to PiG separately.

## Verified (pig 0.3.1+0.99.2, the PiG 0.4.0 release candidate, go1.27.1)
- `npm run check`, `npm test` (111), `npm run stage` (10 Piglets), 10 Piglet builds (all Built), test:go-ports (also with a symlinked TMPDIR), test:pig (2/2, with a tmux server started with a foreign HOME), test:pig-snake (9/9), test:moved, test:porter.
- `-race -count=2` test:go-ports under a symlinked TMPDIR: one failure, pre-existing and unrelated (acp TestE2EWriteToolCallEmitsDiff, a load race on the base commit too; see "Known issue" below).

## Known issue (tracked separately, after the launch)
acp `TestE2EWriteToolCallEmitsDiff` is a load flake, present on the base commit 55f0831 too (26 of 30 failures under `taskset -c 0-1 GOMAXPROCS=2` with 6 CPU burners, `-race`; 20 of 20 pass unloaded). Root cause: `session.go` (~line 677) snapshots the file when it handles `tool_execution_start`, but pig runs the tool without waiting for the adapter; under load the write lands first, `oldText == newText`, and no diff is emitted. pi-acp has the same read-after-start design and there is no protocol hook to snapshot before the tool runs.

## Pending
- Bump every Go module to the 0.4.0 SDK and set scripts/pig-requirement.json to 0.4.0: waits for PiG extensions/sdk v0.4.0.
