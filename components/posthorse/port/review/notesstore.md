# Review: lane C1 (notes store)

Reviewer: notesstore. Scope: `src/note-scopes.ts` (TypeBox schemas and `Check`), `note-snapshots.ts`, `note-store.ts`,
`note-inventory.ts`, `note-discovery.ts`, `note-provenance.ts`, `note-evidence.ts` against `note_scopes.go`,
`note_snapshots.go`, `note_store.go`, `note_inventory.go`, `note_discovery.go`, `note_provenance.go`,
`note_evidence.go`, `note_stat*.go`, with `lane-NotesStoreLane.md` (decisions 1-10).

Proof: `extensions/posthorse/review_notesstore_test.go` (test code only; each failing test has a `// DIVERGENCE:` line).
TS values come from Node 24.22.1 and typebox 1.3.27 (`~/ph-work/pi-oracle/node_modules`) running the unmodified oracle
sources on xd6702 (kernel 6.8, ext4). The probe scripts are in `port/review/notesstore-probes/` (they expect the oracle copy
at `/home/kinsy/ph-work/scratch/notesstore/oracle` with `node_modules` linked; `gen.mjs`/`gen2.mjs` write the case files the
two differential tests read through `REVIEW_NOTESSTORE_CASES` / `REVIEW_NOTESSTORE_PATHS`, `perf.mjs <n>` writes
`grand-<n>.json` for the benchmark through `REVIEW_NOTESSTORE_GRAND`).

Run: `ph run "cd components/posthorse/extensions/posthorse && go test -count=1 -run TestReviewNotesstore ."` ->
5 FAIL (S1, S2, S3, S4, S6), the differential tests skip without the env files and PASS with them;
`go vet` passes for linux, darwin, windows.

## Findings

