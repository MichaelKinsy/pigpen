package ui

// upstream: state/selectors/derivations.ts.

// SelectConfirmedIndicator picks the row of the active tab to mark as previously confirmed, or nil.
// upstream: selectConfirmedIndicator.
func SelectConfirmedIndicator(questions []QuestionData, currentTab int, answers map[int]QuestionAnswer, items []WrappingSelectItem) *OptionListConfirmed {
	if currentTab < 0 || currentTab >= len(questions) || questions[currentTab].MultiSelect {
		return nil
	}
	prior, ok := answers[currentTab]
	if !ok {
		return nil
	}
	if prior.Kind == "custom" {
		otherIndex := -1
		for i, it := range items {
			if it.Kind == KindOther {
				otherIndex = i
				break
			}
		}
		if otherIndex < 0 {
			return nil
		}
		override := ""
		if prior.Answer != nil {
			override = *prior.Answer
		}
		return &OptionListConfirmed{Index: otherIndex, LabelOverride: &override}
	}
	if prior.Kind != "option" || prior.Answer == nil {
		return nil
	}
	for i, it := range items {
		if it.Kind == KindOption && it.Label == *prior.Answer {
			return &OptionListConfirmed{Index: i}
		}
	}
	return nil
}

// SelectActivePreviewPaneIndex is the index of the preview pane to display for the current tab.
// upstream: selectActivePreviewPaneIndex.
func SelectActivePreviewPaneIndex(currentTab, totalQuestions int) int {
	if totalQuestions <= 0 {
		return 0
	}
	return min(currentTab, totalQuestions-1)
}
