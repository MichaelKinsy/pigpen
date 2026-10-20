package powerline_footer_test

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	powerline "github.com/MichaelKinsy/pigpen/powerline-footer"
)

// A bare frame spy for what the fake host drops: the registration's command descriptions and the overlay's rendered lines
// (the ui.custom.render notifications). It speaks the same length-prefixed JSON frames as fakehost_test.go.

type spyFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Register *struct {
		Commands []struct{ Name, Description string } `json:"commands"`
	} `json:"register"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call"`
	Notify *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"notify"`
}

type spy struct {
	t       *testing.T
	nc      net.Conn
	wmu     sync.Mutex
	mu      sync.Mutex
	renders [][]string
	done    chan struct{}
}

func (s *spy) write(v any) {
	data, _ := json.Marshal(v)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, _ = s.nc.Write(hdr[:])
	_, _ = s.nc.Write(data)
}

func (s *spy) read() (spyFrame, bool) {
	var hdr [4]byte
	if _, err := io.ReadFull(s.nc, hdr[:]); err != nil {
		return spyFrame{}, false
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(s.nc, data); err != nil {
		return spyFrame{}, false
	}
	var f spyFrame
	_ = json.Unmarshal(data, &f)
	return f, true
}

// startSpy runs the extension, reads its registration and sends ready; answer maps a host call to its result.
func startSpy(t *testing.T, state map[string]any, answer func(method string, args json.RawMessage) any) (*spy, map[string]string) {
	t.Helper()
	hostSide, extSide := net.Pipe()
	s := &spy{t: t, nc: hostSide, done: make(chan struct{})}
	runDone := make(chan error, 1)
	go func() { runDone <- powerline.Extension().RunWithConn(extSide) }()
	first, ok := s.read()
	if !ok || first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register, got %+v", first)
	}
	descriptions := map[string]string{}
	for _, c := range first.Register.Commands {
		descriptions[c.Name] = c.Description
	}
	stateJSON, _ := json.Marshal(state)
	s.write(map[string]any{"type": "ready", "ready": map[string]any{"session_name": "", "cwd": t.TempDir(), "mode": "tui", "width": 80, "state": json.RawMessage(stateJSON)}})
	go func() {
		defer close(s.done)
		for {
			f, ok := s.read()
			if !ok {
				return
			}
			switch f.Type {
			case "call":
				method, args, id := f.Call.Method, f.Call.Args, f.ID
				go func() {
					result := answer(method, args)
					if result == nil {
						result = map[string]any{}
					}
					s.write(map[string]any{"type": "call_result", "id": id, "call_result": map[string]any{"result": result}})
				}()
			case "notify":
				if f.Notify != nil && f.Notify.Method == "ui.custom.render" {
					var p struct{ Lines []string }
					_ = json.Unmarshal(f.Notify.Args, &p)
					s.mu.Lock()
					s.renders = append(s.renders, p.Lines)
					s.mu.Unlock()
				}
			}
		}
	}()
	t.Cleanup(func() {
		s.write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
		select {
		case <-runDone:
		case <-time.After(10 * time.Second):
		}
		_ = hostSide.Close()
	})
	return s, descriptions
}

func TestCommandDescriptionsAreTheOriginals(t *testing.T) {
	_, descriptions := startSpy(t, map[string]any{"hasUI": true}, func(string, json.RawMessage) any { return nil })
	if got := descriptions["cd"]; got != "Switch the Pi session working directory" {
		t.Errorf("/cd description %q", got)
	}
	if got := descriptions["reply"]; got != "Quote a previous user or assistant message into the editor" {
		t.Errorf("/reply description %q", got)
	}
}

// The picker is drawn with the host's theme and at the terminal width.
func TestReplyOverlayRendersWithTheHostTheme(t *testing.T) {
	theme := map[string]any{"name": "t", "foregrounds": map[string]any{"accent": "\x1b[31m", "success": "\x1b[32m", "borderMuted": "\x1b[33m", "dim": "\x1b[34m", "muted": "\x1b[35m"}, "backgrounds": map[string]any{}}
	branch := []any{msg("aaa111", "user", "first question"), msg("bbb222", "assistant", "an answer")}
	s, _ := startSpy(t, map[string]any{"hasUI": true, "theme": theme}, func(method string, args json.RawMessage) any {
		switch method {
		case "sessionRead":
			var a struct{ Method string }
			_ = json.Unmarshal(args, &a)
			if a.Method == "getBranch" {
				return branch
			}
		case "ui.custom":
			time.Sleep(300 * time.Millisecond) // the overlay's first render arrives while the picker is open
			return map[string]any{"ok": false}
		}
		return nil
	})
	// give the theme update time to apply, then run /reply
	time.Sleep(100 * time.Millisecond)
	s.write(map[string]any{"type": "request", "id": "r1", "request": map[string]any{"method": "command", "tool": "reply", "args": json.RawMessage(`""`)}})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		n := len(s.renders)
		s.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.renders) == 0 {
		t.Fatal("the overlay never rendered")
	}
	lines := strings.Join(s.renders[0], "\n")
	for _, want := range []string{"\x1b[33m╭", "\x1b[34m Reply ", "\x1b[32muser", "\x1b[31massistant", "bbb222", "first question"} {
		if !strings.Contains(lines, want) {
			t.Errorf("rendered picker lacks %q:\n%s", want, lines)
		}
	}
	for i, line := range s.renders[0] {
		if w := powerline.VisibleWidth(line); w != 80 {
			t.Errorf("line %d is %d cells wide, want the terminal width 80", i, w)
		}
	}
}
