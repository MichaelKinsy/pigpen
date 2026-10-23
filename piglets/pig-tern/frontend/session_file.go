package frontend

import "github.com/stencil-hq/tern-sdk/go/tern"

// The session file PiG persists to is named in main by one hidden node,
// added before the transcript and never moved, so whoever reads the pane's
// surface (a Tern plugin, Carly's Lua) finds it by id without knowing the
// process behind the pane: its href is the file, and it has none while the
// session is not persisted. The node draws nothing.

// sessionNodeID is the id of that node, and sessionRole its role.
const (
	sessionNodeID = "pig.session"
	sessionRole   = "pig.session"
)

// publishSessionFile queues the ops that make the session node name file:
// an add before the first transcript node the first time, then a set.
func (s *session) publishSessionFile(file string) {
	if s.sessionNode {
		var href any
		if file != "" {
			href = file
		}
		s.queue(tern.OpSet(sessionNodeID, map[string]any{"href": href}))
		return
	}
	s.sessionNode = true
	props := map[string]any{"hidden": true, "role": sessionRole, "text": ""}
	if file != "" {
		props["href"] = file
	}
	// Before the first transcript node Tern still has: one it evicted is
	// unknown to it, and an add before it would be rejected.
	s.queue(tern.OpAdd("main", s.liveFrom(0), tern.WireNode{ID: sessionNodeID, K: "text", P: props}))
}
