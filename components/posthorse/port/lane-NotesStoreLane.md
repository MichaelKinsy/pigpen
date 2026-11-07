# Lane C1 (NotesStoreLane): the notes store

Files (package `posthorse`, `extensions/posthorse/`): `note_scopes.go`, `note_snapshots.go`, `note_store.go`,
`note_inventory.go`, `note_discovery.go`, `note_provenance.go`, `note_evidence.go`, `note_nodepath.go` (ports of node:path and
`fs.realpathSync`), `note_readdir.go` / `note_readdir_windows.go`, and the platform files
`note_stat.go`, `note_stat_linux.go` (linux, android, openbsd, dragonfly), `note_stat_bsd.go` (darwin, freebsd, netbsd),
`note_stat_windows.go`, `note_stat_other.go`. Tests: `note_scopes_test.go`, `note_snapshots_test.go`,
`note_inventory_test.go`, `note_nodepath_test.go` (+ `testdata/note_node_paths.json.gz`), `note_fserrors_test.go`,
`note_parity_test.go` (differentials against Node/typebox; the case files come from `port/review/notesstore-probes/`),
`fork_notes_test.go`, `fork_notes_recovery_test.go`, `fork_notes_store_test.go`,
`fork_notes_discovery_test.go`, `note_store_bench_test.go`, `note_testutil_test.go`. The original has no ledger tests for
this area; its proof is `test/native-host.ts:203-935` (real Pi), ported as `TestFork_*` below.

## Go API (TS names; lowerCamel, unexported)

| TS (src/) | Go | Notes |
|---|---|---|
| `NOTES_BINDING`, `NOTES_TOUCH` | `notesBindingType`, `notesTouchType` | renamed: `notesTouch` is taken by a test helper |
| types `NoteOwner/NoteScope/NoteAddress/NoteIdentity/NoteSeed/NoteBinding/NoteTouchRecord` | `noteOwner`, `noteScope{root,scope,origin}`, `noteAddress{noteScope;path}`, `noteIdentity`, `noteSeed`, `*noteBinding`, `noteTouchRecord`, `noteTouchFile` | scope functions are generic over anything with `scopeOf()`: scope, address, `*noteBinding` |
| TypeBox `Check(schema, v)` (`note-scopes.ts:9-62`) | `checkOwner/Scope/Address/Identity/Seed/Binding/Touch` | pure Go (no `Value.Check` host round trip); accept/reject tables in `note_scopes_test.go` |
| `parseBinding`, `bindingsOf`, `touchRecords`, `seedsOf`, `ownedPaths` | same | `bindingsOf(entries []rec) []*noteBinding` |
| `forkScope`, `scopeDirectory`, `sameScope`, `sameOwner`, `inside`, `noteRelative`, `canonicalPath`, `notePath`, `matchNoteDirectory`, `addressOf` | same | `matchNoteDirectory` = `sortNoteDirectories` + `matchSortedNoteDirectory` (sort once per scan); `newScopeIndex(scopes).addressOf(path)` resolves the canonical scope directories once per scan |
| `fileIdentity`, `sameIdentity`, `fileDigest`, `readManifest`, `mapBatch`, `snapshot`, `adoptNote`, `ensureManifest`, `saveAdoption` | same (`fileIdentity(path, info)`, `fileDigest(ctx, path)`, `mapBatch[I,O](items, fn) ([]O, error)`) | |
| `notesUnder`, `NoteFile` | `notesUnder(ctx, scope, folder *string, notice)`, `noteFile` | `noteStatHook` test seam |
| `NoteDiscovery`, `noteLabel`, `noteReadTarget`, `noteKey` | `noteDiscovery` (`newNoteDiscovery()`, `.list(ctx, session, opts)`), same funcs | |
| `sourceEntries`, `resolveReference`, `loadNotesBinding` | same | |
| `sessionNoteTouches`, `scopeRecoveryTouches`, `NotesRecovery` | same, `notesRecovery` | |
| `createNotesStore`, `SessionNotes` | `createNotesStore() *notesStore` (`get`, `roots`, `recovery`), `*sessionNotes` | every host-reading method takes the caller's `sdk.Context`; the session object is shared by all handlers and holds no context |

## Decisions and findings (for PORT.md)

