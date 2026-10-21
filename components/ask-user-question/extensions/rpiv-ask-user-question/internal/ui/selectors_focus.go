package ui

// SelectActiveView is the discriminated focus selector. Priority: notes > submit > options.
// upstream: state/selectors/focus.ts selectActiveView.
func SelectActiveView(notesVisible bool, currentTab, totalQuestions int) ActiveView {
	if notesVisible {
		return ActiveViewNotes
	}
	if currentTab == totalQuestions {
		return ActiveViewSubmit
	}
	return ActiveViewOptions
}
