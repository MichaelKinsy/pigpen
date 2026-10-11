// Package screens builds the player's screens in fixed states, for the tests that compare drawings: the same screen drawn
// before and after a change, and the extension's kit view of it against the rows. It is used by tests only.
package screens

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// Screen is one screen in a fixed state at one size.
type Screen struct {
	Name  string
	W, H  int
	Model ui.Model
}

// Widths are the terminal widths every screen is drawn at, one or two in each band of the Player screen's layouts: under 50 cells
// the track's text over the panel, under 80 its cover beside it, under 132 two columns, from 132 three.
var Widths = []int{40, 50, 80, 107, 120, 140, 200}

// All is every screen at every width, 30 rows high (20 under 60 cells), and a strip of 4 rows.
func All() []Screen {
	var out []Screen
	for _, w := range Widths {
		h := 30
		if w < 60 {
			h = 20
		}
		for _, s := range scenes {
			out = append(out, Screen{Name: fmt.Sprintf("%s-%d", s.name, w), W: w, H: h, Model: s.build(w, h)})
		}
	}
	return append(out, Screen{Name: "strip-100", W: 100, H: 4, Model: playerScene(true, true, 0)(100, 4)})
}

// At is the scene called name at w x h; false when there is no such scene.
func At(name string, w, h int) (Screen, bool) {
	for _, s := range scenes {
		if s.name == name {
			return Screen{Name: fmt.Sprintf("%s-%d", name, w), W: w, H: h, Model: s.build(w, h)}, true
		}
	}
	return Screen{}, false
}

type scene struct {
	name  string
	build func(w, h int) ui.Model
}

const (
	lyricsOn   = 1 << iota // a lyrics provider: the lyrics take the queue's place
	noColour               // NO_COLOR
	endOfSong              // the last seconds of the track: the "up next" card
	emojiQueue             // a title in the queue whose width PiG and this extension might measure apart (an emoji)
	entities               // a search finds albums and artists too: the Songs, Albums, Artists line over the results
	home                   // a history and the home's feeds: the Search screen's home before anything is searched
)

var scenes = []scene{
	{"search-idle", func(w, h int) ui.Model { return drive(newModel(false, false, 0), w, h) }},
	{"search-home", func(w, h int) ui.Model {
		return drive(newModel(true, true, home), w, h, playing(), key("down"), key("down"))
	}},
	{"search-results", func(w, h int) ui.Model {
		return drive(newModel(true, true, 0), w, h, playing(), key("/"), typed("night drive"), key("enter"), key("down"))
	}},
	{"search-kinds", func(w, h int) ui.Model {
		return drive(newModel(true, true, entities), w, h, playing(), key("/"), typed("night drive"), key("enter"), key("2"))
	}},
	{"help", func(w, h int) ui.Model { return drive(newModel(true, true, 0), w, h, playing(), key("?")) }},
	{"player-cover", playerScene(true, true, 0)},
	{"player-cover-lyrics", playerScene(true, true, lyricsOn)},
	{"player-cover-end", playerScene(true, true, endOfSong)},
	{"player-disc-plain", playerScene(false, false, 0)},
	{"player-lyrics-plain", playerScene(false, false, lyricsOn)},
	{"player-nocolour", playerScene(true, true, noColour)},
	{"player-emoji", playerScene(true, true, emojiQueue)},
}

func playerScene(cover, vibes bool, flags int) func(w, h int) ui.Model {
	return func(w, h int) ui.Model {
		m := newModel(cover, vibes, flags)
		st := playing()
		if flags&endOfSong != 0 {
			st = endState()
		}
		if flags&emojiQueue != 0 {
			st.Queue[2].Title = "Dance 🎵 Night"
		}
		return drive(m, w, h, st, ui.ShowPlayerMsg{})
	}
}

