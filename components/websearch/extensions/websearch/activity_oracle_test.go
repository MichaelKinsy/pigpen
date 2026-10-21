package websearch

import (
	"reflect"
	"testing"
)

// The recorders of port/ (record-provider-requests.mjs, record-hosted-requests.mjs, record-model-requests.mjs,
// record-gemini-requests.mjs) also write the entries the ORIGINAL's activity monitor holds after each case
// (type, query or url, status, error; never ids and times). The replays of those fixtures clear the monitor
// before the call and require the port's entries to be the same, so every call site that logs, and every
// outcome it logs (an HTTP status, an error message, an abort as status 0, an entry that stays open), is
// proven against the original and not against this port's reading of it.

// oracleActivity is one recorded entry.
type oracleActivity struct {
	Type   string `json:"type"`
	Query  string `json:"query"`
	URL    string `json:"url"`
	Status *int   `json:"status"`
	Error  string `json:"error"`
}

// activityEntries is what the monitor holds now, in the recorder's form.
func activityEntries() []oracleActivity {
	out := []oracleActivity{}
	for _, e := range Activity.Entries() {
		out = append(out, oracleActivity{Type: e.Type, Query: e.Query, URL: e.URL, Status: e.Status, Error: normalizeFixtureText(e.Error)})
	}
	return out
}

// requireActivity fails when the monitor does not hold the recorded entries.
func requireActivity(t *testing.T, label string, want []oracleActivity) {
	t.Helper()
	norm := make([]oracleActivity, len(want))
	for i, w := range want {
		w.Error = normalizeFixtureText(w.Error)
		norm[i] = w
	}
	got := activityEntries()
	if len(got) == 0 && len(norm) == 0 {
		return
	}
	if !reflect.DeepEqual(got, norm) {
		t.Fatalf("%s: activity entries differ\noriginal: %s\nport:     %s", label, showActivity(norm), showActivity(got))
	}
}

func showActivity(list []oracleActivity) string {
	out := ""
	for _, e := range list {
		status := "none"
		if e.Status != nil {
			status = itoa(*e.Status)
		}
		out += "\n  {" + e.Type + " query=" + quoteShort(e.Query) + " url=" + quoteShort(e.URL) + " status=" + status + " error=" + quoteShort(e.Error) + "}"
	}
	if out == "" {
		return "(no entries)"
	}
	return out
}

func quoteShort(s string) string {
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return "\"" + s + "\""
}
