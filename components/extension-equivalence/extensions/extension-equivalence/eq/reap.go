package eq

import (
	"crypto/rand"
	"encoding/hex"
)

// runMarkerVar names the environment variable that tags everything one unit run starts. A mutant
// that removes a port's TERM-to-KILL escalation leaves the port's worker alive after `go test` has
// exited, and the worker sits in a process group and a session of its own, so only the environment
// it inherited says which run it belongs to.
const runMarkerVar = "PIGEQ_RUN"

// newRunMarker returns a value no other run shares (mutants run side by side).
func newRunMarker() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system's random source failing is not recoverable
	}
	return hex.EncodeToString(b)
}
