# internal/ssh — Upstream Tracking

- Upstream pin: `v19.1.1` (`31ffee513f`)
- Upstream directories:
  - `packages/coding-agent/src/ssh/`
  - `packages/coding-agent/src/internal-urls/ssh-protocol.ts`
  - `packages/coding-agent/src/prompts/internal-urls/ssh.md`, `ssh-reference.md`
  - `packages/coding-agent/src/discovery/ssh.ts`, `ssh-config.ts`
  - `packages/coding-agent/src/capability/ssh.ts`
  - `packages/coding-agent/src/utils/shell-quote.ts`, `owner-private-dir.ts`

## Upstream files followed

| File | Ported to |
|---|---|
| `ssh/utils.ts`, `utils/shell-quote.ts` | `utils.go` |
| `utils/owner-private-dir.ts` | `owner_unix.go`, `owner_windows.go` |
| `capability/ssh.ts`, `discovery/ssh.ts`, `discovery/ssh-config.ts` | `config.go` |
| `ssh/connection-manager.ts` | `manager.go`, `proc_unix.go`, `proc_windows.go` |
| `ssh/file-transfer.ts`, `tools/read-format.ts` (`decodeUtf8Text`) | `fs.go` |
| `internal-urls/ssh-protocol.ts`, `prompts/internal-urls/{ssh,ssh-reference}.md` | `protocol.go`, `prompts/{ssh,ssh-reference}.md` |

## Wiring

- `ssh.Register(e)` — registered in `extension.go` `registrars`; registers `ssh://` via `urls.Register(&Handler{Manager: defaultManager})` and hooks session-end ControlMaster teardown via `lifecycle.OnEnd("ssh", ...)`.
- `PromptDoc` / `PromptReference` — matches omp `ssh-protocol.ts:274-280` verbatim: advertised whenever `ssh` is found on `PATH` (independent of whether `~/.ssh/config` has entries).

## Security invariants

- Direct `exec.CommandContext(ctx, sshBin, args...)` argv execution with process-group isolation (`Setpgid` / `-pgid` kill + `WaitDelay`) — never invokes a local shell and bounds timeouts even when a descendant holds stdout/stderr open.
- Always places `--` before the SSH destination in `-G`, `-O check`, `-M -N -f`, `-O exit`, and remote command invocations.
- `BuildSSHTarget` rejects empty hosts, leading `-` on host or username (argument-injection guard against `-oProxyCommand=...`), ASCII whitespace/control characters, and shell metacharacters.
- Strict OpenSSH authority parsing rejects password userinfo, empty usernames (`ssh://@host`), empty ports (`ssh://host:/path`), port 0, out-of-range ports, malformed percent-escapes, URL query strings (`?`), URL fragments (`#`), and named-user `/~user` home expansion.
- `AssertOwnerPrivateDir` uses `O_RDONLY | O_NOFOLLOW | O_DIRECTORY | O_CLOEXEC` + `fstat`/`fchmod` on the pinned fd for any temp-rooted ControlMaster socket directory or host-info cache write (preventing TOCTOU symlink swaps and `sun_path` overflows).
- `DiscoverHosts` is strictly read-only on `~/.ssh/config` and `/etc/ssh/ssh_config` and honours `HOME` override so tests never touch the user's real `~/.ssh`.

## Tests ported

