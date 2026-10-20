package warden

import (
	"strings"
	"testing"
)

const (
	proseDone       = "CON-375 done: draft PR 2688 is pushed with code, tests and screenshots, and Linear is In Review. Worktree millia-con375 awaits review."
	proseParaphrase = "CON-375 is complete: the draft pull request 2688 is pushed together with code, tests and screenshots, and Linear sits In Review. The worktree millia-con375 now awaits review."
	proseFresh      = "The security notice named variable names only: no values were echoed and nothing was committed. pr-evidence holds PNG files."
)

// twin: tests/prose.test.ts:9
func TestRestatedShareReadsAParaphraseOfTheSameStatusAsARestatement(t *testing.T) {
	if share := RestatedShare(proseParaphrase, []string{proseDone}); share < RestateShare {
		t.Fatalf("the paraphrase restates %v of the finished status, at least %v", share, RestateShare)
	}
}

// twin: tests/prose.test.ts:14
func TestRestatedShareKeepsAReplyWithNewInformationBelowTheThreshold(t *testing.T) {
	if share := RestatedShare(proseFresh, []string{proseDone}); share >= RestateShare {
		t.Fatalf("the fresh answer scores %v, below %v", share, RestateShare)
	}
}

// twin: tests/prose.test.ts:19
func TestShortConnectiveSentencesNeverCountTowardARestatement(t *testing.T) {
	if n := len(SubstantiveSentences("Yes. Done. On it.")); n != 0 {
		t.Fatalf("one-line acknowledgements carry no comparable sentence: %d", n)
	}
}

// twin: tests/prose.test.ts:23
func TestAnEmptyComparisonPoolScoresZero(t *testing.T) {
	if got := RestatedShare(proseDone, nil); got != 0 {
		t.Fatal(got)
	}
}

// twin: tests/prose.test.ts:27
func TestRestatementWindowForgetsFinalsBeyondItsLimitAndResetsWithThePrompt(t *testing.T) {
	window := NewRestatementWindow(2)
	window.Record(proseDone)
	window.Record(proseFresh)
	window.Record(proseFresh)
	if RestatedShare(proseParaphrase, []string{proseDone}) <= 0 {
		t.Fatal("the dropped final would still match")
	}
	if window.Share(proseParaphrase) != 0 {
		t.Fatal("only the last two finals are compared")
	}
	window.Reset()
	if window.Share(proseDone) != 0 {
		t.Fatal("a new prompt clears the window")
	}
	_ = strings.TrimSpace
}
