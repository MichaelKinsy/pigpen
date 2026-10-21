package ui

// Row kinds and their static metadata. upstream: state/row-intent.ts.

// RowKind is the row kind discriminator (KindOption, KindOther, KindNext).
type RowKind = string

// SentinelKind is the subset of RowKind for protocol-driven rows (everything but "option").
type SentinelKind = string

// SentinelKinds lists the sentinel kinds in declaration order. upstream: SENTINEL_KINDS.
var SentinelKinds = []SentinelKind{KindOther, KindNext}

// RowIntentMeta is the per-kind static metadata. upstream: RowIntentMeta.
type RowIntentMeta struct {
	Label                    string
	Reserved                 bool
	LivesInMainList          bool
	Numbered                 bool
	ActivatesInputMode       bool
	BlocksMultiToggle        bool
	AutoSubmitsInMulti       bool
	AutoAppendOnSingleSelect bool
	AutoAppendOnMultiSelect  bool
}

// RowIntentMetaTable is ROW_INTENT_META.
var RowIntentMetaTable = map[RowKind]RowIntentMeta{
	KindOption: {Label: "", Reserved: false, LivesInMainList: true, Numbered: true},
	KindOther: {
		Label: "Type something.", Reserved: true, LivesInMainList: true, Numbered: true, ActivatesInputMode: true,
		AutoAppendOnSingleSelect: true, AutoAppendOnMultiSelect: true,
	},
	KindNext: {
		Label: "Next", Reserved: true, LivesInMainList: true, Numbered: false, BlocksMultiToggle: true,
		AutoSubmitsInMulti: true, AutoAppendOnMultiSelect: true,
	},
}

// RowIntent returns ROW_INTENT_META[kind].
func RowIntent(kind RowKind) RowIntentMeta { return RowIntentMetaTable[kind] }

// LabelsByKind is LABELS_BY_KIND (sentinel kinds only).
var LabelsByKind = map[SentinelKind]string{
	KindOther: RowIntentMetaTable[KindOther].Label,
	KindNext:  RowIntentMetaTable[KindNext].Label,
}

// ReservedLabelSet is RESERVED_LABEL_SET: "Other" plus every reserved sentinel label.
var ReservedLabelSet = func() map[string]bool {
	s := map[string]bool{"Other": true}
	for _, k := range SentinelKinds {
		if RowIntentMetaTable[k].Reserved {
			s[RowIntentMetaTable[k].Label] = true
		}
	}
	return s
}()

// SentinelsToAppend synthesizes the sentinel kinds one question gets, in SentinelKinds order.
// upstream: sentinelsToAppend.
func SentinelsToAppend(question QuestionData) []SentinelKind {
	out := []SentinelKind{}
	for _, k := range SentinelKinds {
		meta := RowIntentMetaTable[k]
		if !meta.LivesInMainList {
			continue
		}
		if question.MultiSelect {
			if meta.AutoAppendOnMultiSelect {
				out = append(out, k)
			}
		} else if meta.AutoAppendOnSingleSelect {
			out = append(out, k)
		}
	}
	return out
}
