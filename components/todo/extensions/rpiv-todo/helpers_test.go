package rpiv_todo

import (
	"reflect"
	"testing"
)

func sp(s string) *string { return &s }

// tk builds a pending task; mods adjust it (upstream: the `task()` fixture of the original tests).
func tk(id int, subject string, mods ...func(*task)) task {
	t := task{ID: id, Subject: subject, Status: statusPending}
	for _, m := range mods {
		m(&t)
	}
	return t
}

func withStatus(s string) func(*task)           { return func(t *task) { t.Status = s } }
func withBlockedBy(d ...float64) func(*task)    { return func(t *task) { t.BlockedBy = d } }
func withActiveForm(s string) func(*task)       { return func(t *task) { t.ActiveForm = sp(s) } }
func withDescription(s string) func(*task)      { return func(t *task) { t.Description = sp(s) } }
func withOwner(s string) func(*task)            { return func(t *task) { t.Owner = sp(s) } }
func withMetadata(m map[string]any) func(*task) { return func(t *task) { t.Metadata = m } }

// stateWith mirrors the original's `stateWith`: nextId is one past the highest id.
func stateWith(tasks ...task) taskState {
	max := 0
	for _, t := range tasks {
		if t.ID > max {
			max = t.ID
		}
	}
	return taskState{Tasks: append([]task{}, tasks...), NextID: max + 1}
}

func emptyState() taskState { return taskState{Tasks: []task{}, NextID: 1} }

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", what, got, want)
	}
}

func errOp(msg string) op { return op{Kind: "error", Message: msg} }

type hostError string

func (e hostError) Error() string { return string(e) }

const errHost = hostError("host call failed")

// at is a bounds-safe index: a missing element reads as the zero value, so a wrong result fails the
// assertion that follows instead of panicking the whole test binary.
func at[T any](s []T, i int) T {
	var zero T
	if i < 0 || i >= len(s) {
		return zero
	}
	return s[i]
}
