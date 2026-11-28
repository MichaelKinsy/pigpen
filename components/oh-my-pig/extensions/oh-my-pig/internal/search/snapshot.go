package search

import (
	"os"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

// maxSnapshotFileBytes mirrors MAX_SNAPSHOT_FILE_BYTES in crates/pi-edit/src/store.rs:23.
const maxSnapshotFileBytes = 4 * 1024 * 1024

// snapshotTagForFile reads a host file (if <= maxSnapshotFileBytes), records in the session's
// edit store that lines were displayed for it, and returns its 4-hex uppercase snapshot tag.
// Returns "" on unreadable or oversized files, or when store is nil.
// Recording is what makes a grep hit an anchor edit accepts without a re-read.
// upstream: packages/coding-agent/src/edit/store.ts recordFullySeenRows; crates/edit/src/store.rs.
func snapshotTagForFile(store *hashline.Store, absPath string, lines []int) string {
	if store == nil {
		return ""
	}
	info, err := os.Stat(absPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSnapshotFileBytes {
		return ""
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ""
	}
	_, rest := hashline.StripBOM(string(data))
	return store.RecordSnapshotWithSeenLines(absPath, hashline.NormalizeToLF(rest), lines)
}

// snapshotTagForFileOnly computes the file's 4-hex snapshot tag without recording
// seen lines yet (used before head byte-truncation decides which lines survived).
func snapshotTagForFileOnly(absPath string) string {
	info, err := os.Stat(absPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSnapshotFileBytes {
		return ""
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ""
	}
	_, rest := hashline.StripBOM(string(data))
	return hashline.FileHash(hashline.NormalizeToLF(rest))
}
