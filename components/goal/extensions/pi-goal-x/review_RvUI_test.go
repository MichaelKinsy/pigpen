package pi_goal_x

import "testing"

// RvUI: the questionnaire's structural regexes use JavaScript's \s (NBSP, ideographic space, ...), not RE2's ASCII \s.
func TestRvUIQuestionnaireRegexesUseJSWhitespace(t *testing.T) {
	if !checkboxRe.MatchString("[x]\u00a0done") {
		t.Errorf("checkboxRe must accept NBSP after the bracket")
	}
	if !taskLineRe.MatchString("\u00a0[ ] t1") {
		t.Errorf("taskLineRe must accept a leading NBSP")
	}
	m := sectionMarkerRe.FindStringSubmatch("───\u3000Tasks\u3000───")
	if m == nil || m[1] != "Tasks" {
		t.Errorf("sectionMarkerRe must accept ideographic spaces, got %q", m)
	}
}
