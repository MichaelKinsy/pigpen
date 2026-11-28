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
	kind := tern.KindText
	props := map[string]any{"hidden": true, "role": sessionRole, "text": ""}
	if s.ompRoles {
		// Tern's `.sf-fresh` check allows `spacer` children in `main` (and
		// treats non-notice/non-welcome `text` nodes as transcript content,
		// even when hidden). Using `spacer` preserves the exact id (`pig.session`),
		// `role`, `hidden: true`, and `href` contract while keeping `.sf-fresh` active.
		kind = tern.KindSpacer
		props = map[string]any{"hidden": true, "role": sessionRole}
	}
	if file != "" {
		props["href"] = file
	}
	// Before the first transcript node Tern still has: one it evicted is
	// unknown to it, and an add before it would be rejected.
	s.queue(tern.OpAdd("main", s.liveFrom(0), tern.WireNode{ID: sessionNodeID, K: kind, P: props}))
}
