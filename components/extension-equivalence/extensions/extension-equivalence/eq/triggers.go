package eq

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// A trigger sends a command or releases a held model turn while a step is in flight: the harness's way to
// queue input during a run (steer, follow_up), abort a turn the model is still answering, or compact while
// the host is busy. The step arms its triggers before it sends its command, so no event can be missed;
// every trigger must have fired by the end of the step.

type liveTrigger struct {
	t     Trigger
	index int
	seen  int
	fired bool
	done  chan struct{} // closed when the trigger's work has finished
}

// triggerSet is the triggers of the step that is running.
type triggerSet struct {
	mu   sync.Mutex
	live []*liveTrigger
}

// arm installs the triggers of a step; the previous step's must all have finished.
func (d *driver) arm(ts []Trigger) {
	d.trig.mu.Lock()
	defer d.trig.mu.Unlock()
	d.trig.live = nil
	for i, t := range ts {
		d.trig.live = append(d.trig.live, &liveTrigger{t: t, index: i})
	}
}

// onHostEvent counts a host record against the armed event triggers and starts those it completes. It runs on
// the goroutine that reads the host's output, after the record has entered the trace.
func (d *driver) onHostEvent(rec map[string]any) {
	d.trig.mu.Lock()
	var due []*liveTrigger
	for _, lt := range d.trig.live {
		if lt.fired || lt.t.When.Event == "" || !lt.t.When.eventMatches(rec) {
			continue
		}
		lt.seen++
		nth := lt.t.When.Nth
		if nth == 0 {
			nth = 1
		}
		if lt.seen == nth {
			lt.fired = true
			due = append(due, lt)
			lt.done = make(chan struct{})
		}
	}
	d.trig.mu.Unlock()
	for _, lt := range due {
		go d.fire(lt)
	}
}

// onLLMRequest starts the triggers waiting for the nth model request. The model server calls it after it has
// recorded the request and before it answers it.
func (d *driver) onLLMRequest(n int) {
	d.trig.mu.Lock()
	var due []*liveTrigger
	for _, lt := range d.trig.live {
		if !lt.fired && lt.t.When.LLMRequest == n {
			lt.fired = true
			due = append(due, lt)
			lt.done = make(chan struct{})
		}
	}
	d.trig.mu.Unlock()
	for _, lt := range due {
		go d.fire(lt)
	}
}

// fire sends the trigger's command, waits for its response, then releases the held turns: a steer sent while the
// model's answer is held has reached the host before the answer does.
func (d *driver) fire(lt *liveTrigger) {
	defer close(lt.done)
	label := fmt.Sprintf("trigger %d", lt.index)
	if lt.t.RPC != nil {
		v, err := d.vars.expand(lt.t.RPC)
		if err != nil {
			d.rec.add(ChError, map[string]any{"message": label + ": " + err.Error()})
			return
		}
		cmd := v.(map[string]any)
		id := d.newID(lt.t.Quiet)
		cmd["id"] = id
		if len(lt.t.Save) > 0 || lt.t.Quiet {
			d.mu.Lock()
			d.want[id] = true
			d.mu.Unlock()
		}
		done := make(chan struct{})
		d.mu.Lock()
		d.awaiting[id] = done
		d.mu.Unlock()
		if err := d.send(cmd); err != nil {
			d.rec.add(ChError, map[string]any{"message": label + ": send: " + err.Error()})
			return
		}
		select {
		case <-done:
		case <-d.readerDone:
		case <-d.timer(d.stepTimeout):
			d.rec.add(ChError, map[string]any{"message": fmt.Sprintf("%s: no response to %v within %s", label, cmd["type"], d.stepTimeout)})
		}
		if len(lt.t.Save) > 0 || lt.t.Quiet {
			if err := d.saveVars(id, lt.t.Save); err != nil {
				d.rec.add(ChError, map[string]any{"message": label + ": " + err.Error()})
			}
		}
	}
	if d.release != nil {
		for _, name := range lt.t.Release {
			d.release(name)
		}
	}
}

// finishTriggers waits for the fired triggers' work and reports one that never fired. It disarms the step.
func (d *driver) finishTriggers(timeout time.Duration) error {
	type state struct {
		lt    *liveTrigger
		fired bool
		done  chan struct{}
	}
	d.trig.mu.Lock()
	var live []state
	for _, lt := range d.trig.live {
		live = append(live, state{lt, lt.fired, lt.done})
	}
	d.trig.live = nil
	d.trig.mu.Unlock()
	deadline := d.timer(timeout)
	for _, s := range live {
		if !s.fired {
			return fmt.Errorf("trigger %d never fired (%s)", s.lt.index, s.lt.t.When.describe())
		}
		select {
		case <-s.done:
		case <-deadline:
			return fmt.Errorf("trigger %d: its command got no response within %s", s.lt.index, timeout)
		}
	}
	return nil
}

func (w TriggerWhen) describe() string {
	if w.LLMRequest > 0 {
		return fmt.Sprintf("model request %d", w.LLMRequest)
	}
	b, _ := json.Marshal(w.Match)
	n := w.Nth
	if n == 0 {
		n = 1
	}
	return fmt.Sprintf("%s event number %d matching %s", w.Event, n, b)
}

// saveVars stores the values the step asked to keep from its command's response, and fails the step when the
// response of a command the harness kept (saved from, or Quiet) was a failure.
func (d *driver) saveVars(id string, save map[string]string) error {
	d.mu.Lock()
	rec := d.resp[id]
	delete(d.resp, id)
	d.mu.Unlock()
	if rec == nil {
		if len(save) == 0 {
			return nil
		}
		return fmt.Errorf("save: no response was kept for the command")
	}
	if ok, _ := rec["success"].(bool); !ok {
		return fmt.Errorf("the command failed: %v", rec["error"])
	}
	for name, p := range save {
		segs, err := splitPath(p)
		if err != nil {
			return err
		}
		v, ok := pathGet(rec, segs)
		if !ok {
			return fmt.Errorf("save %s: path %q found nothing in the response", name, p)
		}
		d.vars.set(name, scalarText(v))
	}
	return nil
}
