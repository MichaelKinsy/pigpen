package ssh

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// upstream: packages/coding-agent/src/ssh/file-transfer.ts,
//           packages/coding-agent/src/tools/read-format.ts (decodeUtf8Text)

const (
	// DefaultTransferTimeout is the per-operation timeout for remote file I/O.
	DefaultTransferTimeout = 30 * time.Second
	// MaxTextFileBytes is the largest remote text file `ssh://` materializes (1 MiB).
	MaxTextFileBytes = 1024 * 1024
	// MaxDirectoryListingBytes is the largest remote directory listing payload (1 MiB).
	MaxDirectoryListingBytes = 1024 * 1024
	// remoteMissingExit is the exit code reserved for "nothing at path" (sysexits EX_NOINPUT).
	remoteMissingExit = 66
)

// RemotePathKind classifies a remote path.
// upstream: file-transfer.ts:212
type RemotePathKind string

const (
	RemotePathFile      RemotePathKind = "file"
	RemotePathDirectory RemotePathKind = "directory"
	RemotePathOther     RemotePathKind = "other"
	RemotePathMissing   RemotePathKind = "missing"
)

// RemoteFileType classifies an entry in a remote directory listing.
// upstream: file-transfer.ts:241-250
type RemoteFileType string

const (
	RemoteTypeFile    RemoteFileType = "file"
	RemoteTypeDir     RemoteFileType = "dir"
	RemoteTypeSymlink RemoteFileType = "symlink"
	RemoteTypeFifo    RemoteFileType = "fifo"
	RemoteTypeSocket  RemoteFileType = "socket"
	RemoteTypeChar    RemoteFileType = "char"
	RemoteTypeBlock   RemoteFileType = "block"
)

var validRemoteFileTypes = map[RemoteFileType]bool{
	RemoteTypeFile:    true,
	RemoteTypeDir:     true,
	RemoteTypeSymlink: true,
	RemoteTypeFifo:    true,
	RemoteTypeSocket:  true,
	RemoteTypeChar:    true,
	RemoteTypeBlock:   true,
}

// RemoteDirEntry is one immediate child returned by ListRemoteDir.
type RemoteDirEntry struct {
	Name     string
	FileType RemoteFileType
}

// RemoteFileReadOptions controls ReadRemoteFile.
type RemoteFileReadOptions struct {
	MaxBytes  int
	Timeout   time.Duration
	SessionID string
}

// RemoteFileReadResult holds raw bytes read from a remote file.
type RemoteFileReadResult struct {
	Bytes     []byte
	Truncated bool
}

// RemoteFileWriteOptions controls WriteRemoteFile.
type RemoteFileWriteOptions struct {
	Timeout   time.Duration
	SessionID string
}

// RemoteOpOptions controls stat/list/home operations.
type RemoteOpOptions struct {
	Timeout   time.Duration
	SessionID string
}

// RemoteNotFoundError wraps a remote missing-file failure (matching ENOENT).
type RemoteNotFoundError struct {
	Msg string
}

func (e *RemoteNotFoundError) Error() string { return e.Msg }

func (e *RemoteNotFoundError) Unwrap() error { return os.ErrNotExist }

