package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PollInbox handles every command file in D/inbox, oldest name first, and deletes it.
func (e *Engine) PollInbox() {
	dir := e.path("inbox")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, en := range ents {
		if !en.IsDir() && strings.HasSuffix(en.Name(), ".json") && !strings.HasPrefix(en.Name(), ".") {
			names = append(names, en.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		p := filepath.Join(dir, name)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !json.Valid(raw) {
			// a writer may still be busy; judge a file only once it has been still for a second
			if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) < time.Second {
				continue
			}
		}
		e.HandleCommand(id, raw)
		_ = os.Remove(p)
	}
}

// HandleCommand runs one inbox command and records its ack.
func (e *Engine) HandleCommand(id string, raw []byte) Ack {
	var cmd Command
	ack := Ack{ID: id}
	err := json.Unmarshal(raw, &cmd)
	if err != nil {
		err = fmt.Errorf("bad command: %w", err)
	} else {
		ack.Op = cmd.Op
		err = e.apply(&cmd)
	}
	ack.OK = err == nil
	if err != nil {
		ack.Error = err.Error()
		e.logf("command %s (%s) refused: %v", id, cmd.Op, err)
	}
	e.st.Acks = append(e.st.Acks, ack)
	e.dispatch()
	e.Flush(false)
	return ack
}

func (e *Engine) apply(cmd *Command) error {
	switch cmd.Op {
	case "crate.add":
		return e.opAdd(cmd)
	case "crate.update":
		return e.opUpdate(cmd)
	case "crate.remove":
		return e.opRemove(cmd)
	case "crate.hand":
		return e.opHand(cmd)
	case "crate.run":
		return e.opRun(cmd)
	case "review.approve":
		return e.opApprove(cmd)
	case "review.sendback":
		return e.opSendBack(cmd)
	case "barn.ship":
		return e.opShip(cmd)
	case "oven.run":
		return e.opOven(cmd)
	}
	return fmt.Errorf("unknown op %q", cmd.Op)
}

func (e *Engine) need(id string) (*Crate, error) {
	if id == "" {
		return nil, errors.New("id required")
	}
	c := e.crate(id)
	if c == nil {
		return nil, fmt.Errorf("no crate %s", id)
	}
	return c, nil
}

func validSize(s string) bool { return s == "one-shot" || s == "large" }

func (e *Engine) opAdd(cmd *Command) error {
	n := cmd.Crate
	if n == nil {
		return errors.New("crate required")
	}
	if strings.TrimSpace(n.Title) == "" {
		return errors.New("a crate needs a title")
	}
	if strings.TrimSpace(n.DoneWhen) == "" {
		return errors.New("a crate needs a done-when: how will anyone know it is done?")
	}
	size := n.Size
	if size == "" {
		size = "one-shot"
	}
	if !validSize(size) {
		return fmt.Errorf("size must be one-shot or large, not %q", size)
	}
	asked := n.AskedBy
	if asked == "" {
		asked = "you"
	}
	id := strings.TrimSpace(n.ID)
	if id == "" {
		id = fmt.Sprintf("c%d", e.nextID)
		e.nextID++
	} else if e.crate(id) != nil {
		return fmt.Errorf("crate %s already exists", id)
	} else {
		e.bumpID(id)
	}
	c := &Crate{
		ID: id, Title: strings.TrimSpace(n.Title), Text: n.Text, DoneWhen: strings.TrimSpace(n.DoneWhen),
		Size: size, Pen: n.Pen, AFK: n.AFK, BlockedBy: append([]string{}, n.BlockedBy...), AskedBy: asked,
		Created: e.now(), State: StBoard, History: []Hist{},
	}
	e.st.Crates = append(e.st.Crates, c)
	e.hist(c, "Crate added", "asked by "+asked)
	e.emit(c, "added", "", c.Title)
	return nil
}

func (e *Engine) opUpdate(cmd *Command) error {
	c, err := e.need(cmd.ID)
	if err != nil {
		return err
	}
	if c.State != StBoard {
		return fmt.Errorf("%s is %s: only a crate on the board can be edited", c.ID, c.State)
	}
	nc := *c
	for k, v := range cmd.Patch {
		switch k {
		case "title", "text", "doneWhen", "size", "pen", "askedBy":
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("%s must be a string", k)
			}
			switch k {
			case "title":
				nc.Title = strings.TrimSpace(s)
			case "text":
				nc.Text = s
			case "doneWhen":
				nc.DoneWhen = strings.TrimSpace(s)
			case "size":
				nc.Size = s
			case "pen":
				nc.Pen = s
			case "askedBy":
				nc.AskedBy = s
			}
		case "afk":
			b, ok := v.(bool)
			if !ok {
				return errors.New("afk must be a boolean")
			}
			nc.AFK = b
		case "blockedBy":
			arr, ok := v.([]any)
			if !ok {
				return errors.New("blockedBy must be a list")
			}
			nc.BlockedBy = []string{}
			for _, x := range arr {
				s, ok := x.(string)
				if !ok {
					return errors.New("blockedBy must be a list of crate ids")
				}
				nc.BlockedBy = append(nc.BlockedBy, s)
			}
		default:
			return fmt.Errorf("cannot patch %q", k)
		}
	}
	if nc.Title == "" || nc.DoneWhen == "" {
		return errors.New("a crate needs a title and a done-when")
	}
	if !validSize(nc.Size) {
		return fmt.Errorf("size must be one-shot or large, not %q", nc.Size)
	}
	*c = nc
	e.hist(c, "Crate edited", "")
	return nil
}

