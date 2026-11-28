package browser

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	cdpTraceRingSize   = 256
	cdpTraceSnippetMax = 200
)

// cdpTraceEntry records one CDP wire message (outbound command, inbound reply,
// or inbound event) in a bounded per-tab ring buffer for post-mortem diagnosis.
type cdpTraceEntry struct {
	TS        time.Time
	Dir       string // "->", "<-", "ev"
	Method    string
	ID        int64
	SessionID string
	FrameID   string
	Detail    string
}

type cdpTraceRing struct {
	mu    sync.Mutex
	buf   [cdpTraceRingSize]cdpTraceEntry
	next  int
	count int
}

func (r *cdpTraceRing) add(e cdpTraceEntry) {
	if r == nil {
		return
	}
	if len(e.Detail) > cdpTraceSnippetMax {
		e.Detail = e.Detail[:cdpTraceSnippetMax] + "…"
	}
	r.mu.Lock()
	r.buf[r.next] = e
	r.next = (r.next + 1) % cdpTraceRingSize
	if r.count < cdpTraceRingSize {
		r.count++
	}
	r.mu.Unlock()
}

func (r *cdpTraceRing) snapshot() []cdpTraceEntry {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count == 0 {
		return nil
	}
	out := make([]cdpTraceEntry, r.count)
	start := 0
	if r.count == cdpTraceRingSize {
		start = r.next
	}
	for i := range r.count {
		out[i] = r.buf[(start+i)%cdpTraceRingSize]
	}
	return out
}

// FormatCDPTrace formats the last limit entries (0 = all) of the ring buffer
// with relative millisecond offsets from the first entry in the dump.
func (r *cdpTraceRing) Format(limit int) string {
	entries := r.snapshot()
	if len(entries) == 0 {
		return "(no CDP trace entries)"
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	base := entries[0].TS
	var b strings.Builder
	for _, e := range entries {
		dt := e.TS.Sub(base).Seconds() * 1000
		fmt.Fprintf(&b, "+%8.1fms %s ", dt, e.Dir)
		if e.ID != 0 {
			fmt.Fprintf(&b, "#%d ", e.ID)
		}
		if e.Method != "" {
			b.WriteString(e.Method)
		}
		if e.FrameID != "" {
			fmt.Fprintf(&b, " frame=%s", e.FrameID)
		}
		if e.SessionID != "" {
			sid := e.SessionID
			if len(sid) > 8 {
				sid = sid[:8]
			}
			fmt.Fprintf(&b, " sess=%s", sid)
		}
		if e.Detail != "" {
			fmt.Fprintf(&b, " %s", e.Detail)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// extractTraceFrameID pulls frameId (or frame.id / context.auxData.frameId)
// from a JSON CDP params blob without allocating full structs.
func extractTraceFrameID(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var p struct {
		FrameID string `json:"frameId"`
		Frame   *struct {
			ID string `json:"id"`
		} `json:"frame"`
		Context *struct {
			AuxData *struct {
				FrameID string `json:"frameId"`
			} `json:"auxData"`
		} `json:"context"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	if p.FrameID != "" {
		return p.FrameID
	}
	if p.Frame != nil && p.Frame.ID != "" {
		return p.Frame.ID
	}
	if p.Context != nil && p.Context.AuxData != nil {
		return p.Context.AuxData.FrameID
	}
	return ""
}

func traceSnippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "{}" || s == "null" {
		return ""
	}
	if len(s) > cdpTraceSnippetMax {
		return s[:cdpTraceSnippetMax] + "…"
	}
	return s
}
