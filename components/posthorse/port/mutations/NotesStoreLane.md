# NotesStoreLane mutations

`NotesStoreLane.json`: deliberate defects of `note_scopes.go`, `note_snapshots.go`, `note_store.go`, `note_provenance.go`,
`note_evidence.go`, `note_inventory.go`, `note_discovery.go`, `note_nodepath.go`, `note_readdir.go`, `note_stat*.go`.
Run (xd6702, on a scratch copy holding the lane tests and the fixtures; the whole package has other lanes' failing tests):

    pigeq mutate --go <scratch module> --mutations port/mutations/NotesStoreLane.json --unit-only --sdk-dir $PIG_SDK_DIR --jobs 6

The tests that kill them are the `TestNote*`/`TestFork_*` of the lane, notably `note_mutation_test.go`,
`note_mutation_fork_test.go` and `note_stat_unix_test.go`, written for the survivors of the first runs.

## Mutations removed as equivalent (one line each)

- scopes-inside-absolute: on posix `relative` never returns an absolute path (only on win32 across drives).
- scopes-canonical-relative-cwd: `nodeRealpath` resolves a relative path against the working directory itself.
- scopes-canonical-root-stop: `parent == absolute` only at a filesystem root, where realpath succeeds: unreachable.
- scopes-sortDirs-bytes-not-u16: two directories that both prefix one path are ordered alike by bytes and by UTF-16 units.
- snap-readManifest-syntax: after a decode error `raw` is nil and `parseBinding(nil)` is nil: same result.
- snap-prepare-existing-owner: a manifest valid for a directory carries the id and instant that are encoded in it, so it
  differs from the binding only by a non-canonical timestamp text, which Pi never writes.
- snap-saveAdoption-fallback: `ensureManifest` has just made a readable manifest (or failed), so `previous` is never nil.
- store-select-manifest-forks: manifest.root == binding.root and scopeDirectory(manifest) == directory imply the directory
  is inside `<root>/.forks`.
- store-reference-fork-only: a project binding has no source, so the `source == nil` test already returns.
- store-reference-signal: only the behaviour under cancellation differs.
- store-recovery-ready: `state.value` is nil until initialization finished, so the `done` test is redundant.
- prov-journal-enoent: the file would have to vanish between a line's head and its text (a race).
- prov-journal-first-filter: only an optimization (skips parsing); the second filter gives the same entries.
- prov-legacy-suffix, prov-legacy-root-slice: an absolute touch path ends with `/<note path>` by construction, and the
  canonical form of the root ignores a trailing slash.
- prov-resolve-project-stop: a project binding has no source, so `source = binding.source` ends the walk anyway.
- prov-seedCand-source-binding: a seed candidate's `source` is only used when no source scope exists, and then there is
  no binding to have seeds.
- prov-seedCand-directories: touches are addressed through their absolute path; the directories only change the default
  directory of touches without one, which the receipts of a fork do not produce.
- prov-fork-absent-init: `nil` and `[]` both serialize as `[]`.
- ev-scope-private-path: `notePath` does not depend on the leaked binding the scope may carry.
- inv-batch-size, inv-folder-link, inv-folder-break: the stat batch size and where a batch stops only change how many
  stats run ahead of the walk; the re-listing after every folder (the observable rule) is `TestNoteInventory_*`.
- inv-walk-ctx, inv-walk-alias-state, inv-notesUnder-ctx-after-stat: each check is masked by the next one on the same path
  (`folderEntries` per batch, `notesUnder` at the end); an alias is checked with `ancestors` irrelevant.
- disc-project-notdir: `readManifest` of a path under a file is ENOTDIR, i.e. no manifest, as for a directory without one.
- disc-project-root: a manifest whose directory equals the entry's has the same root (scope suffix of fixed depth).
- disc-scoped-plain: the address is only serialized by the stores, never by discovery.
- disc-ref-writable, disc-ref-missing: references are chosen among unowned paths; an owned path's private file is skipped by
  the stat that follows, and the source file vanishing between resolve and stat is a race.
- disc-known-seed-revision: a rewritten file changes its identity, which the seed comparison already covers.
- disc-collapse-undigested: a note that could not be digested has a unique key either way.
- disc-refs-fork, disc-refs-dedupe, disc-refs-owns: a project session owns every path, a repeated candidate is deduped by
  `noteKey` later, and an owned candidate is a writable read that is skipped.
- disc-list-scope, disc-list-collapse-scope, disc-list-labeled-count: with an explicit scope the manifest only feeds seed
  lineages, which only the (skipped) collapse reads; one scope has nothing to collapse; a listing with more than one scope
  always has one that is not the session's.
- np-realpath-known-hard: a cache of components known not to be links.
- np-islink-windows: Windows-only branch, equal on every other platform.

## Not removable by equivalence, not testable here

- disc-digest-abort (`ctxErr(signal)` after a failed digest, `note_discovery.go` digest): its effect needs a request
  cancelled while a digest is running; the fixture cannot stage a cancellation inside that window deterministically
  (fixture note 4). `fileDigest`'s own abort is `TestNoteSnapshots_fileDigest`.
