package mpv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// A stream YouTube refuses reaches mpv in one of two ways (measured with mpv 0.41, refused_smoke_test.go). An entry whose
// stream is refused from its first byte cannot be opened: end-file with reason "error". One refused part way through simply
// ends where the refusal began (mpv does not ask again for the rest), and mpv takes that for the end of the track: end-file
// with reason "eof" long before the track's length, or, with loop-file (repeat one), a seek back to the start of the same
// entry, which plays the opening that is still in its cache again and again. With Config.Recover the player loads such an
// entry again, resolved afresh, where it was, once; a second refusal soon after makes it move on.

// resumeAt is a position to seek to once the entry with the ID has loaded: Recover's reload. It is not given to loadfile as a
// start option, which mpv keeps with the entry for every later play of it.
type resumeAt struct {
	id int
	at float64
}

// started notes a new entry: what is heard from now on is its. An entry skipped before may be refused, and skipped, again;
// a resume waits for its own entry only.
func (p *Player) started(id int) {
	p.mu.Lock()
	if id != p.heard.id {
		p.prevHeard = p.heard
	}
	p.heard = heardEntry{id: id}
	if id == p.skipped {
		p.skipped = 0
	}
	if id != p.resume.id {
		p.resume = resumeAt{}
	}
	p.mu.Unlock()
}

// loaded seeks a reloaded entry to where its stream was refused, as soon as it has loaded, before its sound starts.
func (p *Player) loaded() {
	p.mu.Lock()
	r := p.resume
	if r.id == 0 || r.id != p.heard.id {
		p.mu.Unlock()
		return
	}
	// Where it is sent counts as heard: a stream cut there ends at once, before mpv reports a position (and an eof right
	// after the resume is then measured from there).
	p.resume, p.heard.pos = resumeAt{}, r.at
	p.mu.Unlock()
	go func() {
		if c, err := p.conn(); err == nil {
			_, _ = p.call(context.Background(), c, "seek", r.at, "absolute")
		}
	}()
}

// ended handles end-file: an entry whose stream ended long before its length was refused part way; one that could not be
// opened was refused if mpv had it from a prefetched answer (StreamFailed says), or if it is one Recover loaded again just now.
// Any other failure to open (a removed track, a 404) is left to mpv, which goes on to the next entry.
func (p *Player) ended(ev Event) {
	if ev.PlaylistEntryID == 0 || (ev.Reason != "error" && ev.Reason != "eof") {
		return
	}
	p.mu.Lock()
	h := heardEntry{id: ev.PlaylistEntryID}
	switch h.id {
	case p.heard.id:
		h = p.heard
	case p.prevHeard.id:
		h = p.prevHeard
	}
	url, paused := p.urlOf(h.id), p.v.pause
	p.mu.Unlock()
	cut := ev.Reason == "eof" && p.cfg.Recover && h.dur > 0 && h.pos < h.dur-cutSlack
	if url == "" || (ev.Reason == "eof" && !cut) {
		return
	}
	// With Recover, only the Player holding the lease acts on a refusal (and forgets its answer): see leaseProp.
	stand, takeOver := p.standAside()
	if stand && !takeOver {
		return
	}
	go func() {
		if takeOver && !p.takeOver(context.Background()) {
			return
		}
		if cut {
			p.recover(h.id, url, h.pos, paused, false)
			return
		}
		kept := p.cfg.StreamFailed != nil && p.cfg.StreamFailed(url)
		if !p.cfg.Recover {
			return
		}
		p.mu.Lock()
		retried := time.Since(p.recovered[url]) < recoverWindow
		p.mu.Unlock()
		if kept || retried {
			p.recover(h.id, url, h.pos, paused, true)
		}
	}()
}

// sought handles a seek event, which is also what loop-file does at the end of an entry: when playback was at the end of what
// the demuxer read, and that end is long before the track's length, the stream was refused there. A person's seek starts
// elsewhere, and is not mistaken for it.
func (p *Player) sought() {
	if !p.cfg.Recover {
		return
	}
	stand, takeOver := p.standAside()
	if stand && !takeOver {
		return
	}
	p.mu.Lock()
	h, paused := p.heard, p.v.pause
	p.mu.Unlock()
	if h.id != 0 && (h.pos > 0 || h.back > 0) {
		go func() {
			if takeOver && !p.takeOver(context.Background()) {
				return
			}
			p.checkCut(h, paused)
		}()
	}
}

