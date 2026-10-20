package ui

// TabBodyHeights is a tab body's current and maximum natural height. upstream: TabBodyHeights.
type TabBodyHeights struct {
	Current int
	Max     int
}

// TabPreview is the PreviewPane surface TabComponents needs. The original types the field as the concrete class
// and its tests cast fakes into it; Go tests need the same seam, so the field is this interface (*PreviewPane
// implements it).
type TabPreview interface {
	StatefulView[PreviewPaneProps]
	SetGlobalLeftWidth(getter func(paneWidth int) int)
	NaturalHeight(width int) int
	MaxNaturalHeight(width int) int
	FocusedItemRowRange(width int) (int, int)
}

// TabMultiSelect is the MultiSelectView surface TabComponents needs (see TabPreview).
type TabMultiSelect interface {
	StatefulView[MultiSelectViewProps]
	FocusedItemRowRange(width int) (int, int)
	NaturalHeight(width int) int
}

// TabComponents are the components built per tab. MultiSelect is a nil interface where the original omits it.
// upstream: view/tab-components.ts TabComponents.
type TabComponents struct {
	OptionList  StatefulView[OptionListViewProps]
	Preview     TabPreview
	MultiSelect TabMultiSelect
	BodyHeights func(width int) TabBodyHeights
}
