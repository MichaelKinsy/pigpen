package ask_user_question

import (
	"encoding/json"
	"testing"
)

const fMain = "ask-user-question"

func twoQ() questionParams {
	return params(qn("Q1?", "A", opt("x", "d"), opt("y", "d")), qn("Q2?", "B", opt("p", "d"), opt("q", "d")))
}

func TestEnvelope(t *testing.T) {
	const prefix, suffix = "User has answered your questions:", "You can now continue with the user's answers in mind."
	tw(t, fMain, "null result → decline envelope + empty answers + cancelled true", func(t *testing.T) {
		out := buildQuestionnaireResponse(nil, single())
		eq(t, out.Text, "User declined to answer questions", "text")
		eq(t, out.Details, questionnaireResult{Cancelled: true}, "details")
	})
	tw(t, fMain, "cancelled result preserves partial answers in details (not in content)", func(t *testing.T) {
		r := &questionnaireResult{Cancelled: true, Answers: []questionAnswer{answerOpt(0, "Which?", "A")}}
		out := buildQuestionnaireResponse(r, single())
		eq(t, out.Text, "User declined to answer questions", "text")
		eq(t, out.Details.Answers, r.Answers, "answers")
	})
	tw(t, fMain, "single answered question → CC envelope wrapper with question text and answer", func(t *testing.T) {
		r := &questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Which?", "A")}}
		eq(t, buildQuestionnaireResponse(r, single()).Text, prefix+` "Which?"="A". `+suffix, "text")
	})
	tw(t, fMain, "envelope uses question text not header (no Q1 prefix)", func(t *testing.T) {
		r := &questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Which?", "A")}}
		text := buildQuestionnaireResponse(r, single()).Text
		if contains(text, "Pick") || contains(text, "Q1") {
			t.Fatalf("header or Q1 leaked into %q", text)
		}
	})
	tw(t, fMain, "two answered questions render as separate sentences inside one envelope", func(t *testing.T) {
		r := &questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Q1?", "x"), answerOpt(1, "Q2?", "q")}}
		eq(t, buildQuestionnaireResponse(r, twoQ()).Text, prefix+` "Q1?"="x". "Q2?"="q". `+suffix, "text")
	})
	tw(t, fMain, "multiSelect answer renders as comma-joined labels in <A> position", func(t *testing.T) {
		p := params(multi(qn("Q?", "H", opt("red", "r"), opt("blue", "b"))))
		r := &questionnaireResult{Answers: []questionAnswer{answerMulti(0, "Q?", "red", "blue")}}
		eq(t, buildQuestionnaireResponse(r, p).Text, prefix+` "Q?"="red, blue". `+suffix, "text")
	})
	tw(t, fMain, "custom typed answer renders raw text (no 'User answered:' prefix)", func(t *testing.T) {
		r := &questionnaireResult{Answers: []questionAnswer{answerCustom(0, "Which?", "my own")}}
		eq(t, buildQuestionnaireResponse(r, single()).Text, prefix+` "Which?"="my own". `+suffix, "text")
	})
	tw(t, fMain, "empty custom answer renders as (no input) in <A> position", func(t *testing.T) {
		a := questionAnswer{QuestionIndex: 0, Question: "Which?", Kind: "custom"}
		eq(t, buildQuestionnaireResponse(&questionnaireResult{Answers: []questionAnswer{a}}, single()).Text, prefix+` "Which?"="(no input)". `+suffix, "text")
		a.Answer = sp("")
		eq(t, buildQuestionnaireResponse(&questionnaireResult{Answers: []questionAnswer{a}}, single()).Text, prefix+` "Which?"="(no input)". `+suffix, "empty string")
	})
	tw(t, fMain, "notes are echoed as 'user notes: <text>' AND preserved in details", func(t *testing.T) {
		a := answerOpt(0, "Which?", "A")
		a.Notes = "because"
		out := buildQuestionnaireResponse(&questionnaireResult{Answers: []questionAnswer{a}}, single())
		eq(t, out.Text, prefix+` "Which?"="A". user notes: because. `+suffix, "text")
		eq(t, at(out.Details.Answers, 0).Notes, "because", "details")
	})
	tw(t, fMain, "multiSelect answer with notes renders 'user notes:' suffix and NO 'selected preview:' suffix (FR-7)", func(t *testing.T) {
		a := answerMulti(0, "Q?", "red")
		a.Notes = "n"
		p := params(multi(qn("Q?", "H", opt("red", "r"), opt("blue", "b"))))
		eq(t, buildQuestionnaireResponse(&questionnaireResult{Answers: []questionAnswer{a}}, p).Text, prefix+` "Q?"="red". user notes: n. `+suffix, "text")
	})
	tw(t, fMain, "cancelled: false with no matching answers still returns DECLINE_MESSAGE text", func(t *testing.T) {
		out := buildQuestionnaireResponse(&questionnaireResult{}, single())
		eq(t, out.Text, "User declined to answer questions", "text")
		eq(t, out.Details, questionnaireResult{Cancelled: true}, "details")
	})
	tw(t, fMain, "success + note: exact envelope bytes with the trailing 'global note: <note>.' segment (raw multiline echo)", func(t *testing.T) {
		r := &questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Which?", "A")}, GlobalNote: "line one\nline two"}
		eq(t, buildQuestionnaireResponse(r, single()).Text, prefix+` "Which?"="A". global note: line one`+"\n"+`line two. `+suffix, "text")
	})
	tw(t, fMain, "the global-note segment is anchored by the envelope suffix", func(t *testing.T) {
		r := &questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Which?", "A")}, GlobalNote: "n"}
		text := buildQuestionnaireResponse(r, single()).Text
		eq(t, tail(text, len(" global note: n. "+suffix)), " global note: n. "+suffix, "tail")
	})
	tw(t, fMain, "zero answers + note is NOT a decline — answered envelope with details by reference", func(t *testing.T) {
		r := &questionnaireResult{GlobalNote: "just this"}
		out := buildQuestionnaireResponse(r, single())
		eq(t, out.Text, prefix+` global note: just this. `+suffix, "text")
		eq(t, out.Details, *r, "details")
	})
	tw(t, fMain, "cancelled + note: DECLINE_MESSAGE text, answers forwarded AND globalNote preserved in details", func(t *testing.T) {
		r := &questionnaireResult{Cancelled: true, GlobalNote: "kept", Answers: []questionAnswer{answerOpt(0, "Which?", "A")}}
		out := buildQuestionnaireResponse(r, single())
		eq(t, out.Text, "User declined to answer questions", "text")
		eq(t, out.Details, questionnaireResult{Cancelled: true, GlobalNote: "kept", Answers: r.Answers}, "details")
	})
	tw(t, fMain, "zero answers + no note still declines (fresh details literal carries no globalNote key)", func(t *testing.T) {
		out := buildQuestionnaireResponse(&questionnaireResult{}, single())
		b, _ := json.Marshal(out.Details)
		eq(t, string(b), `{"answers":[],"cancelled":true}`, "wire")
	})
	tw(t, fMain, "formats 2 answered questions as comma-period-separated segments inside one envelope", func(t *testing.T) {
		r := &questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Q1?", "x"), answerCustom(1, "Q2?", "free")}}
		eq(t, buildQuestionnaireResponse(r, twoQ()).Text, prefix+` "Q1?"="x". "Q2?"="free". `+suffix, "text")
	})
	tw(t, fMain, "formats 3 questions with mixed answer types in single-line envelope", func(t *testing.T) {
		p := params(qn("Q1?", "A", two()...), multi(qn("Q2?", "B", opt("p", "d"), opt("q", "d"))), qn("Q3?", "C", two()...))
		r := &questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Q1?", "A"), answerMulti(1, "Q2?", "p", "q"), answerCustom(2, "Q3?", "z")}}
		text := buildQuestionnaireResponse(r, p).Text
		eq(t, text, prefix+` "Q1?"="A". "Q2?"="p, q". "Q3?"="z". `+suffix, "text")
		if contains(text, "\n") {
			t.Fatalf("envelope spans lines: %q", text)
		}
	})
	tw(t, fMain, "skips unanswered questions (omits their segment from envelope)", func(t *testing.T) {
		r := &questionnaireResult{Answers: []questionAnswer{answerOpt(1, "Q2?", "p")}}
		eq(t, buildQuestionnaireResponse(r, twoQ()).Text, prefix+` "Q2?"="p". `+suffix, "text")
	})
	tw(t, fMain, "preserves notes in details AND echoes them across multiple questions", func(t *testing.T) {
		a, b := answerOpt(0, "Q1?", "x"), answerOpt(1, "Q2?", "p")
		a.Notes, b.Notes = "n1", "n2"
		out := buildQuestionnaireResponse(&questionnaireResult{Answers: []questionAnswer{a, b}}, twoQ())
		eq(t, out.Text, prefix+` "Q1?"="x". user notes: n1. "Q2?"="p". user notes: n2. `+suffix, "text")
		eq(t, at(out.Details.Answers, 1).Notes, "n2", "details")
	})
	tw(t, fMain, "echoes preview text in envelope when single-select answer matches a preview-bearing option", func(t *testing.T) {
		a := answerOpt(0, "Which?", "A")
		a.Preview = "+--+"
		eq(t, buildQuestionnaireResponse(&questionnaireResult{Answers: []questionAnswer{a}}, single()).Text, prefix+` "Which?"="A". selected preview: +--+. `+suffix, "text")
	})
	tw(t, fMain, "omits 'selected preview:' fragment when answer has no preview", func(t *testing.T) {
		text := buildQuestionnaireResponse(&questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Which?", "A")}}, single()).Text
		if contains(text, "selected preview") {
			t.Fatalf("preview fragment in %q", text)
		}
	})
	tw(t, fMain, "omits 'user notes:' fragment when answer has no notes", func(t *testing.T) {
		text := buildQuestionnaireResponse(&questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Which?", "A")}}, single()).Text
		if contains(text, "user notes") {
			t.Fatalf("notes fragment in %q", text)
		}
	})
	tw(t, fMain, "envelope wraps with CC prefix and suffix sentences", func(t *testing.T) {
		text := buildQuestionnaireResponse(&questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Which?", "A")}}, single()).Text
		eq(t, head(text, len(prefix)), prefix, "prefix")
		eq(t, tail(text, len(suffix)), suffix, "suffix")
	})
	tw(t, fMain, "locks the envelope shape", func(t *testing.T) {
		out := buildQuestionnaireResponse(&questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Which?", "A")}}, single())
		b, _ := json.Marshal(out.Details)
		eq(t, string(b), `{"answers":[{"answer":"A","kind":"option","question":"Which?","questionIndex":0}],"cancelled":false}`, "details wire")
	})
	tskip(t, fMain, "passes details by reference (no clone)", "Go passes the result by value into the output; there is no reference identity to assert (the details equal the input: TestEnvelopeDetailsEqualTheInput)")
	tw(t, fMain, "accepts error field in envelope", func(t *testing.T) {
		out := buildToolResult("Error: x", questionnaireResult{Cancelled: true, Error: errNoUI})
		b, _ := json.Marshal(out.Details)
		eq(t, string(b), `{"answers":[],"cancelled":true,"error":"no_ui"}`, "wire")
		eq(t, out.Text, "Error: x", "text")
	})
}

func TestEnvelopeDetailsEqualTheInput(t *testing.T) {
	r := &questionnaireResult{Answers: []questionAnswer{answerOpt(0, "Which?", "A")}}
	eq(t, buildQuestionnaireResponse(r, single()).Details, *r, "details")
}

func TestAnswerSegmentOrdersPreviewBeforeNotes(t *testing.T) {
	a := answerOpt(0, "Q?", "A")
	a.Preview, a.Notes = "P", "N"
	eq(t, buildAnswerSegment(a), `"Q?"="A". selected preview: P. user notes: N.`, "segment")
}

func TestFormatAnswerScalar(t *testing.T) {
	eq(t, formatAnswerScalar(answerMulti(0, "Q", "a", "b")), "a, b", "multi")
	eq(t, formatAnswerScalar(answerMulti(0, "Q")), "(no input)", "multi empty")
	eq(t, formatAnswerScalar(questionAnswer{Kind: "option"}), "(no input)", "option without answer")
	eq(t, formatAnswerScalar(answerOpt(0, "Q", "")), "", "option with an empty answer keeps it (?? only replaces null)")
}
