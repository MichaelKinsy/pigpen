package eq

import (
	"bytes"
	"os"
	"strconv"
	"syscall"
	"time"
)

// killRun kills every process whose environment carries this run's marker, wherever it went (a
// new process group, a new session, re-parented to init). It reads /proc, so it sees only
// processes of the same user, which is all a test run starts.
func killRun(marker string) {
	want := []byte(runMarkerVar + "=" + marker)
	self := os.Getpid()
	// A killed process can leave a child that has not yet run: look again until a pass finds none.
	for pass := 0; pass < 5; pass++ {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return
		}
		found := false
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil || pid == self {
				continue
			}
			env, err := os.ReadFile("/proc/" + e.Name() + "/environ")
			if err != nil {
				continue
			}
			for _, kv := range bytes.Split(env, []byte{0}) {
				if bytes.Equal(kv, want) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
					found = true
					break
				}
			}
		}
		if !found {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
