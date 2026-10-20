package powerline_footer

import (
	"fmt"
	"os"
	"syscall"
)

// inboxIdentity is the fingerprint of an inbox file that queue/store.ts builds from its bigint stat:
// dev:ino:size:mtimeNs:ctimeNs:mode:uid:gid. The change time is what notices an in-place edit that restores the mtime.
func inboxIdentity(fi os.FileInfo) string {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return genericIdentity(fi)
	}
	return fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d:%d", uint64(st.Dev), uint64(st.Ino), st.Size, st.Mtim.Nano(), st.Ctim.Nano(), st.Mode, st.Uid, st.Gid)
}
