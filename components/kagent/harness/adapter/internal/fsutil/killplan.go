package fsutil

// killPlan decides how KillProcessGroup ends a group on Windows. A job object holds its members by handle, so it is
// always used when there is one. taskkill names a pid, and once Wait has released the child its pid can belong to an
// unrelated process, so taskkill runs only for a child that has no job and has not been waited for. The decision is
// here, not in procgroup_windows.go, so that it is tested on every platform.
func killPlan(hasJob, reaped bool) (useJob, useTaskkill bool) {
	return hasJob, !hasJob && !reaped
}