| ID | severity | TS file:line | Go file:line | what differs | proof | proposed fix |
|---|---|---|---|---|---|---|
| S1 | parity-break (model-visible error text) | note-scopes.ts:110-121 (`realpathSync` rethrown) | note_scopes.go:302-325 | For a symlink loop in a non-final component, Node's JS `realpathSync` throws for the component it `stat`s: `canonicalPath("<r>/loop/file.md")` -> `ELOOP: too many symbolic links encountered, stat '<r>/loop'`. Go names the whole input: `... stat '<r>/loop/file.md'`. Reached by `notes read/write loop/x.md` (notePath) and the native-tool guards. The lane's own `TestNoteScopes_canonicalPathLoopIsANodeELOOP` asserts the Go text (wrong expectation). Other non-missing errors (EACCES) are `lstat '<component>'` in Node (`binding.lstat(base)`), `stat '<input>'` in Go [reasoning: Node lib/fs.js realpathSync; not runnable as root]. | `TestReviewNotesstoreCanonicalPathLoopComponent`; Node probe `probe1.mjs`/`probe4.mjs` | On EvalSymlinks failure: for `*PathError` use its `Path` with syscall `lstat`; for "too many links" `os.Stat` each prefix of `absolute` (shortest first) and report the first ELOOP prefix with syscall `stat`. Fix the lane test's expected text. |
| S2 | parity-break (edge) | note-scopes.ts:111-113 | note_scopes.go:302-311 | `realpathSync` starts with `path.resolve(p)`: `..` is removed lexically before any link is followed. `filepath.EvalSymlinks` applies `..` after following the link: `canonicalPath("<r>/root/link/../x")` (link -> `<r>/elsewhere/deep`) is `<r>/root/x` in Node, `<r>/elsewhere/x` in Go. Unclean paths reach canonicalPath through `selectScope(selector)` (model-supplied `scope`) and `addressOf(touch.absPath)` (raw tool argument text from recovery evidence). | `TestReviewNotesstoreCanonicalPathDotDot`; `probe4.mjs` (`dotdot`) | `absolute = filepath.Clean(absolute)` before `EvalSymlinks` (Node's `resolve`), keep the fallback recursion on the cleaned path. |
| S3 | parity-break (model-visible text) | note-snapshots.ts:50-51, 116-131, 145-167, 213-218; note-inventory.ts:27, 71; note-discovery.ts:49, 128, 173-180 | note_snapshots.go:33, 63-74, 137-215, 223-245, 328-345; note_inventory.go:53, 139; note_discovery.go:57, 186, 236-249 | Every fs error of the lane is Go's text, Node prints `CODE: description, syscall 'path'[ -> 'dest']`. Model-visible in the `notes list` notice `Duplicate comparison unavailable for {...}: <message>. Copies remain separate.` (fileDigest/stat), in `Incomplete notes snapshot for <dir>: <message>` (copyFile, stat, mkdir, mkdtemp, writeFile, rename), in `notes` errors from `prepareWrite`/`adopt`/`readManifest` (EISDIR, EACCES) and in walk/readdir failures. Example: Go `open <p>: no such file or directory`, Node `ENOENT: no such file or directory, open '<p>'`. The lane documents this (decision 3) but CoreLane's `nodeFsError`/`nodeFsErrorDest` already produce Node's text. | `TestReviewNotesstoreDigestErrorText`; `probe1.mjs`/`probe4.mjs` (`open`, `stat`, `digest`) | Wrap at each call with Node's syscall: fileDigest `open`/`read`; readManifest `open`/`read`; copyNote `stat`, `mkdir`, copy `copyfile` (src -> dest, nodeFsErrorDest); verifyCopy `stat`, `utime`; prepareSnapshot `lstat`, `mkdir`, `mkdtemp`; writeNew `open`; rename `rename` (dest); ensureManifest `mkdir`; walk `realpath`, `scandir`; statIfPresent `stat`; readReference/digest `stat`. |
| S4 | parity-break (persisted manifest, ownership) | note-snapshots.ts:67-71 (`readFile(utf8)` + `JSON.parse`) | note_snapshots.go:7, 72 | `readManifest` decodes with `encoding/json`: a `\ud800` escape becomes U+FFFD (Node keeps the lone surrogate), invalid UTF-8 is replaced per byte (Node: WHATWG maximal subpart), a number beyond float64 in any member (`1e400`) fails the whole manifest (Node: `Infinity`, accepted unless in a schema field). A lone-surrogate note path (the SDK delivers it as WTF-8) in `absent`/`seeds` no longer matches after a reload: `ownedPaths` misses it in `resolveReference` (falls through to the parent where Node stops), and `saveAdoption` rewrites the manifest with U+FFFD. | `TestReviewNotesstoreManifestLoneSurrogate` (Node: `absent[0]` length 4, `d800`, `probe4.mjs`) | `sdkjson.Unmarshal([]byte(jsFromBytes(data)), &raw)` (the SDK decoder keeps lone units as WTF-8, `jsFromBytes` is Node's utf8 decoding); same for the number edge if the SDK decoder maps overflow to ±Inf. |
| S5 | perf (CPU, memory) | note-scopes.ts:170-182 (`Check` visits declared members only) | note_scopes.go:469-486 (`checkScope` -> `checkBinding(m)` for every leaked address) | Recovering the leaked binding (lane decision 1) re-parses the whole binding behind every seed `source`/`lineage` on every `bindingsOf`/`touchRecords`. A fork of a fork has seeds whose source is the parent's fork binding with all its seeds (resolveReference's `{ ...current, path }`, current = sourceScope's binding), so one `parseBinding` of the grandchild is O(seeds^2) in Go and O(seeds) in TypeBox. n = 100 notes: Go 10.5 ms, 5.3 MB, 31k allocs per call vs TS 2.9 ms; n = 300: Go 103.5 ms, 47 MB, 273k allocs vs TS 8.4 ms. `bindingsOf` runs per candidate inside `resolveReference` (mapBatch over every candidate), in `newSessionNotes`, `sourceScope`, `seedCandidates`, `touchedCandidates`, `sessionNoteTouches` (every `notes list` and recovery scan). | `BenchmarkReviewNotesstoreParseLeakedForkOfFork` (xd6702, 20x) vs `perf.mjs` (Node, 200 iterations) on the same JSON | Keep the raw leaked map (`origin` as the `rec`) and run `checkBinding` only when the address is serialized (manifest/journal writes), or cache by map identity. Validation then visits root/scope/path only, as TypeBox does. |
| S6 | parity-break (edge) | note-scopes.ts:217-231 (`seeds.set(file.path, file.seed)`) | note_scopes.go:712-737 | `seedsOf` keys the Map by the touch file's `path`; Go keys by the seed's own `path`. A touch file whose seed path differs from its path (a journal not written by this code, or a future writer) gives different seeds: binding seed `a.md` + touch `{path:"a.md", seed:{path:"z.md"}}` -> Node `["z.md"]`, Go `["a.md","z.md"]` (feeds seedCandidates and the snapshot). | `TestReviewNotesstoreSeedsOfKeysByFilePath`; `probe5.mjs` | `set(file.path, *file.seed)` with the key passed separately (`set(key string, seed noteSeed)`); the initial binding seeds use `seed.path`. |
| S7 | parity-break (Windows only, alias guard) | note-scopes.ts:110-133 (`realpathSync`), note-inventory.ts:70 (`realpath`) | note_scopes.go:311, note_inventory.go:139 | Since Go 1.23 (`winsymlink=1`, the default for this module's `go 1.26`) a junction is `ModeIrregular`, not `ModeSymlink`, and `filepath.EvalSymlinks` does not follow it (`path/filepath/symlink.go:90-93` returns ENOTDIR for a non-directory, non-link component with a tail). libuv reports junctions as links (`S_IFLNK`, readlink of IO_REPARSE_TAG_MOUNT_POINT), so Node's `realpathSync` resolves them. Effects: `canonicalPath("<scope>\\j\\a.md")` stays unresolved, so `notePath`'s "leaves its scope through a filesystem alias" check and `allowedTarget` miss a junction that leaves a private scope; a junction loop under notes makes `walk` fail with ENOTDIR where Node skips the ancestor. | reasoning: Go 1.27.3 `os/types_windows.go:165-232`, `path/filepath/symlink.go:90-93`; Node's realpathSync walks lstat/readlink [INFERENCE: Node side not run on Windows] | Windows build-tagged resolver: treat a reparse point with tag IO_REPARSE_TAG_MOUNT_POINT as a link (`os.Readlink` handles it) in a component walk equivalent to Node's realpathSync, used by `canonicalPath` and `walk`. |
| S8 | parity-break (Windows only, ordering) | note-inventory.ts:26 (`readdir`), note-discovery.ts:49 | note_inventory.go:53, note_discovery.go:57 | `os.ReadDir` sorts by name. libuv's scandir sorts with `strcmp` on Unix (equal to Go) but returns NTFS order on Windows (nodejs/node#3232: sorted on Linux, not on Windows). The walk order decides the `notes list` order of notes with equal `mtimeMs` (stable sort) and the order of "Not indexed" notices. | reasoning (libuv `uv__fs_scandir_sort` is Unix-only) | Windows file: `f, _ := os.Open(dir); f.ReadDir(-1)` (directory order, unsorted); keep `os.ReadDir` elsewhere. |
| S9 | parity-break (Windows only, edge) | note-scopes.ts:93-107 (`relative(resolve("/"), resolve("/", path))`) | note_scopes.go:262-283 | A drive-relative name `C:x.md` is not absolute in either; Node's win32 `resolve("/", "C:x.md")` is `C:\x.md`, so `noteRelative` returns `x.md` (cwd on C:) and `validNoteName` rejects it. Go `filepath.Clean("\\C:x.md")` keeps `C:x.md`, which then names an NTFS alternate data stream of a file `C` in the notes directory. | reasoning (Node path.win32.resolve, Go `filepath.Clean`) | On Windows compute `normalized` with a port of `path.win32.resolve/relative` (or reject a segment containing `:`, as Node's result differs anyway). |
| S10 | perf (host round trips) | note-store.ts:132-142 (`#reference`) | note_store.go:209-227 | `#reference` reads `getEntries()` per call; in Pi that is an in-process array, in Go a full-journal host transfer. `notes list` in a fork resolves every inherited touched note through `readReference -> read -> reference` (mapBatch, 16 at a time): one `GetEntries` round trip per candidate. All 16 calls of a batch see the same entries in the original (their synchronous prefixes run before any await), so one read per batch is faithful. | code reading (note_discovery.go:318-360 -> note_store.go:213) | Pass the entries read once per mapBatch batch (or per list) into `reference`; keep the per-call `sourceEntries` file read. |
| S11 | nit (documented, owner) | note-snapshots.ts:208-238, note-store.ts:106-112, note-provenance.ts (raw objects re-serialized) | note_scopes.go:900-1089 | Confirmed by the differential: 10 of the 213 compared accepted bindings re-serialize differently: 9 with extra members (top level, owner, source, seeds, seed source/lineage/identity) and 1 whose seed source carries a leaked binding with a `kind` other than project/fork (a shape the original never writes into a seed); Node writes the raw object back (`{ ...previous, seeds }`, `appendEntry(binding)` of a manifest, seeds inside touch records), Go drops unknown members. Matches lane decision 1 ("only the known leak shape is kept"). | `TestReviewNotesstoreTypeBoxDifferential` (logged) | Owner decision (as documented). |

## Owner / SDK items

- **Leak growth (upstream defect, context for lane decision 1).** The leaked bindings make every fork generation
  embed the parent's whole binding in each seed: with 100 notes the grandchild's `posthorse-notes-scope` entry and
  `manifest.json` are 5.0 MB, with 300 notes 45 MB (`perf.mjs`), and the next generation multiplies again. Pi writes the same
  bytes; the port reproduces them. The owner decision on finding 1 should weigh this.
- **Windows** (S7-S9) cannot be run here; they rest on the Go 1.27.3 source and libuv/Node behaviour named above.
- Date.parse of a non-canonical owner timestamp is answered by the host (`sdk.ParseDate`); the differential skips those 8
  cases without a host (TS accepts `"1"` and `"2026-01-02T03:04:05Z"`).

## Checked and found equal

- **TypeBox `Check`** (2419 generated JSON values: base objects, every member deleted, replaced by
  null/0/1/1.5/-0/2/""/"x"/"1"/true/false/[]/{}/["a"]/[1]/{a:1}/±1e308/5e-324/2^53+1, extra members, nested extra members,
  arrays/null/strings/numbers for the whole value): `checkOwner/Scope/Address/Identity/Seed/Binding/Touch` agree with
  typebox 1.3.27 on every value (0 mismatches), and `parseBinding`/`touchRecords` agree with the oracle on all
  host-independent cases (0 mismatches), including the semantic set (relative/empty root, empty id, wrong/encoded/unicode
  fork scope, null/private/invalid source, 21 path shapes in `absent`/seed path/seed source path, private lineage, scope
  regex edge cases incl. `\`, `//`, trailing newline, leaked project/fork bindings in seed source/lineage). JS-only facts:
  typebox rejects NaN/±Infinity for `Type.Number`, rejects arrays-as-objects, accepts `seed: undefined`; none can reach
  Go through JSON. Re-serialization is byte-equal for every other accepted binding (203 of the 213 compared; the 2 accepted host-decided timestamps are not compared).
- **Path helpers** (`gen2.mjs`, 1599 + 128 + 36 + 45 cases, 0 mismatches): `noteRelative` (incl. error texts), `matchNoteDirectory`
  (all rotations of a directory list with trailing slashes, backslashes, `.forks`), `inside`, `forkScope` (encodeURIComponent of
  `!'()*-._~`, `%`, unicode, U+2028, empty id; years 0000/9999, leap day).
- **Number formatting** of identities (2^64, 1e21, fractions, 5e-324, -0) equals `JSON.stringify`.
- **mtime preservation**: Node's `utimes(dest, atimeMs/1000, mtimeMs/1000)` changes the copy's nanoseconds (2987 of 3000 real
  files on ext4) but never its `mtimeMs` (0 of 3000, `probe3.mjs`); Go copies exact ns, so seed identities and the
  persisted `mtimeMs` are equal (lane decision 5 holds). `ctimeMs` differs by nature (time of copy).
- **Identity**: `msOf` = libuv/Node `sec*1e3 + nsec/1e6`; ino/dev/size as float64 from `Stat_t` (statx and stat give the same
  `st_dev` encoding on Linux); struct equality = `sameIdentity`.
- note-snapshots: `fileDigest` (64 KiB chunks, abort check per chunk, lowercase hex), `readManifest` control flow (SyntaxError,
  ENOENT/ENOTDIR undefined, owner/kind/directory checks), `mapBatch` (16 per batch, all settle, first failure in item
  order, no later batch), `copyNote` (stat + mkdir both run, COPYFILE_EXCL, mode + fchmod, destination removed on failure,
  identity recheck text, digest/stat of the copy), `prepareSnapshot` (existing same-owner manifest, unexpected container text,
  empty, `.staging-` under `.forks`, mode 0700), `snapshot` (`{ ...binding, seeds }` order, `wx` manifest, rename,
  error wrapping text + cause, stage removed), `adoptNote`, `ensureManifest` (EEXIST -> same-owner check, preserve text),
  `saveAdoption` (queue on the manifest path, replace seed by path, appended last). Manifests and journal entries:
  no indentation, no trailing newline, key orders `{version,kind,owner,root,scope,source,seeds,absent}`,
  seed `{path,source,lineage,anchors,digest,identity}`, identity `{size,mtimeMs,ctimeMs,ino,dev}`, touch
  `{version,owner,scope,rootToolCallId,toolCallId,files}`, touch file `{root,scope,path,kind[,seed]}` and the leaked forms.
- note-provenance: `journalHeadType` (JS `\s`, `[^"]+`), `journalMetadata` (partial entries on ENOENT), `sourceEntries` (cycle
  stop, first session entry's parent), `legacySource` (`/`-suffix roots, absolute check, canonical set of one), `sourceScope`
  (header id/timestamp, findLast, legacy fallback), `resolveReference` (cycle text, existsSync via stat, manifest before
  journal, project/owned stop), `touchedCandidates` (Map order, findLast owner, seed lineage, receipt ?? entry anchor,
  `clear`), `seedCandidates` (missing-root text, visible anchors), `forkBinding` (absent vs unavailable text, plain source),
  `loadNotesBinding` (header text, own binding, fork decision, project shape).
- note-store: constructor scopes order and ownership, `revision`, `startCall`/`finishCall`/`call`, `owns`, `record` (owned before
  notePath, revision, plain scope, entry after the loop), `selectScope` (lazy short-circuit, manifest root and `.forks`
  check, error text), `read` (scope and reference paths, writable), `adopt` (order: copy, record, unverified text with
  cause, saveAdoption text), `guardReference` texts, `sessionKey` text, `get` single-flight and retry after failure, `roots`,
  `recovery` only when ready.
- note-inventory: walk order on Unix (libuv scandir sorts with strcmp = `os.ReadDir`), batches of 64 stopping after the first
  folder or link, re-listing after a folder, ENOENT/ENOTDIR skips, ancestors by realpath, private-scope alias notice text,
  alias files checked through canonicalPath, single-file target, abort points, stable mtime sort.
- note-discovery: project scopes (`.staging-` skipped, notice text, manifest root check, current scope appended), scoped
  addresses plain, seed lookup, `noteKey`/`noteReadTarget`/`noteLabel` texts, digest cache and seed trust, group key
  `lineage\0size`, current-scope-first stable sort, digest/address match map order and copies, references filter
  (owns first, path prefix), dedupe by `noteKey`, collapse only without scope, final stable sort, `labeled`.
- note-evidence: projected omission, context-edit replacement and id, receipt keys (toolResult, custom_message runId),
  last receipt wins, `shown.get(undefined)`, owner/tool-call dedupe, touch kinds, legacy/recorded merge and owner copy,
  visible-order stable sort, `visibleIds`, `scopeRecoveryTouches` (current scope, foreign owned -> private path, reference
  scope).
- Concurrency: mapBatch callbacks are thread-safe (mutex-guarded session/digest state, per-item notice slices); the two
  goroutines of `list` do not share `notices`; no goroutine outlives its batch.

## Cleanup

Scratch `~/ph-work/scratch/notesstore/` on xd6702 and the local `/tmp/nsreview` removed after the runs. The probe scripts
are kept in `port/review/notesstore-probes/`.
