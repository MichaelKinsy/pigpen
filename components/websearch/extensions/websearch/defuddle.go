package websearch

// The Defuddle fallback of extract.ts (extractWithDefuddle: linkedom document, Defuddle with markdown: true and
// useAsync: false) is not ported: no Go port tracks the vendored Defuddle 0.19.3 (dotcommander/defuddle and
// kaptinlin/defuddle-go build their Markdown with html-to-markdown and a different scoring, vaayne/go-defuddle runs the
// JavaScript in an embedded engine), and the library's removal, scoring, standardization and Markdown steps are several
// thousand lines. Where the original would try it, the port reports the refusal after the original's own verdict.

type defuddleResult struct{ Title, Content string }

var errDefuddleNotPorted = unsupported("the Defuddle fallback extractor")

// extractWithDefuddle always refuses: the page is neither silently skipped nor passed off as processed.
func extractWithDefuddle(string, string) (*defuddleResult, error) { return nil, errDefuddleNotPorted }
