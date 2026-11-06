package frontend

import (
	"io"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// The session is the terminal PiG reports its program status to (OSC 7501,
// fe.ProgramStatusSession). Tern reads the report on any pane: its pane
// icon shows working, waiting on the user, done or failed, its inbox and
// banners carry a blocked report's question, a finished turn can post a
// system notification, and closing the pane asks first only while the
// last state is working or blocked.
var _ fe.ProgramStatusSession = (*session)(nil)

// ProgramStatus writes PiG's report to the terminal as PiG would write it
// itself, through the terminal writer, so it stays whole between the SDK's
// messages. Under the session lock, it keeps its place among the frames.
// After a fallback PiG's own renderer owns the terminal and reports there,
// so the member writes nothing.
func (s *session) ProgramStatus(report string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.fellBack {
		return
	}
	_, _ = io.WriteString(s.out, report)
}