1. **Addresses leak the whole binding (upstream defect, ported; owner decision S11).** `{ ...scope, path }` in `addressOf`,
   `resolveReference` and `SessionNotes.read` spreads a whole `NoteBinding` when the scope object is one, so seeds'
   `source`/`lineage` in manifests and journal entries carry `version`, `kind`, `owner`, `root`, `scope` (and a fork's
   `source`, `seeds`, `absent`) before `path`, and `{ ...address, kind }` of a touch overwrites the leaked `kind` in place.
   Reproduced via `noteScope.origin` (programmatic) and `noteScope.leak` (the decoded map of a journal or manifest entry,
   parsed only when the scope is written, so `bindingsOf` stays linear). Tests: `TestNoteScopes_jsonKeyOrder`,
   `TestNoteScopes_leakedBindingInAddress`, `TestNoteScopes_touchFileKindReplacesTheLeakedBindingKind`. Only the known leak
   shape is kept: extra members of an unknown shape are not preserved when a Go-written file re-serializes an entry (Go
   entries arrive as unordered maps): 10 of 213 accepted bindings of the Node differential re-serialize differently
   (`TestNoteParityTypeBoxDifferential`, logged). **Size warning (inherent to the original, reproduced):** every fork
   generation embeds the parent's whole binding in each seed, so with 100 notes the grandchild's `posthorse-notes-scope`
   entry and `manifest.json` are about 5 MB, with 300 notes about 45 MB, and the next generation multiplies again; Pi writes
   the same bytes. Weigh this in the owner decision on dropping the leak.
