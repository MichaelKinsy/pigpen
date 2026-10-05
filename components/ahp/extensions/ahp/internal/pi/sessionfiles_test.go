package pi_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fixtureSessionDirectory mirrors Pi's default cwd partition solely for synthetic JSONL fixtures
// (upstream test/support/session-files.ts).
func fixtureSessionDirectory(t testing.TB, root, cwd string) string {
	t.Helper()
	abs, err := filepath.Abs(cwd)
	if err != nil {
		t.Fatal(err)
	}
	abs = strings.TrimLeft(abs, `/\`)
	partition := "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(abs) + "--"
	dir := filepath.Join(root, partition)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

type fakeSession struct {
	cwd              string
	firstUserMessage string
	name             string
	mtimeSeconds     int64 // controls catalogue ordering
}

var entryCounter int

// writeFakeSession writes a minimal but genuine Pi session file: a session header followed by
// entries linked through id/parentId (upstream writeFakeSession).
func writeFakeSession(t testing.TB, root, id string, o fakeSession) string {
	t.Helper()
	dir := fixtureSessionDirectory(t, root, o.cwd)
	timestamp := time.Unix(o.mtimeSeconds, 0).UTC().Format("2006-01-02T15:04:05.000Z")
	line := func(v map[string]any) string {
		raw, _ := json.Marshal(v)
		return string(raw)
	}
	lines := []string{line(map[string]any{"type": "session", "id": id, "parentId": nil, "timestamp": timestamp, "version": 3, "cwd": o.cwd})}
	var parent any
	push := func(entry map[string]any) {
		entryCounter++
		entryID := fmt.Sprintf("entry-%d", entryCounter)
		entry["id"], entry["parentId"], entry["timestamp"] = entryID, parent, timestamp
		lines = append(lines, line(entry))
		parent = entryID
	}
	if o.firstUserMessage != "" {
		push(map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": o.firstUserMessage, "timestamp": 0}})
	}
	if o.name != "" {
		push(map[string]any{"type": "session_info", "name": o.name})
	}
	path := filepath.Join(dir, strconv.FormatInt(o.mtimeSeconds, 10)+"_"+id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(o.mtimeSeconds, 0)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}
