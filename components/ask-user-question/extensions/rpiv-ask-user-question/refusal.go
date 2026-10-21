package ask_user_question

import (
	"fmt"
)

// The Go port does not run the interactive questionnaire yet (port/slices.json, slice B). In a terminal it asks
// with one select or input dialog per question, which is what the original does on RPC hosts. On an RPC host that
// is the original's own behaviour, collapseKey and the 600 character preview cut included. In a terminal what
// the walker cannot carry is refused, not trimmed or ignored: the tool fails with this message, which names
// the working alternative.
const (
	packageName  = "@pi-in-go/pigpen-ask-user-question"
	originalSpec = "npm:@juicesharp/rpiv-ask-user-question@2.11.0"
)

func unsupported(feature string) error {
	return fmt.Errorf("%s (Go port) does not support %s yet. Install the original extension, %s, which runs unchanged in PiG.", packageName, feature, originalSpec)
}

// refuseUnported reports the first option of this call, or setting of the user's config, that the dialog walker
// cannot honour in a terminal (mode "tui", where the original shows its questionnaire).
func refuseUnported(mode string, p questionParams) error {
	if mode == "rpc" {
		return nil
	}
	if key := resolveCollapseKey(); key != defaultCollapseKey && key != collapseKeyOff {
		return unsupported(`the collapseKey setting (the collapse key belongs to the interactive questionnaire)`)
	}
	for _, q := range p.Questions {
		for _, o := range q.Options {
			if utf16Len(o.Preview) > maxPreviewChars {
				return unsupported(fmt.Sprintf(`option previews longer than %d characters (the side-by-side preview pane; the dialog walker cuts them)`, maxPreviewChars))
			}
		}
	}
	return nil
}


// utf16Len is s.length for a JavaScript string: a character beyond the Basic Multilingual Plane counts two.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}
