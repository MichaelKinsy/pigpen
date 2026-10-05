package mpvfake

import (
	"fmt"
	"io"
	"net"
	"os"
)

// RunSilent stands in for an mpv that has stopped answering (hung, or stopped
// with SIGSTOP): it opens the socket named by `--input-ipc-server=`, accepts
// connections and reads them, and never replies. The pid goes to the record file
// as Run does. It runs until it is killed.
func RunSilent(args []string) error {
	var path string
	for _, a := range args {
		if len(a) > 19 && a[:19] == "--input-ipc-server=" {
			path = a[19:]
		}
	}
	if path == "" {
		return fmt.Errorf("mpvfake: no --input-ipc-server argument in %q", args)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if rec := os.Getenv("MPVFAKE_RECORD"); rec != "" {
		_ = os.WriteFile(fmt.Sprintf("%s.%d", rec, os.Getpid()), []byte("[]"), 0o600)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() { _, _ = io.Copy(io.Discard, c) }()
	}
}
