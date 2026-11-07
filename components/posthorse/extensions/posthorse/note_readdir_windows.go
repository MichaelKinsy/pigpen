//go:build windows

package posthorse

import (
	"io/fs"
	"os"
)

// readDirNode is fs.promises.readdir(dir, { withFileTypes: true }): libuv's scandir keeps the order
// the file system returns on Windows (NTFS order; the sort of uv__fs_scandir_sort is Unix-only), so
// the directory is read with File.ReadDir, which does not sort, and not with os.ReadDir.
func readDirNode(dir string) ([]fs.DirEntry, error) {
	file, err := os.Open(dir)
	if err != nil {
		return nil, nodeFsError(err, "scandir", dir)
	}
	defer file.Close()
	entries, err := file.ReadDir(-1)
	if err != nil {
		return nil, nodeFsError(err, "scandir", dir)
	}
	return entries, nil
}
