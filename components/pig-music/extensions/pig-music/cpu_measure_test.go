//go:build !windows

package pig_music_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// TestMeasurePulseCPU measures what the pulse costs on this machine, with a real mpv playing a generated tone with a beat
// (--ao=null, no network): the CPU used by this process (the model, its drawing and the level reads over mpv's socket) and by mpv
// (with and without the measuring filter), as a share of one core, in four situations. It is a measurement, not a pass/fail test:
//
//	PIG_MUSIC_SMOKE=1 PIG_MUSIC_CPU=1 go test . -run MeasurePulseCPU -v
func TestMeasurePulseCPU(t *testing.T) {
	if os.Getenv("PIG_MUSIC_SMOKE") != "1" || os.Getenv("PIG_MUSIC_CPU") != "1" {
		t.Skip("set PIG_MUSIC_SMOKE=1 PIG_MUSIC_CPU=1 to measure")
	}
	dir, err := os.MkdirTemp("", "cpu")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	tone := filepath.Join(dir, "beat.wav")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "aevalsrc='0.5*sin(2*PI*110*t)*(0.4+0.6*pow(sin(2*PI*2*t),8))':s=44100:d=300", tone).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	paths := music.PathsIn(filepath.Join(dir, "run"))
	p := mpv.New(mpv.Config{Paths: paths, PlayURL: func(tr music.Track) string { return tr.ID }, ExtraArgs: []string{"--ao=null"}})
	ctx := t.Context()
	if err := p.Attach(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(ctx) })
	if err := p.Replace(ctx, []music.Track{{ID: tone, Title: "Beat", Artists: []string{"Test"}, Duration: 300 * time.Second}}, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	pid := mpvPID(t, paths.Socket)

	type scenario struct {
		name    string
		pulse   bool
		calm    bool
		paused  bool
		seconds int
	}
	for _, sc := range []scenario{
		{"calm (nothing animates)", false, true, false, 12},
		{"palette and disc only (pulse off)", false, false, false, 12},
		{"pulse on, playing", true, false, false, 12},
		{"pulse on, paused", true, false, true, 12},
	} {
		_ = p.SetLevels(ctx, false)
		_ = p.SetPaused(ctx, sc.paused)
		time.Sleep(500 * time.Millisecond)
		var next time.Duration
		tick := func(d time.Duration) tea.Cmd { next = d; return func() tea.Msg { return nil } }
		var m tea.Model = ui.New(ui.Deps{Source: fixedSource{songs(3)}, Player: p, ArtMode: art.TrueColor, Vibes: true, Pulse: sc.pulse, Calm: sc.calm, Tick: tick})
		m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		m, cmd := m.Update(ui.ShowPlayerMsg{})
		_ = cmd
		st := p.State()
		m, cmd = m.Update(ui.StateMsg(st))
		run(t, &m, cmd)

		selfBefore, mpvBefore, wall := selfCPU(), procCPU(pid), time.Now()
		frames := 0
		for time.Since(wall) < time.Duration(sc.seconds)*time.Second {
			if next == 0 {
				time.Sleep(200 * time.Millisecond) // nothing animates: only a state update now and then
				m, cmd = m.Update(ui.StateMsg(p.State()))
				run(t, &m, cmd)
				_ = m.View()
				continue
			}
			time.Sleep(next)
			next = 0
			m, cmd = m.Update(ui.TickMsg{})
			run(t, &m, cmd)
			_ = m.View()
			frames++
		}
		el := time.Since(wall).Seconds()
		self, mp := (selfCPU()-selfBefore).Seconds()/el*100, (procCPU(pid)-mpvBefore).Seconds()/el*100
		fmt.Printf("CPU %-36s frames %4d (%.1f/s)  extension process %5.2f%% of a core   mpv %5.2f%% of a core\n", sc.name, frames, float64(frames)/el, self, mp)
	}
}

// run executes a command and what it asks for, feeding the answers back into the model.
func run(t *testing.T, m *tea.Model, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		default:
			var next tea.Cmd
			*m, next = (*m).Update(msg)
			queue = append(queue, next)
		}
	}
}

func selfCPU() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func mpvPID(t *testing.T, sock string) int {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", "--", "--input-ipc-server="+sock).Output()
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		t.Fatal("no mpv process")
	}
	pid, _ := strconv.Atoi(fields[0])
	return pid
}

// procCPU is the CPU time a process has used, from /proc (Linux).
func procCPU(pid int) time.Duration {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(data)
	f := strings.Fields(s[strings.LastIndex(s, ")")+2:])
	ut, _ := strconv.ParseInt(f[11], 10, 64)
	st, _ := strconv.ParseInt(f[12], 10, 64)
	return time.Duration(ut+st) * 10 * time.Millisecond // clock ticks of 10 ms
}
