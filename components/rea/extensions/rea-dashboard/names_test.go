package readashboard

import (
	"strings"
	"testing"
)

func TestSanitizePigletName(t *testing.T) {
	for in, want := range map[string]string{
		"pig-ls-like":          "pig-ls-like",
		"My Cool_App":          "my-cool-app",
		"  Foo.Bar  ":          "foo-bar",
		"--weird--name--":      "weird-name",
		"UPPER123":             "upper123",
		"a//b\\c":              "a-b-c",
		"héllo wörld":          "h-llo-w-rld",
		"x; rm -rf /":          "x-rm-rf",
		"../../etc":            "etc",
		"":                     "target",
		"!!!":                  "target",
		"日本語":                  "target",
		"already-fine-9":       "already-fine-9",
		"two  spaces\ttab\nnl": "two-spaces-tab-nl",
	} {
		if got := sanitizePigletName(in); got != want {
			t.Errorf("sanitizePigletName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultPigletName(t *testing.T) {
	for in, want := range map[string]string{
		"/bin/ls":                              "pig-ls-like",
		"/Applications/Visual Studio Code.app": "pig-visual-studio-code-like",
		"/opt/tools/Foo_Bar.exe":               "pig-foo-bar-like",
		"/opt/tools/archive.tar.gz":            "pig-archive-tar-like",
		"C:\\Program Files\\Zed\\zed.exe":      "pig-zed-like",
		"https://Example.test/app/main.js":     "pig-example-test-like",
		"/":                                    "pig-target-like",
		"/opt/.hidden":                         "pig-hidden-like",
	} {
		got := defaultPigletName(in)
		if got != want {
			t.Errorf("defaultPigletName(%q) = %q, want %q", in, got, want)
		}
		if sanitizePigletName(got) != got {
			t.Errorf("default name %q is not stable under sanitizing", got)
		}
	}
}

func TestDeriveRequestText(t *testing.T) {
	got := deriveRequest("pig-ls-like", "/bin/ls")
	want := "Use the pigpen-derive-a-harness skill to derive a piglet named pig-ls-like that works like /bin/ls. " +
		"Keep REA output under evidence/, write the behavior spec under spec/ with each claim linked to an evidence_id, " +
		"author piglets/pig-ls-like/ from spec/ only, and end the iteration with pig piglet validate and pig piglet show."
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestInvestigateRequestNamesTheTarget(t *testing.T) {
	got := investigateRequest("/bin/ls", "  what does it link against?  ")
	if !strings.Contains(got, "/bin/ls") || !strings.Contains(got, "Question: what does it link against?") || !strings.Contains(got, `"rea"`) {
		t.Fatalf("got %q", got)
	}
}

func TestTabFromArgs(t *testing.T) {
	for in, want := range map[string]tabID{"": tabInvestigation, "targets": tabTargets, "Prov": tabProviders, "about now": tabAbout, "zzz": tabInvestigation, "inv": tabInvestigation} {
		if got := tabFromArgs(in); got != want {
			t.Errorf("tabFromArgs(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSanitizeAndWidth(t *testing.T) {
	if got := sanitize("a\x1b[31mb\nc\x07"); got != "a [31mb c " {
		t.Fatalf("sanitize = %q", got)
	}
	if textWidth("日本") != 4 || textWidth("é") != 1 || textWidth("e\u0301") != 1 {
		t.Fatal("textWidth")
	}
	if got := truncate("abcdef", 4); got != "abc…" {
		t.Fatalf("truncate = %q", got)
	}
	if got := truncate("日本語", 4); textWidth(got) > 4 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncate wide = %q", got)
	}
	if truncate("abc", 0) != "" || truncate("abcdef", 1) != "…" {
		t.Fatal("truncate edges")
	}
	if got := tailFit("/very/long/path/to/file.bin", 12); textWidth(got) > 12 || !strings.HasSuffix(got, "file.bin") || !strings.HasPrefix(got, "…") {
		t.Fatalf("tailFit = %q", got)
	}
	for _, line := range wrapText("日本語のテキスト and some words", 7) {
		if textWidth(line) > 7 {
			t.Fatalf("wrapText line %q", line)
		}
	}
}

func TestParseKey(t *testing.T) {
	for in, want := range map[string]key{
		"\x1b[A": {kind: keyUp}, "\x1bOB": {kind: keyDown}, "\x1b[1;5C": {kind: keyRight}, "\x1b[D": {kind: keyLeft},
		"\r": {kind: keyEnter}, "\x1b": {kind: keyEsc}, "\t": {kind: keyTab}, "\x1b[Z": {kind: keyShiftTab},
		"\x1b[5~": {kind: keyPageUp}, "\x1b[6~": {kind: keyPageDown}, "\x1b[H": {kind: keyHome}, "\x1b[4~": {kind: keyEnd},
		"q": {kind: keyText, r: 'q'}, "\x1b[113u": {kind: keyText, r: 'q'}, "\x1b[9u": {kind: keyTab}, "\x1b[9;2u": {kind: keyShiftTab},
		"\x03": {kind: keyCtrlC}, "\x1b[99;5u": {kind: keyCtrlC}, "\x1b[113;1:3u": {}, "\x1b[1;1:3A": {}, "": {}, "\x1bx": {}, "\x1b[<0;1;1M": {},
	} {
		if got := parseKey(in); got != want {
			t.Errorf("parseKey(%q) = %+v, want %+v", in, got, want)
		}
	}
}
