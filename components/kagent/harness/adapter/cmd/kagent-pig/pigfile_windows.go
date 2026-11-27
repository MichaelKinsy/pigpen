//go:build windows

package main

import (
	"fmt"
	"os"
)

// checkPigFile accepts a regular file. Windows files carry no executable bit and no uid, so the owner check of the
// unix build has no equivalent here; the rule that pig is never found on PATH and never lives under the durable data
// directory still holds. The kagent harness runs in Linux Actors; this build exists so every Pigpen Go module compiles
// and is scanned for every target a Piglet can build for.
func checkPigFile(info os.FileInfo, resolved string) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", resolved)
	}
	return nil
}
