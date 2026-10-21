// Package ui is the Go port of the interactive questionnaire of @juicesharp/rpiv-ask-user-question 2.11.0: the
// state layer (state/) and the view layer (view/) of the original. It renders to terminal lines and is driven by
// keystrokes; it knows nothing of the PiG SDK (the root package wires it to Context.Custom).
//
// Mapping rules, so that files written apart agree:
//
//   - One Go file per TypeScript file, named after it with `/` and `-` turned into `_`
//     (state/state-reducer.ts is state_reducer.go, view/components/tab-bar.ts is tab_bar.go,
//     view/components/preview/preview-pane.ts is preview_pane.go). A TypeScript file's tests are its twin file
//     (state_reducer_test.go).
//   - An exported TypeScript identifier keeps its name in Go with the first letter upper-cased (QuestionnaireState,
//     WrappingSelectItem, routeKey is RouteKey). A property of a props or state object keeps its name the same way
//     (selectedIndex is SelectedIndex). A TypeScript member that was not exported stays unexported in Go.
//   - TypeScript `T | undefined` is a pointer or an explicit (value, ok) pair, never a zero value that could be
//     confused with a real one: the TypeScript tests check `undefined` and the Go twins check nil.
//   - `ReadonlyMap<number, X>` is map[int]X, `ReadonlySet<number>` is map[int]bool (treated as immutable: copy to
//     change). Indexes and offsets are Go ints. Offsets into a string are UTF-16 code units wherever the original
//     indexes a JavaScript string (use the jsstring helpers of components/pitui).
//   - The original's discriminated unions are a struct with a Kind string and the union's fields; the kinds are the
//     TypeScript strings.
//   - Components implement Component (Render and Invalidate, from components/pitui). StatefulView adds SetProps.
package ui
