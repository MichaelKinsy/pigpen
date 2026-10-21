package tintinweb_subagents

import (
	"strings"
	"testing"
)

// A background resume streams the resumed run into the agent's existing `.output` file: only what the run produces is
// written (the resumed prompt among it), once. upstream: index.ts Agent resume, onStarted -> streamToOutputFile.
func TestABackgroundResumeStreamsIntoTheSameOutputFile(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, "", ""))
	n := r.fleet.count()
	id := idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true}))
	c := r.fleet.wait(t, n+1)[n]
	rec := r.app.mgr.get(id)
	c.spec.Hooks.Event(obj{"type": "message_end", "message": user("go")})
	c.spec.Hooks.Event(obj{"type": "message_end", "message": assistant("first answer")})
	c.finish("one")
	<-r.app.mgr.doneOf(rec)
	waitStatus(t, r, id, statusCompleted)
	file := rec.OutputFile
	eq(t, file != "", true)
	before := len(readEntries(t, file))

	out := r.must("Agent", obj{"prompt": "more", "description": "d", "subagent_type": "general-purpose", "resume": id, "run_in_background": true})
	eq(t, strings.Contains(out, "resumed"), true)
	c.spec.Hooks.Event(obj{"type": "message_end", "message": user("more")})
	c.spec.Hooks.Event(obj{"type": "message_end", "message": assistant("second answer")})
	c.finish("two")
	<-r.app.mgr.doneOf(rec)

	entries := readEntries(t, file)
	eq(t, len(entries), before+2)
	text := ""
	for _, e := range entries[before:] {
		text += string(jsonLine(e))
	}
	eq(t, strings.Count(text, "more"), 1)
	eq(t, strings.Count(text, "second answer"), 1)
	eq(t, strings.Contains(text, "first answer"), false)
	r.app.mgr.mu.Lock()
	eq(t, rec.transcript == nil, true) // the stream is let go when the resumed run ends
	r.app.mgr.mu.Unlock()
}

// A foreground resume writes nothing: the caller has the answer inline, as in the original.
func TestAForegroundResumeLeavesTheOutputFileAlone(t *testing.T) {
	r := startObsRig(t, obsWorkspace(t, "", ""))
	n := r.fleet.count()
	id := idFrom(t, r.must("Agent", obj{"prompt": "go", "description": "d", "subagent_type": "general-purpose", "run_in_background": true}))
	c := r.fleet.wait(t, n+1)[n]
	rec := r.app.mgr.get(id)
	c.finish("one")
	<-r.app.mgr.doneOf(rec)
	waitStatus(t, r, id, statusCompleted)
	before := len(readEntries(t, rec.OutputFile))
	done := make(chan string, 1)
	go func() {
		done <- r.must("Agent", obj{"prompt": "more", "description": "d", "subagent_type": "general-purpose", "resume": id, "run_in_background": false})
	}()
	for i := 0; i < 1000; i++ {
		c.mu.Lock()
		n := len(c.resumed)
		c.mu.Unlock()
		if n > 0 {
			break
		}
		sleepMs(5)
	}
	c.spec.Hooks.Event(obj{"type": "message_end", "message": assistant("inline only")})
	c.finish("two")
	<-done
	eq(t, len(readEntries(t, rec.OutputFile)), before)
}
