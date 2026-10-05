package acp

// Twins of test/component/session-diff.test.ts.

import (
	"os"
	"path/filepath"
	"testing"
)

func completedToolUpdate(c *fakeConn, id string) Update {
	for _, u := range c.all() {
		if u.Update["toolCallId"] == id && u.Update["sessionUpdate"] == "tool_call_update" && u.Update["status"] == "completed" {
			return u.Update
		}
	}
	return nil
}

func findDiff(update Update) map[string]any {
	items, _ := update["content"].([]any)
	for _, i := range items {
		if m, _ := i.(map[string]any); m["type"] == "diff" {
			return m
		}
	}
	return nil
}

func okResult(text string) Event {
	return Event{"type": "tool_execution_end", "toolCallId": "t1", "isError": false, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}}
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSessionDiff(t *testing.T) {
	newSession := func(t *testing.T, dir string) (*fakeConn, *fakeProc, *Session) {
		conn, proc := newFakeConn(), newFakeProc()
		return conn, proc, newTestSession(dir, proc, conn)
	}

	tw(t, "component/session-diff", "PiAcpSession: emits ACP diff content for edit tool from actual before/after file contents", func(t *testing.T) {
		dir := tmp(t)
		file := filepath.Join(dir, "a.txt")
		write(t, file, "before\n")
		conn, proc, s := newSession(t, dir)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "edit", "args": map[string]any{"path": "a.txt"}})
		write(t, file, "after\n")
		proc.emit(okResult("ok"))
		settle(t, s)
		end := completedToolUpdate(conn, "t1")
		if end == nil {
			t.Fatal("expected completed tool_call_update")
		}
		if _, ok := end["content"].([]any); !ok {
			t.Fatal("expected content array")
		}
		d := findDiff(end)
		if d == nil {
			t.Fatal("expected diff content item")
		}
		if d["path"] != "a.txt" || d["oldText"] != "before\n" || d["newText"] != "after\n" {
			t.Errorf("diff = %v", d)
		}
		if _, ok := end["rawOutput"]; ok {
			t.Error("expected raw output to be suppressed when diff is emitted")
		}
	})

	tw(t, "component/session-diff", "PiAcpSession: does not turn requested edit args into finalized ACP diffs at tool start", func(t *testing.T) {
		dir := tmp(t)
		file := filepath.Join(dir, "a.txt")
		write(t, file, "before\n")
		conn, proc, s := newSession(t, dir)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "edit",
			"args": map[string]any{"path": "a.txt", "edits": []any{map[string]any{"oldText": "before", "newText": "after"}}}})
		settle(t, s)
		var start Update
		for _, u := range conn.all() {
			if u.Update["toolCallId"] == "t1" && u.Update["sessionUpdate"] == "tool_call" {
				start = u.Update
			}
		}
		if start == nil {
			t.Fatal("expected tool_call for edit start")
		}
		if _, ok := start["content"]; ok {
			t.Error("expected no start-time diff from requested edit args")
		}
		write(t, file, "after\n")
		proc.emit(okResult("ok"))
		settle(t, s)
		end := completedToolUpdate(conn, "t1")
		if end == nil {
			t.Fatal("expected completed tool_call_update")
		}
		d := findDiff(end)
		if d == nil || d["oldText"] != "before\n" || d["newText"] != "after\n" {
			t.Errorf("diff = %v", d)
		}
	})

	tw(t, "component/session-diff", "PiAcpSession: edit diff uses realized fuzzy-match file contents instead of requested args", func(t *testing.T) {
		dir := tmp(t)
		file := filepath.Join(dir, "fuzzy.txt")
		write(t, file, "FULLWIDTH: ＡＢＣ１２３\n")
		conn, proc, s := newSession(t, dir)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "edit",
			"args": map[string]any{"path": "fuzzy.txt", "edits": []any{map[string]any{"oldText": "FULLWIDTH: ABC123", "newText": "FULLWIDTH: ascii replacement"}}}})
		write(t, file, "FULLWIDTH: ascii replacement\n")
		proc.emit(okResult("Successfully replaced 1 block(s) in fuzzy.txt."))
		settle(t, s)
		end := completedToolUpdate(conn, "t1")
		if end == nil {
			t.Fatal("expected completed tool_call_update")
		}
		d := findDiff(end)
		if d == nil || d["oldText"] != "FULLWIDTH: ＡＢＣ１２３\n" || d["newText"] != "FULLWIDTH: ascii replacement\n" {
			t.Errorf("diff = %v", d)
		}
	})

	tw(t, "component/session-diff", "PiAcpSession: emits write diff content from actual before/after file contents on completion", func(t *testing.T) {
		dir := tmp(t)
		file := filepath.Join(dir, "a.txt")
		write(t, file, "before\n")
		conn, proc, s := newSession(t, dir)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "write", "args": map[string]any{"path": "a.txt", "content": "after\n"}})
		settle(t, s)
		var start Update
		for _, u := range conn.all() {
			if u.Update["toolCallId"] == "t1" && u.Update["sessionUpdate"] == "tool_call" {
				start = u.Update
			}
		}
		if start == nil {
			t.Fatal("expected tool_call for write start")
		}
		if _, ok := start["content"]; ok {
			t.Error("expected no start-time diff for write")
		}
		write(t, file, "after\n")
		proc.emit(okResult("Successfully wrote 6 bytes to a.txt"))
		settle(t, s)
		end := completedToolUpdate(conn, "t1")
		if end == nil {
			t.Fatal("expected completed tool_call_update")
		}
		d := findDiff(end)
		if d == nil || d["path"] != "a.txt" || d["oldText"] != "before\n" || d["newText"] != "after\n" {
			t.Errorf("diff = %v", d)
		}
		if _, ok := end["rawOutput"]; ok {
			t.Error("expected raw output to be suppressed when diff is emitted")
		}
	})

	tw(t, "component/session-diff", "PiAcpSession: emits write diff content for new files on completion", func(t *testing.T) {
		dir := tmp(t)
		file := filepath.Join(dir, "new.txt")
		conn, proc, s := newSession(t, dir)
		proc.emit(Event{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "write", "args": map[string]any{"path": "new.txt", "content": "created\n"}})
		write(t, file, "created\n")
		proc.emit(okResult("Successfully wrote 8 bytes to new.txt"))
		settle(t, s)
		end := completedToolUpdate(conn, "t1")
		if end == nil {
			t.Fatal("expected completed tool_call_update")
		}
		d := findDiff(end)
		if d == nil || d["path"] != "new.txt" || d["oldText"] != nil || d["newText"] != "created\n" {
			t.Errorf("diff = %v", d)
		}
	})
}
