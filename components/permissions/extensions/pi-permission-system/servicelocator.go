package pi_permission_system

import "sync"

// Port of src/service.ts: the cross-extension service surface (PermissionsService), the process-global session-keyed locator
// and its publish / get / unpublish accessors.
//
// The original keeps the slot on `globalThis` under a `Symbol.for()` key, because every Pi extension load gets its own module
// copy. A Go extension is one package in one process (or, in a PiG host, one process of its own): the process-global map here is
// shared by every node built from this package in the process, which is exactly the set the original's slot reaches in an
// in-process subagent tree. A node in another process shares nothing but the filesystem channel (see PORT.md, "What the Go
// process model cannot reproduce").

// PermissionsService is the public interface exposed to other extensions through GetPermissionsService. Each instance belongs to
// one node, and its three registration surfaces are read by that node alone: extractors and formatters by its own gates, chain
// links by its own chain. An agent name is a *string (nil is undefined), a value likewise.
type PermissionsService interface {
	// PermissionQuery is the narrow read-only projection: CheckPermission and GetToolPermission.
	PermissionQuery
	// IsToolFullyDenied reports whether every value under a tool's surface resolves to deny (the question to ask before withholding a
	// tool from a child session's tool list; GetToolPermission reports only the surface's own catch-all).
	IsToolFullyDenied(toolName string, agentName *string) bool
	// RegisterToolInputFormatter registers a preview formatter for one tool name; a second registration for the same name fails. The
	// returned disposer unregisters it.
	RegisterToolInputFormatter(toolName string, formatter ToolInputFormatter) (dispose func(), err error)
	// RegisterToolAccessExtractor registers the path extractor for one tool name; a second registration for the same name fails.
	RegisterToolAccessExtractor(toolName string, extractor ToolAccessExtractor) (dispose func(), err error)
	// GetToolAccessExtractor is the extractor registered on this node for toolName, nil when it has none. A fact-shaping registry:
	// meant to be read across a node boundary (ADR 0012 decision 1).
	GetToolAccessExtractor(toolName string) ToolAccessExtractor
	// GetToolInputFormatter is the formatter registered on this node for toolName, nil when it has none.
	GetToolInputFormatter(toolName string) ToolInputFormatter
	// RegisterAuthorizer registers a named live-authority chain link (ADR 0007 section 4). Registration alone grants no authority:
	// the link decides nothing until the operator names it in authorizerChain.
	RegisterAuthorizer(name string, authorize AuthorizeFunc) (dispose func(), err error)
}

var (
	sessionServicesMu sync.Mutex
	sessionServices   = map[string]PermissionsService{}
)

// PublishPermissionsService publishes service as the service of the node whose session is sessionID (ADR 0012 decision 2).
func PublishPermissionsService(sessionID string, service PermissionsService) {
	sessionServicesMu.Lock()
	sessionServices[sessionID] = service
	sessionServicesMu.Unlock()
}

// GetPermissionsService is the service belonging to the node whose session is sessionID, nil when that node has published none.
// Resolve per use rather than caching the reference. (The original also answers undefined, and warns once, for a caller that passes
// no session id; a Go string is always present, so that case cannot arise.)
func GetPermissionsService(sessionID string) PermissionsService {
	sessionServicesMu.Lock()
	defer sessionServicesMu.Unlock()
	return sessionServices[sessionID]
}

// UnpublishPermissionsService removes the sessionID entry, but only when it still holds service (identity compare-and-delete), so a
// superseded /reload generation's late shutdown cannot wipe the new generation's freshly published service.
func UnpublishPermissionsService(sessionID string, service PermissionsService) {
	sessionServicesMu.Lock()
	defer sessionServicesMu.Unlock()
	if sessionServices[sessionID] == service {
		delete(sessionServices, sessionID)
	}
}
