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
	todoMeta := newTodoMetaStore()
	children := newChildSessionRegistry()

	// Plug into pig-hermes-core global seams once per process (delegating through
	// dynamic pathsFn so $OMH_HOME changes remain respected).
	globalHooksOnce.Do(func() {
		defaultRouter = router
		defaultMemProv = memProv
		delegation.RegisterRouteResolver(defaultRouter)
		memory.RegisterGlobalProvider(defaultMemProv)
	})

	// Register all omh_* tools + EventBus listeners.
	registerDelegateRouteTool(ext, router)
	registerDAGTeamLoopTools(ext, dags, teams, loops)
	registerTodoCompletionMemoryRoleTools(ext, dags, teams, completions, identities, memProv, todoMeta)
	registerMaestroTool(ext, maestro)
	registerLSPTools(ext)
	registerExtendedTools(ext, pathsFn, router, dags, teams, loops, completions, memProv)
	registerExtendedCLI(pathsFn)

	// Register interactive slash commands, status bar, OMH system prompt sections, and live enforcement hooks.
	surf := newSurfaceState(pathsFn, router, dags, teams, loops)
	surf.memProv = memProv
	surf.children = children
	registerSurfaceAndHooks(ext, surf)
	registerOrchestrationHooks(ext, surf, dags, todoMeta, children)
	return ext
}
