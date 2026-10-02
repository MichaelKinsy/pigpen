# pig-doctor

Finds and safely fixes cruft in a PiG setup. A Go extension Package with a `/doctor`
command and a read-only `pig_doctor` tool, and the same engine as a small command line,
`pig-doctor`. It is a PiG original (MIT). Nothing runs automatically: the extension
registers no event handlers.

It was written for a real case: a `~/.pig` whose `settings.json` listed two checkouts of
one Package (so every extension loaded twice), whose 13 Go extensions all failed to build
(`compile: version "go1.26.7" does not match go tool version
"go1.26.1"`), with a legacy `extensions/` directory and `extensions.toml`, 27 GB of unpruned
runtime-cell cache, and agent directories left behind by automation, one with its own
`auth.json` copy.

## Use

```sh
pig install ./components/pig-doctor          # /doctor and pig_doctor in pig
go run ./components/pig-doctor/extensions/pig-doctor/cmd/pig-doctor check   # or build the command line
```

| Command | Effect |
|---|---|
| `pig-doctor check` (default) | Read-only report, grouped by severity. Each finding says what, why it matters, the exact fix, and whether it can be auto-fixed. `--json`, `--strict` (exit 1 on errors or warnings). |
| `pig-doctor fix --dry-run` | The exact list of operations a fix would perform. Changes nothing. |
| `pig-doctor fix` | Confirms per group, then applies. `--yes` applies every **safe** group and still asks about the others. `--group ID` restricts it. |
| `pig-doctor restore TIMESTAMP` | Puts a backup back. Never overwrites: an occupied path is a conflict. `--force` replaces a `settings.json` edited since the fix. |
| `pig-doctor backups` | Lists `~/.pig-doctor-backup/<timestamp>/` backups. |
| `/doctor`, `/doctor fix`, `/doctor restore TS`, `/doctor backups` | The same inside pig. `/doctor fix` needs an interactive session and asks in a dialog per group. |
| `pig_doctor` tool | `mode` `check`, `plan` or `backups`. Read-only by construction: it cannot apply a fix. Its report goes to the model, so it names running processes by PID and executable only, never their arguments. `pig-with-batteries` does not enable it (command only). |

Flags for the home: `--home`, `--agent-dir`, `--backup-dir` (defaults: `$PIG_HOME` else
`$XDG_CONFIG_HOME/pig` else `~/.pig`; `$PIG_CODING_AGENT_DIR` else `<home>/agent`;
`~/.pig-doctor-backup`; a leading `~` is expanded as PiG does), `--orphan-days` (14), `--cache-days` (30).

## What it looks for

| Finding | Meaning | Auto-fix |
|---|---|---|
| `ext.broken` | Extensions that cannot build or load, one line of cause each (go directive newer than the toolchain, toolchain mismatch, causes found in `state/extension-logs`). | manual |
| `pkg.duplicate` | The same Package (same source, package.json name, git repo or npm name) listed twice. The entry to keep is the broadest, then the first listed (`--keep SOURCE` overrides). | **safe** when the same source is repeated or one entry is a strict subset of the other of the same named Package; otherwise confirm. Filters are read as PiG's `ResourceEnabled` reads them (`-x` excludes, `+x` and `!x` include); an entry with a `!` filter is never auto-fixed. A removal is skipped if the entry it duplicates is gone by the time it runs. |
| `pkg.filter-noop` | A filter with only `+` patterns, which force-includes and so does not narrow anything (this is why `+extensions/ask` still loads everything). | manual |
| `ext.duplicate` | One extension name found on several load paths. | manual (fixed by the two above and `legacy.*`) |
| `go.mismatch`, `go.goroot-inherited`, `go.gotoolchain`, `go.mise-multiple`, `go.missing` | Compiler and `go` command disagree, an inherited `GOROOT` or `GOTOOLCHAIN`, several mise Go versions, no `go`. | manual |
| `cache.cells`, `cache.ext` | Runtime-cell and extension caches. Entries whose `usage.json` says unused for 30 days, that hold no usage or build lock and that no running process uses are deleted (regenerable); entries without a usable `usage.json` are kept, as PiG keeps them. Unlike `pig extensions cache prune` the doctor cannot tell which entries your current configuration needs, so prefer PiG's command when pig works. | **safe**; confirm while the Go toolchain is broken (nothing could be rebuilt) |
| `cache.piglet-artifacts` | Size of PiG's managed Piglet Binary store per Piglet. Never moved: each build has a record in `receipts/piglets`, and PiG's Piglet inventory fails when a recorded build is missing. The fix is PiG's own `pig piglet remove <name> --binary`. | manual |
| `cache.piglet-cells`, `cache.piglet-binary-cells` | Sizes are reported; unused `piglet-binary-cells/isolated/<lang>/<cell>` are moved only with `--include-piglet-cells`. | confirm, opt in |
| `orphan.agent-dir`, `orphan.automation-dir` | Agent directories (`*-agent`, with settings, models, auth or sessions inside) and `subagent-output`, `worktree-claims` that settings.json does not reference, that no running process uses (as its executable, working directory, `PIG_CODING_AGENT_DIR` or an absolute argument), that hold no lock and in which nothing changed for 14 days. A reference counts as an absolute or `~/` path in any settings value; while `settings.json` cannot be read nothing is an orphan. Moved to the backup. | **safe**; confirm when they hold sessions or a running pig's environment could not be read |
| `orphan.credentials` | An orphaned agent directory that holds `auth.json` (or a copy): called out, names only, contents never read. The credential files are deleted **without a backup** after you type `remove credentials` (or `--yes --confirm-credentials`); the rest of the directory is moved to the backup. Declining leaves the whole directory alone. | credentials |
| `legacy.extensions-dir`, `legacy.extensions-toml` | `<home>/extensions/` and the 0.84-era `extensions.toml` (`handler_timeout`). An `extensions.toml` inside the agent directory is only reported (the agent directory is never changed). | confirm (manual in the agent directory) |

