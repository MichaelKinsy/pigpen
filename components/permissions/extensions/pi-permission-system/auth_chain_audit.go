package pi_permission_system

import "sync"

// Port of src/authority/authorizer-chain-audit.ts: report a configured authorizerChain link this node's registry could not resolve.
// The review entry is the auditor's durable record and is written for every skipped ask; the visible warning is for the operator
// about to answer the prompt the link should have answered, so it is latched: one per configured name.

// AuthorizerChainAuditLog is the narrow log seam this audit needs: a durable record and a warning.
type AuthorizerChainAuditLog interface {
	Review(event string, details *jsObject)
	Warn(message string)
}

// UnregisteredLink is a configured chain link this node's registry could not resolve.
type UnregisteredLink struct {
	// Name is the operator-configured name, exactly as written in authorizerChain.
	Name string
	// RequestID is the ask whose chain resolution skipped it.
	RequestID string
}

// UnregisteredLinkAuditor is the audit seam AuthorizerSelection drives when it skips a name.
type UnregisteredLinkAuditor interface {
	AuditUnregisteredLink(link UnregisteredLink)
}

// UnregisteredLinkMessage is the agent-facing text for a configured link that is not registered here.
func UnregisteredLinkMessage(name string) string {
	return "pi-permission-system: authorizerChain names \"" + name + "\", but no link with " +
		"that name is registered in this session, so this ask is being decided " +
		"without it. Most often the extension providing the link is not loaded " +
		"here (a subagent child's excludedExtensionPackages does this); it may " +
		"also have failed to load, or declined to register because it has no " +
		"configuration of its own. Every skipped ask is recorded in the " +
		"permission review log as authorizer_chain_unregistered_link."
}

// AuthorizerChainAudit records each skipped chain link, and warns the operator once per name. The latch has no re-arm hook: the
// extension factory is re-invoked per session generation, which builds a fresh audit.
type AuthorizerChainAudit struct {
	log    AuthorizerChainAuditLog
	mu     sync.Mutex
	warned map[string]bool
}

// NewAuthorizerChainAudit is `new AuthorizerChainAudit(log)`.
func NewAuthorizerChainAudit(log AuthorizerChainAuditLog) *AuthorizerChainAudit {
	return &AuthorizerChainAudit{log: log, warned: map[string]bool{}}
}

// AuditUnregisteredLink implements UnregisteredLinkAuditor.
func (a *AuthorizerChainAudit) AuditUnregisteredLink(link UnregisteredLink) {
	entry := newObject()
	entry.set("requestId", link.RequestID)
	entry.set("name", link.Name)
	a.log.Review("authorizer_chain_unregistered_link", entry)
	a.mu.Lock()
	if a.warned[link.Name] {
		a.mu.Unlock()
		return
	}
	a.warned[link.Name] = true
	a.mu.Unlock()
	a.log.Warn(UnregisteredLinkMessage(link.Name))
}
