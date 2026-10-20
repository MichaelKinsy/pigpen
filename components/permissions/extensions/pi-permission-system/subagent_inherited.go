package pi_permission_system

// Port of src/authority/inherited-registrations.ts: complete a child node's fact-shaping lookups from its ancestors in the same
// process.
//
// Registrations are node-local (ADR 0012 decision 1); what this adds is the decision's fact-shaping clause: a lookup may cross an
// in-process node boundary, because an extractor produces a path and a formatter produces display text, and neither decides
// anything. It is monotone (without an extractor the gate does not run at all) and carries no authority.
//
// PROCESS MODEL. "In-process" is the whole premise: the original reaches an ancestor's published service through globalThis. In PiG a
// child's extension runs in its own process, where the service locator sees only that process's own services, so the walk finds an
// ancestor only when the ancestor's node lives in the same process. An extractor is a closure and cannot be serialized to another
// process, so there is no filesystem route to port.

// NodeIdentity is this node's own session id, or false when the host exposes none.
type NodeIdentity interface {
	CurrentSessionID() (string, bool)
}

// PermissionsServiceLocator resolves a node's published service by session id; nil is undefined.
type PermissionsServiceLocator func(sessionID string) PermissionsService

// AncestorFinder is the walk the inheriting lookups use: the first non-nil answer pick gives for an ancestor's service.
type AncestorFinder interface {
	FindFirst(pick func(service PermissionsService) any) any
}

// AncestorNodes is this node's ancestors in the current process, nearest first. It binds the three collaborators the walk needs.
type AncestorNodes struct {
	node     NodeIdentity
	registry ParentChainRegistry
	locate   PermissionsServiceLocator
}

// NewAncestorNodes is `new AncestorNodes(node, registry, locate)`.
func NewAncestorNodes(node NodeIdentity, registry ParentChainRegistry, locate PermissionsServiceLocator) *AncestorNodes {
	return &AncestorNodes{node: node, registry: registry, locate: locate}
}

// FindFirst is findFirst: the first non-nil answer pick gives for an ancestor's service, or nil. The walk is transitive, because an
// exclusion applies to every descendant equally. A hop that published no service is stepped over rather than ending the walk, and each
// hop is resolved through the locator per call, never cached, so a torn-down node simply stops answering. A malformed chain must not
// hang a tool call: a node is consulted once.
func (a *AncestorNodes) FindFirst(pick func(service PermissionsService) any) any {
	sessionID, ok := a.node.CurrentSessionID()
	visited := map[string]bool{}
	for ok && !visited[sessionID] {
		visited[sessionID] = true
		info, found := a.registry.Get(sessionID)
		if !found || info.ParentSessionID == nil {
			return nil
		}
		parent := *info.ParentSessionID
		if service := a.locate(parent); service != nil {
			if answer := pick(service); answer != nil {
				return answer
			}
		}
		sessionID = parent
	}
	return nil
}

// InheritingToolAccessExtractorLookup is an extractor lookup that falls back to this node's ancestors. The local registry always
// wins, so a child that registers its own extractor for a tool keeps it.
type InheritingToolAccessExtractorLookup struct {
	local     ToolAccessExtractorLookup
	ancestors AncestorFinder
}

// NewInheritingToolAccessExtractorLookup is `new InheritingToolAccessExtractorLookup(local, ancestors)`.
func NewInheritingToolAccessExtractorLookup(local ToolAccessExtractorLookup, ancestors AncestorFinder) *InheritingToolAccessExtractorLookup {
	return &InheritingToolAccessExtractorLookup{local: local, ancestors: ancestors}
}

// Resolve implements ToolAccessExtractorLookup.
func (l *InheritingToolAccessExtractorLookup) Resolve(toolName string) *ResolvedToolAccessExtractor {
	if own := l.local.Resolve(toolName); own != nil {
		return own
	}
	inherited := l.ancestors.FindFirst(func(service PermissionsService) any {
		if e := service.GetToolAccessExtractor(toolName); e != nil {
			return e
		}
		return nil
	})
	if e, ok := inherited.(ToolAccessExtractor); ok {
		return &ResolvedToolAccessExtractor{Extractor: e, Origin: RegistrationInherited}
	}
	return nil
}

// InheritingToolInputFormatterLookup is a formatter lookup that falls back to this node's ancestors. Unlike the extractor lookup it
// reports no origin, because its consumer records none.
type InheritingToolInputFormatterLookup struct {
	local     ToolInputFormatterLookup
	ancestors AncestorFinder
}

// NewInheritingToolInputFormatterLookup is `new InheritingToolInputFormatterLookup(local, ancestors)`.
func NewInheritingToolInputFormatterLookup(local ToolInputFormatterLookup, ancestors AncestorFinder) *InheritingToolInputFormatterLookup {
	return &InheritingToolInputFormatterLookup{local: local, ancestors: ancestors}
}

// Get implements ToolInputFormatterLookup.
func (l *InheritingToolInputFormatterLookup) Get(toolName string) ToolInputFormatter {
	if own := l.local.Get(toolName); own != nil {
		return own
	}
	inherited := l.ancestors.FindFirst(func(service PermissionsService) any {
		if f := service.GetToolInputFormatter(toolName); f != nil {
			return f
		}
		return nil
	})
	f, _ := inherited.(ToolInputFormatter)
	return f
}
