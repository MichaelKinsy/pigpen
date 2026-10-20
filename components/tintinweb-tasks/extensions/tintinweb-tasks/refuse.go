package tintinweb_tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	refusePrefix = "@pi-in-go/pigpen-tintinweb-tasks (Go port) does not support "
	refuseSuffix = " yet. Install the original extension, npm:@tintinweb/pi-tasks@0.9.0, which runs unchanged in PiG."

	// maxSupportedVisible is the most rows the host shows for the pre-rendered widget.
	maxSupportedVisible = 10
)

// refuseUnported reports configuration the port cannot honour, instead of capping or ignoring it: a
// maxVisible above what the host widget shows, and a named PI_TASKS list that exists only at the original's
// location (the port keeps named lists under <agent dir>/tasks/). Call with the app lock held.
func (a *app) refuseUnported() error {
	if n, ok := numberOf(a.cfg["maxVisible"]); ok && n > maxSupportedVisible {
		return fmt.Errorf("%smaxVisible above %d%s", refusePrefix, maxSupportedVisible, refuseSuffix)
	}
	if p := a.piTasks; p != "" && p != "off" && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, ".") {
		home, _ := os.UserHomeDir()
		piPath := filepath.Join(home, ".pi", "tasks", p+".json")
		ownPath := filepath.Join(agentDir(), "tasks", p+".json")
		if piPath != ownPath {
			if _, err := os.Stat(piPath); err == nil {
				if _, err := os.Stat(ownPath); err != nil {
					return fmt.Errorf("%sPI_TASKS list %q found at %s: this port keeps named lists in %s, so that list would not be read. Move it there, or install the original extension, npm:@tintinweb/pi-tasks@0.9.0, which runs unchanged in PiG", refusePrefix, p, piPath, ownPath)
				}
			}
		}
	}
	return nil
}

func numberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}
