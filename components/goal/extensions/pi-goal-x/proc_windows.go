//go:build windows

package pi_goal_x

// A lock file on Windows is never judged stale by its process id: only its age counts.
func pidAlive(pid int) bool { return pid > 0 }

func lockPid(string) int { return 0 }
