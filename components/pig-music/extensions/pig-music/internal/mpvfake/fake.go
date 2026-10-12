// Package mpvfake is a small in-memory mpv: a unix socket that speaks mpv's JSON
// IPC and models the playlist, pause, volume and seek commands the player uses.
// It exists for tests (the mpv client, the player, the command line) and runs
// as a standalone process when a test re-executes itself as "mpv".
//
// It models mpv as documented, not as measured: where it and a real mpv might
// differ, the real smoke run (`PIG_MUSIC_SMOKE`) is the arbiter.
package mpvfake

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
)

// DefaultDuration is the length, in seconds, of every file unless Options.Duration says otherwise.
const DefaultDuration = 200.0

// Options configures a Server.
type Options struct {
	// Duration returns the length in seconds of the file at url.
	Duration func(url string) float64
	// NoUnshuffle makes playlist-unshuffle an unknown command, as in mpv before 0.37.
	NoUnshuffle bool
	// NoUserData makes the user-data properties unknown, as in mpv before 0.36.
	NoUserData bool
	// NoCacheEnd leaves cache-end out of demuxer-cache-state, as mpv does when it has no timestamp for it.
	NoCacheEnd bool
	// Version is mpv-version ("mpv 0.37.0" when empty); from 0.38 loadfile takes flags insert-at with an index.
	Version string
}

type entry struct {
	id  int
	url string
	// start is the per-file start option loadfile was given (seconds): as in mpv, it stays with the entry, and every play of it
	// starts there.
	start float64
}

// Server is the fake mpv.
type Server struct {
	ln   net.Listener
	opts Options

	mu      sync.Mutex
	list    []entry
	nextID  int
	pos     int // -1 when nothing plays
	timePos float64
	// cutAt is where the playing entry's stream ended early (CutCurrent), 0 while it has not: the demuxer is at its end there.
	cutAt  float64
	pause  bool
	volume float64
	// loop is loop-playlist and loop-file; "no" until set.
	loopPlaylist, loopFile string
	// before is the queue order from before playlist-shuffle, nil when not shuffled.
	before []entry
	// userData is the user-data/ properties set by clients, by name, as their JSON; they live as long as the server.
	userData map[string]json.RawMessage
	conns    map[*conn]bool
	quit     chan struct{}
	closed   bool
	// Commands records every command received, in order, as JSON.
	commands []string
	// filters are the audio filters added with "af add", by label; levelDB is what an astats filter measures (RMS, peak).
	filters []string
	levelDB [2]float64
}

type conn struct {
	c        net.Conn
	writeMu  sync.Mutex
	observed map[int]string // observe id -> property name
}

// Listen starts a fake mpv on the unix socket at path.
func Listen(path string, opts Options) (*Server, error) {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if opts.Duration == nil {
		opts.Duration = func(string) float64 { return DefaultDuration }
	}
	s := &Server{ln: ln, opts: opts, pos: -1, volume: 100, loopPlaylist: "no", loopFile: "no", conns: map[*conn]bool{}, quit: make(chan struct{})}
	go s.accept()
	return s, nil
}

// Wait blocks until a client sends quit or Close is called.
func (s *Server) Wait() { <-s.quit }

// Close stops the server and drops every client.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conns := s.conns
	s.conns = map[*conn]bool{}
	s.mu.Unlock()
	_ = s.ln.Close()
	for c := range conns {
		_ = c.c.Close()
	}
	close(s.quit)
}

func (s *Server) accept() {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		c := &conn{c: nc, observed: map[int]string{}}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = nc.Close()
			return
		}
		s.conns[c] = true
		s.mu.Unlock()
		go s.serve(c)
	}
}

