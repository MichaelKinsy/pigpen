package engine

import (
	"errors"
	"fmt"
	"strings"

	"pigtorio/internal/hc"
)

// enter puts crate c at stop idx of its line. approved says the crate comes straight
// from an approved Review (the Barn then ships without waiting).
func (e *Engine) enter(c *Crate, idx int, approved bool) {
	l := e.line(c.Line)
	if l == nil {
		e.fail(c, fmt.Sprintf("line %q is gone", c.Line))
		return
	}
	if idx >= len(l.Stops) {
		c.State, c.At = StShipped, nil
		e.hist(c, "End of the line", "")
		e.emit(c, "shipped", "", "end of line "+l.ID+", no Barn")
		return
	}
	c.Stop = idx
	s := l.Stops[idx]
	switch s.Kind {
	case "pig":
		e.toPig(c, s.Pig)
		e.hist(c, e.pigName(s.Pig)+" gets the crate", "")
		e.emit(c, "moved", s.Pig, "to pig "+s.Pig)
	case "oven":
		e.emit(c, "moved", "", "to the Oven")
		e.startOven(c)
	case "review":
		c.State, c.At = StHolding, &At{Kind: "review"}
		e.hist(c, "Waiting at Review", "")
		e.emit(c, "moved", "", "to Review")
		e.emit(c, "holding", "", "waiting for you at Review")
	case "barn":
		c.At = &At{Kind: "barn"}
		e.emit(c, "moved", "", "to the Barn")
		if approved {
			e.startBarn(c)
		} else {
			c.State = StBarn
			e.hist(c, "Waiting at the Barn", "")
		}
	default:
		e.fail(c, fmt.Sprintf("unknown stop kind %q", s.Kind))
	}
}

// toPig points crate c at a pig; dispatch sends the brief once the pig is free.
func (e *Engine) toPig(c *Crate, pig string) {
	c.State, c.At = StWorking, &At{Kind: "pig", Pig: pig}
	c.Sent, c.SentAt = false, 0
	delete(e.away, c.ID)
	delete(e.sendErr, c.ID)
}

func (e *Engine) advance(c *Crate) { e.enter(c, c.Stop+1, false) }

// returnToPig sends c back along the belt to the last pig stop before its stop.
func (e *Engine) returnToPig(c *Crate, note string) error {
	l := e.line(c.Line)
	if l != nil {
		for i := c.Stop - 1; i >= 0 && i < len(l.Stops); i-- {
			if l.Stops[i].Kind == "pig" {
				c.Note = note
				e.emit(c, "returned", l.Stops[i].Pig, "back to "+e.pigName(l.Stops[i].Pig))
				e.hist(c, "Returned to "+e.pigName(l.Stops[i].Pig), "")
				e.enter(c, i, false)
				return nil
			}
		}
	}
	return errors.New("no pig stop before this one to return to")
}

func (e *Engine) fail(c *Crate, why string) {
	c.State = StFailed
	e.hist(c, "Failed", why)
	e.emit(c, "failed", pigOf(c), why)
}

func pigOf(c *Crate) string {
	if c.At != nil {
		return c.At.Pig
	}
	return ""
}

// brief is the message a pig gets for a crate.
func (e *Engine) brief(c *Crate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Crate %s: %s\n\nMandate\n- Asked by: %s\n- Done when: %s\n", c.ID, c.Title, c.AskedBy, c.DoneWhen)
	if strings.TrimSpace(c.Text) != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(c.Text))
	}
	if strings.TrimSpace(c.Report) != "" {
		from := c.HandFrom
		if from == "" {
			from = "the previous stop"
		}
		fmt.Fprintf(&b, "\nPrevious hand-off (from %s):\n%s\n", from, strings.TrimSpace(c.Report))
	}
	if strings.TrimSpace(c.Note) != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(c.Note))
	}
	b.WriteString("\nWhen you are done, end with a hand-off in this form:\nGoal: ...\nDone: ...\nUnverified: ...\nFiles touched: ...\n")
	return b.String()
}

// Reconcile takes a fresh terminal list and moves crates along.
func (e *Engine) Reconcile(terms []hc.Term) {
	e.sendHold = map[string]bool{} // fresh terminal news: failed sends may be tried again
	e.terms = make(map[string]hc.Term, len(terms))
	for _, t := range terms {
		e.terms[t.Address] = t
	}
	e.trackStatuses()
	for _, c := range append([]*Crate(nil), e.st.Crates...) {
		e.checkPig(c)
	}
	e.dispatch()
	e.Flush(false)
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true}

// isAgent says the terminal runs an agent that reports a status (not a bare shell).
func isAgent(t hc.Term) bool { return t.Status != "" && !shells[t.Agent] }

// readyForBrief says the agent sits at its prompt: idle or done, nothing else.
func readyForBrief(t hc.Term) bool {
	return isAgent(t) && (t.Status == "idle" || t.Status == "done")
}

// markAway counts a reconcile in which the crate's pig could not take or finish it; a pig that
// stays away for two reconciles (and GoneAfter) fails the crate.
func (e *Engine) markAway(c *Crate, why string) {
	r := e.away[c.ID]
	if r.n == 0 {
		r.first = e.now()
	}
	r.n++
	e.away[c.ID] = r
	if r.n >= 2 && e.now()-r.first >= e.GoneAfter.Milliseconds() {
		delete(e.away, c.ID)
		e.fail(c, why)
	}
}

