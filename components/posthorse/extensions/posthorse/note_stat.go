package posthorse

import (
	"io/fs"
	"time"
)

// msOf is how Node derives a Stats `...Ms` field from a timespec (the double sec*1e3 + nsec/1e6).
func msOf(t time.Time) float64 {
	return float64(t.Unix())*1e3 + float64(t.Nanosecond())/1e6
}

// fileIdentity builds the NoteIdentity of a stat result (note-snapshots.ts:32). The platform files
// supply ctime, inode and device (note_stat_*.go).
func fileIdentity(path string, info fs.FileInfo) noteIdentity {
	ctime, ino, dev := platformStat(path, info)
	return noteIdentity{
		size:    float64(info.Size()),
		mtimeMs: msOf(info.ModTime()),
		ctimeMs: msOf(ctime),
		ino:     ino,
		dev:     dev,
	}
}
