package typesafe_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// TestLive_AgainstTheRealAPI runs only when the owner supplies TYPESAFE_API_KEY; without it,
// nothing touches the network. It checks that a real response decodes into the typed
// results and, when TYPESAFE_LIVE_JS_DIST names the official SDK's dist/index.mjs and node is
// installed, that the official SDK sees the same model list and the same answer shapes.
func TestLive_AgainstTheRealAPI(t *testing.T) {
	if os.Getenv("TYPESAFE_API_KEY") == "" {
		t.Skip("TYPESAFE_API_KEY is not set: no live check (the fake-server cross-check runs instead)")
	}
	client, err := typesafe.NewClient(typesafe.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.Models().List(t.Context(), nil)
	if err != nil || len(models) == 0 {
		t.Fatalf("models: %v %v", models, err)
	}
	res, err := client.SystemOne(t.Context(), typesafe.SystemOneRequest{
		State: typesafe.Text("I was charged twice for my subscription. Please refund one payment."),
		Questions: typesafe.Questions{
			typesafe.Ask("billing", typesafe.Noul("Is this about billing?")),
			typesafe.Ask("kind", typesafe.Choice("What does the customer want?", typesafe.Opt("refund", "a refund"), typesafe.Opt("cancel", "to cancel"), typesafe.Opt("other", nil))),
			typesafe.Ask("urgency", typesafe.Score("How urgent is it?", "not urgent", "somewhat urgent", "urgent")),
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	billing, _ := res.Noul("billing")
	kind, _ := res.Choice("kind")
	urgency, _ := res.Score("urgency")
	if billing.Noul < 0 || billing.Noul > 1 || len(kind.Probabilities) != 3 || len(urgency.Probabilities) != 3 || res.Usage.InputTokens == 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	dist := os.Getenv("TYPESAFE_LIVE_JS_DIST")
	if dist == "" {
		t.Log("TYPESAFE_LIVE_JS_DIST not set: no comparison with the official SDK")
		return
	}
	out, err := exec.CommandContext(t.Context(), "node", "../../port/crosscheck/live_js.mjs", dist).Output()
	if err != nil {
		t.Fatalf("live_js.mjs: %v", err)
	}
	var js struct {
		Models []typesafe.ModelCard `json:"models"`
		Result struct {
			Answers map[string]struct {
				Type          string             `json:"type"`
				Probabilities map[string]float64 `json:"probabilities"`
			} `json:"answers"`
		} `json:"result"`
	}
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&js); err != nil {
		t.Fatal(err)
	}
	names := func(ms []typesafe.ModelCard) []string {
		var n []string
		for _, m := range ms {
			n = append(n, m.Name)
		}
		sort.Strings(n)
		return n
	}
	if a, b := names(models), names(js.Models); !equalStrings(a, b) {
		t.Errorf("model lists differ: go %v, official %v", a, b)
	}
	for name, want := range map[string]int{"kind": 3, "urgency": 3} {
		if got := len(js.Result.Answers[name].Probabilities); got != want {
			t.Errorf("official SDK: %s has %d probabilities, want %d", name, got, want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
