package langsmith

// Port of utils/profile-lock.js (langsmith 0.10.5) and the file primitives of utils/fs.js it uses: an exclusive
// cross-process lock for refreshing OAuth tokens, an atomic-mkdir directory at `<configPath>.oauth.lock.lock` with
// a stale-break heuristic and an owner-checked release.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Timing and I/O of the lock. Variables so tests can shorten the timing and fail the metadata write; nothing else
// assigns them.
var (
	lockPollInterval  = 10 * time.Millisecond // LOCK_POLL_INTERVAL_MS (profile-lock.js:3)
	lockStaleAfterMs  = float64(10_000)       // LOCK_STALE_AFTER_MS (profile-lock.js:4)
	lockWriteMetadata = writeFileAtomic       // the `created_at` write of profile-lock.js:84-92
)

const lockMetadataFile = "created_at" // LOCK_METADATA_FILE (profile-lock.js:5)

// profileNowMs is Date.now() for the profile code; tests replace it.
var profileNowMs = func() int64 { return nowMs() }

// mkdirAll is fs.js mkdir: `mkdir(dir, {recursive: true})`.
func mkdirAll(dir string) error { return os.MkdirAll(dir, 0o777) }

// writeFileAtomic is fs.js writeFileAtomic: write `<path>.tmp` (mode 0600 when created) and rename it over path.
func writeFileAtomic(path string, content []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// pathExists is `existsSync(path)`: true for a file or a directory, symlinks followed.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// mkdirExclusive is fs.js mkdirExclusive: a non-recursive mkdir that fails with an ErrExist error when dir exists.
func mkdirExclusive(dir string) error { return os.Mkdir(dir, 0o700) }

// statMtimeMs is fs.js statMtimeMs: the modification time in milliseconds (fractional), false when stat fails.
func statMtimeMs(path string) (float64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return float64(fi.ModTime().UnixNano()) / 1e6, true
}

// rmRecursive is fs.js rmRecursive: `rm(path, {recursive: true, force: true})`.
func rmRecursive(path string) error { return os.RemoveAll(path) }

// readFileUTF8 is `readFileSync(path, "utf-8")`: invalid UTF-8 becomes U+FFFD, a BOM is kept.
func readFileUTF8(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return decodeUTF8Replacing(b), nil
}

// parseDateMs is `Date.parse(text)`: ok is false for NaN; err is a host failure of the SDK's date parser.
func parseDateMs(text string) (ms float64, ok bool, err error) {
	return sdk.ParseDate(text)
}

// lockMetadataLines is profile-lock.js:13-22: the lines of `<lockDir>/created_at`, ok false when it cannot be read.
func lockMetadataLines(lockDir string) ([]string, bool) {
	text, err := readFileUTF8(filepath.Join(lockDir, lockMetadataFile))
	if err != nil {
		return nil, false
	}
	return strings.Split(text, "\n"), true
}

// lockCreatedAtMs is profile-lock.js:24-33: the first metadata line parsed as a date, else the directory's mtime.
func lockCreatedAtMs(lockDir string) (float64, bool, error) {
	lines, ok := lockMetadataLines(lockDir)
	if ok && lines[0] != "" {
		if t := pfTrim(lines[0]); t != "" {
			ms, valid, err := parseDateMs(t)
			if err != nil {
				return 0, false, err
			}
			if valid {
				return ms, true, nil
			}
		}
	}
	ms, ok := statMtimeMs(lockDir)
	return ms, ok, nil
}

// lockOwner is profile-lock.js:35-41: the second metadata line, trimmed, ok false when absent or blank.
func lockOwner(lockDir string) (string, bool) {
	lines, ok := lockMetadataLines(lockDir)
	if ok && len(lines) >= 2 {
		if o := pfTrim(lines[1]); o != "" {
			return o, true
		}
	}
	return "", false
}

// removeStaleLock is profile-lock.js:42-49.
func removeStaleLock(lockDir string) (bool, error) {
	createdAt, ok, err := lockCreatedAtMs(lockDir)
	if err != nil {
		return false, err
	}
	if !ok || float64(profileNowMs())-createdAt <= lockStaleAfterMs {
		return false, nil
	}
	if err := rmRecursive(lockDir); err != nil {
		return false, err
	}
	return true, nil
}

// oauthRefreshLock is the handle acquireOAuthRefreshLock returns.
type oauthRefreshLock struct {
	lockDir string
	owner   string
}

// release is the handle's release(): remove the lock directory only when it still carries this owner's id.
// A nil handle (acquisition failed) is a no-op, as `lock?.release()` is.
func (l *oauthRefreshLock) release() error {
	if l == nil {
		return nil
	}
	if o, ok := lockOwner(l.lockDir); ok && o == l.owner {
		return rmRecursive(l.lockDir)
	}
	return nil
}

// randomUUIDv4 is `crypto.randomUUID()`.
func randomUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0F | 0x40
	b[8] = b[8]&0x3F | 0x80
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:]), nil
}

// acquireOAuthRefreshLock is profile-lock.js:60-98. deadline is a Date.now()-based timestamp (milliseconds);
// acquisition fails with "timed out acquiring OAuth refresh lock" once it passes. ctx (shutdown) only cuts the
// poll sleep short. Callers treat any error as "skip refresh, use the current token".
func acquireOAuthRefreshLock(ctx context.Context, configPath string, deadline int64) (*oauthRefreshLock, error) {
	lockDir := configPath + ".oauth.lock.lock"
	if parent := filepath.Dir(lockDir); parent != "" {
		if err := mkdirAll(parent); err != nil {
			return nil, err
		}
	}
	owner, err := randomUUIDv4()
	if err != nil {
		return nil, err
	}
	for {
		if err := mkdirExclusive(lockDir); err != nil {
			if !errors.Is(err, fs.ErrExist) {
				return nil, err
			}
			stale, serr := removeStaleLock(lockDir)
			if serr != nil {
				return nil, serr
			}
			if !stale {
				if profileNowMs() >= deadline {
					return nil, errors.New("timed out acquiring OAuth refresh lock")
				}
				wait := min(lockPollInterval, time.Duration(max(0, deadline-profileNowMs()))*time.Millisecond)
				t := time.NewTimer(wait)
				select {
				case <-t.C:
				case <-ctx.Done():
					t.Stop()
					return nil, context.Cause(ctx)
				}
			}
			continue
		}
		meta := jsISOString(profileNowMs()) + "\n" + owner + "\n"
		if err := lockWriteMetadata(filepath.Join(lockDir, lockMetadataFile), []byte(meta)); err != nil {
			if rerr := rmRecursive(lockDir); rerr != nil {
				return nil, rerr
			}
			return nil, err
		}
		break
	}
	return &oauthRefreshLock{lockDir: lockDir, owner: owner}, nil
}
