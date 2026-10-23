package frontend

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// benchOut is the terminal: it counts what the member writes and, when
// stamping, when it last wrote.
type benchOut struct {
	bytes, writes, last atomic.Int64
	stamp               atomic.Bool
}

func (o *benchOut) Write(p []byte) (int, error) {
	if o.stamp.Load() {
		o.last.Store(time.Now().UnixNano())
	}
	o.bytes.Add(int64(len(p)))
	o.writes.Add(1)
	return len(p), nil
}

// openBench opens a member session through the public API, answers the
// hello with ample credits, and applies the first frame: messages transcript
// nodes in main and the usual dock.
func openBench(b *testing.B, messages int) (fe.Session, *benchOut) {
	b.Helper()
	out := &benchOut{}
	env := fe.Env{
		Out:      out,
		Columns:  120,
		Getenv:   func(key string) string { return map[string]string{"TERM_PROGRAM": "tern"}[key] },
		Version:  "bench",
		Fallback: func(reason string) { b.Errorf("fallback: %s", reason) },
		Edit:     func(fe.Edit) {}, Undo: func() {}, Send: func(string) {}, Act: func(fe.Action) {},
	}
	s, err := Frontend().Open(env)
	if err != nil || s == nil {
		b.Fatalf("open: %v %v", s, err)
	}
	time.Sleep(20 * time.Millisecond)
	s.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1,\"credits\":1000000000,\"cols\":120,\"cell\":{\"w\":8,\"h\":16}}\x1b\\")
	s.InputReady()
	time.Sleep(50 * time.Millisecond)
	var ops []fe.Op
	para := strings.Repeat("Some **markdown** text with `code` and words. ", 10)
	for i := range messages {
		id := fmt.Sprintf("m%d", i)
		var node fe.Node
		switch i % 3 {
		case 0:
			node = fe.MarkdownText{Role: fe.RoleUser, Text: "Question " + id}
		case 1:
			node = fe.ToolCard{Name: "bash", Arguments: map[string]any{"command": "ls " + id}, Header: "$ ls", Status: fe.ToolDone, Output: "a\nb\nc\n"}
		default:
			node = fe.MarkdownText{Role: fe.RoleAssistant, Text: para}
		}
		ops = append(ops, fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: id, Index: i, Node: node})
	}
	ops = append(ops,
		fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "stream", Index: messages, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: "Start", Streaming: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock", Index: 0, Node: fe.Lines{Lines: []string{""}}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "working", Index: 1, Node: fe.Working{Kind: fe.WorkingAgent, Message: "Working"}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 2, Node: fe.Editor{Sendable: true}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "dock.below", Index: 3, Node: fe.Lines{}},
		fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "footer", Index: 4, Node: benchFooter(0)},
	)
	if err := s.Apply(fe.Frame{Ops: ops}); err != nil {
		b.Fatal(err)
	}
	if out.writes.Load() < 3 {
		b.Fatalf("the session did not connect: %d writes", out.writes.Load())
	}
	b.Cleanup(func() { _ = s.Close() })
	return s, out
}

func benchFooter(output int) fe.Footer {
	return fe.Footer{Cwd: "~/src", GitBranch: "main", Model: "faux", ThinkingLevel: "high",
		UsageTotals:  fe.UsageTotals{Input: 1200, Output: output},
		ContextUsage: fe.ContextUsage{Percent: 20, ContextWindow: 200000}}
}

// report adds what reached the terminal per op.
func report(b *testing.B, out *benchOut, bytes0, writes0 int64) {
	b.ReportMetric(float64(out.bytes.Load()-bytes0)/float64(b.N), "wireB/op")
	b.ReportMetric(float64(out.writes.Load()-writes0)/float64(b.N), "writes/op")
}

// streamText is a long assistant reply; token i shows its first 5+4i bytes.
var streamText = "Start" + strings.Repeat(" tok", 1000)

// One streaming token: the streaming assistant node grows by one word.
func benchmarkStreamingToken(b *testing.B, messages int) {
	s, out := openBench(b, messages)
	bytes0, writes0 := out.bytes.Load(), out.writes.Load()
	n := 5
	b.ReportAllocs()
	for b.Loop() {
		n += 4
		if n > len(streamText) {
			n = 5
		}
		if err := s.Apply(fe.Frame{Ops: []fe.Op{{Kind: fe.Update, Region: fe.RegionMain, ID: "stream", Index: messages, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: streamText[:n], Streaming: true}}}}); err != nil {
			b.Fatal(err)
		}
	}
	report(b, out, bytes0, writes0)
}

func BenchmarkStreamingToken100(b *testing.B)   { benchmarkStreamingToken(b, 100) }
func BenchmarkStreamingToken2000(b *testing.B)  { benchmarkStreamingToken(b, 2000) }
func BenchmarkStreamingToken10000(b *testing.B) { benchmarkStreamingToken(b, 10000) }

