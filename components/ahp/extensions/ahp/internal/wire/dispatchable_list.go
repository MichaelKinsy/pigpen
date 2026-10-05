package wire

import "sort"

// ClientDispatchableTypes lists every action type a client may dispatch, sorted.
func ClientDispatchableTypes() []string {
	var out []string
	for t, ok := range clientDispatchable {
		if ok {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}
