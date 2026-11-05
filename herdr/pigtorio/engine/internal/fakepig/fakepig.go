// Package fakepig is a mock coding agent for tests and the tutorial. It speaks OSC 7501
// status reports, reads messages from its terminal, and writes PiG-shaped session JSONL.
package fakepig

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Options configure one fake pig.
type Options struct {
	SessionDir string
	Name       string
	Work       time.Duration // how long a message takes
	Quiet      time.Duration // input quiet period that ends a message (300 ms)
	Idle       time.Duration // done turns into idle after this (2 s)
	Cwd        string
	In         io.Reader
	Out        io.Writer
}

// OSC formats an OSC 7501 status report: pairs joined by ":".
func OSC(pairs ...string) string {
	return "\x1b]7501;" + strings.Join(pairs, ":") + "\x1b\\"
}

var pasteMarkers = strings.NewReplacer("\x1b[200~", "", "\x1b[201~", "")

// CleanMessage strips bracketed-paste markers, normalizes line ends and trims.
func CleanMessage(s string) string {
	s = pasteMarkers.Replace(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

// ReadMessages reads r and sends one message per burst of input that is followed by
// `quiet` of silence. It closes out when r ends (flushing what is pending).
func ReadMessages(r io.Reader, quiet time.Duration, out chan<- string) {
	defer close(out)
	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		b := make([]byte, 4096)
		for {
			n, err := r.Read(b)
			if n > 0 {
				chunks <- append([]byte(nil), b[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	var buf []byte
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	var tick <-chan time.Time
	flush := func() {
		msg := CleanMessage(string(buf))
		buf = nil
		if msg != "" {
			out <- msg
		}
	}
	for {
		select {
		case c, ok := <-chunks:
			if !ok {
				flush()
				return
			}
			buf = append(buf, c...)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(quiet)
			tick = timer.C
		case <-tick:
			tick = nil
			flush()
		}
	}
}

type session struct {
	path   string
	parent string
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newSession(dir, cwd string) (*session, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	id := "sess-" + randHex(12)
	s := &session{path: filepath.Join(dir, now.Format("2006-01-02T15-04-05.000000Z")+"_"+id+".jsonl")}
	hdr := map[string]any{"type": "session", "version": 3, "id": id, "timestamp": now.Format(time.RFC3339Nano), "cwd": cwd}
	return s, s.write(hdr)
}

func (s *session) write(v any) error {
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(v)
	_, err = f.Write(append(b, '\n'))
	return err
}

func (s *session) message(role, text string, usage map[string]any) error {
	now := time.Now().UTC()
	id := randHex(8)
	msg := map[string]any{
		"role":      role,
		"content":   []map[string]any{{"type": "text", "text": text}},
		"timestamp": now.UnixMilli(),
	}
	if usage != nil {
		msg["usage"] = usage
	}
	err := s.write(map[string]any{"type": "message", "id": id, "parentId": s.parent, "timestamp": now.Format(time.RFC3339Nano), "message": msg})
	s.parent = id
	return err
}

func tokensOf(s string) int { return len(s)/4 + 1 }

type pig struct {
	o    Options
	mu   sync.Mutex
	sess *session
	n    int
}

func (p *pig) say(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.o.Out, format+"\r\n", args...)
}

func (p *pig) osc(pairs ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	io.WriteString(p.o.Out, OSC(append([]string{pairs[0], "app=pig"}, pairs[1:]...)...))
}

func (p *pig) state(state string, extra ...string) {
	p.osc(append([]string{"state=" + state}, extra...)...)
}

func (p *pig) pause(d time.Duration) { time.Sleep(d) }

func summary(m string) string {
	for _, ln := range strings.Split(m, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			if len(ln) > 70 {
				ln = ln[:70] + "…"
			}
			return ln
		}
	}
	return ""
}

// handle works one message. next supplies the following message (the answer to ASK:).
func (p *pig) handle(m string, next func() (string, bool)) error {
	p.n++
	n := p.n
	file := fmt.Sprintf("pigtorio-fake-%d.txt", n)
	p.state("working")
	p.say("[%s] message %d: %s", p.o.Name, n, summary(m))
	if err := p.sess.message("user", m, nil); err != nil {
		return err
	}
	step := p.o.Work / 6
	if p.o.Work <= 0 {
		step = 0
	}
	fail := strings.Contains(m, "FAIL")
	asked := ""
	if i := strings.Index(m, "ASK:"); i >= 0 {
		asked = strings.TrimSpace(strings.SplitN(m[i+4:], "\n", 2)[0])
	}
	p.say("[%s] reading the crate", p.o.Name)
	p.pause(step)
	p.say("[%s] looking around the repo", p.o.Name)
	p.pause(step)
	answer := ""
	if strings.Contains(m, "ASK:") {
		p.say("[%s] I need an answer: %s", p.o.Name, asked)
		p.state("blocked", "kind=question")
		a, ok := next()
		if !ok {
			return io.EOF
		}
		answer = a
		p.state("working")
		p.say("[%s] got the answer: %s", p.o.Name, summary(a))
	}
	files := "none"
	if fail {
		p.say("[%s] FAIL requested: changing nothing", p.o.Name)
		p.pause(step)
	} else {
		p.say("[%s] writing %s", p.o.Name, file)
		body := fmt.Sprintf("fakepig %s message %d\n%s\n", p.o.Name, n, summary(m))
		if answer != "" {
			body += "answer: " + summary(answer) + "\n"
		}
		if err := os.WriteFile(filepath.Join(p.o.Cwd, file), []byte(body), 0o644); err != nil {
			p.say("[%s] could not write %s: %v", p.o.Name, file, err)
		} else {
			files = file
		}
		p.pause(step)
	}
	p.say("[%s] running checks (not really)", p.o.Name)
	p.pause(step)
	p.say("[%s] writing the hand-off", p.o.Name)
	p.pause(step)
	done := "Pretended to work on the crate."
	if fail {
		done = "Nothing: a FAIL was asked for."
	}
	if answer != "" {
		done += " Used your answer: " + summary(answer) + "."
	}
	reply := fmt.Sprintf("Goal: %s\nDone: %s\nUnverified: everything, I am a fake pig.\nFiles touched: %s", summary(m), done, files)
	in, out := tokensOf(m+answer), tokensOf(reply)
	usage := map[string]any{
		"input": in, "output": out, "cacheRead": 0, "cacheWrite": 0, "totalTokens": in + out,
		"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": float64(in+out) * 1e-6},
	}
	if err := p.sess.message("assistant", reply, usage); err != nil {
		return err
	}
	p.state("done")
	p.say("[%s] done: %s", p.o.Name, files)
	return nil
}

// Run serves messages until the input ends.
func Run(o Options) error {
	if o.Name == "" {
		o.Name = "fakepig"
	}
	if o.Quiet <= 0 {
		o.Quiet = 300 * time.Millisecond
	}
	if o.Idle <= 0 {
		o.Idle = 2 * time.Second
	}
	if o.Cwd == "" {
		o.Cwd, _ = os.Getwd()
	}
	sess, err := newSession(o.SessionDir, o.Cwd)
	if err != nil {
		return err
	}
	p := &pig{o: o, sess: sess}
	msgs := make(chan string)
	go ReadMessages(o.In, o.Quiet, msgs)
	p.state("idle")
	p.say("[%s] fakepig ready in %s (session %s)", o.Name, o.Cwd, filepath.Base(sess.path))
	next := func() (string, bool) { m, ok := <-msgs; return m, ok }
	idle := time.NewTimer(time.Hour)
	idle.Stop()
	var idleC <-chan time.Time
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				return nil
			}
			idleC = nil
			if err := p.handle(m, next); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
			idle.Reset(o.Idle)
			idleC = idle.C
		case <-idleC:
			idleC = nil
			p.state("idle")
		}
	}
}

// DefaultSessionDir is D/sessions/<terminal address> for the terminal the pig runs in.
func DefaultSessionDir(home string) string {
	addr := os.Getenv("HC_TERMINAL_ID")
	if addr == "" {
		addr = fmt.Sprintf("fake-%d", os.Getpid())
	}
	if home == "" {
		home = os.Getenv("HERDR_APP_HOME")
	}
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".config", "herdr-app")
	}
	return filepath.Join(home, "pigtorio", "sessions", addr)
}

// RawTerminal puts a terminal on stdin into character mode (no line editing, no echo,
// so long pastes are not cut at 1024 bytes). The returned func restores it.
func RawTerminal() func() {
	st, err := os.Stdin.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return func() {}
	}
	get := exec.Command("stty", "-g")
	get.Stdin = os.Stdin
	saved, err := get.Output()
	if err != nil {
		return func() {}
	}
	set := exec.Command("stty", "-icanon", "-echo", "min", "1", "time", "0")
	set.Stdin = os.Stdin
	if set.Run() != nil {
		return func() {}
	}
	restore := func() {
		c := exec.Command("stty", strings.TrimSpace(string(saved)))
		c.Stdin = os.Stdin
		_ = c.Run()
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sig
		restore()
		os.Exit(130)
	}()
	return restore
}
