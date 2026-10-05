package warden

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

// The question texts are the upstream ones, dumped verbatim from pi-warden@a12b270 (src/guard.ts,
// src/stuck.ts, src/done.ts) into questions.json so a paraphrase cannot drift the judge's answers.
//
//go:embed questions.json
var questionsJSON []byte

type questionSets struct {
	Base          map[string]rawQuestion `json:"base"`
	Visible       map[string]rawQuestion `json:"visible"`
	Intent        map[string]rawQuestion `json:"intent"`
	ShouldProceed map[string]rawQuestion `json:"shouldProceed"`
	Approval      map[string]rawQuestion `json:"approval"`
	Stuck         map[string]rawQuestion `json:"stuck"`
	Done          map[string]rawQuestion `json:"done"`
}

type rawQuestion struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria"`
}

func (r rawQuestion) build() Question {
	q := Question{Type: r.Type, Instructions: r.Instructions}
	crit := bytes.TrimSpace(r.Criteria)
	if len(crit) == 0 || crit[0] == 'n' {
		return q
	}
	if crit[0] == '[' {
		_ = json.Unmarshal(crit, &q.Levels)
		return q
	}
	q.Criteria = map[string]string{}
	dec := json.NewDecoder(bytes.NewReader(crit))
	_, _ = dec.Token() // {
	for dec.More() {
		key, _ := dec.Token()
		var val string
		_ = dec.Decode(&val)
		k := key.(string)
		q.Criteria[k] = val
		q.CriteriaOrder = append(q.CriteriaOrder, k)
	}
	return q
}

func loadQuestions() questionSets {
	var s questionSets
	if err := json.Unmarshal(questionsJSON, &s); err != nil {
		panic(fmt.Sprintf("warden: questions.json: %v", err))
	}
	return s
}

var qs = loadQuestions()

func build(m map[string]rawQuestion) Questions {
	out := Questions{}
	for id, r := range m {
		out[id] = r.build()
	}
	return out
}

func mergeQuestions(sets ...Questions) Questions {
	out := Questions{}
	for _, s := range sets {
		for k, v := range s {
			out[k] = v
		}
	}
	return out
}
