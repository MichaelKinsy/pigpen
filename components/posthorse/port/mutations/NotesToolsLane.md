# Mutation check of lane C2 (NotesToolsLane)

Files mutated: notes.go, note_guards.go, note_tool_effects.go, node_paths.go (`port/mutations/NotesToolsLane.json`, 340 mutants).

Command (xd6702, scratch copy of the module, `--unit-only`: the package's own Go tests judge):

    pigeq mutate --go extensions/posthorse --mutations port/mutations/NotesToolsLane.json --unit-only --sdk-dir $PIG_SDK_DIR --jobs 6

## Equivalent or untestable mutants removed from the list (33)

| mutant | why it cannot be killed |
|---|---|
| list-no-yield | Cancellation must land in the microseconds between the walk's last abort check (the end of notesUnder, which throws) and the first format batch; the original has the same two checks and no seam separates them. |
| cooperate-ignores-abort | Same as list-no-yield: cooperate is only reached right after discovery returned, which has just checked the signal. |
| hits-no-abort-check | The search's per-batch abort check follows discovery.list, whose notesUnder ends with a throwIfAborted; a cancel can only land in the same microsecond window. |
| list-select-scope-error-ignored | discovery.list selects the same scope again and returns the identical error (notes.ts:54-62 runs both, both reject alike). |
| list-format-batch | FORMAT_BATCH only sets how often the walk yields to the scheduler; the rows are identical. |
| stat-error-not-node-style | session.read performs the same lstat/stat first and already reports Node-style errors (EACCES, ELOOP: TestNotesReadStatErrorIsNodeStyle, TestNotesReadSymlinkLoopIsNodeStyle); this branch needs a stat that fails right after those succeeded. |
| append-no-first-abort-check | The check that follows adopt reports the same abort; adopt is a no-op ('owned') in a project session, and in a fork a cancel before adoption cannot be staged apart from a cancel during it (a cancel also closes the in-flight host call that carries the request). |
| append-no-second-abort-check | Needs a cancel between the end of adopt and prepareWrite, a window with no host call to hold. |
| record-close-error-path | Needs close(2) to fail after a successful write on a local file system; there is no injectable fault (the write/open/EFBIG paths are tested). |
| record-close-error-ignored | Same: close(2) failing after a successful write. |
| sep-read-error-path | Needs pread to fail after fstat succeeded (EIO); no injectable fault (EISDIR, EACCES and the truncate race are handled and tested). |
| match-ascii-text-nonascii-query | foldIndex returns -1 for a non-ASCII needle in an ASCII text, so the early return only saves the scan. |
| match-surrogate-precheck-query-only | A note read from a file never holds a lone surrogate (invalid UTF-8 decodes to U+FFFD), so hasSurrogateForm(lowered) is always false. |
| register-notesdir-error-ignored | store.get repeats notesDir(cwd) with the same working directory and returns the same error. |
| guards-home-error | The error exists only when neither $HOME nor a passwd entry is available; the empty-HOME and set-HOME cases are tested. |
| guards-forks-length | A path equal to a fork's notes directory itself resolves to no address either way (TestNoteGuardsForeignAndManagedTexts asserts the managed-metadata answer for it, identical under the mutant). |
| guards-addressof-error | With the addressOf error ignored the next canonicalPath returns the same failure for the same path. |
| guards-canonical-error | canonicalPath can only fail here if addressOf (same path) already failed. |
| guards-foreign-dest-error | notePath of the path of an address the session already resolved cannot fail. |
| guards-op-owns-error | owns fails only if the own-scope path cannot be examined, which addressOf's canonicalPath reports earlier for the same path. |
| guards-record-error | recordedFiles fails only on a path toolPath/canonicalPath rejects, and tool_call already failed (no captured call) for such a path. |
| guards-result-files-error | Same as guards-record-error. |
| effects-patch-non-string | A non-string patch input becomes the empty text, which has no headers. |
| effects-move-resets | After a move the next Move header is separated from the update header by more than one newline, so it is never adjacent either way. |
| effects-batch-record | A non-record file entry has no string path and is skipped by the next check. |
| effects-ops-preview-tool | preview_patch has no case in singleOperation and yields no operation anyway. |
| effects-committed-bytes | bytesAfter is never negative. |
| paths-posix-empty | posixResolve is only called with an absolute cwd; the empty-argument fallback of Node (process.cwd()) is not reproduced and not reachable. |
| paths-win-empty | Same for win32Resolve. |
| paths-win-rooted | The leading separator left in the tail is dropped by normalizeString. |
| paths-win-drive-abs | The separator after the drive that stays in the tail is dropped by normalizeString. |
| paths-decode-continuation | utf8.DecodeRune rejects the same sequences (RuneError) as the explicit continuation-byte check. |
| paths-decode-size | An overlong or short sequence decodes as RuneError with size 1, already rejected. |

## Result

| | count |
|---|---|
| mutants written | 373 |
| removed as equivalent or untestable (table above) | 33 |
| kept in `NotesToolsLane.json` | 340 |
| killed by the first full run (370 mutants, 18 of which did not build and were rewritten) | 256 |
| first re-run after new tests (117: the 96 survivors, the 18 rewritten, 3 new mutants): killed | 75 (42 still alive) |
| second re-run after more tests (12 of those 42): killed | 9 |
| the other 33 of the 42 | equivalent or untestable, removed (table above) |
| kept mutants not killed | 0 |

The full list was not re-run in one piece after the last tests were added (a full run takes about 60 minutes under
the shared load): the kept mutants were killed by the first full run or by the two re-runs above, each against a
snapshot of the module taken after the tests that kill them existed.

Real defects found by the check (production fixes): `notes list` of a note whose mtime is outside years 0-9999 printed
minutes (`+010000-01-01T00:00Z`; `toISOString().slice(0, 16)` stops after the hour), and the append errors of
write/close/fstat/read carried a path although Node's descriptor errors have none (`EFBIG: file too large, write`).

New tests (files of this lane): `notes_tool_behaviour_test.go`, `note_guards_foreign_test.go`,
`note_guards_behaviour_test.go`, `note_effects_cases_test.go`, `notes_append_efbig_unix_test.go`.
