package ohmyhermes

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/delegation"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
)

var (
	globalHooksOnce sync.Once
	defaultRouter   *RouteRouter
	defaultMemProv  *OmhMemoryProvider
)

// Extension is the PiG factory entry point for oh-my-hermes.
func Extension() *sdk.Extension {
	ext := sdk.New("oh-my-hermes")
	pathsFn := func() OMHPaths { return ResolvePaths("") }

	identities := NewIdentityStore(pathsFn)
	router := NewRouteRouter(pathsFn, identities)
	dags := NewDAGStore(pathsFn)
	teams := NewTeamStore(pathsFn)
	loops := NewLoopStore(pathsFn)
	completions := NewCompletionStore(pathsFn)
	memProv := NewOmhMemoryProvider(pathsFn)
	maestro := NewMaestroStore(pathsFn)

	// Plug into pig-hermes-core global seams once per process (delegating through
	// dynamic pathsFn so $OMH_HOME changes remain respected).
	globalHooksOnce.Do(func() {
		defaultRouter = router
		defaultMemProv = memProv
		delegation.RegisterRouteResolver(defaultRouter)
		memory.RegisterGlobalProvider(defaultMemProv)
	})

	// Register all 9 omh_* tools + EventBus listeners.
	registerDelegateRouteTool(ext, router)
	registerDAGTeamLoopTools(ext, dags, teams, loops)
	registerTodoCompletionMemoryRoleTools(ext, dags, teams, completions, identities, memProv)
	registerMaestroTool(ext, maestro)
	registerLSPTools(ext)

	// Register 5 interactive slash commands, status bar, and OMH system prompt sections.
	surf := newSurfaceState(pathsFn, router, dags, teams, loops)
	registerSurfaceAndHooks(ext, surf)

	return ext
}