2. **Dates.** `Date.parse(owner.timestamp)` and `new Date(ts).toISOString()` use `sdk.ParseDate` (canonical ISO form decided
   locally, other text answered by the host's V8) and a local `noteISOString`. A host failure while parsing a non-canonical
   timestamp rejects the binding (the original cannot fail there). Pi writes canonical timestamps only.
3. **Node error text and fs semantics (fixed after review S1-S3).** Every fs error of the lane is `CODE: description,
   syscall 'path'[ -> 'dest']` as Node prints it (`nodeFsError`/`nodeFsErrorDest` with the syscall Node names: `open`, `read`
   (no path), `stat`, `lstat`, `mkdir`, `mkdtemp`, `copyfile`, `utime`, `rename`, `scandir`, `realpath`), checked against
   Node's own texts (`TestNoteFsErrors_*`). `canonicalPath` is a port of the JS `fs.realpathSync` (`note_nodepath.go`): `..`
   is removed lexically before any link is followed, errors name the component and syscall (`ELOOP ... stat '<loop>'`), the
   fallback uses Node's `dirname`/`relative`. `mkdir -p` over a file is `EEXIST` (Go says ENOTDIR). The path helpers are ports
   of node:path (posix and win32 `resolve`, `relative`, `dirname`, `isAbsolute`) verified against 25k cases of Node 24 for both
   flavours (`TestNoteNodePath_matchesNode`, `testdata/note_node_paths.json.gz`).
4. **Manifest decoding (S4).** `readFile(utf8)` + `JSON.parse`: invalid UTF-8 replaced per WHATWG (`jsFromBytes`), lone
   surrogate escapes kept, `1e400` read as Infinity (only a schema member rejects it): the SDK's decoder.
5. **Windows (S7-S9; not runnable here, rests on the Go/Node sources and on Node-measured tables of the pure parts).**
   S9: `noteRelative` on win32 is the port of `relative(resolve("/"), resolve("/", path))` (`C:x.md` -> `x.md`, verified by
   table). S8: `readDirNode` reads a directory unsorted with `File.ReadDir` on Windows (libuv's sort is Unix-only), sorted
   `os.ReadDir` elsewhere. S7: `nodeRealpath` follows junctions (an irregular file Go cannot resolve: it reads their target
   with `os.Readlink`, as libuv reports them as links), `isLinkEntry` treats them as links in the walk. Residue: the walk's
   `realpath` (libuv native, `GetFinalPathNameByHandle`) also normalizes the case of the path, which the JS-port stand-in
   does not; the per-drive working directory (`process.env["=C:"]`) is not modelled; ino/dev/ctime identities as in finding 6.
6. **Windows identity.** `fileIdentity` has no `ChangeTime` and no inode in the standard library for a path: ino/dev come
   from `GetFileInformationByHandle`, ctime is the last write time (`note_stat_windows.go`); platforms without a stat record
   use mtime and zero ino/dev. Identities written by Node and by Go never compare equal there, so the digest is recomputed
   (correct, slower). Linux/macOS use `Stat_t` exactly as libuv does (`sec*1e3 + nsec/1e6`).
7. **mtime preservation** copies the exact nanosecond timestamps (`os.Chtimes`); Node rounds through a double of seconds
   (about 0.24 us). Equality of copy and source holds in both.
8. **copyFile** = O_EXCL create with the source's mode (fchmod, as libuv), destination removed on failure.
9. **Concurrency.** `mapBatch` runs up to 16 goroutines per batch and settles the batch before reporting the first failure
   in item order; the walk overlaps up to 64 stats as the original. `sessionNotes` state (`owned`, `calls`, `revisions`) is
   mutex-guarded (parallel tool calls). `notesStore.get` shares one initialization between concurrent first callers; a
   failed one is forgotten. The initializing caller's `ctx` carries the host calls (the original is not cancellable there).
10. **Host round trips (S10).** `sessionKey`, `get` and `loadNotesBinding` read `getHeader`/`getEntries` a bounded number of
    times (one `getEntries` shared by the binding lookups); `seedCandidates` also reads `getBranch` and
    `buildSessionProjection` once (a whole-projection transfer per fork initialization, as the original computes it
    in-process). `#reference` reads `getEntries` once per `mapBatch` batch of 16, like the original (whose calls run their
    synchronous prefix together): `mapBatchPrepared` + `sharedEntries`; a listing with 20 inherited notes is 3 reads, not 21
    (`TestNoteStore_referencesReadTheEntriesOncePerBatch`).
11. **Test seams:** `noteStatHook` (after each successful stat of a walk, may run concurrently) and `noteAfterCopy` (after the
    bytes are copied, before verification): the original's tests replace `fs.promises.stat` / `copyFile` to the same effect.
12. Name collisions resolved: `isNoEntry` (HistoryLane, ENOENT or missing binary) and `isMissing` (CoreLane, ENOENT) exist, so
    mine is `noteMissing` (ENOENT or ENOTDIR). `win32Resolve`/`normalizeString` of NotesToolsLane's `node_paths.go` are
    single-purpose; `note_nodepath.go` has the variadic `win32ResolveAll` and `nodeStyle.normalizeString`.
13. **Fixed after the review (port/review/notesstore.md):** S1 and S2 (realpath port, `..` lexical), S3 (Node fs error text at
    every site), S4 (manifest decoding), S5 (lazy leaked binding: `parseBinding` of a fork of a fork 0.12 ms / 34 KB / 714
    allocs at n=100 and 0.34 ms / 101 KB / 2114 allocs at n=300, was 10.5 ms / 5.3 MB / 31k and 103 ms / 47 MB / 273k;
    `BenchmarkNoteParseLeakedForkOfFork100/300`), S6 (`seedsOf` keys touch seeds by the file's path), S7-S9 on Windows as in
    finding 5, S10 as above. S11 stays an owner decision (finding 1, size warning).

## native-host.ts (fork notes) -> Go

| native-host.ts case | Go test | Not reproduced layer-1 (cover with a recorded scenario) |
|---|---|---|
| `:203` fork notes canonicalize legacy receipt aliases before resolving their source | `TestFork_legacyReceiptAliasesCanonicalizeBeforeResolvingTheirSource` | native `read` tool executes; the parent's receipts are synthesized entries. Fixture creation failure of the original is `session_start` failure here |
| `:250` fork notes retry initialization after a missing source is restored | `TestFork_retryInitializationAfterAMissingSourceIsRestored` | `onError` plumbing of `bindExtensions` (the failing `session_start` handler reply stands in) |
| `:325` retain deleted-worktree source roots across projects and interactive descendants | `TestFork_retainDeletedWorktreeSourceRootsAcrossProjectsAndInteractiveDescendants` (real `git worktree`) | `AgentSessionRuntime.fork` (the descendant is `fxForkSession` + `session_start`) |
| `:405` snapshot the selected branch without sharing its writable checkpoint | `TestFork_snapshotTheSelectedBranchWithoutSharingItsWritableCheckpoint` | none |
| `:469` keep ownership across tree navigation, adoption, deletion, and resume | `TestFork_keepOwnershipAcrossTreeNavigationAdoptionDeletionAndResume` | native read/write/edit execution (effect closures do what the tools do to the file), the codemode tool (two nested notes calls issued concurrently under one codemode receipt), `navigateTree` events (`fakeSession.branch`) |
| `:591` fork adoption reports a private copy left by failed source verification | `TestFork_adoptionReportsAPrivateCopyLeftByFailedSourceVerification` | `fs.promises.copyFile` mock -> `noteAfterCopy` seam |
| `:837` recover metadata-only nested writes through edited receipts and compaction | `TestFork_recoverMetadataOnlyNestedWritesThroughEditedReceiptsAndCompaction` (fork half) | native per-call argument record (8 KiB omission), the `dump` tool, native auto-rollover and the compaction summary text: recovery lane / scenario |
| `:884` associate late cancelled background effects with their launch branch | `TestFork_associateLateCancelledBackgroundEffectsWithTheirLaunchBranch` (ownership half: a touch recorded on another branch belongs to its launch receipt's branch, and only while that receipt is not deleted) | background codemode runner, the cancelled `ctx.signal`, `codemode-background` custom messages, recovery summaries after `navigateTree`: scenario |

Further tests of this lane: `TestFork_deletedOwnedNoteDoesNotFallThroughToAnAncestor`,
`TestFork_descendantSnapshotsOnlySeedsVisibleOnItsBranch`, `TestNoteProvenance_sourceEntriesFollowsTheParentChain`,
`TestNoteStore_*` (probe extension on the fixture: single-flight init, binding persisted once, header required, `record`/
ownership/revision, call bookkeeping, `selectScope`, `read`, `adopt`, `guardReference`), `TestNoteDiscovery_*` (notices,
staging ignored, identical-copy collapse with labels, same-size divergence by digest, references, seed digest trust and digest
cache), `TestNoteEvidence_scopeRecoveryTouches`, `TestNoteInventory_*`, `TestNoteSnapshots_*`, `TestNoteScopes_*` (schema
tables: 75 binding cases, 28 touch cases). 59 test functions in all.

## Proof

- Red: `port/red/NotesStoreLane.log` (stubbed scratch copy; the implementation existed before the tests ran: 36 of 38 tests of
  that moment failed without the code).
- `go vet` clean for linux, darwin and windows on the whole package; `go test -race -count=3 -run 'TestFork_|TestNote'` green;
  mutation check: 19 edits of the implementation, each killed (`port/runs/NotesStoreLane.log`).
- Benchmarks (`Benchmark*` in `note_store_bench_test.go`), 20 iterations, xd6702: snapshot of 300 notes 21 ms, 6.4 MB, 59.8k
  allocs; walk of 500 notes 5.5 ms, 0.66 MB, 5.7k allocs; digest collapse of 600 notes 28 ms cold / 17 ms warm; validation of
  1000 bindings in a 3000-entry journal 3.3 ms, 23k allocs; touch records of the same journal 1.2 ms; evidence scan 6.7 ms.
  Max RSS of the benchmark process 18 MiB. Not compared with the TypeScript original.

## Open decisions for the owner

1. Finding 1 (leaked bindings in addresses) is ported on purpose: the manifests and the `posthorse-notes-scope` /
   `posthorse-note-touch` journal entries are byte-identical in shape to the original's. Dropping the leak would shrink them
   and cannot be observed by any reader of the schema; say so if that fix is wanted (it is an observable change of the files).
2. Finding 6 (Windows identity) is an SDK/stdlib gap, not a port choice: only a `ChangeTime` source would close it.

## Mutation check

`port/mutations/NotesStoreLane.json` (347 deliberate defects of the lane's files) and `port/mutations/NotesStoreLane.md`
(41 mutants removed as equivalent, one line each, and one not testable here). All 347 are killed by the lane's tests
(`pigeq mutate --unit-only` on a scratch module of the lane tests and fixtures, since the whole package has other lanes'
failing tests); the survivors of the first runs produced `note_mutation_test.go`, `note_mutation_fork_test.go` and
`note_stat_unix_test.go`. No production code changed for it.