// A keystroke in the editor: a dock-only change.
func BenchmarkEditorKeystroke(b *testing.B) {
	s, out := openBench(b, 2000)
	bytes0, writes0 := out.bytes.Load(), out.writes.Load()
	n := 0
	b.ReportAllocs()
	for b.Loop() {
		n++
		if n > 200 {
			n = 0
		}
		text := streamText[:n]
		if err := s.Apply(fe.Frame{Ops: []fe.Op{{Kind: fe.Update, Region: fe.RegionDock, ID: "editor", Index: 2, Node: fe.Editor{Text: text, Cursor: n, Sendable: true}}}}); err != nil {
			b.Fatal(err)
		}
	}
	report(b, out, bytes0, writes0)
}

// The footer's usage changes and the spinner's message changes, as at the end
// of a streamed response.
func BenchmarkFooterAndSpinnerUpdate(b *testing.B) {
	s, out := openBench(b, 2000)
	bytes0, writes0 := out.bytes.Load(), out.writes.Load()
	labels := []string{"Working", "Reading files"}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		if err := s.Apply(fe.Frame{Ops: []fe.Op{
			{Kind: fe.Update, Region: fe.RegionDock, ID: "working", Index: 1, Node: fe.Working{Kind: fe.WorkingAgent, Message: labels[i%2]}},
			{Kind: fe.Update, Region: fe.RegionDock, ID: "footer", Index: 4, Node: benchFooter(i % 50000)},
		}}); err != nil {
			b.Fatal(err)
		}
	}
	report(b, out, bytes0, writes0)
}

// openPicker opens the model picker over 2000 items in place of the editor.
func openPicker(b *testing.B) (fe.Session, *benchOut, fe.Selector) {
	s, out := openBench(b, 100)
	all := fe.Selector{Title: "Select model", Searchable: true}
	for i := range 2000 {
		all.Items = append(all.Items, fe.SelectorItem{ID: fmt.Sprintf("provider/model-%d", i), Label: fmt.Sprintf("model-%d", i), Detail: "provider"})
	}
	all.Selected = all.Items[0].ID
	if err := s.Apply(fe.Frame{Ops: []fe.Op{
		{Kind: fe.Remove, Region: fe.RegionDock, ID: "editor", Index: 2},
		{Kind: fe.Insert, Region: fe.RegionDock, ID: "selector.1", Index: 2, Node: all},
	}}); err != nil {
		b.Fatal(err)
	}
	return s, out, all
}

// Filtering the model picker over 2000 items: alternate the full list and a
// narrowed one, as typing and erasing one character does.
func BenchmarkModelPickerFilter2000(b *testing.B) {
	s, out, all := openPicker(b)
	narrowed := all
	narrowed.Query, narrowed.Items = "1", nil
	for _, item := range all.Items {
		if item.Label[len(item.Label)-1] == '1' {
			narrowed.Items = append(narrowed.Items, item)
		}
	}
	narrowed.Selected = narrowed.Items[0].ID
	bytes0, writes0 := out.bytes.Load(), out.writes.Load()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		next := all
		if i%2 == 0 {
			next = narrowed
		}
		if err := s.Apply(fe.Frame{Ops: []fe.Op{{Kind: fe.Update, Region: fe.RegionDock, ID: "selector.1", Index: 2, Node: next}}}); err != nil {
			b.Fatal(err)
		}
	}
	report(b, out, bytes0, writes0)
}

// Moving the selection in the model picker over 2000 items, as an arrow key
// does: PiG sends the same rows with another selected item.
func BenchmarkModelPickerMove2000(b *testing.B) {
	s, out, all := openPicker(b)
	bytes0, writes0 := out.bytes.Load(), out.writes.Load()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		next := all
		next.Selected = all.Items[i%len(all.Items)].ID
		if err := s.Apply(fe.Frame{Ops: []fe.Op{{Kind: fe.Update, Region: fe.RegionDock, ID: "selector.1", Index: 2, Node: next}}}); err != nil {
			b.Fatal(err)
		}
	}
	report(b, out, bytes0, writes0)
}

// The time from Apply to the frame's Write when a credit is free: the write
// happens inside Apply, on the caller's goroutine.
func BenchmarkApplyToWrite2000(b *testing.B) {
	s, out := openBench(b, 2000)
	out.stamp.Store(true)
	var total time.Duration
	n := 5
	for b.Loop() {
		n += 4
		if n > len(streamText) {
			n = 5
		}
		start := time.Now()
		writes := out.writes.Load()
		if err := s.Apply(fe.Frame{Ops: []fe.Op{{Kind: fe.Update, Region: fe.RegionMain, ID: "stream", Index: 2000, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: streamText[:n], Streaming: true}}}}); err != nil {
			b.Fatal(err)
		}
		if out.writes.Load() <= writes {
			b.Fatal("Apply returned before its frame was written")
		}
		total += time.Duration(out.last.Load() - start.UnixNano())
	}
	b.ReportMetric(float64(total.Nanoseconds())/float64(b.N), "applyToWrite-ns")
}

