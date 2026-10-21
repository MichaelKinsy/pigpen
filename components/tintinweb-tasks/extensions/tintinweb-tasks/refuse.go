package tintinweb_tasks

import "fmt"

const (
	refusePrefix = "@pi-in-go/pigpen-tintinweb-tasks (Go port) does not support "
	refuseSuffix = " yet. Install the original extension, npm:@tintinweb/pi-tasks@0.9.0, which runs unchanged in PiG."

	// maxSupportedVisible is the most rows the host shows for the pre-rendered widget.
	maxSupportedVisible = 10
)

// refuseUnported reports configuration the port cannot honour, instead of capping it: a maxVisible above
// what the host widget shows. Call with the app lock held.
func (a *app) refuseUnported() error {
	if n, ok := numberOf(a.cfg["maxVisible"]); ok && n > maxSupportedVisible {
		return fmt.Errorf("%smaxVisible above %d%s", refusePrefix, maxSupportedVisible, refuseSuffix)
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