// standAside reports whether this Player leaves refusals to the one holding the lease, and whether it should take the lease
// over instead: nobody holds it (a claim that failed), or its holder's process has ended (a PiG that was killed never gave it
// up). It is asked on refusal and seek events only: the check of the holder is one signal to its process.
func (p *Player) standAside() (stand, takeOver bool) {
	p.mu.Lock()
	stand = p.cfg.Recover && !p.leaseless && p.lease != p.token
	holder := p.lease
	p.mu.Unlock()
	return stand, stand && (holder == "" || !holderAlive(holder))
}

// takeOver claims the lease, and reports whether this Player holds it then.
func (p *Player) takeOver(ctx context.Context) bool {
	c, err := p.conn()
	if err != nil {
		return false
	}
	p.claimLease(ctx, c)
	return p.leaseConfirmed(ctx, c)
}

// cutReach is how near the end of what the demuxer read playback must have been for a seek to be loop-file's at that end.
const cutReach = 1.5

// reached reports whether playback was at end just before the seek: where it is, or where it was before it last went back.
func (h heardEntry) reached(end float64) bool {
	near := func(pos float64) bool { return pos > 0 && end-pos <= cutReach && pos-end <= cutReach }
	return near(h.pos) || near(h.back)
}

// checkCut asks mpv where the stream of the entry ends and how long the track is (the seek that follows a load comes before
// the player has heard the length).
func (p *Player) checkCut(h heardEntry, paused bool) {
	c, err := p.conn()
	if err != nil {
		return
	}
	var demux struct {
		EOF bool     `json:"eof"`
		End *float64 `json:"cache-end"` // absent when mpv has no timestamp for it
	}
	var length float64
	raw, err := p.call(context.Background(), c, "get_property", "demuxer-cache-state")
	if err != nil || json.Unmarshal(raw, &demux) != nil || !demux.EOF || demux.End == nil || !h.reached(*demux.End) {
		return
	}
	if raw, err = p.call(context.Background(), c, "get_property", "duration"); err != nil || json.Unmarshal(raw, &length) != nil || *demux.End >= length-cutSlack {
		return
	}
	p.mu.Lock()
	url, current := p.urlOf(h.id), p.heard.id == h.id
	p.mu.Unlock()
	if current && url != "" {
		p.recover(h.id, url, *demux.End, paused, false)
	}
}

// urlOf is the URL of the queue entry with the ID, "" when there is none. The caller holds p.mu.
func (p *Player) urlOf(id int) string {
	for _, e := range p.v.entries {
		if e.ID == id {
			return e.Filename
		}
	}
	return ""
}

// recover loads the entry whose stream was refused again, in its place and resolved afresh, and resumes it at seconds, paused
// if it was; or, when the same URL was loaded again less than recoverWindow ago, moves past it (skip). An entry that is no
// longer in the queue (removed, or already loaded again after an earlier report of the same refusal) is left alone. forgotten
// is whether StreamFailed was called for this refusal already.
func (p *Player) recover(id int, url string, at float64, paused, forgotten bool) {
	p.opMu.Lock()
	defer p.opMu.Unlock()
	c, err := p.conn()
	if err != nil {
		return
	}
	ctx := context.Background() // every command is bounded by the CommandTimeout
	if !p.leaseConfirmed(ctx, c) {
		return
	}
	list, ok := p.playlistNow(ctx, c)
	index := indexOf(list, id)
	if !ok {
		return
	}
	now := time.Now()
	p.mu.Lock()
	if index < 0 || id == p.skipped { // the same refusal, reported again (mpv seeks more than once as it loads)
		p.mu.Unlock()
		return
	}
	again := now.Sub(p.recovered[url]) < recoverWindow
	for u, t := range p.recovered {
		if now.Sub(t) >= recoverWindow {
			delete(p.recovered, u)
		}
	}
	if again {
		p.skipped = id
	} else {
		p.recovered[url] = now
	}
	title, loopList := trackFor(list[index], p.meta).Title, looping(p.v.loopList)
	p.mu.Unlock()
	if p.cfg.StreamFailed != nil && !forgotten {
		p.cfg.StreamFailed(url) // before the entry is loaded again: its yt-dlp must not answer from what was refused
	}
	if again {
		p.skip(ctx, c, list, index, title, loopList)
		return
	}
	p.reload(ctx, c, id, url, at, paused)
}