// The heap a 10000-message session keeps per message, and what streaming
// tokens add to it once collected: the growth from the 500th to the 1000th
// token of one reply.
func BenchmarkHeapAfterTokens10000(b *testing.B) {
	token := func(s fe.Session, i int) {
		if err := s.Apply(fe.Frame{Ops: []fe.Op{{Kind: fe.Update, Region: fe.RegionMain, ID: "stream", Index: 10000, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: streamText[:5+4*i], Streaming: true}}}}); err != nil {
			b.Fatal(err)
		}
	}
	for b.Loop() {
		var before, opened, half, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		s, _ := openBench(b, 10000)
		runtime.GC()
		runtime.ReadMemStats(&opened)
		for i := range 500 {
			token(s, i+1)
		}
		runtime.GC()
		runtime.ReadMemStats(&half)
		for i := range 500 {
			token(s, i+501)
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		b.ReportMetric(float64(opened.HeapInuse-before.HeapInuse)/10000, "heapB/message")
		b.ReportMetric(float64(int64(after.HeapInuse)-int64(half.HeapInuse))/500, "heapB/token")
		runtime.KeepAlive(s)
	}
}

// frameOut is the terminal for the hidden stream: it counts the frames and
// the bytes the member writes.
type frameOut struct {
	bytes, writes, frames atomic.Int64
}

func (o *frameOut) Write(p []byte) (int, error) {
	o.bytes.Add(int64(len(p)))
	o.writes.Add(1)
	if bytes.HasPrefix(p, []byte("\x1b_tsp;f;")) {
		o.frames.Add(1)
	}
	return len(p), nil
}

// hiddenTokens is how many tokens the hidden stream streams: a 32 KB reply.
const hiddenTokens = 8000

// A reply streamed while the pane is hidden, as an agent in a background tab
// does: Tern answers with the default two credits and acknowledges what it
// drew, then the tab is hidden and no ack arrives until it shows again. The
// metrics are per stream: what reached the terminal while hidden, what the
// member allocated, and the frames that went out when the pane showed.
func BenchmarkHiddenStream8000(b *testing.B) {
	text := "Start" + strings.Repeat(" tok", hiddenTokens)
	var hiddenWrites, hiddenBytes, shownFrames, allocated int64
	for b.Loop() {
		out := &frameOut{}
		env := fe.Env{
			Out: out, Columns: 120, Version: "bench",
			Getenv:   func(key string) string { return map[string]string{"TERM_PROGRAM": "tern"}[key] },
			Fallback: func(reason string) { b.Errorf("fallback: %s", reason) },
			Edit:     func(fe.Edit) {}, Undo: func() {}, Send: func(string) {}, Act: func(fe.Action) {},
		}
		s, err := Frontend().Open(env)
		if err != nil || s == nil {
			b.Fatalf("open: %v %v", s, err)
		}
		time.Sleep(10 * time.Millisecond)
		s.HandleInput("\x1b_tsp;r;{\"r\":\"hello\",\"v\":1,\"credits\":2,\"cols\":120,\"cell\":{\"w\":8,\"h\":16}}\x1b\\")
		s.InputReady()
		time.Sleep(20 * time.Millisecond)
		ops := []fe.Op{
			{Kind: fe.Insert, Region: fe.RegionDock, ID: "editor", Index: 0, Node: fe.Editor{Sendable: true}},
			{Kind: fe.Insert, Region: fe.RegionMain, ID: "stream", Index: 0, Node: fe.MarkdownText{Role: fe.RoleAssistant, Text: text[:5], Streaming: true}},
		}
		if err := s.Apply(fe.Frame{Ops: ops}); err != nil {
			b.Fatal(err)
		}
		s.HandleInput(fmt.Sprintf("\x1b_tsp;e;{\"ev\":\"ack\",\"sf\":\"pig\",\"s\":%d}\x1b\\", out.frames.Load()))
		time.Sleep(20 * time.Millisecond)
		s.HandleInput("\x1b_tsp;e;{\"ev\":\"visible\",\"sf\":\"pig\",\"visible\":false}\x1b\\")
		writes0, bytes0 := out.writes.Load(), out.bytes.Load()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for i := 1; i <= hiddenTokens; i++ {
			node := fe.MarkdownText{Role: fe.RoleAssistant, Text: text[:5+4*i], Streaming: true}
			if err := s.Apply(fe.Frame{Ops: []fe.Op{{Kind: fe.Update, Region: fe.RegionMain, ID: "stream", Index: 0, Node: node}}}); err != nil {
				b.Fatal(err)
			}
		}
		runtime.ReadMemStats(&after)
		hiddenWrites += out.writes.Load() - writes0
		hiddenBytes += out.bytes.Load() - bytes0
		allocated += int64(after.TotalAlloc - before.TotalAlloc)
		frames := out.frames.Load()
		s.HandleInput("\x1b_tsp;e;{\"ev\":\"visible\",\"sf\":\"pig\",\"visible\":true}\x1b\\")
		shownFrames += out.frames.Load() - frames
		_ = s.Close()
	}
	n := float64(b.N)
	b.ReportMetric(float64(hiddenWrites)/n, "hiddenWrites/stream")
	b.ReportMetric(float64(hiddenBytes)/n, "hiddenWireB/stream")
	b.ReportMetric(float64(allocated)/n, "allocB/stream")
	b.ReportMetric(float64(shownFrames)/n, "framesOnShow/stream")
}
