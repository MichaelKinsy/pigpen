package mpv

import (
	"encoding/json"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// propertyNames are the mpv properties the player observes, in observe_property
// id order (id = index + 1).
var propertyNames = []string{"pause", "time-pos", "duration", "volume", "playlist", "playlist-pos", "media-title", "idle-active", "loop-playlist", "loop-file"}

// playlistEntry is one element of mpv's `playlist` property.
type playlistEntry struct {
	ID       int    `json:"id"`
	Filename string `json:"filename"`
	Title    string `json:"title"`
	Current  bool   `json:"current"`
}

// view is what has been read from mpv, as plain values.
type view struct {
	connected bool
	pause     bool
	timePos   float64
	duration  float64
	volume    float64
	entries   []playlistEntry
	pos       int
	title     string
	loopList  string // loop-playlist
	loopFile  string // loop-file
	shuffled  bool   // set by the Player: mpv has no property for it
}

func newView() view { return view{pos: -1} }

// apply stores one property value. A null or absent value is mpv's "unavailable".
func (v *view) apply(name string, raw json.RawMessage) {
	null := len(raw) == 0 || string(raw) == "null"
	switch name {
	case "pause":
		_ = json.Unmarshal(raw, &v.pause)
	case "time-pos":
		v.timePos = 0
		if !null {
			_ = json.Unmarshal(raw, &v.timePos)
		}
	case "duration":
		v.duration = 0
		if !null {
			_ = json.Unmarshal(raw, &v.duration)
		}
	case "volume":
		if !null {
			_ = json.Unmarshal(raw, &v.volume)
		}
	case "playlist":
		v.entries = nil
		if !null {
			_ = json.Unmarshal(raw, &v.entries)
		}
	case "playlist-pos":
		v.pos = -1
		if !null {
			_ = json.Unmarshal(raw, &v.pos)
		}
	case "loop-playlist":
		v.loopList = ""
		if !null {
			_ = json.Unmarshal(raw, &v.loopList)
		}
	case "loop-file":
		v.loopFile = ""
		if !null {
			_ = json.Unmarshal(raw, &v.loopFile)
		}
	case "media-title":
		v.title = ""
		if !null {
			_ = json.Unmarshal(raw, &v.title)
		}
	}
}

// state turns the view into a music.State, naming queue entries from meta. An
// entry mpv holds that meta does not know is named by its video ID.
func (v *view) state(meta map[string]music.Track) music.State {
	st := music.State{
		Connected: v.connected,
		Paused:    v.pause,
		Position:  seconds(v.timePos),
		Duration:  seconds(v.duration),
		Volume:    int(v.volume + 0.5),
		Index:     v.pos,
		Shuffle:   v.shuffled,
		Repeat:    v.repeat(),
	}
	for _, e := range v.entries {
		st.Queue = append(st.Queue, trackFor(e, meta))
	}
	if v.pos >= 0 && v.pos < len(st.Queue) {
		cur := st.Queue[v.pos]
		if cur.Title == cur.ID && v.title != "" {
			cur.Title = v.title // mpv's own title for a track the metadata file lacks
		}
		st.Track = &cur
	} else {
		st.Index = -1
	}
	return st
}

func trackFor(e playlistEntry, meta map[string]music.Track) music.Track {
	id := videoID(e.Filename)
	if t, ok := meta[id]; ok && id != "" {
		return t
	}
	title := e.Title
	if title == "" {
		title = id
	}
	if title == "" {
		title = e.Filename
	}
	if id == "" {
		id = e.Filename
	}
	return music.Track{ID: id, Title: title}
}

func seconds(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// repeat maps mpv's loop-file and loop-playlist (no, yes, inf, force or a count) to a mode: a track that loops
// wins over a queue that loops.
func (v *view) repeat() music.RepeatMode {
	on := func(s string) bool { return s != "" && s != "no" && s != "0" && s != "false" }
	switch {
	case on(v.loopFile):
		return music.RepeatOne
	case on(v.loopList):
		return music.RepeatAll
	}
	return music.RepeatOff
}
