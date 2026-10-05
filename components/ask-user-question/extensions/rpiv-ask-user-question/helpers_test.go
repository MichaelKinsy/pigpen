package ask_user_question

import (
	"reflect"
	"strings"
	"testing"
)

func sp(s string) *string { return &s }

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", what, got, want)
	}
}

func opt(label, description string) option { return option{Label: label, Description: description} }

// qn builds a single-select question; ms makes it a multi-select.
func qn(text, header string, options ...option) question {
	return question{Question: text, Header: header, Options: options}
}

func multi(q question) question { q.MultiSelect = true; return q }

func params(qs ...question) questionParams { return questionParams{Questions: qs} }

// two is the smallest valid option list.
func two() []option { return []option{opt("A", "a"), opt("B", "b")} }

func single() questionParams { return params(qn("Which?", "Pick", two()...)) }

// answerOpt, answerCustom and answerMulti build answers the way the original's fixtures do.
func answerOpt(i int, q, label string) questionAnswer {
	return questionAnswer{QuestionIndex: i, Question: q, Kind: "option", Answer: sp(label)}
}

func answerCustom(i int, q, text string) questionAnswer {
	return questionAnswer{QuestionIndex: i, Question: q, Kind: "custom", Answer: sp(text)}
}

func answerMulti(i int, q string, selected ...string) questionAnswer {
	return questionAnswer{QuestionIndex: i, Question: q, Kind: "multi", Selected: selected, HasSelected: true}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// head, tail and at are bounds-safe: a wrong result fails the assertion that follows instead of panicking the binary.
func head(s string, n int) string {
	if n > len(s) {
		return s
	}
	return s[:n]
}

func tail(s string, n int) string {
	if n > len(s) {
		return s
	}
	return s[len(s)-n:]
}

func at[T any](s []T, i int) T {
	var zero T
	if i < 0 || i >= len(s) {
		return zero
	}
	return s[i]
}
