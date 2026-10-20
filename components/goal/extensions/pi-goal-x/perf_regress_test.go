package pi_goal_x

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
)

// The activity feed's defensive sort is stable and matches the original's items.sort((a, b) => a.at < b.at ? -1 : a.at > b.at ? 1 : 0)
// (checked against the insertion sort it replaced, on ordered, reversed and shuffled timestamps with ties), and it stays fast on a
// ledger whose timestamps run backwards.
func TestActivitySortStableMatchesInsertionSort(t *testing.T) {
	insertion := func(s []goalActivityItem) {
		for i := 1; i < len(s); i++ {
			for j := i; j > 0 && s[j].at < s[j-1].at; j-- {
				s[j], s[j-1] = s[j-1], s[j]
			}
		}
	}
	rng := rand.New(rand.NewPCG(7, 11))
	for _, n := range []int{0, 1, 2, 5, 40, 500} {
		for _, shape := range []string{"ordered", "reversed", "shuffled", "ties"} {
			items := make([]goalActivityItem, n)
			for i := range items {
				at := i
				switch shape {
				case "reversed":
					at = n - i
				case "shuffled":
					at = rng.IntN(1000)
				case "ties":
					at = rng.IntN(4)
				}
				items[i] = goalActivityItem{at: fmt.Sprintf("2026-01-02T03:04:%02d.%03dZ", at%60, at%1000), kind: "k", text: fmt.Sprint(i)}
			}
			want := append([]goalActivityItem(nil), items...)
			insertion(want)
			got := append([]goalActivityItem(nil), items...)
			activitySortStable(got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s n=%d: order differs from the stable insertion sort", shape, n)
			}
		}
	}
}
