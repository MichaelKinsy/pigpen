package pi_subagents

// The parent-session marker every child carries (PIG_PARENT_SESSION): the id of the session that delegated it. It is
// plain provenance — this plugin declares no policy and knows nothing of its consumers — and it is what child-side
// tooling such as pig-bg-tasks (../pig-bg-tasks/DELEGATED-WORKSPACES.md) uses to orient toward the session that spawned
// the child: its system socket, its approval relay, its workspace policy. Nested children re-declare at every level:
// the marker names the immediate parent's session, and childEnv drops an inherited value.

import "sync"

// parentSession is this session's id (sdk.Context.SessionManager().GetSessionID()), captured at session start.
var parentSession sync.Map // key "id" -> string

func setParentSessionID(id string) {
	if id == "" {
		parentSession.Delete("id")
		return
	}
	parentSession.Store("id", id)
}

func parentSessionIDFor() string {
	if v, ok := parentSession.Load("id"); ok {
		s, _ := v.(string)
		return s
	}
	return ""
}
