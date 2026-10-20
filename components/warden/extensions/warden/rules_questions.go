package warden

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// The rules guard's question texts are the upstream ones (src/rules.ts, src/turn-rules.ts, src/rules-lint.ts), dumped
// verbatim from the vendored original into rules_questions.json by port/gen-rules-questions.ts, so a paraphrase cannot
// drift the judge's answers.
//
//go:embed rules_questions.json
var rulesQuestionsJSON []byte

type rulesQuestionSet struct {
	Outcomes json.RawMessage `json:"outcomes"`
	Rule     struct {
		Frame string `json:"frame"`
	} `json:"rule"`
	Turn struct {
		Frame string `json:"frame"`
	} `json:"turn"`
	Aggregate rawQuestion `json:"aggregate"`
	Locator   rawQuestion `json:"locator"`
	Check     struct {
		About      string      `json:"about"`
		Judgeable  rawQuestion `json:"judgeable"`
		Mechanical rawQuestion `json:"mechanical"`
	} `json:"check"`
}

// rulesQuestionTexts are the loaded question texts.
type rulesQuestionTexts struct {
	outcomes     map[string]string
	outcomeOrder []string
	ruleFrame    string
	turnFrame    string
	aggregate    Question
	locator      Question
	checkAbout   string
	judgeable    Question
	mechanical   Question
}

func loadRulesQuestions() rulesQuestionTexts {
	var set rulesQuestionSet
	if err := json.Unmarshal(rulesQuestionsJSON, &set); err != nil {
		panic(fmt.Sprintf("warden: rules_questions.json: %v", err))
	}
	outcomes := rawQuestion{Type: "choice", Criteria: set.Outcomes}.build()
	return rulesQuestionTexts{
		outcomes:     outcomes.Criteria,
		outcomeOrder: outcomes.CriteriaOrder,
		ruleFrame:    set.Rule.Frame,
		turnFrame:    set.Turn.Frame,
		aggregate:    set.Aggregate.build(),
		locator:      set.Locator.build(),
		checkAbout:   set.Check.About,
		judgeable:    set.Check.Judgeable.build(),
		mechanical:   set.Check.Mechanical.build(),
	}
}

// rulesQuestions loads the texts on first use: the package must not parse them at start.
var rulesQuestions = sync.OnceValue(loadRulesQuestions)
