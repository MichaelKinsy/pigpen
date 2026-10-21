package pi_background_tasks_test

import "testing"

// tw and tskip are twin_test.go's helpers for the twins that need the fake host (package pi_background_tasks_test).
func tw(t *testing.T, file, title string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(title, fn)
}

func tskip(t *testing.T, file, title, reason string) {
	t.Helper()
	t.Run(title, func(t *testing.T) { t.Skip(reason) })
}