// reload puts a new entry for the URL before the refused one (old), plays it and removes the old one. On mpv 0.38 and later
// loadfile inserts it there; before, it is appended and moved. Every step finds the two by their IDs in the queue as it is
// then: another client (a pigmusic command, a person's Remove) may change it between two steps. When the old entry has gone,
// the new entry is taken out again and nothing else is done. (Only the Player holding the lease reloads: see leaseProp.)
func (p *Player) reload(ctx context.Context, c *Client, old int, url string, at float64, paused bool) {
	list, ok := p.playlistNow(ctx, c)
	o := indexOf(list, old)
	if !ok || o < 0 {
		return
	}
	p.mu.Lock()
	insertAt := p.insertAt
	p.mu.Unlock()
	load := map[string]any{"name": "loadfile", "url": url, "flags": "append"}
	if insertAt {
		load["flags"], load["index"] = "insert-at", o
	}
	raw, err := p.callNamed(ctx, c, load)
	if err != nil {
		return
	}
	var reply struct {
		ID int `json:"playlist_entry_id"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.ID == 0 {
		return // every mpv pig-music runs on (0.35 and later) names the new entry
	}
	added := reply.ID
	p.mu.Lock()
	if at > 0 {
		p.resume = resumeAt{id: added, at: at}
	}
	p.mu.Unlock()
	if !insertAt { // the new entry goes before the old one, unless the old one has gone
		list, ok = p.playlistNow(ctx, c)
		o, n := indexOf(list, old), indexOf(list, added)
		if !ok || n < 0 {
			return
		}
		if o < 0 {
			_, _ = p.call(ctx, c, "playlist-remove", n)
			return
		}
		if _, err := p.call(ctx, c, "playlist-move", n, o); err != nil {
			return
		}
	}
	// It plays, unless the old one went meanwhile (then another client has dealt with it, and the new one goes too).
	list, ok = p.playlistNow(ctx, c)
	o, n := indexOf(list, old), indexOf(list, added)
	if !ok || n < 0 {
		return
	}
	if o < 0 {
		_, _ = p.call(ctx, c, "playlist-remove", n)
		return
	}
	if _, err := p.call(ctx, c, "playlist-play-index", n); err != nil {
		return
	}
	// The old one goes.
	list, ok = p.playlistNow(ctx, c)
	if o = indexOf(list, old); ok && o >= 0 {
		_, _ = p.call(ctx, c, "playlist-remove", o)
	}
	if paused {
		_, _ = p.call(ctx, c, "set_property", "pause", true)
	}
}

// playlistNow reads the queue from mpv, not from the observed state, whose events may still be on their way.
func (p *Player) playlistNow(ctx context.Context, c *Client) ([]playlistEntry, bool) {
	raw, err := p.call(ctx, c, "get_property", "playlist")
	var list []playlistEntry
	if err != nil || json.Unmarshal(raw, &list) != nil {
		return nil, false
	}
	return list, true
}

func indexOf(list []playlistEntry, id int) int {
	for i, e := range list {
		if e.ID == id {
			return i
		}
	}
	return -1
}

// skip moves past the entry at index of list, refused again: mpv plays the next entry (the first after the last when the queue
// repeats), or stops, keeping the queue, when there is no other. loop-file is left as it is: it applies to what plays next.
// The reason goes to Config.Unplayable.
func (p *Player) skip(ctx context.Context, c *Client, list []playlistEntry, index int, title string, loopList bool) {
	reason := `Skipped "` + title + `": its stream stopped again after a retry`
	if list[index].Current {
		if index+1 < len(list) || (loopList && len(list) > 1) {
			_, _ = p.call(ctx, c, "playlist-next")
		} else {
			reason = `Stopped: "` + title + `" could not be played after a retry`
			_, _ = p.call(ctx, c, "playlist-play-index", "none")
		}
	}
	if p.cfg.Unplayable != nil {
		go p.cfg.Unplayable(reason)
	}
}

// ── the recovery lease ───────────────────────────────────────────────────────────────────────────────────────────────────
//
// Every Player attached to an mpv sees the same end-file and seek events. Only one of them may act on a refusal, or two would
// reload the same entry at once. The one that may is named in mpv itself (leaseProp, a user-data property that lives as long
// as the mpv): a Player with Recover claims it on Attach when nobody holds it (or its holder's process has ended), and again
// whenever it sees it emptied; it gives it up on Close. On an mpv without user-data (before 0.36) there is nothing to share it
// through, and every Player recovers, as one alone does.

// leaseProp names the Player that recovers refused streams for this mpv, by its token.
const leaseProp = "user-data/pig-music/recoverer"

var (
	leaseSeq     atomic.Int64
	processStart = time.Now()
)

// newLeaseToken is unique to a Player: the process, when it started, and a count (a process can hold several Players, one per
// generation of the extension, and the tests two).
func newLeaseToken() string {
	return fmt.Sprintf("%d:%d:%d", os.Getpid(), processStart.UnixNano(), leaseSeq.Add(1))
}

// holderAlive reports whether the process named in a lease token still runs (a PiG that was killed never gave it up).
func holderAlive(token string) bool {
	pid, err := strconv.Atoi(strings.SplitN(token, ":", 2)[0])
	return err != nil || processAlive(pid) // a token in another form is taken as held
}

// holdsLease reports whether this Player is the one that recovers, as far as it has heard.
func (p *Player) holdsLease() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.leaseless || p.lease == p.token
}

// leaseChanged notes who holds the lease, and claims it when it has been given up. The caller holds p.mu.
func (p *Player) leaseChanged(raw json.RawMessage) {
	p.lease = ""
	_ = json.Unmarshal(raw, &p.lease)
	if p.lease == "" && p.cfg.Recover && p.client != nil && !p.closed {
		go p.claimLease(context.Background(), p.client)
	}
}

// claimLease takes the lease when nobody holds it, or its holder's process has ended. Who holds it is then what mpv reports
// (leaseChanged): when two claim at once, the last one written holds it, and both hear so.
func (p *Player) claimLease(ctx context.Context, c *Client) {
	// An unset user-data property is "not found" (mpv 0.41, measured); so is any on an mpv before 0.36, whose set fails too.
	holder := ""
	if raw, err := p.call(ctx, c, "get_property", leaseProp); err == nil {
		_ = json.Unmarshal(raw, &holder)
	}
	if holder != "" && (holder == p.token || holderAlive(holder)) {
		return
	}
	if _, err := p.call(ctx, c, "set_property", leaseProp, p.token); err != nil && err.Error() == "mpv: property not found" {
		p.mu.Lock()
		p.leaseless = true // no user-data to keep the lease in: this Player recovers, as one alone does
		p.mu.Unlock()
	}
}

// leaseConfirmed asks mpv whether this Player holds the lease now: the last check before a recovery changes the queue.
func (p *Player) leaseConfirmed(ctx context.Context, c *Client) bool {
	p.mu.Lock()
	leaseless := p.leaseless
	p.mu.Unlock()
	if leaseless {
		return true
	}
	raw, err := p.call(ctx, c, "get_property", leaseProp)
	var holder string
	return err == nil && json.Unmarshal(raw, &holder) == nil && holder == p.token
}

// prepareRecovery reads what this mpv's loadfile takes, observes the lease (its id follows the state's properties) and claims
// it when nobody holds it.
func (p *Player) prepareRecovery(ctx context.Context, c *Client) {
	if raw, err := p.call(ctx, c, "get_property", "mpv-version"); err == nil {
		var version string
		_ = json.Unmarshal(raw, &version)
		p.mu.Lock()
		p.insertAt = mpvAtLeast(version, 38)
		p.mu.Unlock()
	}
	_, _ = p.call(ctx, c, "observe_property", len(propertyNames)+1, leaseProp)
	p.claimLease(ctx, c)
}

// releaseLease gives the lease up if this Player holds it, so that another Player on the same mpv takes it.
func (p *Player) releaseLease(c *Client) {
	p.mu.Lock()
	held := p.cfg.Recover && !p.leaseless && p.lease == p.token
	p.mu.Unlock()
	if held {
		_, _ = p.call(context.Background(), c, "set_property", leaseProp, "")
	}
}

// mpvAtLeast reports whether an mpv-version ("mpv 0.38.0", "mpv v0.41.0-dev-...") is at least 0.minor.
func mpvAtLeast(version string, minor int) bool {
	i := strings.IndexAny(version, "0123456789")
	if i < 0 {
		return false
	}
	var major, min int
	if n, _ := fmt.Sscanf(version[i:], "%d.%d", &major, &min); n < 2 {
		return false
	}
	return major > 0 || min >= minor
}
