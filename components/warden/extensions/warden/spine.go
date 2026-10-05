package warden

// The task spine (src/shape.ts): the thread's first user turn, the latest turn and up to four earlier ones,
// so "now the tests" is judged against the goal it belongs to and not on its words alone.

const (
	spineCap          = 1200
	spineHistoryTurns = 4
	spineGoalLimit    = 1200
	spineHistoryLimit = 750
)

// BranchMessage is the slice of a session entry the spine reads.
type BranchMessage struct {
	Type string
	Role string
	Text string
}

func userTurnTexts(entries []BranchMessage) []string {
	var turns []string
	for _, e := range entries {
		if e.Type != "message" || e.Role != "user" {
			continue
		}
		if t := jsTrim(e.Text); t != "" {
			turns = append(turns, t)
		}
	}
	return turns
}

// TaskSpineOf builds the spine over branch entries. The whole spine fits spineCap: history is clipped first
// (newest turns keep their text), then goal; the latest turn is never clipped. Goal and history leave
// redacted. Nil when there is no user turn or the latest turn is the first.
func TaskSpineOf(entries []BranchMessage, latest string) *TaskSpine {
	turns := userTurnTexts(entries)
	supplied := jsTrim(latest)
	last, haveLast := "", len(turns) > 0
	if haveLast {
		last = turns[len(turns)-1]
	}
	task := supplied
	if task == "" {
		task = last
	}
	if task == "" {
		return nil
	}
	var earlier []string
	switch {
	case supplied != "" && haveLast && last == task:
		earlier = turns[:len(turns)-1]
	case supplied != "":
		earlier = turns
	case haveLast:
		earlier = turns[:len(turns)-1]
	}
	if len(earlier) == 0 {
		return nil
	}
	goal := Redact(earlier[0])
	rest := earlier[1:]
	if len(rest) > spineHistoryTurns {
		rest = rest[len(rest)-spineHistoryTurns:]
	}
	var history []string
	for i := len(rest) - 1; i >= 0; i-- {
		history = append(history, Redact(rest[i]))
	}
	budget := spineCap - utf16Len(goal) - utf16Len(task)
	kept := []string{}
	for _, turn := range history {
		if budget <= 0 {
			break
		}
		take := utf16Slice(turn, 0, max(0, budget))
		kept = append(kept, take)
		budget -= utf16Len(take)
	}
	historyLength := 0
	for _, t := range kept {
		historyLength += utf16Len(t)
	}
	if utf16Len(goal)+utf16Len(task)+historyLength > spineCap {
		goal = utf16Slice(goal, 0, max(0, spineCap-utf16Len(task)-historyLength))
	}
	return &TaskSpine{Goal: goal, Task: task, History: kept}
}
