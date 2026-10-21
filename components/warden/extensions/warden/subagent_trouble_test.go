package warden

import (
	"math/rand/v2"
	"testing"
)

var troubleVocab = []string{
	"fail", "failed", "failure", "failures", "fails", "error", "errors", "errored", "blocked", "stopped", "crash", "crashed", "timeout", "timed out", "aborted", "interrupted",
	"declined", "denied", "could not", "couldn't", "cannot", "incomplete", "unfinished", "nonzero", "non-zero", "exit 1", "exited code 2", "exit code 3", "exit 0", "exited 7",
	"needs a decision", "needs input", "needs attention", "needs help", "waiting for you", "waiting on input", "waiting for it", "asks you", "asks the user", "requires you", "requires the user", "requires it",
	"FAILED", "Error", "TIMED OUT", "Needs Approval", "xfail", "failover", "prefix_fail", "stop", "stopwatch", "error_code", "9fail", "_fail",
	" ", " ", "  ", "\n", ".", ",", ":", "-", "'", "(", ")", "ok", "done", "all good", "ſ", "K", "é", "日本", "\xff", "Kelvin",
}

// MentionsTrouble scans for the first letters of the expression's alternatives and runs the expression only on a
// hit: both must answer alike for every text.
func TestMentionsTroubleAgreesWithItsExpression(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 11))
	for i := range 60000 {
		text := genText(r, troubleVocab, 8)
		if got, want := MentionsTrouble(text), reTrouble.MatchString(text); got != want {
			t.Fatalf("case %d %q: got %v want %v", i, text, got, want)
		}
	}
	for _, text := range []string{"", " ", "fail", "xfail", "Fail", "FAIL.", "a fail", "needs  a decision", "needs a  decision", "exit 0", "exit 10", "exit  1", "exited code 1", "timed  out", "asks you!", "waiting on the user", "non zero", "x fail", "f", "fa", "é fail"} {
		if got, want := MentionsTrouble(text), reTrouble.MatchString(text); got != want {
			t.Errorf("%q: got %v want %v", text, got, want)
		}
	}
}