func (e *Engine) opRemove(cmd *Command) error {
	c, err := e.need(cmd.ID)
	if err != nil {
		return err
	}
	out := e.st.Crates[:0]
	for _, x := range e.st.Crates {
		if x != c {
			out = append(out, x)
		}
	}
	e.st.Crates = out
	delete(e.running, c.ID)
	return nil
}

// blockedByOpen names a crate that still blocks c.
func (e *Engine) blockedByOpen(c *Crate) error {
	for _, id := range c.BlockedBy {
		if b := e.crate(id); b != nil && b.State != StShipped {
			return fmt.Errorf("%s is blocked by %s (%s)", c.ID, b.ID, b.State)
		}
	}
	return nil
}

func canLeave(c *Crate) bool {
	return c.State == StBoard || c.State == StBurnt || c.State == StFailed
}

func (e *Engine) opHand(cmd *Command) error {
	c, err := e.need(cmd.ID)
	if err != nil {
		return err
	}
	if cmd.To == "" {
		return errors.New("to required")
	}
	if !canLeave(c) {
		return fmt.Errorf("%s is %s: it is already on its way", c.ID, c.State)
	}
	if strings.TrimSpace(c.DoneWhen) == "" {
		return errors.New("a crate with an empty done-when cannot leave the board")
	}
	if err := e.blockedByOpen(c); err != nil {
		return err
	}
	c.Line, c.Stop, c.Direct = "", 0, true
	e.resetRun(c)
	e.toPig(c, cmd.To)
	e.hist(c, "Handed to "+e.pigName(cmd.To), "")
	e.emit(c, "handed", cmd.To, c.Title)
	return nil
}

func (e *Engine) opRun(cmd *Command) error {
	c, err := e.need(cmd.ID)
	if err != nil {
		return err
	}
	if !canLeave(c) {
		return fmt.Errorf("%s is %s: it is already on its way", c.ID, c.State)
	}
	if strings.TrimSpace(c.DoneWhen) == "" {
		return errors.New("a crate with an empty done-when cannot leave the board")
	}
	l := e.line(cmd.Line)
	if l == nil {
		return fmt.Errorf("no line %q", cmd.Line)
	}
	if len(l.Stops) == 0 {
		return fmt.Errorf("line %s has no stops", l.ID)
	}
	if err := e.blockedByOpen(c); err != nil {
		return err
	}
	if c.Pen == "" {
		c.Pen = l.Pen
	}
	c.Line, c.Direct = l.ID, false
	e.resetRun(c)
	e.hist(c, "Put on line "+l.ID, "")
	e.enter(c, 0, false)
	return nil
}

// resetRun clears what a previous run left behind.
func (e *Engine) resetRun(c *Crate) {
	c.Sent, c.SentAt, c.SentTurn = false, 0, 0
	c.Note, c.Report, c.HandFrom = "", "", ""
	c.Burns = 0
}

func (e *Engine) opApprove(cmd *Command) error {
	c, err := e.need(cmd.ID)
	if err != nil {
		return err
	}
	if c.State != StHolding {
		return fmt.Errorf("%s is %s: nothing to approve", c.ID, c.State)
	}
	if c.Direct {
		c.State = StShipped
		c.At = nil
		e.hist(c, "Accepted", "handed to one pig, no line")
		e.emit(c, "approved", "", "accepted, no line")
		e.emit(c, "shipped", "", "accepted, no line")
		return nil
	}
	l := e.line(c.Line)
	if l == nil || c.Stop >= len(l.Stops) || l.Stops[c.Stop].Kind != "review" {
		return fmt.Errorf("%s is not at a Review", c.ID)
	}
	e.hist(c, "Approved", "")
	e.emit(c, "approved", "", "")
	e.enter(c, c.Stop+1, true)
	return nil
}

func (e *Engine) opSendBack(cmd *Command) error {
	c, err := e.need(cmd.ID)
	if err != nil {
		return err
	}
	if c.State != StHolding {
		return fmt.Errorf("%s is %s: nothing to send back", c.ID, c.State)
	}
	note := strings.TrimSpace(cmd.Note)
	e.emit(c, "sent-back", "", note)
	e.hist(c, "Sent back", note)
	msg := "You sent this crate back from Review."
	if note != "" {
		msg += " Your note: " + note
	}
	if c.Direct {
		c.Note = msg
		c.Sent = false
		e.emit(c, "returned", c.At.Pig, note)
		c.State = StWorking
		return nil
	}
	return e.returnToPig(c, msg)
}

func (e *Engine) opShip(cmd *Command) error {
	c, err := e.need(cmd.ID)
	if err != nil {
		return err
	}
	if c.At == nil || c.At.Kind != "barn" || (c.State != StBarn && c.State != StFailed) {
		return fmt.Errorf("%s is not waiting at the Barn", c.ID)
	}
	if e.shipping[c.ID] {
		return fmt.Errorf("the Barn is already shipping %s", c.ID)
	}
	e.startBarn(c)
	return nil
}

func (e *Engine) opOven(cmd *Command) error {
	c, err := e.need(cmd.ID)
	if err != nil {
		return err
	}
	if c.At == nil || c.At.Kind != "oven" || (c.State != StOven && c.State != StBurnt && c.State != StFailed) {
		return fmt.Errorf("%s is not at the Oven", c.ID)
	}
	if e.running[c.ID] {
		return fmt.Errorf("the Oven is already running %s", c.ID)
	}
	e.startOven(c)
	return nil
}
