package pi_permission_system

import "testing"

// The replacer's seen set is a slice up to seenSetSmall entries and a map past it; both must mark a repeated reference.
func TestSeenSetMarksRepeatsAcrossTheSliceToMapSwitch(t *testing.T) {
	for _, n := range []int{1, seenSetSmall - 1, seenSetSmall, seenSetSmall + 1, 3 * seenSetSmall} {
		items := make([]any, n)
		for i := range items {
			items[i] = newObject()
			items[i].(*jsObject).set("i", float64(i))
		}
		// the first and last objects appear again after all the others
		list := append(append([]any{}, items...), items[0], items[n-1])
		got, ok := SafeJSONStringify(list)
		if !ok {
			t.Fatalf("n=%d: undefined", n)
		}
		want := "["
		for i := range items {
			if i > 0 {
				want += ","
			}
			want += `{"i":` + jsNumber(float64(i)) + `}`
		}
		want += `,"[Circular]","[Circular]"]`
		if got != want {
			t.Fatalf("n=%d:\n got %s\nwant %s", n, got, want)
		}
	}
}
