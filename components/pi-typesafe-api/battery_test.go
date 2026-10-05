package pitypesafe

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// TestTypeBoxMessagesForCommonFailures compares admission errors with the messages the original's TypeBox
// validator produced for the same requests (port/oracle/battery.json through parseEvaluationRequest, see
// testdata/typebox-messages.json). Union-branch wording for deep question failures is not reproduced; those rows
// are marked "-" in the file and skipped.
func TestTypeBoxMessagesForCommonFailures(t *testing.T) {
	raw, err := os.ReadFile("testdata/typebox-messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Request  json.RawMessage `json:"request"`
		Message  string          `json:"message"`
		Compared bool            `json:"compared"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	strip := regexp.MustCompile(` Expected \{.*$`)
	for _, r := range rows {
		if !r.Compared {
			continue
		}
		_, err := ParseEvaluationRequest(mustTree(t, string(r.Request)))
		got := "OK"
		if err != nil {
			got = strip.ReplaceAllString(err.Error(), "")
		}
		if got != r.Message {
			t.Errorf("%s:\n got  %s\n want %s", r.Request, got, r.Message)
		}
	}
}
