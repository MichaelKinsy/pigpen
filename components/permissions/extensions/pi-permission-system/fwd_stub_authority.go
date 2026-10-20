package pi_permission_system

// Remaining lane-local stand-ins: the tool-input registries and the service surface (owner: the presentation and service lanes).

// ── types.ts ───────────────────────────────────────────────────────────────

// ── service.ts ─────────────────────────────────────────────────────────────

// PermissionsService is the part of service.ts PermissionsService the cross-node lookups read.
type PermissionsService interface {
	GetToolAccessExtractor(toolName string) ToolAccessExtractor
	GetToolInputFormatter(toolName string) ToolInputFormatter
}
