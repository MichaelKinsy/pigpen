package settings

import (
	"encoding/json"
	"fmt"
	"os"
)

// Access is what the settings file lets remote clients do beyond talking to the session. Every
// capability is off unless the file turns it on; a file without these keys grants nothing.
type Access struct {
	// AllowedOrigins are browser origins that may connect (a request without an Origin header,
	// from a native client, is always allowed).
	AllowedOrigins []string
	// Filesystem: browse, read, write and watch files. Roots default to the session's working
	// directory; Unrestricted lifts the confinement and must be stated.
	Filesystem struct {
		Enabled      bool
		Roots        []string
		Unrestricted bool
	}
	// Terminals: shells on this machine, claimed by the client that starts them.
	Terminals struct{ Enabled bool }
	// AllowSessionDeletion lets a client delete other sessions' files. The running session can
	// never be deleted by a client.
	AllowSessionDeletion bool
}

// LoadAccess reads the optional capability keys of the settings file at path. The file must exist
// (LoadDirect creates it); keys of the wrong type are errors naming the file.
func LoadAccess(path string) (Access, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Access{}, err
	}
	var stored struct {
		AllowedOrigins []string `json:"allowedOrigins"`
		Filesystem     *struct {
			Enabled      bool     `json:"enabled"`
			Roots        []string `json:"roots"`
			Unrestricted bool     `json:"unrestricted"`
		} `json:"filesystem"`
		Terminals *struct {
			Enabled bool `json:"enabled"`
		} `json:"terminals"`
		AllowSessionDeletion bool `json:"allowSessionDeletion"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return Access{}, &Error{path, fmt.Sprintf("a capability setting has the wrong type (%v)", err)}
	}
	var out Access
	out.AllowedOrigins = stored.AllowedOrigins
	if stored.Filesystem != nil {
		out.Filesystem.Enabled = stored.Filesystem.Enabled
		out.Filesystem.Roots = stored.Filesystem.Roots
		out.Filesystem.Unrestricted = stored.Filesystem.Unrestricted
	}
	if stored.Terminals != nil {
		out.Terminals.Enabled = stored.Terminals.Enabled
	}
	out.AllowSessionDeletion = stored.AllowSessionDeletion
	return out, nil
}
