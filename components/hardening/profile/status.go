package profile

import "strings"

// FlagState is one flag in a Status.
type FlagState struct {
	Name string
	On   bool
}

// Status is what /doctor and a Package's status command report. It holds no secret and no path.
type Status struct {
	Package string
	// ProfileFound is whether a profile file was read.
	ProfileFound bool
	Flags        []FlagState
	// LastError is the code of the last load error, or "".
	LastError Code
}

// Status reports each flag as on or off, whether the file was found, and the last error code.
func (f Flags) Status() Status {
	s := Status{Package: f.pkg, ProfileFound: f.found, LastError: f.lastErr}
	for _, n := range FlagNames {
		s.Flags = append(s.Flags, FlagState{Name: n, On: f.On(n)})
	}
	return s
}

// String renders the status on one line: "profile=found credentialFile=off ... last_error=none".
func (s Status) String() string {
	var b strings.Builder
	b.WriteString("profile=")
	if s.ProfileFound {
		b.WriteString("found")
	} else {
		b.WriteString("absent")
	}
	for _, f := range s.Flags {
		b.WriteString(" " + f.Name + "=")
		if f.On {
			b.WriteString("on")
		} else {
			b.WriteString("off")
		}
	}
	b.WriteString(" last_error=")
	if s.LastError == "" {
		b.WriteString("none")
	} else {
		b.WriteString(string(s.LastError))
	}
	return b.String()
}
