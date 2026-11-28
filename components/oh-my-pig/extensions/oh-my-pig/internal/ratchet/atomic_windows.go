//go:build windows

package ratchet

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// renameAtomic replaces dst with src on Windows, retrying transient share locks and
// falling back to backup-and-swap when os.Rename fails over an existing target.
// upstream: packages/coding-agent/src/utils/atomic-file.ts:36-85
func renameAtomic(src, dst string) error {
	deadline := time.Now().Add(2 * time.Second)
	delay := 5 * time.Millisecond
	for {
		err := os.Rename(src, dst)
		if err == nil {
			return nil
		}
		if _, statErr := os.Stat(dst); statErr == nil {
			var suffix [6]byte
			_, _ = rand.Read(suffix[:])
			backup := fmt.Sprintf("%s.%d.%s.bak", dst, os.Getpid(), hex.EncodeToString(suffix[:]))
			if mvErr := os.Rename(dst, backup); mvErr == nil {
				if putErr := os.Rename(src, dst); putErr == nil {
					_ = os.Remove(backup)
					return nil
				} else {
					_ = os.Rename(backup, dst)
					err = putErr
				}
			} else if errors.Is(mvErr, fs.ErrNotExist) {
				if putErr := os.Rename(src, dst); putErr == nil {
					return nil
				} else {
					err = putErr
				}
			}
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		delay *= 2
		if delay > 100*time.Millisecond {
			delay = 100 * time.Millisecond
		}
	}
}
