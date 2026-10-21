package ui

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeEditorTui struct {
	stops, starts int
	renders       []bool
}

func (f *fakeEditorTui) Stop()                    { f.stops++ }
func (f *fakeEditorTui) Start()                   { f.starts++ }
func (f *fakeEditorTui) RequestRender(force bool) { f.renders = append(f.renders, force) }

func TestEditWithExternalEditor(t *testing.T) {
	const f = "state/external-editor"
	old := ExternalEditorBanner
	ExternalEditorBanner = io.Discard
	t.Cleanup(func() { ExternalEditorBanner = old })

	tw(t, f, "round-trips the temp file and restores the TUI after the editor exits", func(t *testing.T) {
		editor := filepath.Join(t.TempDir(), "editor.sh")
		if err := os.WriteFile(editor, []byte("printf 'edited answer\\n' > \"$1\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		tui := &fakeEditorTui{}
		result, err := EditWithExternalEditor(tui, "/bin/sh "+editor, "draft")
		if err != nil {
			t.Fatal(err)
		}
		if result != "edited answer" {
			t.Fatalf("result %q", result)
		}
		if tui.stops != 1 || tui.starts != 1 || !reflect.DeepEqual(tui.renders, []bool{true}) {
			t.Fatalf("lifecycle %+v", tui)
		}
	})
	tw(t, f, "restores the TUI and rejects when the editor exits unsuccessfully", func(t *testing.T) {
		editor := filepath.Join(t.TempDir(), "failing-editor.sh")
		if err := os.WriteFile(editor, []byte("exit 7\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		tui := &fakeEditorTui{}
		_, err := EditWithExternalEditor(tui, "/bin/sh "+editor, "draft")
		if err == nil || !strings.Contains(err.Error(), "exit code 7") {
			t.Fatalf("err %v", err)
		}
		if tui.starts != 1 || !reflect.DeepEqual(tui.renders, []bool{true}) {
			t.Fatalf("lifecycle %+v", tui)
		}
	})
}