// DecodeUTF8Text validates raw bytes as NUL-free, valid UTF-8 text and strips
// a leading UTF-8 BOM (0xEF 0xBB 0xBF), matching Web TextDecoder("utf-8", {fatal:true}).
// Returns ("", false) for binary or non-UTF-8 content.
// upstream: packages/coding-agent/src/tools/read-format.ts:709-717
func DecodeUTF8Text(data []byte) (string, bool) {
	if bytes.IndexByte(data, 0) != -1 {
		return "", false
	}
	if !utf8.Valid(data) {
		return "", false
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	return string(data), true
}

func (m *Manager) transferTimeout(override time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	if m.HelperTimeout > 0 {
		return m.HelperTimeout
	}
	return DefaultTransferTimeout
}

// ensurePosixRemote ensures the connection and returns the verified POSIX
// transfer shell ("sh", "bash", or "zsh"), refusing Windows and non-POSIX hosts.
// upstream: file-transfer.ts:36-50
func (m *Manager) ensurePosixRemote(ctx context.Context, target ConnectionTarget, sessionID string) (string, error) {
	if err := m.EnsureConnection(ctx, target, sessionID); err != nil {
		return "", err
	}
	info, err := m.EnsureHostInfo(ctx, target)
	if err != nil {
		return "", err
	}
	if info.OS == HostOSWindows {
		return "", fmt.Errorf("ssh://: %s is a Windows host; ssh:// supports POSIX remotes only (head/cat/mv) — use `bash` with a remote SSH command for Windows hosts", target.Name)
	}
	if info.TransferShell == "" {
		return "", fmt.Errorf("ssh://: %s has no verified POSIX shell for ssh:// read/write — none of sh/bash/zsh round-tripped a capability probe (use `bash` with a remote SSH command for this host)", target.Name)
	}
	return info.TransferShell, nil
}

// RemoteHomeDirectory resolves and caches the remote account's $HOME.
// upstream: file-transfer.ts:71-90
func (m *Manager) RemoteHomeDirectory(ctx context.Context, target ConnectionTarget, opts RemoteOpOptions) (string, error) {
	if ctx != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	cacheKey := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", target.Name, target.Host, target.Username, target.Port, target.KeyPath)
	m.mu.Lock()
	if cached, ok := m.remoteHomes[cacheKey]; ok {
		m.mu.Unlock()
		return cached, nil
	}
	m.mu.Unlock()

	shell, err := m.ensurePosixRemote(ctx, target, opts.SessionID)
	if err != nil {
		return "", err
	}
	args, err := m.BuildRemoteCommand(ctx, target, WrapInPosixShell(shell, `printf '%s' "$HOME"`), ArgsOptions{})
	if err != nil {
		return "", err
	}
	res, err := m.execSSH(ctx, args, nil, m.transferTimeout(opts.Timeout))
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", &NonZeroExitError{ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	home, valid := DecodeUTF8Text(res.Stdout)
	if !valid || !strings.HasPrefix(home, "/") {
		return "", fmt.Errorf("ssh://: %s did not return an absolute remote HOME", target.Name)
	}
	m.mu.Lock()
	m.remoteHomes[cacheKey] = home
	m.mu.Unlock()
	return home, nil
}

// ReadRemoteFile reads up to opts.MaxBytes+1 raw bytes from a remote path,
// reporting truncation and distinguishing missing paths (RemoteNotFoundError)
// from other remote errors.
// upstream: file-transfer.ts:104-131
func (m *Manager) ReadRemoteFile(ctx context.Context, target ConnectionTarget, remotePath string, opts RemoteFileReadOptions) (RemoteFileReadResult, error) {
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = MaxTextFileBytes
	}
	shell, err := m.ensurePosixRemote(ctx, target, opts.SessionID)
	if err != nil {
		return RemoteFileReadResult{}, err
	}
	p := QuotePosixArgument(remotePath)
	command := fmt.Sprintf(
		"head -c %d %s || { s=$?; if [ ! -e %s ] && [ ! -L %s ]; then exit %d; fi; exit $s; }",
		maxBytes+1, p, p, p, remoteMissingExit,
	)
	args, err := m.BuildRemoteCommand(ctx, target, WrapInPosixShell(shell, command), ArgsOptions{})
	if err != nil {
		return RemoteFileReadResult{}, err
	}
	res, err := m.execSSH(ctx, args, nil, m.transferTimeout(opts.Timeout))
	if err != nil {
		return RemoteFileReadResult{}, err
	}
	if res.ExitCode != 0 {
		stderr := strings.TrimSpace(string(res.Stderr))
		nzErr := &NonZeroExitError{ExitCode: res.ExitCode, Stderr: stderr}
		if res.ExitCode == remoteMissingExit {
			msg := nzErr.Error()
			if stderr == "" {
				msg = fmt.Sprintf("Process exited with code %d:\nNo such file or directory: %s", res.ExitCode, remotePath)
			}
			return RemoteFileReadResult{}, &RemoteNotFoundError{Msg: msg}
		}
		return RemoteFileReadResult{}, nzErr
	}
	raw := res.Stdout
	truncated := len(raw) > maxBytes
	if truncated {
		raw = raw[:maxBytes]
	}
	return RemoteFileReadResult{Bytes: raw, Truncated: truncated}, nil
}