See `ssh_test.go`:
- `test/ssh-control-path.test.ts`: `ControlPathFitsBudget` (darwin 40-byte / linux 44-byte boundaries), `ControlFallbackDir` determinism + 20-hex hash parity (`omp-5434354bc38f9a50fbbd`), `ResolveControlDir` (fits / overflows / Windows / nil uid), `OwnerPrivateDirError`, `AssertOwnerPrivateDir` (mode normalization, symlink TOCTOU refusal, non-directory refusal).
- `test/discovery/ssh-config.test.ts`: `~/.ssh/config` parsing with `Include` globs (including dotfile skip), symlinks, circular include guard, wildcard/negated skip, `Host=eq`, quoted aliases, trailing comments, CRLF, case-insensitive keywords, `ssh.json` shadowing `~/.ssh/config`, per-entry malformed-object resilience, and key-order preservation.
- `test/internal-urls/ssh-protocol.test.ts`: byte-exact read (no `SourcePath`), contentType derivation (upstream markdown extensions + dotfile `.json`), BOM stripping, user/port override rejection on pinned `ssh.json` host vs pass-through on `~/.ssh/config` alias, opaque destination, percent-encoded reserved-char aliases (`alice%40prod`, `%5Bprod%3A2222%5D`, `prod%3A`), IPv6 bracket stripping, binary / non-UTF-8 rejection (including invalid byte past 8 KiB), >1 MiB truncation refusal, special-file refusal without reading, directory listing (non-empty + empty + `SkipDirectoryListing`), missing-file error propagation (`Process exited with code 66:` + `os.ErrNotExist`), bare `ssh://` host index (configured + empty), host-less path rejection (`ssh:///etc/hosts`), malformed/empty/zero port & password/empty-username/bad-percent-escape authority rejections, query/fragment rejections vs `%3F` literal, and host autocomplete.
- `test/internal-urls/ssh-home-paths.test.ts`: `/~/...` and `/~` remote `$HOME` resolution (caching per account and port, not caching failed lookups), literal `/%7E/` and `/~%2F`, `/~user` rejection, root HOME `/` joining without `//`, remote parent traversal `/~/../`, and shell metacharacter literal filenames.
- Fake-`ssh` script integration tests (no network, no real `~/.ssh`): ControlMaster lifecycle (`-G`, `-O check`, `-M -N -f`, `-O exit` on `lifecycle.End`), user-owned `ControlPath` never exited, cancelled `ssh -G` not cached, Windows client fallback (no ControlMaster flags), temp-rooted fallback control/cache dir symlink hardening with empty `HOME`, hostile host rejection before spawning `ssh`, remote read + line ranges via `urls.Split`, remote dir listing + `ReadDirectory` (grep/search traversal), remote write (new nested dirs, in-place overwrite preserving permissions, trailing-slash refusal, directory overwrite refusal with temp cleanup), timeout enforcement with open-pipe descendants (`execSSH` and `ListRemoteDir`), and `PromptDoc`/`PromptReference` PATH gating.

## Not Ported

| Feature / File | Upstream location | Reason |
|---|---|---|
| `sshfs` FUSE mounting (`mountRemote`, `unmountRemote`, `unmountAll`, `hasSshfs`, `isMounted`) | `packages/coding-agent/src/ssh/sshfs-mount.ts:1-148` | Requires host FUSE kernel support and external `sshfs`/`fusermount` binaries; out of scope for `ssh://` remote file I/O. |
| `connection-manager` cache management helpers (`invalidateHostMetadata`, `closeConnection`, `getHostInfo`, `getHostInfoForHost`, `getCachedHostInfoSync`) | `packages/coding-agent/src/ssh/connection-manager.ts:643-696,807-830` | Consumed by `SshTool` startup prompt summary, not by `ssh://` protocol operations. `Manager.CloseSession` and `Manager.CloseAllConnections` cover lifecycle teardown. |
| Standalone `ssh` tool (`SshTool`) | `packages/coding-agent/src/tools/ssh.ts` | Interactive `/ssh` slash command (`add`, `list`, `remove`, `help`) and `ssh.json` config writer ARE ported in `command.go`; standalone `SshTool` is not ported. |
| `ssh://` remote-mirror hashline `edit` (`remote-mirror.ts`) | `packages/coding-agent/src/edit/remote-mirror.ts` | Owned by `internal/files` edit pipeline (`ssh://` declares `Write.Via = "handler"` for whole-file `write`, matching `ssh-protocol.ts:270`). |
| `InternalUrlFilesystem` native Rust VFS callback | `packages/coding-agent/src/internal-urls/url-filesystem.ts` | PiG has no embedded Rust shell VFS; Go `internal/search` traverses `ssh://` directories via `urls.DirectoryReader` (`Handler.ReadDirectory`). |
| Windows `compatShell` execution dispatch (`executeSSH`) | `packages/coding-agent/src/ssh/connection-manager.ts:620-645` (probing IS ported; command execution wrapper is part of `SshTool`) | `ssh://` explicitly refuses Windows hosts (`file-transfer.ts:39-43`); `HostInfo.CompatShell`/`CompatEnabled` probing is preserved for parity. |
