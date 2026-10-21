package ui

import "testing"

// A questionnaire with no questions is never fully answered (the Submit tab must not offer submitting nothing).
func TestAllAnsweredIsFalseWithoutQuestions(t *testing.T) {
	if AllAnswered(QuestionnaireState{}, QuestionnaireRuntime{}) {
		t.Fatal("no questions must not count as all answered")
	}
}
