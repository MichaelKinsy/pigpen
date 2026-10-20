package pi_permission_system

import "sync"

// Port of src/authority/child-node-audit.ts: report an in-process subagent child that runs with no permission node.
//
// Gating is node-local (ADR 0012 decision 1): each node loads its own instance of this extension and gates its own tool calls. A
// child that loads none has no gate, so every tool in its allowlist runs ungated while the parent's own gating is unaffected. The
// signal is publication: a child that has published nothing by the time a subagent implementation announces it bound has no node.
//
// The review entry is the durable record and is written for every affected child; the visible warning is capped at one per parent
// session, since the cause is a single line of configuration.

// NodePresenceLookup answers whether the node whose session is sessionID published a service.
type NodePresenceLookup func(sessionID string) bool

// ChildNodeAuditLog is the narrow log seam this audit needs (ISP): a durable record and a warning.
type ChildNodeAuditLog interface {
	Review(event string, details *jsObject)
	Warn(message string)
}

// BoundChild is the fields read from the child-bound announcement. A nil ParentSessionID is undefined.
type BoundChild struct {
	// SessionID is the child session id: the key its node would have published under.
	SessionID       string
	ParentSessionID *string
}

// ChildNodeAbsentMessage is the agent-facing text for an unguarded child. The parent cannot tell a deliberate exclusion from a load
// failure, so the message names the likelier cause and admits the other in the same sentence.
func ChildNodeAbsentMessage(childSessionID string) string {
	return "pi-permission-system: subagent child session " + childSessionID + " is " +
		"running with no permission node \u2014 this extension is not loaded in it, so " +
		"its tool calls are not gated there. Most often the package is listed in " +
		"pi-subagents' excludedExtensionPackages; a failure to load this extension " +
		"in the child does the same. Further affected children are recorded in the " +
		"permission review log as child_node_absent."
}

// BoundChildAuditor is the audit seam the child-lifecycle subscription drives (ISP).
type BoundChildAuditor interface {
	AuditBoundChild(child BoundChild)
}

// ChildNodeAudit audits each child that finishes binding, and alarms on one with no node. The warn-once latch has no re-arm hook,
// because the extension factory is re-invoked per session generation. Safe for concurrent use.
type ChildNodeAudit struct {
	hasNode NodePresenceLookup
	log     ChildNodeAuditLog
	mu      sync.Mutex
	warned  bool
}

// NewChildNodeAudit is `new ChildNodeAudit(hasNode, log)`.
func NewChildNodeAudit(hasNode NodePresenceLookup, log ChildNodeAuditLog) *ChildNodeAudit {
	return &ChildNodeAudit{hasNode: hasNode, log: log}
}

// AuditBoundChild implements BoundChildAuditor.
func (a *ChildNodeAudit) AuditBoundChild(child BoundChild) {
	if a.hasNode(child.SessionID) {
		return
	}
	d := newObject()
	d.set("childSessionId", child.SessionID)
	d.set("parentSessionId", strOrNull(child.ParentSessionID))
	a.log.Review("child_node_absent", d)
	a.mu.Lock()
	first := !a.warned
	a.warned = true
	a.mu.Unlock()
	if !first {
		return
	}
	a.log.Warn(ChildNodeAbsentMessage(child.SessionID))
}
