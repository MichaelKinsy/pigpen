//go:build linux || darwin

package posthorse

import (
	"math"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The identity of a file is what libuv/Node reports: milliseconds with the nanosecond fraction, the inode and device
// as numbers, the change time from the stat record, the access time of utimes.
func TestNoteStat_identityMatchesTheStatRecord(t *testing.T) {
	dir := nsRealTemp(t)
	path := filepath.Join(dir, "f.md")
	forkMust(t, os.WriteFile(path, []byte("12345"), 0o644))
	access := time.Unix(1700000000, 111111111)
	modified := time.Unix(1700000001, 123456789)
	forkMust(t, os.Chtimes(path, access, modified))
	info, err := os.Stat(path)
	forkMust(t, err)
	st := info.Sys().(*syscall.Stat_t)
	id := fileIdentity(path, info)
	if id.size != 5 || id.ino != float64(st.Ino) || id.dev != float64(st.Dev) {
		t.Fatalf("identity %+v vs stat %+v", id, st)
	}
	if math.Abs(id.mtimeMs-1700000001123.456789) > 0.001 {
		t.Fatalf("mtimeMs %v keeps no sub-millisecond fraction", id.mtimeMs)
	}
	ctime := ctimeOf(st)
	if math.Abs(id.ctimeMs-(float64(ctime.Unix())*1e3+float64(ctime.Nanosecond())/1e6)) > 0.001 {
		t.Fatalf("ctimeMs %v, stat %v", id.ctimeMs, ctime)
	}
	if got := accessTime(info); !got.Equal(access) {
		t.Fatalf("accessTime %v, want %v", got, access)
	}
}
