package pi_permission_system

// Port of src/service/permissions-service.ts: the in-process implementation of the cross-extension PermissionsService.

// ResolverForService is the resolution surface the service needs: answer a gate-style intent (composing the session ruleset
// internally) and report a tool-level state. *PermissionResolver satisfies it.
type ResolverForService interface {
	Resolve(intent AccessIntent) PermissionCheckResult
	// ResolvePrefixed answers an mcp query's path-values intent (the original's `resolve` takes either kind).
	ResolvePrefixed(intent PathValuesAccessIntent) PermissionCheckResult
	GetToolPermission(toolName string, agentName *string) string
	IsToolFullyDenied(toolName string, agentName *string) bool
}

// PathNormalizerProvider is the narrow session view that hands out the cwd-bound path normalizer.
type PathNormalizerProvider interface {
	GetPathNormalizer() *PathNormalizer
}

// ToolInputFormatterStore is the registry the service registers formatters into and reads them back from.
type ToolInputFormatterStore interface {
	ToolInputFormatterRegistrar
	ToolInputFormatterLookup
}

// ToolAccessExtractorStore is the registry the service registers extractors into and reads them back from.
type ToolAccessExtractorStore interface {
	ToolAccessExtractorRegistrar
	ToolAccessExtractorLookup
}

// LocalPermissionsService is the in-process PermissionsService. It is constructed once in the composition root and backed by the
// single shared resolver and session the gates also use, so service queries and gate-path decisions see the same state. Path-shaped
// surface queries route through the resolver as an access-path intent, so they match the lexical aliases and the canonical
// (symlink-resolved) set the gates do (#503); non-path surfaces stay on the tool intent.
type LocalPermissionsService struct {
	resolver            ResolverForService
	session             PathNormalizerProvider
	formatterRegistry   ToolInputFormatterStore
	accessExtractors    ToolAccessExtractorStore
	authorizerRegistrar AuthorizerRegistrar

	// bashAdvisory is the advisory bash resolver; a field so a twin can assert the delegation as the original's module mock does.
	bashAdvisory func(command string, agentName *string, resolver ScopedPermissionResolver) PermissionCheckResult
}

// NewLocalPermissionsService is `new LocalPermissionsService(resolver, session, formatterRegistry, accessExtractorRegistry,
// authorizerRegistry)`.
func NewLocalPermissionsService(resolver ResolverForService, session PathNormalizerProvider, formatters ToolInputFormatterStore,
	extractors ToolAccessExtractorStore, authorizers AuthorizerRegistrar) *LocalPermissionsService {
	return &LocalPermissionsService{resolver: resolver, session: session, formatterRegistry: formatters, accessExtractors: extractors, authorizerRegistrar: authorizers, bashAdvisory: ResolveBashAdvisoryCheck}
}

// CheckPermission queries the policy for a surface and an optional value and agent name. Bash decomposes at gate parity: a
// chained or nested command is split into its command-pattern units and resolved most restrictive, matching what the enforcement gate
// enforces (#309). A cold parser falls back to the whole-string match.
func (s *LocalPermissionsService) CheckPermission(surface string, value, agentName *string) PermissionCheckResult {
	if surface == "bash" {
		command := ""
		if value != nil {
			command = *value
		}
		return s.bashAdvisory(command, agentName, s.resolver)
	}
	switch intent := BuildAccessIntentForSurface(surface, value, s.session.GetPathNormalizer(), agentName).(type) {
	case PathValuesAccessIntent:
		return s.resolver.ResolvePrefixed(intent)
	case AccessIntent:
		return s.resolver.Resolve(intent)
	}
	panic("permissions service: unknown query intent")
}

// GetToolPermission is a surface's catch-all permission state.
func (s *LocalPermissionsService) GetToolPermission(toolName string, agentName *string) string {
	return s.resolver.GetToolPermission(toolName, agentName)
}

// IsToolFullyDenied reports whether every value under a tool's surface resolves to deny.
func (s *LocalPermissionsService) IsToolFullyDenied(toolName string, agentName *string) bool {
	return s.resolver.IsToolFullyDenied(toolName, agentName)
}

// RegisterToolInputFormatter registers a preview formatter.
func (s *LocalPermissionsService) RegisterToolInputFormatter(toolName string, formatter ToolInputFormatter) (func(), error) {
	return s.formatterRegistry.Register(toolName, formatter)
}

// RegisterToolAccessExtractor registers a path extractor.
func (s *LocalPermissionsService) RegisterToolAccessExtractor(toolName string, extractor ToolAccessExtractor) (func(), error) {
	return s.accessExtractors.Register(toolName, extractor)
}

// GetToolAccessExtractor answers the capability; where it came from (the origin) is the gates' concern, not a caller's.
func (s *LocalPermissionsService) GetToolAccessExtractor(toolName string) ToolAccessExtractor {
	if resolved := s.accessExtractors.Resolve(toolName); resolved != nil {
		return resolved.Extractor
	}
	return nil
}

// GetToolInputFormatter is the formatter registered on this node for toolName, nil for none.
func (s *LocalPermissionsService) GetToolInputFormatter(toolName string) ToolInputFormatter {
	return s.formatterRegistry.Get(toolName)
}

// RegisterAuthorizer registers a named chain link.
func (s *LocalPermissionsService) RegisterAuthorizer(name string, authorize AuthorizeFunc) (func(), error) {
	return s.authorizerRegistrar.Register(name, authorize)
}
