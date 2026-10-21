package powerline_footer_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Go SDK has no keybinding lookup (E10) and no tui.hasOverlay(). Nothing in the extension may depend on them: a call would
// fail or silently return nothing at run time, so a feature that needs one has to be replaced by
// a documented stand-in instead of half working. This test reads every non-test source file, drops the comments, and fails when
// the code reaches for one of those host capabilities. (SetEditorComponent and GetEditorComponent exist since PiG v0.5.0 and are
// allowed.)
func TestNoCodePathDependsOnAKeybindingOrOverlayCapabilityTheSdkLacks(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)\b(hasOverlay|KeybindingsManager|keybindings\.matches|GetKeybindings|Keybinding(s)?\w*)\b`)
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources found: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			code := line
			if at := strings.Index(code, "//"); at >= 0 {
				code = code[:at]
			}
			if loc := forbidden.FindString(code); loc != "" {
				t.Errorf("%s:%d uses %q, which the Go SDK does not offer; refuse the feature instead", file, i+1, loc)
			}
		}
	}
}
