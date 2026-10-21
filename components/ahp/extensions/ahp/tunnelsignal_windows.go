//go:build windows

package ahp

import "os"

// Windows cannot deliver a polite signal to a child; Kill is the only stop there.
var stopSignal os.Signal = os.Kill
