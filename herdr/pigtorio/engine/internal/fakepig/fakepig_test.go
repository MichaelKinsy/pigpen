package fakepig

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOSCWireForm(t *testing.T) {
	if got := OSC("state=working", "app=pig"); got != "\x1b]7501;state=working:app=pig\x1b\\" {
		t.Fatalf("%q", got)
	}
}

func TestReadMessagesFramesByQuietPeriod(t *testing.T) {
	pr, pw := io.Pipe()
	out := make(chan string, 8)
	go ReadMessages(pr, 100*time.Millisecond, out)
	// one message arriving in pieces, with paste markers and a trailing enter
	pw.Write([]byte("\x1b[200~line one\nli"))
	time.Sleep(30 * time.Millisecond)
	pw.Write([]byte("ne two\x1b[201~\r"))
	select {
	case m := <-out:
		t.Fatalf("message cut before the quiet period: %q", m)
	case <-time.After(40 * time.Millisecond):
	}
	if m := <-out; m != "line one\nline two" {
		t.Fatalf("first message %q", m)
	}
	pw.Write([]byte("second\r\n"))
	if m := <-out; m != "second" {
		t.Fatalf("second message %q", m)
	}
	pw.Write([]byte("\r\n")) // an empty enter is not a message
	pw.Write([]byte("last"))
	pw.Close() // EOF flushes
	if m := <-out; m != "last" {
		t.Fatalf("last message %q", m)
	}
	if _, ok := <-out; ok {
		t.Fatal("out not closed")
	}
}

type harness struct {
	t       *testing.T
	w       *io.PipeWriter
	reports chan string
	cwd     string
	dir     string
	done    chan error
}

func start(t *testing.T) *harness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &harness{t: t, w: inW, reports: make(chan string, 64), cwd: t.TempDir(), dir: t.TempDir(), done: make(chan error, 1)}
	go func() { // drain the terminal output, keeping only the OSC reports
		rd := bufio.NewReader(outR)
		var buf strings.Builder
		for {
			b, err := rd.ReadByte()
			if err != nil {
				return
			}
			buf.WriteByte(b)
			s := buf.String()
			if i := strings.Index(s, "\x1b]7501;"); i >= 0 {
				if j := strings.Index(s[i:], "\x1b\\"); j >= 0 {
					h.reports <- s[i+len("\x1b]7501;") : i+j]
					buf.Reset()
					buf.WriteString(s[i+j+2:])
				}
			}
		}
	}()
	go func() {
		err := Run(Options{SessionDir: h.dir, Name: "fp", Work: 60 * time.Millisecond, Quiet: 60 * time.Millisecond, Idle: 200 * time.Millisecond, Cwd: h.cwd, In: inR, Out: outW})
		outW.Close()
		h.done <- err
	}()
	t.Cleanup(func() { inW.Close() })
	return h
}

// states returns the next n OSC reports.
func (h *harness) states(n int) []string {
	h.t.Helper()
	var got []string
	for len(got) < n {
		select {
		case r := <-h.reports:
			got = append(got, r)
		case <-time.After(10 * time.Second):
			h.t.Fatalf("timed out waiting for %d reports, have %v", n, got)
		}
	}
	return got
}

func (h *harness) session() []map[string]any {
	h.t.Helper()
	ents, _ := filepath.Glob(filepath.Join(h.dir, "*.jsonl"))
	if len(ents) != 1 {
		h.t.Fatalf("session files %v", ents)
	}
	b, _ := os.ReadFile(ents[0])
	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			h.t.Fatalf("bad jsonl %q", l)
		}
		lines = append(lines, m)
	}
	return lines
}

func TestFakepigReportsAndWritesSession(t *testing.T) {
	h := start(t)
	if st := h.states(1); st[0] != "state=idle:app=pig" {
		t.Fatalf("first report %v", st)
	}
	h.w.Write([]byte("\x1b[200~Crate c1: Make it\nDone when: it exists\x1b[201~\r"))
	st := h.states(2)
	if st[0] != "state=working:app=pig" || st[1] != "state=done:app=pig" {
		t.Fatalf("reports %v", st)
	}
	// done is only reported after the session and the file are on disk
	if b, err := os.ReadFile(filepath.Join(h.cwd, "pigtorio-fake-1.txt")); err != nil || !strings.Contains(string(b), "Crate c1: Make it") {
		t.Fatalf("file: %v %q", err, b)
	}
	lines := h.session()
	if lines[0]["type"] != "session" || lines[0]["cwd"] != h.cwd {
		t.Fatalf("header %v", lines[0])
	}
	last := lines[len(lines)-1]
	msg := last["message"].(map[string]any)
	usage := msg["usage"].(map[string]any)
	text := msg["content"].([]any)[0].(map[string]any)["text"].(string)
	if last["type"] != "message" || msg["role"] != "assistant" || usage["totalTokens"].(float64) <= 0 || !strings.Contains(text, "Files touched: pigtorio-fake-1.txt") {
		t.Fatalf("assistant line %v", last)
	}
	if usage["cost"].(map[string]any)["total"].(float64) <= 0 {
		t.Fatal("no cost")
	}
	// idle follows done after the idle delay
	if st := h.states(1); st[0] != "state=idle:app=pig" {
		t.Fatalf("after done: %v", st)
	}
	// the next message writes the next file
	h.w.Write([]byte("again\r"))
	h.states(2)
	if _, err := os.Stat(filepath.Join(h.cwd, "pigtorio-fake-2.txt")); err != nil {
		t.Fatal(err)
	}
	h.w.Close()
	if err := <-h.done; err != nil {
		t.Fatal(err)
	}
}

func TestFakepigFailChangesNothing(t *testing.T) {
	h := start(t)
	h.states(1)
	h.w.Write([]byte("please FAIL this\r"))
	if st := h.states(2); st[1] != "state=done:app=pig" {
		t.Fatalf("%v", st)
	}
	if m, _ := filepath.Glob(filepath.Join(h.cwd, "pigtorio-fake-*.txt")); len(m) != 0 {
		t.Fatalf("FAIL wrote %v", m)
	}
}

func TestFakepigAskBlocksUntilNextMessage(t *testing.T) {
	h := start(t)
	h.states(1)
	h.w.Write([]byte("build it ASK: which colour?\r"))
	st := h.states(2)
	if st[0] != "state=working:app=pig" || st[1] != "state=blocked:app=pig:kind=question" {
		t.Fatalf("%v", st)
	}
	// still blocked: no done arrives by itself
	time.Sleep(300 * time.Millisecond)
	h.w.Write([]byte("blue\r"))
	st = h.states(2)
	if st[0] != "state=working:app=pig" || st[1] != "state=done:app=pig" {
		t.Fatalf("%v", st)
	}
	b, _ := os.ReadFile(filepath.Join(h.cwd, "pigtorio-fake-1.txt"))
	if !strings.Contains(string(b), "answer: blue") {
		t.Fatalf("%q", b)
	}
}
