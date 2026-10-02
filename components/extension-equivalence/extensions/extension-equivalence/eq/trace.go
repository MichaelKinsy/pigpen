package eq

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Channel names of a trace event.
const (
	ChResponse = "response" // an RPC command response
	ChUI       = "ui"       // an extension UI request from the extension
	ChHost     = "host"     // a captured host event (tool execution, agent lifecycle, ...)
	ChExec     = "exec"     // a child process the extension started
	ChExecEnd  = "exec_end" // that child process finished
	ChLLM      = "llm"      // a request that reached the model
	ChHTTP     = "http"     // a request a scenario's fake upstream server received
	ChExit     = "exit"     // the host process ended
	ChError    = "error"    // a harness-level failure (never equal to a success trace)
)

// Event is one observable effect. Order in a trace is the order the effects
// reached the harness, which follows causal order within a step.
type Event struct {
	Step string          `json:"step"`
	Ch   string          `json:"ch"`
	Data json.RawMessage `json:"data"`
}

// Header identifies how a trace was recorded. It is not part of the comparison.
type Header struct {
	Kind       string `json:"kind"` // always "header"
	Scenario   string `json:"scenario"`
	Lane       string `json:"lane"`
	Host       string `json:"host"`
	Extension  string `json:"extension"`
	Normalizer string `json:"normalizer"`
}

// Trace is a header plus the ordered events.
type Trace struct {
	Header Header
	Events []Event
}

// canonical re-encodes JSON with sorted keys and preserved numbers.
func canonical(v any) json.RawMessage {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(fmt.Sprintf("eq: encode: %v", err))
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func decodeAny(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// Write encodes the trace as JSON lines: the header, then one event per line.
func (t *Trace) Write(w io.Writer) error {
	t.Header.Kind = "header"
	if _, err := fmt.Fprintf(w, "%s\n", canonical(t.Header)); err != nil {
		return err
	}
	for _, e := range t.Events {
		if _, err := fmt.Fprintf(w, "%s\n", canonical(e)); err != nil {
			return err
		}
	}
	return nil
}

// WriteFile writes the trace to path.
func (t *Trace) WriteFile(path string) error {
	var buf bytes.Buffer
	if err := t.Write(&buf); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// ReadTrace decodes a trace file.
func ReadTrace(path string) (*Trace, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	t := &Trace{}
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if first {
			first = false
			if err := json.Unmarshal([]byte(line), &t.Header); err != nil || t.Header.Kind != "header" {
				return nil, fmt.Errorf("%s: first line must be a trace header", path)
			}
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		t.Events = append(t.Events, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if first {
		return nil, fmt.Errorf("%s: empty trace", path)
	}
	return t, nil
}

// Divergence is the first difference between two traces.
type Divergence struct {
	Index int    // event index, or the shorter length when one trace is a prefix
	Want  *Event // nil when the wanted trace ended
	Got   *Event // nil when the got trace ended
}

func (d Divergence) String() string {
	show := func(e *Event) string {
		if e == nil {
			return "<end of trace>"
		}
		return fmt.Sprintf("[%s] %s %s", e.Step, e.Ch, e.Data)
	}
	return fmt.Sprintf("first difference at event %d\n  want: %s\n  got:  %s", d.Index, show(d.Want), show(d.Got))
}

// Diff compares two traces event by event. It returns nil when they are
// identical. The header is ignored.
func Diff(want, got *Trace) *Divergence {
	n := max(len(want.Events), len(got.Events))
	for i := range n {
		var w, g *Event
		if i < len(want.Events) {
			w = &want.Events[i]
		}
		if i < len(got.Events) {
			g = &got.Events[i]
		}
		if w == nil || g == nil || w.Step != g.Step || w.Ch != g.Ch || !bytes.Equal(w.Data, g.Data) {
			return &Divergence{Index: i, Want: w, Got: g}
		}
	}
	return nil
}

// Failed reports whether the trace contains a harness-level failure.
func (t *Trace) Failed() (Event, bool) {
	for _, e := range t.Events {
		if e.Ch == ChError {
			return e, true
		}
	}
	return Event{}, false
}