// now is the screens' clock: the lyrics' sweep and the toasts read it.
var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func newModel(cover, vibes bool, flags int) ui.Model {
	d := ui.Deps{
		Source:  source{},
		Player:  &player{},
		ArtMode: art.TrueColor,
		Vibes:   vibes,
		Calm:    true, // nothing moves: the same state draws the same rows
		Now:     func() time.Time { return now },
		Tick:    func(time.Duration) tea.Cmd { return nil },
	}
	if cover {
		d.Art = artwork{}
	}
	if flags&lyricsOn != 0 {
		d.Lyrics = lyricsSource{}
	}
	if flags&noColour != 0 {
		d.NoColor, d.ArtMode = true, art.Mono
	}
	if flags&entities != 0 {
		d.Source = entitySource{}
	}
	if flags&home != 0 {
		d.Source, d.Recents = homeSource{}, &recents{list: []music.Recent{
			{Query: "night drive", At: 1},
			{Track: &Tracks()[1], At: 2},
			{Query: "yoasobi", At: 3},
		}}
	}
	return ui.New(d)
}

// Tracks is the queue of the screens: plain titles, an accented one, a wide (CJK) one, an explicit one with an album.
func Tracks() []music.Track {
	ts := []music.Track{
		{ID: "trk00", Title: "Night Drive", Artists: []string{"The Midnight"}, Album: "Endless Summer", Duration: 214 * time.Second},
		{ID: "trk01", Title: "Café del Mar", Artists: []string{"Energy 52"}, Duration: 400 * time.Second},
		{ID: "trk02", Title: "夜に駆ける", Artists: []string{"YOASOBI"}, Duration: 261 * time.Second},
		{ID: "trk03", Title: "Explicit Content", Artists: []string{"Somebody", "Else"}, Album: "Loud", Explicit: true, Duration: 187 * time.Second},
	}
	for i := 4; i < 24; i++ {
		ts = append(ts, music.Track{ID: fmt.Sprintf("trk%02d", i), Title: fmt.Sprintf("Song %d", i+1), Artists: []string{"Artist"}, Duration: time.Duration(150+i) * time.Second})
	}
	return ts
}

func playing() ui.StateMsg {
	ts := Tracks()
	cur := ts[0]
	return ui.StateMsg{Connected: true, Track: &cur, Position: 50 * time.Second, Duration: cur.Duration, Volume: 40, Queue: ts, Index: 0}
}

func endState() ui.StateMsg {
	st := playing()
	st.Position = st.Duration - 6*time.Second
	return st
}

func key(name string) tea.Msg {
	switch name {
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "down":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	}
	r := []rune(name)
	return tea.KeyPressMsg(tea.Key{Code: r[0], Text: name})
}

type typed string

// drive sizes m and delivers msgs, each followed by everything its commands produce, as the extension's host does. A
// command that does not answer at once (a timer) is dropped.
func drive(m ui.Model, w, h int, msgs ...tea.Msg) ui.Model {
	var model tea.Model = m
	queue := []tea.Msg{tea.WindowSizeMsg{Width: w, Height: h}}
	for _, msg := range msgs {
		if s, ok := msg.(typed); ok {
			for _, c := range s {
				queue = append(queue, tea.KeyPressMsg(tea.Key{Code: c, Text: string(c)}))
			}
			continue
		}
		queue = append(queue, msg)
	}
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		if _, ok := msg.(tea.QuitMsg); ok {
			continue
		}
		var cmd tea.Cmd
		model, cmd = model.Update(msg)
		queue = append(queue, run(cmd)...)
	}
	return model.(ui.Model)
}

