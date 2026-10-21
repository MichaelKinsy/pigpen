package ui

// Component bindings. upstream: view/component-binding.ts.

// ComponentBinding binds one component to a global selector.
type ComponentBinding[P any] struct {
	Component StatefulView[P]
	Select    GlobalSelector[P]
}

// PerTabBinding binds a per-tab component. Resolve returns nil where the original returns undefined; Predicate
// may be nil.
type PerTabBinding[P any] struct {
	Resolve   func(tab *TabComponents) StatefulView[P]
	Select    PerTabSelector[P]
	Predicate PerTabSelector[bool]
}

// BoundGlobalBinding is a global binding with its props type erased.
type BoundGlobalBinding struct {
	Apply      func(state QuestionnaireState, ctx BindingContext)
	Invalidate func()
}

// BoundPerTabBinding is a per-tab binding with its props type erased.
type BoundPerTabBinding struct {
	Apply func(state QuestionnaireState, ctx PerTabBindingContext)
}

// GlobalBinding binds spec. upstream: globalBinding.
func GlobalBinding[P any](spec ComponentBinding[P]) BoundGlobalBinding {
	return BoundGlobalBinding{
		Apply:      func(state QuestionnaireState, ctx BindingContext) { spec.Component.SetProps(spec.Select(state, ctx)) },
		Invalidate: func() { spec.Component.Invalidate() },
	}
}

// PerTabBindingOf binds spec. upstream: perTabBinding.
func PerTabBindingOf[P any](spec PerTabBinding[P]) BoundPerTabBinding {
	return BoundPerTabBinding{
		Apply: func(state QuestionnaireState, ctx PerTabBindingContext) {
			if spec.Predicate != nil && !spec.Predicate(state, ctx) {
				return
			}
			if v := spec.Resolve(ctx.Tab); v != nil {
				v.SetProps(spec.Select(state, ctx))
			}
		},
	}
}
