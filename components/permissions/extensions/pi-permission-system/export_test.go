package pi_permission_system

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

// NewExtensionForTest is Extension plus a view of what the config system holds, for the external tests.
func NewExtensionForTest() (*sdk.Extension, func() ResolvedPolicyPaths) {
	e, st := newExtension()
	return e, func() ResolvedPolicyPaths { return policyPaths{st}.GetResolvedPolicyPaths() }
}
