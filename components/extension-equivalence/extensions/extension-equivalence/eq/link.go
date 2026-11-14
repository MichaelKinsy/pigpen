package eq

import (
	"os"
	"path/filepath"
	"runtime"
)

// linkOrCopy makes link point at target, copying the file when a symlink cannot serve: a Windows
// without the symlink privilege (a CI without Developer Mode) cannot create one, and an
// extensionless copy cannot be found either, since Windows PATH search tries only PATHEXT
// extensions (Go's exec.LookPath among them).
func linkOrCopy(target, link string) error {
	if runtime.GOOS != "windows" {
		return os.Symlink(target, link)
	}
	copyTarget := target
	if _, err := os.Stat(target); err != nil {
		copyTarget = target + ".exe"
	}
	copyLink := link
	if filepath.Ext(copyLink) == "" {
		copyLink += ".exe"
	}
	data, err := os.ReadFile(copyTarget)
	if err != nil {
		return err
	}
	return os.WriteFile(copyLink, data, 0o755)
}
