//go:build !windows

package pig_music_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/teahost"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// countingModel counts the views drawn.
type countingModel struct {
	tea.Model
	views *atomic.Int64
}

func (c countingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := c.Model.Update(msg)
	return countingModel{m, c.views}, cmd
}

func (c countingModel) View() tea.View { c.views.Add(1); return c.Model.View() }

type sheetLyrics struct{}

func (sheetLyrics) Lyrics(context.Context, music.Track) (music.Lyrics, error) {
	l := music.Lyrics{Synced: true, Source: "lrclib"}
	for i := 0; i < 80; i++ {
		l.Lines = append(l.Lines, music.LyricLine{At: time.Duration(i) * 3 * time.Second, End: time.Duration(i+1) * 3 * time.Second, Text: fmt.Sprintf("line %d of a sheet that is long enough to wrap now and then on a narrow column", i)})
	}
	return l, nil
}
func (sheetLyrics) Offset(string) time.Duration     { return 0 }
func (sheetLyrics) SetOffset(string, time.Duration) {}

// TestMeasureHiddenCPU measures what the extension costs while the player is hidden and a track plays (real mpv, --ao=null, a
// generated tone, no network): the CPU of this process over 30 s, and how many views were drawn nobody could see.
//
//	PIG_MUSIC_SMOKE=1 PIG_MUSIC_CPU=1 go test . -run MeasureHiddenCPU -v
func TestMeasureHiddenCPU(t *testing.T) {
	if os.Getenv("PIG_MUSIC_SMOKE") != "1" || os.Getenv("PIG_MUSIC_CPU") != "1" {
		t.Skip("set PIG_MUSIC_SMOKE=1 PIG_MUSIC_CPU=1 to measure")
	}
	dir := t.TempDir()
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
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	if err := p.Replace(ctx, []music.Track{{ID: tone, Title: "Beat", Artists: []string{"Test"}, Duration: 300 * time.Second}}, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	views := &atomic.Int64{}
	model := countingModel{ui.New(ui.Deps{Source: fixedSource{songs(3)}, Player: p, Lyrics: sheetLyrics{}, ArtMode: art.TrueColor, Vibes: true, Pulse: true}), views}
	redraws := &atomic.Int64{}
	host := teahost.New(model, teahost.Options{Redraw: func() { redraws.Add(1) }})
	host.Start(120, 40)
	defer host.Stop()
	host.Send(ui.ShowPlayerMsg{})
	go func() {
		for st := range p.Subscribe() {
			host.Send(ui.StateMsg(st))
		}
	}()
	host.Send(ui.StateMsg(p.State()))
	time.Sleep(2 * time.Second) // shown: the lyrics arrive, the pulse runs
	host.Send(ui.ClosedMsg{})
	host.Hide()
	time.Sleep(time.Second)
	views.Store(0)
	redraws.Store(0)
	before, wall := selfCPU(), time.Now()
	time.Sleep(30 * time.Second)
	el := time.Since(wall).Seconds()
	fmt.Printf("CPU hidden, playing, lyrics on: extension process %.2f%% of a core over %.0f s; views drawn %d, redraws asked %d\n",
		(selfCPU()-before).Seconds()/el*100, el, views.Load(), redraws.Load())
}
