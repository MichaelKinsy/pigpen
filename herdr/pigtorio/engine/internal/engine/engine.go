package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"pigtorio/internal/hc"
)

// ErrLocked means another engine holds D/engine.lock.
var ErrLocked = errors.New("another pigtorio engine is already running for this home")

type awayRec struct {
	n     int
	first int64
}

type pigTerm struct {
	argv []string
	cwd  string
}

// Engine owns crates, lines and stations. All state is touched from one goroutine:
// the one running Run (tests call the methods directly).
type Engine struct {
	Home string // H
	Dir  string // D = H/pigtorio
	HC   hc.Client
	Now  func() time.Time

	OvenTimeout time.Duration
	PigSessions string // root of PiG's sessions; "" disables read path (2)
	// ShipFunc is what the Barn runs (Ship; tests swap it).
	ShipFunc  func(dir, title, crateID string) (ShipResult, error)
	GoneAfter time.Duration // a pig that cannot take its crate this long (and 2 reconciles) fails it

	farm      Farm
	farmSig   string
	st        State
	terms     map[string]hc.Term
	argv      map[string][]string
	statuses  map[string]statusRec
	away      map[string]awayRec
	sending   map[string]string // crate -> pig while its brief is being typed
	sendingTo map[string]string // pig -> crate, the same
	shipping  map[string]bool
	sendHold  map[string]bool // crates whose last send failed: wait for the next listing
	sigs      map[string]uint64
	sendErr   map[string]string
	running   map[string]bool
	events    []Event
	nextID    int
	results   chan func()
	jobs      int
	lastCmp   []byte
	lastBeat  time.Time
	started   int64
}

// New makes an engine for home H.
func New(home string, client hc.Client) *Engine {
	return &Engine{
		Home:        home,
		Dir:         filepath.Join(home, "pigtorio"),
		HC:          client,
		Now:         time.Now,
		OvenTimeout: 10 * time.Minute,
		PigSessions: defaultPigSessionsRoot(),
		terms:       map[string]hc.Term{},
		argv:        map[string][]string{},
		statuses:    map[string]statusRec{},
		away:        map[string]awayRec{},
		sending:     map[string]string{},
		sendingTo:   map[string]string{},
		shipping:    map[string]bool{},
		sendHold:    map[string]bool{},
		sigs:        map[string]uint64{},
		GoneAfter:   8 * time.Second,
		ShipFunc:    Ship,
		sendErr:     map[string]string{},
		running:     map[string]bool{},
		results:     make(chan func(), 64),
		nextID:      1,
	}
}

func (e *Engine) now() int64 { return e.Now().UnixMilli() }

func (e *Engine) logf(format string, args ...any) { log.Printf(format, args...) }

func (e *Engine) path(parts ...string) string {
	return filepath.Join(append([]string{e.Dir}, parts...)...)
}

