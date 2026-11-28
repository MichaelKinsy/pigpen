// Package ompsession reads omp's session files as PiG sessions. omp and PiG both descend from Pi's v3 session
// tree (JSON lines, each entry an id and a parentId), so a session translates entry by entry:
//
//   - omp's first line, a `title` record, is dropped; the `session` header keeps Pi's members.
//   - `model_usage` and `ttsr_injection` entries have no PiG counterpart: they are dropped and their children
//     take their parent.
//   - `title_change` becomes `session_info`; `model_change` splits omp's "provider/id" into PiG's two members;
//     a `message` with omp's `custom` or `developer` role becomes a `custom_message`.
//   - a snapcompact `compaction` keeps its archived history in `preserveData`, which omp appends to the summary
//     when it builds the context (as text and as images of text). PiG sends the summary alone, so the archive's
//     source text is written into the summary.
//
// Every other entry is copied byte for byte.
package ompsession

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Options selects what Translate writes.
type Options struct {
	// Full writes every entry. Without it the file starts at the first entry the latest compaction kept, which
	// is all the model is sent and keeps a session of hundreds of megabytes small enough for PiG to hand to an
	// extension in one frame.
	Full bool
}

// Result describes a translated session.
type Result struct {
	SessionID string
	Title     string
	Entries   int // entries written, the header not counted
	Converted int // entries written in another form
	Dropped   int // records with no PiG counterpart
	Bytes     int64
}

// IsOmpSession reports whether the file at path starts with omp's title record, which no Pi or PiG session has.
func IsOmpSession(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	line, _, err := (&lineReader{r: bufio.NewReaderSize(f, 4096)}).next()
	if err != nil && len(line) == 0 {
		return false
	}
	p, ok := probe(line)
	return ok && p.Type == "title"
}

// Translate writes the omp session at src to dst as a PiG session.
func Translate(src, dst string, opts Options) (Result, error) {
	in, err := os.Open(src)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = in.Close() }()
	p, err := scan(in, opts)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", src, err)
	}
	if _, err := in.Seek(p.start, io.SeekStart); err != nil {
		return Result{}, err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return Result{}, err
	}
	w := &countingWriter{w: bufio.NewWriterSize(out, 1<<20)}
	err = p.emit(in, w)
	if flushErr := w.w.Flush(); err == nil {
		err = flushErr
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Result{}, err
	}
	p.res.Bytes = w.n
	return p.res, nil
}

// entryProbe is the part of a record that decides how it is translated. Decoding into it reads past the rest
// of the record without keeping it.
type entryProbe struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	FirstKept string  `json:"firstKeptEntryId"`
	Model     *string `json:"model"`
	ModelID   *string `json:"modelId"`
	Method    string  `json:"method"`
	Message   *struct {
		Role string `json:"role"`
	} `json:"message"`
}

// probe decodes the deciding part of a record. A member of an unexpected type leaves its field empty and the
// record usable; a line that is not JSON, such as the last line of a session omp is writing, is not.
func probe(line []byte) (e entryProbe, ok bool) {
	err := json.Unmarshal(line, &e)
	if _, mismatch := errors.AsType[*json.UnmarshalTypeError](err); mismatch {
		err = nil
	}
	return e, err == nil
}

// dropped reports a record PiG has no counterpart for.
func (p *entryProbe) dropped() bool {
	switch p.Type {
	case "title", "session", "model_usage", "ttsr_injection":
		return true
	}
	return false
}

// converted reports an entry that is written in another form.
func (p *entryProbe) converted() bool {
	switch p.Type {
	case "title_change":
		return true
	case "model_change":
		return p.Model != nil && p.ModelID == nil
	case "message":
		return p.Message != nil && (p.Message.Role == "custom" || p.Message.Role == "developer")
	case "compaction":
		return p.Method == "snapcompact"
	}
	return false
}

// plan is what the first pass over a session finds: its header, and where the second pass starts.
type plan struct {
	res     Result
	header  []byte
	start   int64    // offset of the first record the second pass reads
	tail    bool     // start is past the first entry
	carried [][]byte // the latest title, model and thinking level set before start, as omp wrote them
}

// carriedTypes are the entries that set state a session opened at a later entry still needs, in the order they
// are written.
var carriedTypes = [...]string{"title_change", "model_change", "thinking_level_change"}

