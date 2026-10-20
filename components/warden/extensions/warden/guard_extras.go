package warden

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The parts of src/guard.ts that ride the action request beside the base questions: the regret question, the large-output
// steer, the slop labels. The question texts are the upstream ones, dumped verbatim into questions_guard.json by
// port/gen-questions.ts.

//go:embed questions_guard.json
var guardQuestionsJSON []byte

type guardQuestionSets struct {
	LargeOutput map[string]rawQuestion `json:"largeOutput"`
	Slop        map[string]rawQuestion `json:"slop"`
	Security    map[string]rawQuestion `json:"security"`
	Regret      map[string]rawQuestion `json:"regret"`
}

var gqs = func() guardQuestionSets {
	var s guardQuestionSets
	if err := json.Unmarshal(guardQuestionsJSON, &s); err != nil {
		panic(fmt.Sprintf("warden: questions_guard.json: %v", err))
	}
	return s
}()

// PreviousAction is a call the guard allowed in the previous turn, as the regret question sees it: redacted summary
// fields only.
type PreviousAction struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	Command string `json:"command,omitempty"`
	Path    string `json:"path,omitempty"`
}

// previousCommandLimit bounds a previous command in the request.
const previousCommandLimit = 300

// regretQuestions is one yes/no on whether the user's reply regrets what the agent did last turn; with several candidates a
// choice names the one. It labels the allowed calls for hold calibration and never changes the verdict on the current call.
func regretQuestions(actions []PreviousAction) Questions {
	out := Questions{"regretted": gqs.Regret["regretted"].build()}
	if len(actions) < 2 {
		return out
	}
	q := Question{Type: "choice", Instructions: "If `task` regrets one of `previous_actions`, which one does it most likely mean?", Criteria: map[string]string{}}
	for _, a := range actions {
		detail := a.Command
		if detail == "" {
			detail = a.Path
		}
		if detail == "" {
			detail = "(no detail)"
		}
		if _, dup := q.Criteria[a.ID]; !dup {
			q.CriteriaOrder = append(q.CriteriaOrder, a.ID)
		}
		q.Criteria[a.ID] = a.Tool + ": " + detail
	}
	out["regret_target"] = q
	return out
}

// SlopJudgment is one probability per slop symptom; the steer names the symptoms above the threshold.
type SlopJudgment struct {
	Stub, Comments, Dead, Hedging float64
}

// SlopSymptoms are the symptoms in upstream order.
var SlopSymptoms = []string{"stub", "comments", "dead", "hedging"}

// SlopLabels say each symptom in the steer and the trace.
var SlopLabels = map[string]string{
	"stub":     "stub or placeholder code where a working implementation is needed",
	"comments": "comments that restate the code",
	"dead":     "dead or duplicated code",
	"hedging":  "hedging or vague notes",
}

// Score is the probability of one symptom.
func (s SlopJudgment) Score(symptom string) float64 {
	switch symptom {
	case "stub":
		return s.Stub
	case "comments":
		return s.Comments
	case "dead":
		return s.Dead
	}
	return s.Hedging
}

// ---------------------------------------------------------------------------
// Large output: prevention before the call; the context saver still compresses whatever does print. Steers, never holds.

// subcommandHeads are tools whose first word names what runs: `git log` and `git status` print very different amounts.
var subcommandHeads = setOf("git", "npm", "pnpm", "yarn", "bun", "cargo", "go", "docker", "kubectl")

var reSubcommandWord = regexp.MustCompile(`^[A-Za-z][\w:.-]*$`)

// commandFamily is the command family the large-output steer is remembered by: the head of the first segment that is
// not a `cd`, plus the subcommand for tools that have one (`git log`, `npm test`, `npm run build`). "" when no head can
// be read.
func commandFamily(command string) string {
	var segment, head string
	for _, part := range splitShell(command) {
		if h, ok := headOf(part); ok && h != "cd" && h != "pushd" {
			segment, head = part, h
			break
		}
	}
	if segment == "" || head == "" {
		return ""
	}
	if !subcommandHeads[head] {
		return head
	}
	tokens := jsSplitWhitespace(jsTrim(segment))
	start := 0
	for i, token := range tokens {
		if stripPathPrefix(token) == head {
			start = i + 1
			break
		}
	}
	var rest []string
	for _, token := range tokens[start:] {
		if !strings.HasPrefix(token, "-") {
			rest = append(rest, token)
		}
	}
	word := func(i int) bool { return i < len(rest) && reSubcommandWord.MatchString(rest[i]) }
	if !word(0) {
		return head
	}
	if rest[0] == "run" && word(1) {
		return head + " run " + rest[1]
	}
	return head + " " + rest[0]
}

var reNonAlnumRun = regexp.MustCompile(`[^A-Za-z0-9]+`)

// largeOutputSteer is what the agent reads the first time a command family is judged noisy: the family and one concrete
// way to print less.
func largeOutputSteer(v Verdict) string {
	family := v.LargeOutputFamily
	if family == "" {
		family = v.Summary.Tool
	}
	score := ""
	if v.Judgment != nil && v.Judgment.LargeOutput != nil {
		score = " (large-output " + percent(*v.Judgment.LargeOutput) + ")"
	}
	log := "/tmp/warden-" + reNonAlnumRun.ReplaceAllString(family, "-") + ".log"
	return "pi-warden: `" + family + "` commands may print far more than you need" + score + ". This one runs unchanged.\n" +
		"Next time, redirect and show the tail: `" + family + " … > " + log + " 2>&1; tail -40 " + log + "`."
}

// largeOutputNotice is the large-output steer for this verdict, once per command family per session; steered is the
// session's memory. "" when there is nothing to say.
func largeOutputNotice(v Verdict, steered map[string]bool) string {
	family := v.LargeOutputFamily
	if family == "" || steered[family] {
		return ""
	}
	steered[family] = true
	return largeOutputSteer(v)
}
