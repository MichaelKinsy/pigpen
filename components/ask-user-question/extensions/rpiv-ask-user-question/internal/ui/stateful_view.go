package ui

import tui "github.com/MichaelKinsy/pigpen/components/pitui"

// StatefulView is the generic prop-driven component contract. upstream: view/stateful-view.ts StatefulView<P>.
type StatefulView[P any] interface {
	tui.Component
	SetProps(props P)
}

// ActiveView is the discriminated focus union: "notes" | "options" | "submit".
type ActiveView string

const (
	ActiveViewNotes   ActiveView = "notes"
	ActiveViewOptions ActiveView = "options"
	ActiveViewSubmit  ActiveView = "submit"
)