func scan(in io.Reader, opts Options) (*plan, error) {
	p := &plan{}
	type located struct {
		off int64
		raw []byte
	}
	var (
		offsets    map[string]int64
		latest     [len(carriedTypes)]located
		keptFrom   string
		compactAt  int64
		compaction bool
	)
	if !opts.Full {
		offsets = map[string]int64{}
	}
	lines := &lineReader{r: bufio.NewReaderSize(in, 1<<20)}
	for {
		line, off, err := lines.next()
		if len(line) > 0 {
			if e, ok := probe(line); ok {
				switch e.Type {
				case "title":
					var t struct {
						Title string `json:"title"`
					}
					if json.Unmarshal(line, &t) == nil && t.Title != "" {
						p.res.Title = t.Title
					}
				case "session":
					if p.header == nil {
						header, headerErr := translateHeader(line, &p.res)
						if headerErr != nil {
							return nil, headerErr
						}
						p.header = header
					}
				default:
					if offsets != nil && e.ID != "" {
						offsets[e.ID] = off
					}
					for i, t := range carriedTypes {
						if e.Type == t {
							latest[i] = located{off, bytes.Clone(line)}
						}
					}
					if e.Type == "compaction" {
						compaction, compactAt, keptFrom = true, off, e.FirstKept
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
	}
	if p.header == nil {
		return nil, errors.New("not an omp session: no session header")
	}
	if !opts.Full && compaction {
		p.start = compactAt
		if off, ok := offsets[keptFrom]; ok {
			p.start = off
		}
		p.tail = p.start > 0
		for _, l := range latest {
			if l.raw != nil && l.off < p.start {
				p.carried = append(p.carried, l.raw)
			}
		}
	}
	return p, nil
}

// translateHeader is omp's session header with Pi's members only.
func translateHeader(line []byte, res *Result) ([]byte, error) {
	var h struct {
		ID            string `json:"id"`
		Timestamp     string `json:"timestamp"`
		Cwd           string `json:"cwd"`
		ParentSession string `json:"parentSession"`
		Title         string `json:"title"`
	}
	if err := json.Unmarshal(line, &h); err != nil {
		return nil, fmt.Errorf("session header: %w", err)
	}
	res.SessionID = h.ID
	if res.Title == "" {
		res.Title = h.Title
	}
	return json.Marshal(struct {
		Type          string `json:"type"`
		Version       int    `json:"version"`
		ID            string `json:"id"`
		Timestamp     string `json:"timestamp"`
		Cwd           string `json:"cwd"`
		ParentSession string `json:"parentSession,omitempty"`
	}{"session", 3, h.ID, h.Timestamp, h.Cwd, h.ParentSession})
}

// emit writes the session from p.start on. in is positioned there.
func (p *plan) emit(in io.Reader, w io.Writer) error {
	if _, err := w.Write(append(p.header, '\n')); err != nil {
		return err
	}
	enc := newEntryEncoder(w)
	// prev is the entry the first written entry hangs from: the last carried one, or none.
	var prev *string
	for _, raw := range p.carried {
		e, ok := probe(raw)
		if !ok {
			continue
		}
		if err := enc.write(raw, &e, prev, &p.res); err != nil {
			return err
		}
		id := e.ID
		prev = &id
	}

	// remap names, for a dropped entry, the entry its children hang from instead.
	remap := map[string]*string{}
	resolve := func(id *string) *string {
		for id != nil {
			next, ok := remap[*id]
			if !ok {
				break
			}
			id = next
		}
		return id
	}
	// anchor is the parent of the first record read: an entry before p.start, which a session opened at
	// p.start does not hold. Entries that hang from it hang from prev.
	var anchor *string
	first := true
	lines := &lineReader{r: bufio.NewReaderSize(in, 1<<20)}
	for {
		line, _, err := lines.next()
		if len(line) > 0 {
			e, ok := probe(line)
			if !ok {
				p.res.Dropped++
			} else {
				if first {
					first, anchor = false, e.ParentID
				}
				parent := resolve(e.ParentID)
				if p.tail && sameID(parent, anchor) {
					parent = prev
				}
				// keptDropped: the compaction names a dropped entry as the first one it kept.
				_, keptDropped := remap[e.FirstKept]
				switch {
				case e.dropped():
					if e.ID != "" {
						remap[e.ID] = parent
					}
					p.res.Dropped++
				case !e.converted() && sameID(parent, e.ParentID) && !keptDropped:
					if _, err := w.Write(append(line, '\n')); err != nil {
						return err
					}
					p.res.Entries++
				default:
					if keptDropped {
						if kept := resolve(&e.FirstKept); kept != nil {
							e.FirstKept = *kept
						}
					}
					if err := enc.write(line, &e, parent, &p.res); err != nil {
						return err
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func sameID(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// entryEncoder writes entries that differ from omp's record.
type entryEncoder struct {
	buf bytes.Buffer
	enc *json.Encoder
	w   io.Writer
}

func newEntryEncoder(w io.Writer) *entryEncoder {
	e := &entryEncoder{w: w}
	e.enc = json.NewEncoder(&e.buf)
	e.enc.SetEscapeHTML(false)
	return e
}

// write writes the record raw, probed as e, in PiG's form under parent.
func (enc *entryEncoder) write(raw []byte, e *entryProbe, parent *string, res *Result) error {
	// Numbers keep their digits: a float64 would rewrite an integer beyond 2^53.
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return err
	}
	var parentValue any
	if parent != nil {
		parentValue = *parent
	}
	base := func(typ string) map[string]any {
		return map[string]any{"type": typ, "id": obj["id"], "parentId": parentValue, "timestamp": obj["timestamp"]}
	}
	out := obj
	out["parentId"] = parentValue
	if e.converted() {
		res.Converted++
	}
	switch {
	case e.Type == "title_change":
		out = base("session_info")
		out["name"] = obj["title"]
		if title, _ := obj["title"].(string); title != "" {
			res.Title = title
		}
	case e.Type == "model_change" && e.converted():
		out = base("model_change")
		out["provider"], out["modelId"] = splitModel(*e.Model)
	case e.Type == "message" && e.converted():
		message, _ := obj["message"].(map[string]any)
		out = base("custom_message")
		customType, _ := message["customType"].(string)
		if customType == "" {
			customType = "omp:" + e.Message.Role
		}
		display, _ := message["display"].(bool)
		out["customType"], out["display"] = customType, display
		out["content"] = message["content"]
		if out["content"] == nil {
			out["content"] = ""
		}
	case e.Type == "compaction":
		out["firstKeptEntryId"] = e.FirstKept
		if e.converted() {
			summary, _ := obj["summary"].(string)
			out["summary"] = summary + archiveText(obj["preserveData"])
			delete(out, "preserveData")
		}
	}
	enc.buf.Reset()
	if err := enc.enc.Encode(out); err != nil {
		return err
	}
	if _, err := enc.w.Write(enc.buf.Bytes()); err != nil {
		return err
	}
	res.Entries++
	return nil
}

// plainText is omp's toPlainText: an archive holds its text as omp draws it, a full block (U+2588) for each
// line end and shift-out and shift-in around reasoning, which a frame shows dimmed.
var plainText = strings.NewReplacer("\x0e", "", "\x0f", "", "\u2588", "\n")

// archiveText is the source text of a compaction's snapcompact archive: the history omp appends to the summary.
// omp also cuts data URLs out of it (elideDataUrls); they are kept here.
// upstream: packages/snapcompact/src/snapcompact.ts archiveSourceText
func archiveText(preserveData any) string {
	data, _ := preserveData.(map[string]any)
	archive, _ := data["snapcompact"].(map[string]any)
	text, ok := archive["text"].(string)
	if !ok {
		var parts []string
		for _, key := range [...]string{"textHead", "textTail"} {
			if part, _ := archive[key].(string); part != "" {
				parts = append(parts, part)
			}
		}
		text = strings.Join(parts, "\u2588")
	}
	return plainText.Replace(text)
}

// splitModel is omp's "provider/id" model name as PiG's provider and model id.
func splitModel(model string) (provider, id string) {
	provider, id, found := strings.Cut(model, "/")
	if !found {
		return "", model
	}
	return provider, id
}

// lineReader reads the lines of a file with their offsets, in one buffer that grows to the longest line.
type lineReader struct {
	r   *bufio.Reader
	buf []byte
	off int64
}

// next returns the next line without its line end, and the offset it starts at. The line is valid until the
// next call. At the end of the file err is io.EOF, with the last line when the file does not end in a line end.
func (l *lineReader) next() (line []byte, off int64, err error) {
	off = l.off
	l.buf = l.buf[:0]
	for {
		part, err := l.r.ReadSlice('\n')
		l.buf = append(l.buf, part...)
		l.off += int64(len(part))
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(l.buf, "\r\n"), off, err
	}
}

type countingWriter struct {
	w *bufio.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