// Lock takes D/engine.lock. The returned func releases it.
func (e *Engine) Lock() (func(), error) {
	if err := os.MkdirAll(e.Dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(e.path("engine.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrLocked
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// Load reads state.json and crates.jsonl left by an earlier run, and farm.json.
func (e *Engine) Load() {
	for _, d := range []string{"inbox", "state", "sessions"} {
		_ = os.MkdirAll(e.path(d), 0o755)
	}
	if b, err := os.ReadFile(e.path("state", "state.json")); err == nil {
		var st State
		if json.Unmarshal(b, &st) == nil {
			e.st = st
		}
	}
	if e.st.Crates == nil {
		e.st.Crates = []*Crate{}
	}
	for _, c := range e.st.Crates {
		e.bumpID(c.ID)
		if c.History == nil {
			c.History = []Hist{}
		}
		if c.BlockedBy == nil {
			c.BlockedBy = []string{}
		}
	}
	e.events = nil
	if b, err := os.ReadFile(e.path("crates.jsonl")); err == nil {
		cut := e.now() - 24*3600*1000
		for _, line := range bytes.Split(b, []byte{'\n'}) {
			var ev Event
			if len(line) == 0 || json.Unmarshal(line, &ev) != nil {
				continue
			}
			e.bumpID(ev.Crate)
			if ev.T >= cut {
				e.events = append(e.events, ev)
			}
		}
	}
	e.loadFarm()
}

func (e *Engine) bumpID(id string) {
	if n, err := strconv.Atoi(strings.TrimPrefix(id, "c")); err == nil && strings.HasPrefix(id, "c") && n >= e.nextID {
		e.nextID = n + 1
	}
}

// loadFarm re-reads farm.json when it changed. A broken file keeps the last good one.
func (e *Engine) loadFarm() {
	p := e.path("farm.json")
	info, err := os.Stat(p)
	if err != nil {
		return
	}
	sig := fmt.Sprintf("%d-%d", info.ModTime().UnixNano(), info.Size())
	if sig == e.farmSig {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var f Farm
	if err := json.Unmarshal(b, &f); err != nil {
		e.logf("farm.json: %v", err)
		return
	}
	e.farm, e.farmSig = f, sig
}

// SetFarm replaces the farm in memory (tests).
func (e *Engine) SetFarm(f Farm) { e.farm = f; e.farmSig = "" }

func (e *Engine) line(id string) *Line {
	for i := range e.farm.Lines {
		if e.farm.Lines[i].ID == id {
			return &e.farm.Lines[i]
		}
	}
	return nil
}

func (e *Engine) crate(id string) *Crate {
	for _, c := range e.st.Crates {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func (e *Engine) pigName(addr string) string {
	if p, ok := e.farm.Pigs[addr]; ok && p.Name != "" {
		return p.Name
	}
	return addr
}

// State returns the live state (tests).
func (e *Engine) State() *State { return &e.st }

// hist adds a history line.
func (e *Engine) hist(c *Crate, what, detail string) {
	c.History = append(c.History, Hist{T: e.now(), What: what, Detail: detail})
}

// emit appends one event to crates.jsonl.
func (e *Engine) emit(c *Crate, event, pig, detail string) {
	e.emitUsage(c, event, pig, detail, 0, 0)
}

func (e *Engine) emitUsage(c *Crate, event, pig, detail string, tokens int, cost float64) {
	ev := Event{T: e.now(), Crate: c.ID, Event: event, Pig: pig, Detail: detail, Tokens: tokens, Cost: cost}
	e.events = append(e.events, ev)
	b, _ := json.Marshal(ev)
	f, err := os.OpenFile(e.path("crates.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		e.logf("crates.jsonl: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func (e *Engine) stats() Stats {
	cut := e.now() - 24*3600*1000
	var s Stats
	kept := e.events[:0]
	for _, ev := range e.events {
		if ev.T < cut {
			continue
		}
		kept = append(kept, ev)
		switch ev.Event {
		case "added":
			s.CratesIn24h++
		case "shipped":
			s.TrufflesOut24h++
		case "oven-burnt":
			s.Burnt24h++
		}
		s.Tokens24h += ev.Tokens
	}
	e.events = kept
	return s
}

// statusRec is a terminal's status word and when it began.
type statusRec struct {
	word  string
	since int64
}

// trackStatuses keeps the status word and its start for every live terminal that reports one.
// The first sighting (also after an engine restart) takes `status_ms` from `hc get`; later
// changes of the word are stamped with the time the engine noticed them.
func (e *Engine) trackStatuses() {
	seen := map[string]statusRec{}
	for addr, t := range e.terms {
		if t.Status == "" {
			continue
		}
		rec, ok := e.statuses[addr]
		switch {
		case !ok:
			rec = statusRec{word: t.Status, since: e.now()}
			if g, err := e.HC.Get(addr); err == nil && g.StatusMs > 0 {
				rec.since = g.StatusMs
			}
		case rec.word != t.Status:
			rec = statusRec{word: t.Status, since: e.now()}
		}
		seen[addr] = rec
	}
	e.statuses = seen
}

func (e *Engine) rebuildPigs() {
	pigs := map[string]PigSlot{}
	for addr, rec := range e.statuses {
		pigs[addr] = PigSlot{Status: rec.word, StatusSince: rec.since}
	}
	for _, c := range e.st.Crates {
		if c.At != nil && c.At.Kind == "pig" && c.Sent && (c.State == StWorking || c.State == StBlocked) {
			slot := pigs[c.At.Pig]
			slot.Crate, slot.Since = c.ID, c.SentAt
			pigs[c.At.Pig] = slot
		}
	}
	e.st.Pigs = pigs
}

// Flush writes state.json when something changed (or when force is set, for the heartbeat).
func (e *Engine) Flush(force bool) {
	e.touchCrates()
	e.pruneFinished()
	e.rebuildPigs()
	e.st.Stats = e.stats()
	if len(e.st.Acks) > 50 {
		e.st.Acks = e.st.Acks[len(e.st.Acks)-50:]
	}
	if e.st.Acks == nil {
		e.st.Acks = []Ack{}
	}
	e.st.Engine.PID = os.Getpid()
	e.st.Engine.Version = Version
	if e.started == 0 {
		e.started = e.now()
	}
	e.st.Engine.Started = e.started
	e.st.Engine.Beat = e.lastBeat.UnixMilli()
	cmp, err := json.Marshal(e.st)
	if err != nil {
		e.logf("marshal state: %v", err)
		return
	}
	for len(cmp) > maxStateBytes && e.dropOldestFinished() {
		cmp, _ = json.Marshal(e.st)
	}
	if !force && bytes.Equal(cmp, e.lastCmp) {
		return
	}
	e.lastBeat = e.Now()
	e.st.Engine.Beat = e.lastBeat.UnixMilli()
	data, _ := json.Marshal(e.st) // compact: the mod reads at most 1 MiB
	dir := e.path("state")
	_ = os.MkdirAll(dir, 0o755)
	tmp := filepath.Join(dir, "state.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		e.logf("write state: %v", err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(dir, "state.json")); err != nil {
		e.logf("rename state: %v", err)
		return
	}
	e.lastCmp = data // what the next comparison marshals (with the beat just written)
}

const (
	maxStateBytes = 900_000 // the mod reads at most 1 MiB of state.json
	maxFinished   = 60      // shipped/failed crates kept in state.json; crates.jsonl keeps everything
	maxHistory    = 100     // history lines kept per crate
)

// touchCrates caps free text, resolves each crate's folder and stamps `updated` whenever
// anything else about the crate changed since the last flush.
func (e *Engine) touchCrates() {
	for _, c := range e.st.Crates {
		c.Report, c.Log, c.Note = capText(c.Report, false), capText(c.Log, true), capText(c.Note, true)
		if len(c.History) > maxHistory {
			c.History = append([]Hist(nil), c.History[len(c.History)-maxHistory:]...)
		}
		if f, err := e.folder(c); err == nil {
			c.Folder = f
		}
		updated, folder := c.Updated, c.Folder
		c.Updated, c.Folder = 0, ""
		b, _ := json.Marshal(c)
		c.Updated, c.Folder = updated, folder
		h := fnv.New64a()
		h.Write(b)
		sum := h.Sum64()
		if prev, seen := e.sigs[c.ID]; (!seen && c.Updated == 0) || (seen && prev != sum) {
			c.Updated = e.now()
		}
		e.sigs[c.ID] = sum
	}
}

// dropOldestFinished removes the least recently changed shipped/failed crate; false when none is left.
func (e *Engine) dropOldestFinished() bool {
	oldest := -1
	for i, c := range e.st.Crates {
		if (c.State == StShipped || c.State == StFailed) && (oldest < 0 || c.Updated < e.st.Crates[oldest].Updated) {
			oldest = i
		}
	}
	if oldest < 0 {
		return false
	}
	delete(e.sigs, e.st.Crates[oldest].ID)
	e.st.Crates = append(e.st.Crates[:oldest], e.st.Crates[oldest+1:]...)
	return true
}

// pruneFinished drops all but the most recent maxFinished shipped/failed crates from the state.
func (e *Engine) pruneFinished() {
	var done []*Crate
	for _, c := range e.st.Crates {
		if c.State == StShipped || c.State == StFailed {
			done = append(done, c)
		}
	}
	if len(done) <= maxFinished {
		return
	}
	sort.SliceStable(done, func(i, j int) bool { return done[i].Updated > done[j].Updated })
	drop := map[*Crate]bool{}
	for _, c := range done[maxFinished:] {
		drop[c] = true
		delete(e.sigs, c.ID)
	}
	kept := e.st.Crates[:0]
	for _, c := range e.st.Crates {
		if !drop[c] {
			kept = append(kept, c)
		}
	}
	e.st.Crates = kept
}

// Run holds the lock and serves until ctx ends.
func (e *Engine) Run(ctx context.Context) error {
	unlock, err := e.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	e.Load()
	e.Refresh()
	e.Resume()
	e.Flush(true)

	wake := make(chan struct{}, 1)
	go e.follow(ctx, func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	inbox := time.NewTicker(300 * time.Millisecond)
	reconcile := time.NewTicker(5 * time.Second)
	beat := time.NewTicker(15 * time.Second)
	defer inbox.Stop()
	defer reconcile.Stop()
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			e.Flush(true)
			return nil
		case <-inbox.C:
			e.loadFarm()
			e.PollInbox()
		case <-wake:
			// events come in bursts; let them settle, then read the truth with one `ls`
			select {
			case <-time.After(100 * time.Millisecond):
			case <-ctx.Done():
			}
			e.Refresh()
		case <-reconcile.C:
			e.Refresh()
		case f := <-e.results:
			e.finishJob(f)
			e.Flush(false)
		case <-beat.C:
			e.Flush(true)
		}
	}
}

func (e *Engine) follow(ctx context.Context, wake func()) {
	for ctx.Err() == nil {
		err := e.HC.Follow(ctx, wake)
		if ctx.Err() != nil {
			return
		}
		e.logf("hc events ended: %v; reconnecting", err)
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

// Refresh lists terminals and reconciles crates against them.
func (e *Engine) Refresh() {
	terms, err := e.HC.List()
	if err != nil {
		e.logf("hc ls: %v", err)
		return
	}
	e.Reconcile(terms)
}

// WaitJobs applies finished oven/barn jobs until none are running (tests, shutdown).
func (e *Engine) WaitJobs() {
	for e.jobs > 0 {
		e.finishJob(<-e.results)
	}
	e.Flush(false)
}

// finishJob applies a job's result; the crate it moved may be bound for a free pig.
func (e *Engine) finishJob(f func()) {
	e.jobs--
	f()
	e.dispatch()
}

// startJob runs work off the main goroutine; the func it returns is applied on the main one.
func (e *Engine) startJob(work func() func()) {
	e.jobs++
	go func() { e.results <- work() }()
}

// Resume restarts the Oven for crates that were in it when an earlier engine stopped.
func (e *Engine) Resume() {
	for _, c := range append([]*Crate(nil), e.st.Crates...) {
		if c.State == StOven && !e.running[c.ID] {
			e.startOven(c)
		}
	}
}
