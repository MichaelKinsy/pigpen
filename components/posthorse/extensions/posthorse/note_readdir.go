//go:build !windows

package posthorse

import (
	"io/fs"
	"os"
)

// readDirNode is fs.promises.readdir(dir, { withFileTypes: true }): libuv's scandir sorts the names
// with strcmp on Unix, which is the order of os.ReadDir. Errors are Node's `scandir`.
func readDirNode(dir string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nodeFsError(err, "scandir", dir)
	}
	return entries, nil
}
