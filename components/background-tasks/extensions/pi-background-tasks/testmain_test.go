package pi_background_tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestMain lets the test binary stand in for external tools, as the Skill prescribes: a symlink named systemctl or
// systemd-run (or the name in BGT_FAKE_TOOL) re-executes the test binary, which then behaves as that tool, appends its call
// to RESOURCE_PROBE_LOG and exits with the status the test chose.
func TestMain(m *testing.M) {
	base := filepath.Base(os.Args[0])
	if log := os.Getenv("RESOURCE_PROBE_LOG"); log != "" && (base == "systemctl" || base == "systemd-run") {
		status := os.Getenv("RESOURCE_SYSTEMCTL_STATUS")
		if base == "systemd-run" {
			status = os.Getenv("RESOURCE_SYSTEMD_RUN_STATUS")
		}
		code, err := strconv.Atoi(status)
		if err != nil {
			os.Stderr.WriteString("resource-probe-status=missing\n")
			os.Exit(1)
		}
		line, _ := json.Marshal(map[string]any{"command": base, "args": os.Args[1:]})
		f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err == nil {
			f.WriteString(string(line) + "\n")
			f.Close()
		}
		os.Exit(code)
	}
	os.Exit(m.Run())
}
