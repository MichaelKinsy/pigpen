package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kagent-dev/kagent/go/harness/runtime"
)

func approval(phase Phase) Record {
	return Record{
		Version: Version, TaskID: "t", ContextID: "c", SessionID: "s", Phase: phase, Kind: KindApproval, AssistantEntryID: "a1",
		Calls:  []Call{{ID: "call-1", Name: "mcp__kb__probe", Arguments: []byte(`{"note":"x"}`)}, {ID: "call-2", Name: "echo", Arguments: []byte(`{}`)}},
		CallID: "call-1", Server: "kb", Tool: "probe",
		Approval: &runtime.ApprovalRequest{ID: "ap", CallID: "call-1", Name: "mcp__kb__probe", Args: map[string]any{"note": "x"}},
	}
}

func TestSaveLoadRoundTripAndPermissions(t *testing.T) {
	store := Open(filepath.Join(t.TempDir(), "adapter"))
	if r, err := store.Load(); r != nil || err != nil {
		t.Fatalf("empty = %v, %v", r, err)
	}
	record := approval(Waiting)
	if err := store.Save(&record); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil || got.CallID != "call-1" || len(got.Calls) != 2 || got.Approval.ID != "ap" || got.Phase != Waiting {
		t.Fatalf("loaded = %+v, %v", got, err)
	}
	info, _ := os.Stat(store.Path())
	dir, _ := os.Stat(filepath.Dir(store.Path()))
	if info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
		t.Fatalf("modes = %v %v", info.Mode().Perm(), dir.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(store.Path()))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestSaveReplacesAtomicallyEvenWhenAStaleTempFileExists(t *testing.T) {
	store := Open(t.TempDir())
	record := approval(Waiting)
	if err := store.Save(&record); err != nil {
		t.Fatal(err)
	}
	// a crash between "write temp" and "rename" leaves a temp file; the real record is untouched
	os.WriteFile(filepath.Join(filepath.Dir(store.Path()), ".pig-pending.json-crash.tmp"), []byte(`{"phase":"complete"`), 0o600)
	got, err := store.Load()
	if err != nil || got.Phase != Waiting {
		t.Fatalf("loaded = %+v, %v", got, err)
	}
	next := approval(Waiting)
	next.Phase, next.Decision = Executing, &runtime.ApprovalDecision{ID: "ap", Approved: true}
	if err := store.Save(&next); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Load(); got.Phase != Executing {
		t.Fatalf("phase = %s", got.Phase)
	}
}

func TestValidateTable(t *testing.T) {
	decision := &runtime.ApprovalDecision{ID: "ap", Approved: true}
	rejected := &runtime.ApprovalDecision{ID: "ap", Approved: false}
	out := &Outcome{Content: []Block{{Type: "text", Text: "r"}}}
	ask := func(phase Phase) Record {
		r := approval(phase)
		r.Kind, r.Approval, r.Server, r.Tool = KindAskUser, nil, "", ""
		r.Question = &runtime.AskUserRequest{ID: "q", Questions: []runtime.AskUserQuestion{{ID: "q1", Question: "which?"}}}
		return r
	}
	mutate := func(r Record, f func(*Record)) Record { f(&r); return r }
	tests := []struct {
		name   string
		record Record
		ok     bool
	}{
		{"waiting approval", approval(Waiting), true},
		{"waiting ask", ask(Waiting), true},
		{"executing approved", mutate(approval(Executing), func(r *Record) { r.Decision = decision }), true},
		{"executing without decision", approval(Executing), false},
		{"executing rejected", mutate(approval(Executing), func(r *Record) { r.Decision = rejected }), false},
		{"executing ask", mutate(ask(Executing), func(r *Record) { r.Answer = &runtime.AskUserResponse{ID: "q"} }), false},
		{"result approval", mutate(approval(Result), func(r *Record) { r.Decision, r.Result = rejected, out }), true},
		{"result without result", mutate(approval(Result), func(r *Record) { r.Decision = rejected }), false},
		{"result ask", mutate(ask(Result), func(r *Record) {
			r.Answer, r.Result = &runtime.AskUserResponse{ID: "q", Answers: [][]string{{"a"}}}, out
		}), true},
		{"continuing needs answer", mutate(ask(Continuing), func(r *Record) { r.Result = out }), false},
		{"complete", mutate(approval(Complete), func(r *Record) { r.Decision, r.Result = decision, out }), true},
		{"canceled", approval(Canceled), true},
		{"abandoned", approval(Abandoned), true},
		{"waiting with decision", mutate(approval(Waiting), func(r *Record) { r.Decision = decision }), false},
		{"unknown phase", approval("weird"), false},
		{"unknown kind", mutate(approval(Waiting), func(r *Record) { r.Kind = "x" }), false},
		{"bad version", mutate(approval(Waiting), func(r *Record) { r.Version = 2 }), false},
		{"no task", mutate(approval(Waiting), func(r *Record) { r.TaskID = "" }), false},
		{"no assistant entry", mutate(approval(Waiting), func(r *Record) { r.AssistantEntryID = "" }), false},
		{"no calls", mutate(approval(Waiting), func(r *Record) { r.Calls = nil }), false},
		{"call outside message", mutate(approval(Waiting), func(r *Record) { r.CallID = "zzz"; r.Approval.CallID = "zzz" }), false},
		{"approval call mismatch", mutate(approval(Waiting), func(r *Record) { r.Approval.CallID = "call-2" }), false},
		{"approval without server", mutate(approval(Waiting), func(r *Record) { r.Server = "" }), false},
		{"both requests", mutate(approval(Waiting), func(r *Record) {
			r.Question = &runtime.AskUserRequest{ID: "q", Questions: []runtime.AskUserQuestion{{ID: "x"}}}
		}), false},
		{"ask without questions", mutate(ask(Waiting), func(r *Record) { r.Question.Questions = nil }), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.record.Validate()
			if (err == nil) != tc.ok {
				t.Fatalf("Validate = %v, want ok=%v", err, tc.ok)
			}
			store := Open(t.TempDir())
			if saveErr := store.Save(&tc.record); (saveErr == nil) != tc.ok {
				t.Fatalf("Save = %v, want ok=%v", saveErr, tc.ok)
			}
		})
	}
}

func TestLoadRejectsCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir)
	good := approval(Waiting)
	if err := store.Save(&good); err != nil {
		t.Fatal(err)
	}
	valid, _ := os.ReadFile(store.Path())
	for name, content := range map[string]string{
		"garbage": "not json", "trailing": string(valid) + "{}", "unknown field": strings.Replace(string(valid), `"version":1`, `"version":1,"extra":true`, 1),
		"invalid record": strings.Replace(string(valid), `"phase":"waiting"`, `"phase":"nonsense"`, 1),
	} {
		os.WriteFile(store.Path(), []byte(content), 0o600)
		if r, err := store.Load(); err == nil {
			t.Errorf("%s: loaded %+v", name, r)
		}
	}
	os.Remove(store.Path())
	os.Symlink("/etc/hostname", store.Path())
	if _, err := store.Load(); err == nil {
		t.Error("a symlink record was read")
	}
}

func TestLoadRereadsTheFileEveryTime(t *testing.T) {
	store := Open(t.TempDir())
	record := approval(Waiting)
	store.Save(&record)
	first, _ := store.Load()
	canceled := approval(Canceled)
	store.Save(&canceled)
	second, _ := store.Load()
	if first.Phase != Waiting || second.Phase != Canceled {
		t.Fatalf("phases = %s, %s", first.Phase, second.Phase)
	}
}