func randomTempSuffix() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// WriteRemoteFile writes content byte-exact to remotePath via staged temp file
// + in-place rewrite (preserving inode/mode on existing regular files) or
// atomic rename (new files / symlinks), refusing directories and special files.
// upstream: file-transfer.ts:165-208
func (m *Manager) WriteRemoteFile(ctx context.Context, target ConnectionTarget, remotePath string, content []byte, opts RemoteFileWriteOptions) error {
	shell, err := m.ensurePosixRemote(ctx, target, opts.SessionID)
	if err != nil {
		return err
	}
	if strings.HasSuffix(remotePath, "/") {
		return errors.New("ssh://: destination is a directory path (trailing '/'); ssh:// write requires a file path")
	}
	dest := QuotePosixArgument(remotePath)
	tmp := QuotePosixArgument(fmt.Sprintf("%s.omp-tmp.%s", remotePath, randomTempSuffix()))
	command := fmt.Sprintf(
		`t=%s; trap 'rm -f -- "$t"' 0; `+
			`mkdir -p -- "$(dirname "$t")" && `+
			`cat > "$t" && { `+
			`if [ -d %s ]; then echo 'ssh://: destination is a directory' >&2; exit 1; `+
			`elif [ -f %s ] && [ ! -L %s ]; then cat "$t" > %s || exit 1; `+
			`elif [ -e %s ] && [ ! -L %s ]; then echo 'ssh://: destination is a special file (not a regular file)' >&2; exit 1; `+
			`else mv "$t" %s; fi; `+
			`}`,
		tmp, dest, dest, dest, dest, dest, dest, dest,
	)
	args, err := m.BuildRemoteCommand(ctx, target, WrapInPosixShell(shell, command), ArgsOptions{AllowStdin: true})
	if err != nil {
		return err
	}
	res, err := m.execSSH(ctx, args, content, m.transferTimeout(opts.Timeout))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return &NonZeroExitError{ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	return nil
}

// StatRemotePath classifies a remote path using POSIX `test`.
// upstream: file-transfer.ts:218-234
func (m *Manager) StatRemotePath(ctx context.Context, target ConnectionTarget, remotePath string, opts RemoteOpOptions) (RemotePathKind, error) {
	shell, err := m.ensurePosixRemote(ctx, target, opts.SessionID)
	if err != nil {
		return RemotePathMissing, err
	}
	p := QuotePosixArgument(remotePath)
	command := fmt.Sprintf("if [ -d %s ]; then echo directory; elif [ -f %s ]; then echo file; elif [ -e %s ]; then echo other; else echo missing; fi", p, p, p)
	args, err := m.BuildRemoteCommand(ctx, target, WrapInPosixShell(shell, command), ArgsOptions{})
	if err != nil {
		return RemotePathMissing, err
	}
	res, err := m.execSSH(ctx, args, nil, m.transferTimeout(opts.Timeout))
	if err != nil {
		return RemotePathMissing, err
	}
	if res.ExitCode != 0 {
		return RemotePathMissing, &NonZeroExitError{ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	switch strings.TrimSpace(string(res.Stdout)) {
	case "directory":
		return RemotePathDirectory, nil
	case "file":
		return RemotePathFile, nil
	case "other":
		return RemotePathOther, nil
	default:
		return RemotePathMissing, nil
	}
}

// ListRemoteDir lists immediate children of remotePath as NUL-delimited
// type/name pairs via POSIX `find ... -prune`, enforcing the 1 MiB cap and
// returning directories first sorted by name.
// upstream: file-transfer.ts:241-291
func (m *Manager) ListRemoteDir(ctx context.Context, target ConnectionTarget, remotePath string, opts RemoteOpOptions) ([]RemoteDirEntry, error) {
	shell, err := m.ensurePosixRemote(ctx, target, opts.SessionID)
	if err != nil {
		return nil, err
	}
	classify := `for p do ` +
		`if [ -L "$p" ]; then t=symlink; elif [ -d "$p" ]; then t=dir; ` +
		`elif [ -f "$p" ]; then t=file; elif [ -p "$p" ]; then t=fifo; ` +
		`elif [ -S "$p" ]; then t=socket; elif [ -c "$p" ]; then t=char; ` +
		`elif [ -b "$p" ]; then t=block; else continue; fi; ` +
		`printf '%s\000%s\000' "$t" "${p#./}"; done`
	script := fmt.Sprintf(
		"cd %s && LC_ALL=C find . ! -name . -prune -exec %s -c %s %s {} +\n",
		QuotePosixArgument(remotePath),
		shell,
		QuotePosixArgument(classify),
		shell,
	)
	args, err := m.BuildRemoteCommand(ctx, target, shell+" -s", ArgsOptions{AllowStdin: true})
	if err != nil {
		return nil, err
	}

	sshBin, err := m.ResolveSSHBinary()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := m.transferTimeout(opts.Timeout)
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, sshBin, args...)
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return nil
	}
	cmd.WaitDelay = sshDrainGrace
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Close stdoutPipe on context cancellation so io.ReadAll unblocks promptly
	// even if a remote/descendant process held stdout open.
	readDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = stdoutPipe.Close()
		case <-readDone:
		}
	}()

	// Read at most MaxDirectoryListingBytes+1 bytes so oversized directories fail fast.
	limited := io.LimitReader(stdoutPipe, MaxDirectoryListingBytes+1)
	rawOut, readErr := io.ReadAll(limited)
	close(readDone)
	oversized := len(rawOut) > MaxDirectoryListingBytes
	if oversized {
		_ = stdoutPipe.Close()
		killProcessGroup(cmd)
		_ = cmd.Wait()
		return nil, errors.New("ssh://: directory listing exceeds the 1 MiB limit; use a narrower directory scope")
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			return nil, &NonZeroExitError{ExitCode: ee.ExitCode(), Stderr: strings.TrimSpace(stderr.String())}
		}
		return nil, waitErr
	}

	if !utf8.Valid(rawOut) {
		return nil, errors.New("ssh://: invalid UTF-8 in directory listing")
	}
	if len(rawOut) == 0 {
		return nil, nil
	}
	fields := strings.Split(string(rawOut), "\x00")
	if fields[len(fields)-1] != "" {
		return nil, errors.New("ssh://: malformed directory listing (expected NUL-delimited type/name pairs)")
	}
	fields = fields[:len(fields)-1]
	if len(fields)%2 != 0 {
		return nil, errors.New("ssh://: malformed directory listing (expected NUL-delimited type/name pairs)")
	}

	entries := make([]RemoteDirEntry, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		ft := RemoteFileType(fields[i])
		name := fields[i+1]
		if !validRemoteFileTypes[ft] || name == "" || strings.Contains(name, "/") || name == "." || name == ".." {
			return nil, errors.New("ssh://: invalid directory entry")
		}
		entries = append(entries, RemoteDirEntry{Name: name, FileType: ft})
	}
	sort.Slice(entries, func(i, j int) bool {
		di := entries[i].FileType == RemoteTypeDir
		dj := entries[j].FileType == RemoteTypeDir
		if di != dj {
			return di
		}
		return compareLocale(entries[i].Name, entries[j].Name) < 0
	})
	return entries, nil
}

func compareLocale(a, b string) int {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		if la < lb {
			return -1
		}
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
