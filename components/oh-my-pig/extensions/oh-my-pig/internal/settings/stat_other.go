//go:build !darwin && !linux && !windows

package settings

import "os"

func platformStatExtras(_ os.FileInfo) (dev, ino uint64, ctimeNs int64) {
	return 0, 0, 0
}
