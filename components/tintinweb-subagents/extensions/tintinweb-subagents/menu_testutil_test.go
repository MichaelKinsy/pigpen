package tintinweb_subagents

import (
	"sync"
)

// fakeMenuUI is the scripted ctx.ui of the menu tests: the stubs of the original's tests (`select` returns whichever
// label the test says, `confirm` a fixed answer), recording every call.
type fakeMenuUI struct {
	mu sync.Mutex
	// pick answers a select; nil cancels.
	pick func(title string, options []string) (string, bool)
	// confirm answers every confirm.
	confirm func(title, message string) bool
	input   func(title, placeholder string) (string, bool)
	editor  func(title, prefill string) (string, bool)
	custom  func(factory menuFactory, opts *menuOverlay) (any, bool)

	selects  []selectCall
	confirms []confirmCall
	inputs   []selectCall
	editors  []selectCall
	notes    []noteCall
	customs  int
}

type selectCall struct {
	Title   string
	Options []string
}

type confirmCall struct{ Title, Message string }
type noteCall struct{ Message, Level string }

func (u *fakeMenuUI) Select(title string, options []string) (string, bool, error) {
	u.mu.Lock()
	u.selects = append(u.selects, selectCall{title, append([]string(nil), options...)})
	pick := u.pick
	u.mu.Unlock()
	if pick == nil {
		return "", false, nil
	}
	s, ok := pick(title, options)
	return s, ok, nil
}

func (u *fakeMenuUI) Confirm(title, message string) (bool, error) {
	u.mu.Lock()
	u.confirms = append(u.confirms, confirmCall{title, message})
	confirm := u.confirm
	u.mu.Unlock()
	if confirm == nil {
		return false, nil
	}
	return confirm(title, message), nil
}

func (u *fakeMenuUI) Input(title, placeholder string) (string, bool, error) {
	u.mu.Lock()
	u.inputs = append(u.inputs, selectCall{title, []string{placeholder}})
	input := u.input
	u.mu.Unlock()
	if input == nil {
		return "", false, nil
	}
	s, ok := input(title, placeholder)
	return s, ok, nil
}

func (u *fakeMenuUI) Editor(title, prefill string) (string, bool, error) {
	u.mu.Lock()
	u.editors = append(u.editors, selectCall{title, []string{prefill}})
	editor := u.editor
	u.mu.Unlock()
	if editor == nil {
		return "", false, nil
	}
	s, ok := editor(title, prefill)
	return s, ok, nil
}

func (u *fakeMenuUI) Notify(message, level string) {
	u.mu.Lock()
	u.notes = append(u.notes, noteCall{message, level})
	u.mu.Unlock()
}

func (u *fakeMenuUI) Custom(factory menuFactory, opts *menuOverlay) (any, bool) {
	u.mu.Lock()
	u.customs++
	custom := u.custom
	u.mu.Unlock()
	if custom == nil {
		return nil, false
	}
	return custom(factory, opts)
}

func (u *fakeMenuUI) notified(message, level string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, n := range u.notes {
		if n.Message == message && n.Level == level {
			return true
		}
	}
	return false
}

func (u *fakeMenuUI) noteTexts() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]string, len(u.notes))
	for i, n := range u.notes {
		out[i] = n.Message
	}
	return out
}

// pickIndex answers a select with the option at index i.
func pickIndex(i int) func(string, []string) (string, bool) {
	return func(_ string, options []string) (string, bool) { return options[i], true }
}