Anything else at the top of the PiG home (for example `harness-overlay`, `piglet-cli`,
`receipts`, marketplace files, vendor configuration) is listed as **unknown, left alone**. The doctor
does not guess about it.

## Safety

- `check` never writes. `fix` changes only operations listed in a Plan, and before applying it
  re-scans and refuses any operation the doctor would not offer now (a stale plan, an unknown
  path, a protected file). Every operation also passes a structural guard: inside the PiG home,
  no symlink on the way, nothing moved or deleted in the agent directory (there it only edits
  `settings.json` for duplicate Packages), never `auth.json`, `trust.json`, models files, sessions, skills or prompts.
- Symlinks are never followed; they are reported and left alone.
- When the backup directory is on another file system than the PiG home, a move falls back to a
  copy that refuses protected files, so a directory holding `models.json` or `trust.json` cannot be
  moved there: the fix stops with an error and leaves it in place.
- `auth.json` and every credential are never read: all reads go through one function that refuses
  protected names, and the cross-device copy refuses them too.
- Removals move to `~/.pig-doctor-backup/<timestamp>/` (`manifest.json` plus the files);
  `settings.json` is rewritten atomically with only the affected array element deleted (formatting,
  key order and every other key stay byte-identical) and the result is verified. Only regenerable
  caches and credential copies are deleted; the backup can never be inside the PiG home.
- `fix` refuses the whole run (exit 3, nothing changed) while another process holds a lock it
  would change: `settings.json.lock`, the cache GC lock, a lock inside an orphan directory. Cache
  entries with a usage or build lease, or used by a running process, are kept.
- Files outside the PiG home (your Package checkouts) are never edited. The one exception is the
  agent directory's `settings.json` when `PIG_CODING_AGENT_DIR` points outside the home; its edit is
  backed up and restores like the others.
- Restore trusts nothing in a manifest: it never writes a protected name, never replaces a symlink or
  anything else that is not a regular file, and never overwrites.
- Running processes are read for in-use checks only; from their environments only
  `PIG_CODING_AGENT_DIR`, `PIG_HOME` and `HOME` are kept, and reports never show their arguments.

## Tests and evidence

Fixture homes for every finding and fix (including a reproduction of the audited case), restore
and conflicts, the unknown-left-alone rule, symlinks and locks:

```sh
PIG_BIN=/path/to/pig npm run test:go-ports -- -race       # from the Pigpen root
PIG_BIN=/path/to/pig node components/pig-doctor/mutations/run.mjs   # 61 mutations of the guards, all must be caught
```

Tests use only `t.TempDir()` homes and an injected process list; they never read the real
`~/.pig` or the process environment. Windows: the engine compiles and vets (`GOOS=windows go vet`)
but lock probing is not implemented there, so a fix that needs a lock check is refused; only Linux was run in tests, and macOS is covered by cross-compilation and a fake `ps` listing, not by a run on a Mac.