func (e *Engine) checkPig(c *Crate) {
	if c.At == nil || c.At.Kind != "pig" || (c.State != StWorking && c.State != StBlocked) {
		return
	}
	addr := c.At.Pig
	t, ok := e.terms[addr]
	switch {
	case !ok:
		e.markAway(c, "pig "+e.pigName(addr)+" is gone")
		return
	case !isAgent(t) && !(c.Sent && t.Turn > c.SentTurn):
		e.markAway(c, "pig "+e.pigName(addr)+" is no longer an agent: its terminal reports no status")
		return
	}
	delete(e.away, c.ID)
	if !c.Sent {
		return
	}
	switch t.Status {
	case "blocked":
		if c.State != StBlocked {
			c.State = StBlocked
			e.hist(c, e.pigName(addr)+" is blocked", "waiting for you")
			e.emit(c, "blocked", addr, "pig is blocked")
		}
	case "working":
		if c.State == StBlocked {
			c.State = StWorking
			e.hist(c, e.pigName(addr)+" is back to work", "")
		}
	default: // done, idle, or no status after a finished turn
		if t.Turn > c.SentTurn {
			e.pigFinished(c, t)
		}
	}
}

func (e *Engine) pigFinished(c *Crate, t hc.Term) {
	addr := c.At.Pig
	pt := pigTerm{argv: e.argvOf(addr), cwd: t.Cwd}
	rep := e.readReport(c, addr, pt)
	text := rep.Text
	if text == "" {
		text = "(the pig left no hand-off)"
	}
	c.Report, c.HandFrom = capText(text, false), e.pigName(addr)
	c.Tokens += rep.Tokens
	c.Cost += rep.Cost
	c.Sent, c.Note, c.State = false, "", StWorking
	e.hist(c, e.pigName(addr)+" hands off", firstLine(text))
	e.emitUsage(c, "done", addr, firstLine(text), rep.Tokens, rep.Cost)
	if c.Direct {
		c.State = StHolding
		e.hist(c, "Holding for you", "")
		e.emit(c, "holding", addr, "waiting for you")
		return
	}
	e.advance(c)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// maxText caps the free text a crate keeps in state.json (report, log, note).
const maxText = 4000

// capText cuts s to maxText runes, keeping its start (head) or its end (tail).
func capText(s string, tail bool) string {
	r := []rune(s)
	if len(r) <= maxText {
		return s
	}
	if tail {
		return "…" + string(r[len(r)-maxText+1:])
	}
	return string(r[:maxText-1]) + "…"
}

func (e *Engine) argvOf(addr string) []string {
	if a, ok := e.argv[addr]; ok {
		return a
	}
	t, err := e.HC.Get(addr)
	if err != nil {
		return nil
	}
	e.argv[addr] = t.Argv
	return t.Argv
}

// holder is the crate whose brief is out with pig addr (sent, or being sent).
func (e *Engine) holder(addr string) *Crate {
	if id, ok := e.sendingTo[addr]; ok {
		return e.crate(id)
	}
	for _, c := range e.st.Crates {
		if c.Sent && c.At != nil && c.At.Kind == "pig" && c.At.Pig == addr && (c.State == StWorking || c.State == StBlocked) {
			return c
		}
	}
	return nil
}

// dispatch starts the sending of briefs to free agents, oldest crate first. A pig that is
// not an agent at its prompt (no status, working, blocked, done after exit) keeps its crates queued.
func (e *Engine) dispatch() {
	for _, c := range e.st.Crates {
		if c.State != StWorking || c.Sent || c.At == nil || c.At.Kind != "pig" {
			continue
		}
		if _, busy := e.sending[c.ID]; busy || e.sendHold[c.ID] {
			continue
		}
		addr := c.At.Pig
		t, ok := e.terms[addr]
		if !ok || !readyForBrief(t) || e.holder(addr) != nil {
			continue
		}
		e.startSend(c, addr)
	}
}

// startSend looks at the terminal again, then types the brief, off the main loop: hc can be slow
// and the heartbeat must keep beating.
func (e *Engine) startSend(c *Crate, addr string) {
	id, text, started := c.ID, e.brief(c), e.now()
	e.sending[id] = addr
	e.sendingTo[addr] = id
	e.startJob(func() func() {
		var turn int
		fresh, err := e.HC.Get(addr)
		switch {
		case err != nil:
		case !readyForBrief(fresh):
			err = hc.ErrNotReady
		default:
			ifStatus := ""
			if fresh.Status == "idle" {
				ifStatus = "idle"
			}
			turn = fresh.Turn
			err = e.HC.Send(addr, text, ifStatus)
		}
		return func() { e.sendDone(id, addr, started, turn, err) }
	})
}

func (e *Engine) sendDone(id, addr string, started int64, turn int, err error) {
	delete(e.sending, id)
	delete(e.sendingTo, addr)
	c := e.crate(id)
	if c == nil || c.State != StWorking || c.Sent || c.At == nil || c.At.Pig != addr {
		if err == nil {
			e.logf("brief for %s reached %s after the crate moved on", id, addr)
		}
		return
	}
	if err != nil {
		e.sendHold[id] = true // try again after the next listing, not in a loop
		if !errors.Is(err, hc.ErrBlocked) && !errors.Is(err, hc.ErrNotReady) && e.sendErr[c.ID] != err.Error() {
			e.sendErr[c.ID] = err.Error()
			e.hist(c, "Could not reach "+e.pigName(addr), err.Error())
			e.logf("send %s to %s: %v", c.ID, addr, err)
		}
		return
	}
	delete(e.sendErr, c.ID)
	c.Sent, c.SentAt, c.SentTurn = true, started, turn
	e.hist(c, "Brief sent to "+e.pigName(addr), "")
}
