package ui

import (
	"reflect"
	"testing"
)

func TestRowIntent(t *testing.T) {
	const f = "state/row-intent"
	allKinds := []RowKind{"option", "other", "next"}

	tw(t, f, "has an entry for every RowKind", func(t *testing.T) {
		for _, k := range allKinds {
			if _, ok := RowIntentMetaTable[k]; !ok {
				t.Fatalf("no META for %s", k)
			}
		}
	})
	tw(t, f, "only `option` has empty label; sentinels carry user-facing labels", func(t *testing.T) {
		if RowIntentMetaTable["option"].Label != "" || RowIntentMetaTable["other"].Label != "Type something." || RowIntentMetaTable["next"].Label != "Next" {
			t.Fatal("labels")
		}
	})
	tw(t, f, "`option` is the only non-reserved kind", func(t *testing.T) {
		if RowIntentMetaTable["option"].Reserved {
			t.Fatal("option reserved")
		}
		for _, k := range SentinelKinds {
			if !RowIntentMetaTable[k].Reserved {
				t.Fatalf("%s not reserved", k)
			}
		}
	})
	tw(t, f, "every sentinel lives in the main list", func(t *testing.T) {
		for _, k := range SentinelKinds {
			if !RowIntentMetaTable[k].LivesInMainList {
				t.Fatal(k)
			}
		}
		if !RowIntentMetaTable["option"].LivesInMainList {
			t.Fatal("option")
		}
	})
	tw(t, f, "`next` is the only kind excluded from numbering", func(t *testing.T) {
		if RowIntentMetaTable["next"].Numbered || !RowIntentMetaTable["option"].Numbered || !RowIntentMetaTable["other"].Numbered {
			t.Fatal("numbered")
		}
	})
	tw(t, f, "`other` is the only kind that activates inputMode", func(t *testing.T) {
		if !RowIntentMetaTable["other"].ActivatesInputMode {
			t.Fatal("other")
		}
		for _, k := range []RowKind{"option", "next"} {
			if RowIntentMetaTable[k].ActivatesInputMode {
				t.Fatal(k)
			}
		}
	})
	tw(t, f, "`next` is the only multi-select toggle blocker / auto-submitter", func(t *testing.T) {
		if !RowIntentMetaTable["next"].BlocksMultiToggle || !RowIntentMetaTable["next"].AutoSubmitsInMulti {
			t.Fatal("next")
		}
		for _, k := range []RowKind{"option", "other"} {
			if RowIntentMetaTable[k].BlocksMultiToggle || RowIntentMetaTable[k].AutoSubmitsInMulti {
				t.Fatal(k)
			}
		}
	})
	tw(t, f, "matches META labels for sentinel kinds only", func(t *testing.T) {
		if LabelsByKind["other"] != RowIntentMetaTable["other"].Label || LabelsByKind["next"] != RowIntentMetaTable["next"].Label {
			t.Fatal("labels")
		}
	})
	tw(t, f, "contains 'Other' plus every reserved sentinel label", func(t *testing.T) {
		for _, l := range []string{"Other", "Type something.", "Next"} {
			if !ReservedLabelSet[l] {
				t.Fatal(l)
			}
		}
	})
	tw(t, f, "does NOT contain non-reserved or unrelated labels", func(t *testing.T) {
		if ReservedLabelSet[""] || ReservedLabelSet["Submit"] {
			t.Fatal("unexpected")
		}
	})

	baseSingle := QuestionData{Question: "Q?", Header: "H", Options: []OptionData{{Label: "A", Description: "a"}, {Label: "B", Description: "b"}}}
	baseMulti := baseSingle
	baseMulti.MultiSelect = true
	tw(t, f, "appends `other` for single-select regardless of preview", func(t *testing.T) {
		if got := SentinelsToAppend(baseSingle); !reflect.DeepEqual(got, []SentinelKind{"other"}) {
			t.Fatalf("%v", got)
		}
	})
	tw(t, f, "`other` now appends on multi-select too (autoAppendOnMultiSelect === true)", func(t *testing.T) {
		if !RowIntentMetaTable["other"].AutoAppendOnMultiSelect {
			t.Fatal("flag")
		}
	})
	tw(t, f, "appends `other` + `next` for multi-select (other no longer suppressed)", func(t *testing.T) {
		if got := SentinelsToAppend(baseMulti); !reflect.DeepEqual(got, []SentinelKind{"other", "next"}) {
			t.Fatalf("%v", got)
		}
	})
}
