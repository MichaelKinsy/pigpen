package pi_subagents

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// What the mission files need of shared/atomic-json.ts and shared/file-system-retry.ts: a private atomic JSON write and the
// retry ladder the state lock indexes by attempt. (Another slice ports those modules whole; this is the part the missions call.)

const fsRetryMaxTotalMsEnv = "PI_SUBAGENT_FS_RETRY_MAX_TOTAL_MS"

var baseFileSystemRetryDelaysMs = []int{10, 25, 50, 100, 200, 500, 1000, 2000, 4000}

// fileSystemRetryDelays is DEFAULT_FILE_SYSTEM_RETRY_DELAYS_MS: the ladder, its sleeps clamped to the optional total budget
// (the length stays, because the state lock treats running off its end as a timeout).
func fileSystemRetryDelays() []int {
	raw := strings.TrimSpace(os.Getenv(fsRetryMaxTotalMsEnv))
	budget, err := strconv.Atoi(raw)
	if raw == "" || err != nil || budget < 0 {
		return baseFileSystemRetryDelaysMs
	}
	spent := 0
	out := make([]int, len(baseFileSystemRetryDelaysMs))
	for i, d := range baseFileSystemRetryDelaysMs {
		allowed := d
		if budget-spent < allowed {
			allowed = budget - spent
		}
		if allowed < 0 {
			allowed = 0
		}
		spent += allowed
		out[i] = allowed
	}
	return out
}

func waitForFileSystemRetry(delayMs int) {
	if delayMs > 0 {
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
	}
}

// isRetryableFileSystemError is EACCES, EBUSY and EPERM.
func isRetryableFileSystemError(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EBUSY)
}

// writePrivateAtomicJSON is writePrivateAtomicJson: JSON.stringify(payload, null, 2) to a temporary file of mode 0600 beside
// the target, then a rename (retried on the errors Windows gives a file another process holds).
func writePrivateAtomicJSON(filePath string, payload any) error {
	retry := runtime.GOOS == "windows"
	delays := fileSystemRetryDelays()
	dir := filepath.Dir(filePath)
	mkdir := func() error { return os.MkdirAll(dir, 0o777) }
	if err := withRetry(mkdir, retry, delays); err != nil {
		return err
	}
	temp := filepath.Join(dir, atomicTempName(filePath))
	writeErr := os.WriteFile(temp, []byte(marshalJSON(payload, "  ")), 0o600)
	if writeErr == nil {
		writeErr = withRetry(func() error { return os.Rename(temp, filePath) }, retry, delays)
	}
	cleanupErr := os.Remove(temp)
	if errors.Is(cleanupErr, fs.ErrNotExist) {
		cleanupErr = nil
	}
	if writeErr != nil {
		return writeErr
	}
	return cleanupErr
}

func withRetry(op func() error, retry bool, delays []int) error {
	for attempt := 0; ; attempt++ {
		err := op()
		if err == nil || !retry || attempt >= len(delays) || !isRetryableFileSystemError(err) {
			return err
		}
		waitForFileSystemRetry(delays[attempt])
	}
}

func atomicTempName(filePath string) string {
	suffix := "." + strconv.Itoa(os.Getpid()) + "." + strconv.FormatInt(time.Now().UnixMilli(), 10) + "." + strings.ReplaceAll(missionUUID()[:13], "-", "") + ".tmp"
	base := filepath.Base(filePath)
	if preferred := "." + base + suffix; len(preferred) <= 255 {
		return preferred
	}
	return "." + sha256Hex(base) + suffix
}