func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(200 * time.Millisecond):
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, run(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

type source struct{}

func (source) Search(context.Context, string, int) ([]music.Track, error) { return Tracks(), nil }
func (source) Library(context.Context) ([]music.Collection, error)        { return nil, nil }
func (source) Tracks(context.Context, string) ([]music.Track, error)      { return nil, nil }
func (source) PlayURL(t music.Track) string                               { return "u/" + t.ID }

// entitySource is source finding an album and an artist as well.
type entitySource struct{ source }

func (entitySource) SearchEntities(context.Context, string, int) ([]music.Entity, error) {
	return []music.Entity{
		{Kind: "album", ID: "MPREb_1", Title: "Night Drive", Subtitle: "Neon Coast • 2024"},
		{Kind: "artist", ID: "UC1", Title: "Neon Coast", Subtitle: "1.2M monthly audience"},
	}, nil
}

func (entitySource) OpenEntity(context.Context, music.Entity) (string, []music.Track, error) {
	return "", nil, nil
}

// homeSource is source with the home's feeds: a chart and two sections of suggestions, a shelf of songs and one of playlists.
type homeSource struct{ source }

func (homeSource) Popular(context.Context) (music.Section, error) {
	return music.Section{Title: "Top 100 Music Videos United States", Tracks: Tracks()[2:16]}, nil
}

func (homeSource) Suggested(context.Context) ([]music.Section, error) {
	return []music.Section{
		{Title: "Quick picks", Tracks: Tracks()[3:6]},
		{Title: "Mixed for you", Entities: []music.Entity{
			{Kind: "playlist", ID: "VLRDTMAK5uy_mix1", Title: "My Supermix", Subtitle: "Mix"},
			{Kind: "album", ID: "MPREb_album0001", Title: "Endless Summer", Subtitle: "The Midnight • 2016"},
		}},
	}, nil
}

func (homeSource) CachedHome(context.Context) (music.Section, []music.Section, bool) {
	return music.Section{}, nil, false
}

// recents is a history in memory.
type recents struct{ list []music.Recent }

func (r *recents) Load() ([]music.Recent, error) { return r.list, nil }
func (r *recents) Add(x music.Recent) ([]music.Recent, error) {
	r.list = append([]music.Recent{x}, r.list...)
	return r.list, nil
}
func (r *recents) Clear() error { r.list = nil; return nil }

// player is a music.Player that does nothing: the screens get their state as messages.
type player struct{}

func (*player) Attach(context.Context) error                      { return nil }
func (*player) Replace(context.Context, []music.Track, int) error { return nil }
func (*player) Enqueue(context.Context, ...music.Track) error     { return nil }
func (*player) Remove(context.Context, int) error                 { return nil }
func (*player) Move(context.Context, int, int) error              { return nil }
func (*player) Jump(context.Context, int) error                   { return nil }
func (*player) Next(context.Context) error                        { return nil }
func (*player) Prev(context.Context) error                        { return nil }
func (*player) TogglePause(context.Context) error                 { return nil }
func (*player) SetPaused(context.Context, bool) error             { return nil }
func (*player) SeekRelative(context.Context, time.Duration) error { return nil }
func (*player) SetVolume(context.Context, int) error              { return nil }
func (*player) SetShuffle(context.Context, bool) error            { return nil }
func (*player) SetRepeat(context.Context, music.RepeatMode) error { return nil }
func (*player) State() music.State                                { return music.State{Index: -1} }
func (*player) Sync(context.Context) (music.State, error)         { return music.State{Index: -1}, nil }
func (*player) Subscribe() <-chan music.State                     { return nil }
func (*player) Close() error                                      { return nil }
func (*player) Shutdown(context.Context) error                    { return nil }

// CoverPNG is the cover every track of the screens has: a 64x64 PNG of two colours.
var CoverPNG = func() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: uint8(40 + 3*x), G: uint8(20 + 2*y), B: 160, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}()

type artwork struct{}

func (artwork) Image(context.Context, music.Track) (image.Image, error) {
	img, err := png.Decode(bytes.NewReader(CoverPNG))
	if err != nil {
		return nil, err
	}
	return picture(img, CoverPNG), nil
}

type lyricsSource struct{}

func (lyricsSource) Lyrics(context.Context, music.Track) (music.Lyrics, error) {
	l := music.Lyrics{Source: "lrclib", Synced: true}
	words := []string{"Cruising down the empty road", "Neon lights are all I know", "", "Hold on, the night is young", "And the city sings along with a line long enough to wrap in a narrow column of the stage", "Night drive"}
	for i, w := range words {
		l.Lines = append(l.Lines, music.LyricLine{At: time.Duration(34+4*i) * time.Second, End: time.Duration(38+4*i) * time.Second, Text: w})
	}
	return l, nil
}
func (lyricsSource) Offset(string) time.Duration     { return 0 }
func (lyricsSource) SetOffset(string, time.Duration) {}
