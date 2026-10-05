package ask_user_question

import (
	"strconv"
	"strings"
)

// The dialog walker for hosts that cannot render the questionnaire component (RPC): one native select or input
// dialog per question. upstream: rpc-fallback.ts.
const (
	multiSelectInstructions = `Enter the numbers of all that apply, comma-separated (e.g. "1,3"), or type a custom answer as plain text.`
	customAnswerTitle       = "Type your answer:"
	multiSelectPlaceholder  = "1,3"
	maxPreviewChars         = 600
)

type dialogUI interface {
	Select(title string, options []string) (string, bool, error)
	Input(title, placeholder string) (string, bool, error)
}

func formatOptionLine(o option, index int) string {
	return strconv.Itoa(index+1) + ". " + o.Label + " — " + o.Description
}

// jsParseInt is parseInt(s, 10): optional leading white space and sign, then the leading digits; ok is false for NaN.
func jsParseInt(s string) (int, bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0, false // beyond int: out of range of any list, exactly as the huge float is in JavaScript
	}
	if neg {
		n = -n
	}
	return n, true
}

// parseIndex is the zero-based index a token names (`3` is 2), or false when it is out of range or not a number.
func parseIndex(token string, count int) (int, bool) {
	n, ok := jsParseInt(token)
	i := n - 1
	if !ok || i < 0 || i >= count {
		return 0, false
	}
	return i, true
}

func buildPreviewBlock(q question) string {
	var blocks []string
	for i, o := range q.Options {
		if o.Preview == "" {
			continue
		}
		blocks = append(blocks, "--- "+strconv.Itoa(i+1)+". "+o.Label+" preview ---\n"+utf16Prefix(o.Preview, maxPreviewChars))
	}
	if len(blocks) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(blocks, "\n\n")
}

// utf16Prefix is s.slice(0, n) for a JavaScript string: n UTF-16 code units (a surrogate pair counts two).
func utf16Prefix(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}

// runRpcQuestionnaire asks every question in turn. A dismissed dialog cancels the questionnaire; the answers
// given so far are kept. An error from the host is returned as is.
func runRpcQuestionnaire(ui dialogUI, p questionParams) (questionnaireResult, error) {
	var answers []questionAnswer
	for qi, q := range p.Questions {
		header := ""
		if q.Header != "" {
			header = "[" + q.Header + "] "
		}
		var a *questionAnswer
		var err error
		if q.MultiSelect {
			a, err = askMultiSelect(ui, q, qi, header)
		} else {
			a, err = askSingleSelect(ui, q, qi, header)
		}
		if err != nil {
			return questionnaireResult{}, err
		}
		if a == nil {
			return questionnaireResult{Answers: answers, Cancelled: true}, nil
		}
		answers = append(answers, *a)
	}
	return questionnaireResult{Answers: answers}, nil
}

func askSingleSelect(ui dialogUI, q question, qi int, header string) (*questionAnswer, error) {
	options := make([]string, 0, len(q.Options)+1)
	for i, o := range q.Options {
		options = append(options, formatOptionLine(o, i))
	}
	options = append(options, strconv.Itoa(len(q.Options)+1)+". "+labelOther)
	chosen, ok, err := ui.Select(header+q.Question+buildPreviewBlock(q), options)
	if err != nil || !ok {
		return nil, err
	}
	idx, ok := parseIndex(chosen, len(options))
	if !ok {
		return nil, nil
	}
	if idx < len(q.Options) {
		o := q.Options[idx]
		return &questionAnswer{QuestionIndex: qi, Question: q.Question, Kind: "option", Answer: &o.Label, Preview: o.Preview}, nil
	}
	typed, ok, err := ui.Input(header+q.Question+"\n\n"+customAnswerTitle, "")
	if err != nil || !ok {
		return nil, err
	}
	return &questionAnswer{QuestionIndex: qi, Question: q.Question, Kind: "custom", Answer: &typed}, nil
}

func askMultiSelect(ui dialogUI, q question, qi int, header string) (*questionAnswer, error) {
	lines := make([]string, 0, len(q.Options))
	for i, o := range q.Options {
		lines = append(lines, formatOptionLine(o, i))
	}
	value, ok, err := ui.Input(header+q.Question+"\n\n"+strings.Join(lines, "\n")+"\n\n"+multiSelectInstructions, multiSelectPlaceholder)
	if err != nil || !ok {
		return nil, err
	}
	trimmed := jsTrim(value)
	if trimmed == "" {
		return &questionAnswer{QuestionIndex: qi, Question: q.Question, Kind: "multi", HasSelected: true}, nil
	}
	var indices []int
	valid := true
	for _, tok := range strings.FieldsFunc(trimmed, func(r rune) bool { return r == ',' || isJSSpace(r) }) {
		i, ok := -1, false
		if isIndexToken(tok) {
			i, ok = parseIndex(tok, len(q.Options))
		}
		if !ok {
			valid = false
			break
		}
		indices = append(indices, i)
	}
	if valid {
		selected := []string{}
		for _, i := range indices {
			label := q.Options[i].Label
			if !hasString(selected, label) {
				selected = append(selected, label)
			}
		}
		return &questionAnswer{QuestionIndex: qi, Question: q.Question, Kind: "multi", Selected: selected, HasSelected: true}, nil
	}
	return &questionAnswer{QuestionIndex: qi, Question: q.Question, Kind: "custom", Answer: &trimmed}, nil
}

// isIndexToken is /^\d+\.?$/.
func isIndexToken(tok string) bool {
	tok = strings.TrimSuffix(tok, ".")
	if tok == "" {
		return false
	}
	for _, r := range tok {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hasString(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}
