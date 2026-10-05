// Command pig-doctor finds and safely fixes cruft in a PiG setup.
//
//	pig-doctor [check]   read-only report (default)
//	pig-doctor fix       explicit, confirmed per group; moves removals to ~/.pig-doctor-backup
//	pig-doctor restore   TIMESTAMP   put a backup back
//	pig-doctor backups   list backups
package main

import (
	"os"

	"github.com/MichaelKinsy/pigpen/pig-doctor/doctor"
)

func main() {
	os.Exit(doctor.Run(os.Args[1:], doctor.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, os.Environ()))
}
