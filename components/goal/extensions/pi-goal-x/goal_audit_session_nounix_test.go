//go:build !unix

package pi_goal_x

import "os"

// Windows has no SIGTERM to ignore, and a pid cannot be probed without a signal: the abort tests still check that the
// prompt returns after abort.
var osSIGTERM = os.Interrupt

func processGone(int) bool { return true }