func (s *Server) serve(c *conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		_ = c.c.Close()
	}()
	sc := bufio.NewScanner(c.c)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var req struct {
			Command   json.RawMessage `json:"command"`
			RequestID *int64          `json:"request_id"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		cmd := commandArgs(req.Command)
		if len(cmd) == 0 {
			continue
		}
		data, errText := s.run(c, cmd)
		reply := map[string]any{"error": "success"}
		if errText != "" {
			reply["error"] = errText
		} else if data != nil {
			reply["data"] = data
		}
		if req.RequestID != nil {
			reply["request_id"] = *req.RequestID
		}
		c.send(reply)
	}
}

func (c *conn) send(v any) {
	data, _ := json.Marshal(v)
	c.writeMu.Lock()
	_, _ = c.c.Write(append(data, '\n'))
	c.writeMu.Unlock()
}

// run executes one command. It returns the reply data or an mpv error string.
func (s *Server) run(c *conn, cmd []json.RawMessage) (any, string) {
	str := func(i int) string {
		if i >= len(cmd) {
			return ""
		}
		var v string
		if json.Unmarshal(cmd[i], &v) != nil {
			return string(cmd[i])
		}
		return v
	}
	num := func(i int) (float64, bool) {
		if i >= len(cmd) {
			return 0, false
		}
		var v float64
		if json.Unmarshal(cmd[i], &v) == nil {
			return v, true
		}
		if f, err := parseFloat(str(i)); err == nil {
			return f, true
		}
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, commandText(cmd))
	switch name := str(0); name {
	case "observe_property":
		id, ok := num(1)
		if !ok {
			return nil, "invalid parameter"
		}
		prop := str(2)
		// mpv observes a user-data property that is not set yet, and reports it once it is (measured, mpv 0.41).
		userData := strings.HasPrefix(prop, "user-data/") && !s.opts.NoUserData
		v, known := s.property(prop)
		if !known && !userData {
			return nil, "property not found"
		}
		c.observed[int(id)] = prop
		if known { // mpv reports the current value right after observe_property
			c.send(map[string]any{"event": "property-change", "id": int(id), "name": prop, "data": v})
		}
		return nil, ""
	case "unobserve_property":
		id, _ := num(1)
		delete(c.observed, int(id))
		return nil, ""
	case "get_property":
		v, known := s.property(str(1))
		if !known {
			return nil, "property not found"
		}
		if v == nil {
			return nil, "property unavailable"
		}
		return v, ""
	case "set_property":
		switch prop := str(1); prop {
		case "pause":
			s.setPause(str(2) == "yes" || str(2) == "true")
		case "volume":
			f, ok := num(2)
			if !ok {
				return nil, "invalid parameter"
			}
			s.volume = min(max(f, 0), 130)
			s.changed("volume")
		case "loop-playlist", "loop-file":
			v := str(2)
			if v != "no" && v != "inf" && v != "yes" && v != "force" {
				return nil, "invalid parameter"
			}
			if prop == "loop-playlist" {
				s.loopPlaylist = v
			} else {
				s.loopFile = v
			}
			s.changed(prop)
		default:
			if strings.HasPrefix(prop, "user-data/") && !s.opts.NoUserData && len(cmd) > 2 {
				if s.userData == nil {
					s.userData = map[string]json.RawMessage{}
				}
				s.userData[prop] = cmd[2]
				s.changed(prop)
				return nil, ""
			}
			return nil, "property not found"
		}
		return nil, ""
	case "af":
		return s.af(str(1), str(2))
	case "playlist-shuffle":
		s.shuffle()
		return nil, ""
	case "playlist-unshuffle":
		if s.opts.NoUnshuffle {
			return nil, "unknown command"
		}
		s.unshuffle()
		return nil, ""
	case "cycle":
		if str(1) != "pause" {
			return nil, "property not found"
		}
		s.setPause(!s.pause)
		return nil, ""
	case "seek":
		amount, ok := num(1)
		if !ok {
			return nil, "invalid parameter"
		}
		if s.pos < 0 {
			return nil, "error running command"
		}
		switch str(2) {
		case "absolute":
			s.timePos = amount
		default:
			s.timePos += amount
		}
		s.timePos = min(max(s.timePos, 0), s.currentDuration())
		for c := range s.conns {
			c.send(map[string]any{"event": "seek"})
		}
		s.changed("time-pos")
		return nil, ""
	case "loadfile":
		return s.loadfile(str(1), str(2), str(3), str(4))
	case "playlist-next":
		if s.pos < 0 {
			return nil, "error running command"
		}
		if s.pos == len(s.list)-1 {
			if s.loopPlaylist != "no" { // loop-playlist wraps to the first entry
				s.play(0)
				return nil, ""
			}
			s.stop()
		} else {
			s.play(s.pos + 1)
		}
		return nil, ""
	case "playlist-prev":
		if s.pos <= 0 {
			return nil, "error running command"
		}
		s.play(s.pos - 1)
		return nil, ""
	case "playlist-play-index":
		if str(1) == "none" { // stops playing and keeps the queue
			s.stop()
			return nil, ""
		}
		i, ok := num(1)
		if !ok || int(i) < 0 || int(i) >= len(s.list) {
			return nil, "invalid parameter"
		}
		s.play(int(i))
		return nil, ""
	case "playlist-remove":
		i, ok := num(1)
		if !ok || int(i) < 0 || int(i) >= len(s.list) {
			return nil, "invalid parameter"
		}
		s.remove(int(i))
		return nil, ""
	case "playlist-move":
		a, ok1 := num(1)
		b, ok2 := num(2)
		if !ok1 || !ok2 || int(a) < 0 || int(a) >= len(s.list) || int(b) < 0 || int(b) > len(s.list) {
			return nil, "invalid parameter"
		}
		s.move(int(a), int(b))
		return nil, ""
	case "playlist-clear":
		s.clear()
		return nil, ""
	case "quit":
		go s.Close()
		return nil, ""
	default:
		return nil, "unknown command"
	}
}

// af models "af add <filter>" and "af remove <label>": a labelled filter ("@pmlv:lavfi=...") can be added once.
func (s *Server) af(op, arg string) (any, string) {
	label, _, _ := strings.Cut(strings.TrimPrefix(arg, "@"), ":")
	switch op {
	case "add":
		for _, f := range s.filters {
			if strings.HasPrefix(f, "@"+label+":") {
				return nil, "error running command" // stricter than mpv 0.37, which replaces a filter with the same label (measured); SetLevels handles both
			}
		}
		s.filters = append(s.filters, arg)
	case "remove":
		for i, f := range s.filters {
			if strings.HasPrefix(f, "@"+label+":") || f == arg {
				s.filters = append(s.filters[:i], s.filters[i+1:]...)
				return nil, ""
			}
		}
		return nil, "error running command"
	default:
		return nil, "invalid parameter"
	}
	return nil, ""
}

// SetLevelDB sets what the astats filter measures, in dBFS (-Inf for silence).
func (s *Server) SetLevelDB(rms, peak float64) {
	s.mu.Lock()
	s.levelDB = [2]float64{rms, peak}
	s.mu.Unlock()
}

// Filters lists the audio filters currently added.
func (s *Server) Filters() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.filters...)
}

func (s *Server) loadfile(url, flags, index, options string) (any, string) {
	if url == "" {
		return nil, "invalid parameter"
	}
	s.nextID++
	e := entry{id: s.nextID, url: url}
	for _, kv := range strings.Split(options, ",") {
		if v, ok := strings.CutPrefix(kv, "start="); ok {
			e.start, _ = parseFloat(v)
		}
	}
	switch flags {
	case "insert-at": // mpv 0.38 and later: at index, or at the end when it is past it (or -1)
		at, err := strconv.Atoi(index)
		if !s.atLeast038() || err != nil {
			return nil, "invalid parameter"
		}
		if at < 0 || at > len(s.list) {
			at = len(s.list)
		}
		s.list = append(s.list[:at], append([]entry{e}, s.list[at:]...)...)
		if s.pos >= at {
			s.pos++
			s.changed("playlist-pos")
		}
		s.changed("playlist")
	case "", "replace":
		s.list = []entry{e}
		s.play(0)
		s.changed("playlist")
	case "append":
		s.list = append(s.list, e)
		s.changed("playlist")
	case "append-play":
		s.list = append(s.list, e)
		s.changed("playlist")
		if s.pos < 0 {
			s.play(len(s.list) - 1)
		}
	default:
		return nil, "invalid parameter"
	}
	return map[string]any{"playlist_entry_id": e.id}, ""
}

func (s *Server) remove(i int) {
	wasCurrent := i == s.pos
	s.list = append(s.list[:i], s.list[i+1:]...)
	switch {
	case wasCurrent && len(s.list) == 0:
		s.pos = -1
		s.timePos = 0
		s.changed("playlist-pos", "time-pos", "duration", "media-title", "path", "idle-active")
	case wasCurrent:
		s.pos = min(i, len(s.list)-1)
		s.timePos = 0
		s.changed("playlist-pos", "time-pos", "duration", "media-title", "path")
	case i < s.pos:
		s.pos--
		s.changed("playlist-pos")
	}
	s.changed("playlist")
}

// move puts the entry at a before the entry at b (b == len moves it to the end).
func (s *Server) move(a, b int) {
	if b == a || b == a+1 {
		return
	}
	cur := entry{}
	if s.pos >= 0 {
		cur = s.list[s.pos]
	}
	e := s.list[a]
	rest := append(append([]entry{}, s.list[:a]...), s.list[a+1:]...)
	at := b
	if b > a {
		at = b - 1
	}
	s.list = append(rest[:at], append([]entry{e}, rest[at:]...)...)
	if s.pos >= 0 {
		for i, x := range s.list {
			if x.id == cur.id {
				s.pos = i
			}
		}
	}
	s.changed("playlist", "playlist-pos")
}

func (s *Server) clear() {
	s.before = nil
	if s.pos < 0 {
		s.list = nil
	} else {
		s.list = []entry{s.list[s.pos]}
		s.pos = 0
	}
	s.changed("playlist", "playlist-pos")
}

func (s *Server) play(i int) {
	s.pos = i
	s.timePos = s.list[i].start
	s.cutAt = 0
	s.pause = false
	for c := range s.conns {
		c.send(map[string]any{"event": "start-file", "playlist_entry_id": s.list[i].id})
	}
	s.changed("playlist-pos", "playlist", "time-pos", "duration", "media-title", "path", "pause", "idle-active")
	for c := range s.conns {
		c.send(map[string]any{"event": "file-loaded"})
	}
}

func (s *Server) stop() {
	s.pos = -1
	s.timePos = 0
	s.cutAt = 0
	s.changed("playlist-pos", "playlist", "time-pos", "duration", "media-title", "path", "idle-active")
}

// advance is what mpv does when the playing entry has ended: the next entry plays, the first after the last with
// loop-playlist (the same one again when it is alone), or nothing.
func (s *Server) advance() {
	switch {
	case s.pos < len(s.list)-1:
		s.play(s.pos + 1)
	case s.loopPlaylist != "no":
		s.play(0)
	default:
		s.stop()
	}
}

func (s *Server) setPause(p bool) {
	s.pause = p
	s.changed("pause")
}

func (s *Server) currentDuration() float64 {
	if s.pos < 0 {
		return 0
	}
	return s.opts.Duration(s.list[s.pos].url)
}

// property returns a property's JSON value (nil for unavailable) and whether mpv knows it.
func (s *Server) property(name string) (any, bool) {
	switch name {
	case "pause":
		return s.pause, true
	case "volume":
		return s.volume, true
	case "loop-playlist":
		return s.loopPlaylist, true
	case "loop-file":
		return s.loopFile, true
	case "idle-active":
		return s.pos < 0, true
	case "af-metadata/pmlv":
		for _, f := range s.filters {
			if strings.HasPrefix(f, "@pmlv:") && s.pos >= 0 {
				db := func(v float64) string {
					if math.IsInf(v, -1) {
						return "-inf"
					}
					return strconv.FormatFloat(v, 'f', 6, 64)
				}
				return map[string]string{"lavfi.astats.Overall.RMS_level": db(s.levelDB[0]), "lavfi.astats.Overall.Peak_level": db(s.levelDB[1])}, true
			}
		}
		return nil, true // no such filter metadata (mpv: property unavailable)
	case "playlist-pos":
		return s.pos, true
	case "time-pos":
		if s.pos < 0 {
			return nil, true
		}
		return s.timePos, true
	case "duration":
		if s.pos < 0 {
			return nil, true
		}
		return s.currentDuration(), true
	case "media-title", "path":
		if s.pos < 0 {
			return nil, true
		}
		return s.list[s.pos].url, true
	case "demuxer-cache-state":
		if s.pos < 0 {
			return nil, true
		}
		if s.cutAt > 0 { // the demuxer has read all there is: up to where the stream was cut
			if s.opts.NoCacheEnd {
				return map[string]any{"eof": true}, true
			}
			return map[string]any{"eof": true, "cache-end": s.cutAt}, true
		}
		end := min(s.timePos+30, s.currentDuration())
		return map[string]any{"eof": end >= s.currentDuration(), "cache-end": end}, true
	case "playlist":
		out := make([]map[string]any, 0, len(s.list))
		for i, e := range s.list {
			m := map[string]any{"filename": e.url, "id": e.id}
			if i == s.pos {
				m["current"], m["playing"] = true, true
			}
			out = append(out, m)
		}
		return out, true
	}
	if name == "mpv-version" {
		return s.version(), true
	}
	if v, ok := s.userData[name]; ok && !s.opts.NoUserData {
		return v, true
	}
	return nil, false
}

func (s *Server) version() string {
	if s.opts.Version != "" {
		return s.opts.Version
	}
	return "mpv 0.37.0"
}

// atLeast038 is whether the fake is an mpv 0.38 or later (its loadfile takes an index: flags insert-at).
func (s *Server) atLeast038() bool {
	var major, minor int
	_, _ = fmt.Sscanf(strings.TrimPrefix(strings.TrimPrefix(s.version(), "mpv "), "v"), "%d.%d", &major, &minor)
	return major > 0 || minor >= 38
}

// FailCurrent does what mpv does when the current entry could not be opened (a stream URL that was refused): it sends end-file
// with reason "error" and the entry's ID, as a real mpv 0.37 does, and goes on to the next entry. With loop-playlist and one
// entry it stops, as mpv does when every entry of a looping queue has failed.
func (s *Server) FailCurrent() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos < 0 {
		return
	}
	for c := range s.conns {
		c.send(map[string]any{"event": "end-file", "reason": "error", "playlist_entry_id": s.list[s.pos].id, "file_error": "loading failed"})
	}
	if len(s.list) == 1 {
		s.stop()
		return
	}
	s.advance()
}

// CutCurrent does what mpv does when the stream of the playing entry ends at seconds, before the end of the track (YouTube cut
// the connection and refuses the rest, measured with a real mpv 0.41): the demuxer is at its end there, and mpv takes it for
// the end of the file. With loop-file it seeks back to the start of the same entry (a seek event); otherwise it sends end-file
// with reason "eof" and goes on as at the end of any track.
func (s *Server) CutCurrent(seconds float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos < 0 {
		return
	}
	s.timePos, s.cutAt = seconds, seconds
	s.changed("time-pos")
	if s.loopFile != "no" {
		for c := range s.conns {
			c.send(map[string]any{"event": "seek"})
		}
		s.timePos = 0
		s.changed("time-pos")
		return
	}
	for c := range s.conns {
		c.send(map[string]any{"event": "end-file", "reason": "eof", "playlist_entry_id": s.list[s.pos].id})
	}
	s.advance()
}

// CutAhead makes the stream of the playing entry end at seconds, ahead of where it plays: the demuxer has read up to there and
// is at its end, and the sound has not reached it yet.
func (s *Server) CutAhead(seconds float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cutAt = seconds
}

// IDs returns the entry IDs of the playlist, in order: an entry loaded again has a new one.
func (s *Server) IDs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int, len(s.list))
	for i, e := range s.list {
		ids[i] = e.id
	}
	return ids
}

// TimePos is the playback position in seconds.
func (s *Server) TimePos() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timePos
}

// changed sends a property-change event for each named property to the clients observing it.
func (s *Server) changed(names ...string) {
	for _, name := range names {
		v, _ := s.property(name)
		for c := range s.conns {
			for id, prop := range c.observed {
				if prop == name {
					msg := map[string]any{"event": "property-change", "id": id, "name": name}
					if v != nil {
						msg["data"] = v
					}
					c.send(msg)
				}
			}
		}
	}
}

// Advance moves the playback position forward by seconds, as time passing would,
// while something plays and is not paused.
func (s *Server) Advance(seconds float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos < 0 || s.pause {
		return
	}
	s.timePos = min(s.timePos+seconds, s.currentDuration())
	s.changed("time-pos")
}

// Commands returns every command received so far as compact JSON.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

// Playlist returns the URLs in the playlist and the current index.
func (s *Server) Playlist() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	urls := make([]string, len(s.list))
	for i, e := range s.list {
		urls[i] = e.url
	}
	return urls, s.pos
}

// Paused reports the pause property.
func (s *Server) Paused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pause
}

// Run serves a fake mpv on the socket named by an `--input-ipc-server=` argument
// until it is told to quit. It is what a test binary does when it is started as "mpv".
func Run(args []string) error {
	var path string
	for _, a := range args {
		if len(a) > 19 && a[:19] == "--input-ipc-server=" {
			path = a[19:]
		}
	}
	if path == "" {
		return fmt.Errorf("mpvfake: no --input-ipc-server argument in %q", args)
	}
	s, err := Listen(path, Options{})
	if err != nil {
		return err
	}
	if rec := os.Getenv("MPVFAKE_RECORD"); rec != "" {
		// One file per process, named by pid, holding the command line.
		data, _ := json.Marshal(args)
		_ = os.WriteFile(fmt.Sprintf("%s.%d", rec, os.Getpid()), data, 0o600)
	}
	s.Wait()
	_ = os.Remove(path)
	return nil
}

// shuffle reorders every entry, the current one included, as mpv's playlist-shuffle does (mpv 0.37 moves the playing
// entry to a random place; the fake reverses the list so tests are repeatable). The current entry keeps playing and
// playlist-pos follows it. The order it had is remembered for playlist-unshuffle.
func (s *Server) shuffle() {
	if len(s.list) < 2 {
		return
	}
	if s.before == nil {
		s.before = append([]entry(nil), s.list...)
	}
	out := make([]entry, len(s.list))
	for i, e := range s.list {
		out[len(s.list)-1-i] = e
	}
	s.list = out
	if s.pos >= 0 {
		s.pos = len(s.list) - 1 - s.pos
	}
	s.changed("playlist", "playlist-pos")
}

// unshuffle puts the remembered order back, for the entries still in the queue.
func (s *Server) unshuffle() {
	if s.before == nil {
		return
	}
	cur := -1
	if s.pos >= 0 {
		cur = s.list[s.pos].id
	}
	have := map[int]entry{}
	for _, e := range s.list {
		have[e.id] = e
	}
	var out []entry
	for _, e := range s.before {
		if h, ok := have[e.id]; ok {
			out = append(out, h)
			delete(have, e.id)
		}
	}
	for _, e := range s.list { // anything added since goes at the end
		if _, ok := have[e.id]; ok {
			out = append(out, e)
		}
	}
	s.list, s.before = out, nil
	for i, e := range s.list {
		if e.id == cur {
			s.pos = i
		}
	}
	s.changed("playlist", "playlist-pos")
}

// Loop is loop-playlist and loop-file.
func (s *Server) Loop() [2]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return [2]string{s.loopPlaylist, s.loopFile}
}

// Shuffled reports whether playlist-shuffle was applied and not undone.
func (s *Server) Shuffled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.before != nil
}
