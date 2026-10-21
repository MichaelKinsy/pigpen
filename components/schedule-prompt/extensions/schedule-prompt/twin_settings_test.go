package schedule_prompt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// Twins of the original's test/settings.test.ts (9 cases). The original reads <agentDir>/<file> + <cwd>/.pi/<file> and sets only the
// project file; so do these (the global directory is an empty one).

const settings_ = "settings"

func settingsCwd(t *testing.T) string {
	t.Helper()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	old := agentSettingsDir
	agentSettingsDir = func() string { return empty }
	t.Cleanup(func() { agentSettingsDir = old })
	return cwd
}

func writeProjectSettings(t *testing.T, cwd, raw string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "schedule-prompts-settings.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readProjectFile(t *testing.T, cwd string) *jsobj.Object {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cwd, ".pi", "schedule-prompts-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsobj.Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsobj.Object)
}

func TestSettingsSanitize(t *testing.T) {
	tw(t, settings_, "accepts defaultJobScope='session'", func(t *testing.T) {
		cwd := settingsCwd(t)
		writeProjectSettings(t, cwd, `{"defaultJobScope":"session"}`)
		if loadSettings(cwd).defaultJobScope != "session" {
			t.Fatal("not accepted")
		}
	})
	tw(t, settings_, "accepts defaultJobScope='workdir'", func(t *testing.T) {
		cwd := settingsCwd(t)
		writeProjectSettings(t, cwd, `{"defaultJobScope":"workdir"}`)
		if loadSettings(cwd).defaultJobScope != "workdir" {
			t.Fatal("not accepted")
		}
	})
	tw(t, settings_, "drops a typo'd defaultJobScope value (no garbage propagates)", func(t *testing.T) {
		cwd := settingsCwd(t)
		writeProjectSettings(t, cwd, `{"defaultJobScope":"sesssion"}`)
		if loadSettings(cwd).defaultJobScope != "" {
			t.Fatal("garbage propagated")
		}
	})
	tw(t, settings_, "drops a non-string defaultJobScope", func(t *testing.T) {
		cwd := settingsCwd(t)
		writeProjectSettings(t, cwd, `{"defaultJobScope":42}`)
		if loadSettings(cwd).defaultJobScope != "" {
			t.Fatal("garbage propagated")
		}
	})
	tw(t, settings_, "preserves widgetVisible alongside defaultJobScope", func(t *testing.T) {
		cwd := settingsCwd(t)
		writeProjectSettings(t, cwd, `{"widgetVisible":false,"defaultJobScope":"workdir"}`)
		s := loadSettings(cwd)
		if s.widgetVisible == nil || *s.widgetVisible || s.defaultJobScope != "workdir" {
			t.Fatalf("settings = %+v", s)
		}
	})
}

func boolp(b bool) *bool { return &b }

func TestSaveSettingsPartialUpdate(t *testing.T) {
	text := func(t *testing.T, cwd string) string {
		s, _ := jsobj.Stringify(readProjectFile(t, cwd))
		return s
	}
	tw(t, settings_, "creates the project file when none exists, with only the changed field", func(t *testing.T) {
		cwd := settingsCwd(t)
		if !saveSettings(cwd, boolp(false), "") {
			t.Fatal("not saved")
		}
		if got := text(t, cwd); got != "{\n  \"widgetVisible\": false\n}" {
			t.Fatalf("file = %s", got)
		}
	})
	tw(t, settings_, "merges into an existing project file rather than overwriting", func(t *testing.T) {
		cwd := settingsCwd(t)
		saveSettings(cwd, boolp(false), "")
		saveSettings(cwd, nil, "workdir")
		if got := text(t, cwd); got != "{\n  \"widgetVisible\": false,\n  \"defaultJobScope\": \"workdir\"\n}" {
			t.Fatalf("file = %s", got)
		}
	})
	tw(t, settings_, "does NOT pin global defaults into the project file (the bug we just fixed)", func(t *testing.T) {
		// The user has a global default for defaultJobScope, then toggles widgetVisible through the UI: the project file must hold only
		// widgetVisible, so defaultJobScope keeps bleeding through from the global file.
		cwd := settingsCwd(t)
		saveSettings(cwd, boolp(true), "")
		o := readProjectFile(t, cwd)
		if got, _ := jsobj.Stringify(o); got != "{\n  \"widgetVisible\": true\n}" {
			t.Fatalf("file = %s", got)
		}
		if o.Has("defaultJobScope") {
			t.Fatal("defaultJobScope was pinned")
		}
	})
	tw(t, settings_, "overwrites a previously-set field", func(t *testing.T) {
		cwd := settingsCwd(t)
		saveSettings(cwd, boolp(false), "")
		saveSettings(cwd, boolp(true), "")
		if got := text(t, cwd); got != "{\n  \"widgetVisible\": true\n}" {
			t.Fatalf("file = %s", got)
		}
	})
}
