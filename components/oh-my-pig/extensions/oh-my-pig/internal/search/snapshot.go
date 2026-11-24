package search

import (
	"os"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

// maxSnapshotFileBytes mirrors MAX_SNAPSHOT_FILE_BYTES in crates/pi-edit/src/store.rs:23.
const maxSnapshotFileBytes = 4 * 1024 * 1024

// snapshotTagForFile reads a host file (if <= maxSnapshotFileBytes), records in the shared edit store that
// lines were displayed for it, and returns its 4-hex uppercase snapshot tag. Returns "" on unreadable or
// oversized files. Recording is what makes a grep hit an anchor edit accepts without a re-read.
// upstream: packages/coding-agent/src/edit/store.ts recordFullySeenRows; crates/pi-edit/src/store.rs.
func snapshotTagForFile(absPath string, lines []int) string {
	info, err := os.Stat(absPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSnapshotFileBytes {
		return ""
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ""
	}
	_, rest := hashline.StripBOM(string(data))
	return hashline.Default().RecordSnapshotWithSeenLines(absPath, hashline.NormalizeToLF(rest), lines)
}
