package posthorse

// Port of src/session-lines.ts: LF-delimited session records, scanned once. Irrelevant lines keep
// only their prefix, byte range and needle overlap, not their payload.

import (
	"context"
	"errors"
	"io"
	"iter"
	"os"
	"unicode/utf8"
	"unsafe"
)

// lineMatcher is the TS LineMatcher (session-lines.ts:5-10). test receives a window of text that
// is only valid during the call (it may alias a reused buffer); it must not retain it.
//
// encoding "latin1": the window is the raw bytes of tail+chunk, one byte per character, and the
// tail is the last overlap bytes. encoding "utf8": the window is the decoded text (StringDecoder
// semantics: an incomplete trailing sequence is held back) and the tail is its last overlap UTF-16
// units. As in TS, `window.slice(-overlap)` with overlap 0 keeps the whole window.
type lineMatcher struct {
	test     func(text string) bool
	encoding string
	// overlap is enough characters to include every case-fold alternative of any needle.
	overlap int
}

// sessionLine is the TS SessionLine (session-lines.ts:11-15). head and the text text returns are
// valid until the iterator advances (they may alias the read buffer; clone what must outlive the
// step), and text only while the sessionLines iteration is running (the file handle closes with it).
type sessionLine struct {
	head    string
	matches bool
	text    func() (string, error)
}

// lineRange is the TS LineRange (session-lines.ts:16-22).
type lineRange struct {
	start, end int64
	head       string
	matches    bool
	bytes      []byte // set only when the whole line arrived in one chunk
	hasBytes   bool
}

// utf8Decoder is Node's StringDecoder("utf8"): it holds back an incomplete trailing sequence.
type utf8Decoder struct {
	buffered [4]byte
	nbuf     int // kBufferedBytes: bytes of an unfinished character held back
	missing  int // kMissingBytes: bytes the held lead byte still needs
}

// write appends the decoded text of p to dst, following node's string_decoder.cc (DecodeData) byte
// for byte: a trailing run of continuation bytes and its lead-shaped byte ((b&0xE0)==0xC0, 0xE0,
// 0xF0 patterns, valid or not) is held back until the next write or end; the held bytes and their
// completion are decoded apart from the rest.
func (d *utf8Decoder) write(dst, p []byte) []byte {
	if d.missing > 0 {
		for i := 0; i < len(p) && i < d.missing; i++ {
			if p[i]&0xC0 != 0x80 {
				// Not a continuation byte: stop the unfinished character here; this byte starts a new one.
				d.missing = 0
				d.nbuf += copy(d.buffered[d.nbuf:], p[:i])
				p = p[i:]
				break
			}
		}
		found := min(len(p), d.missing)
		d.nbuf += copy(d.buffered[d.nbuf:], p[:found])
		p = p[found:]
		d.missing -= found
		if d.missing == 0 {
			dst = appendJSFromBytes(dst, d.buffered[:d.nbuf])
			d.nbuf = 0
		}
	}
	if len(p) == 0 {
		return dst
	}
	if p[len(p)-1]&0x80 != 0 {
		// Ended on a non-ASCII byte: find where its character begins.
		for i := len(p) - 1; ; i-- {
			d.nbuf++
			if p[i]&0xC0 == 0x80 {
				if d.nbuf >= 4 || i == 0 {
					// Not the tail of any character the engine's decoder could finish: let it decode as is.
					d.nbuf = 0
					break
				}
				continue
			}
			switch {
			case p[i]&0xE0 == 0xC0:
				d.missing = 2
			case p[i]&0xF0 == 0xE0:
				d.missing = 3
			case p[i]&0xF8 == 0xF0:
				d.missing = 4
			default:
				d.nbuf = 0
			}
			if d.nbuf != 0 {
				if d.nbuf >= d.missing {
					d.missing, d.nbuf = 0, 0
				}
				d.missing -= d.nbuf
			}
			break
		}
	}
	if d.nbuf > 0 {
		copy(d.buffered[:], p[len(p)-d.nbuf:])
		p = p[:len(p)-d.nbuf]
	}
	return appendJSFromBytes(dst, p)
}

// end flushes the held bytes through the engine's decoder (an unfinished character is replaced).
func (d *utf8Decoder) end(dst []byte) []byte {
	dst = appendJSFromBytes(dst, d.buffered[:d.nbuf])
	d.nbuf, d.missing = 0, 0
	return dst
}

// unsafeString views b as a string without copying; the caller guarantees b is not written while
// the string is in use.
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// utf8Tail is `window.slice(-overlap)` over a valid UTF-8 window, written into dst.
func utf8Tail(dst, window []byte, overlap int) []byte {
	dst = dst[:0]
	if overlap <= 0 {
		return append(dst, window...)
	}
	units, i := 0, len(window)
	for i > 0 && units < overlap {
		r, size := utf8.DecodeLastRune(window[:i])
		n := 1
		if r >= 0x10000 {
			n = 2
		}
		if units+n > overlap {
			// The cut falls between the halves of an astral character: its low half stays.
			sur := surrogateBytes(0xDC00 + (r-0x10000)&0x3FF)
			dst = append(dst, sur[:]...)
			return append(dst, window[i:]...)
		}
		units += n
		i -= size
	}
	return append(dst, window[i:]...)
}

// lineRanges is `lineRanges` (session-lines.ts:23-60).
func lineRanges(ctx context.Context, f *os.File, name string, matcher *lineMatcher) iter.Seq2[lineRange, error] {
	return func(yield func(lineRange, error) bool) {
		var start, position int64
		var head, tail, window []byte
		var decoder utf8Decoder
		matches := matcher == nil
		for chunk, err := range lfChunksFile(ctx, f, name, 1024*1024, true) {
			if err != nil {
				yield(lineRange{}, err)
				return
			}
			bytes, end := chunk.bytes, chunk.end
			if len(head) < 512 {
				head = append(head, bytes[:min(len(bytes), 512-len(head))]...)
			}
			if !matches && matcher != nil {
				window = append(window[:0], tail...)
				if matcher.encoding == "latin1" {
					window = append(window, bytes...)
					matches = matcher.test(unsafeString(window))
					tail = tail[:0]
					if matcher.overlap <= 0 {
						tail = append(tail, window...)
					} else {
						tail = append(tail, window[max(0, len(window)-matcher.overlap):]...)
					}
				} else {
					window = decoder.write(window, bytes)
					if end {
						window = decoder.end(window)
					}
					matches = matcher.test(unsafeString(window))
					tail = utf8Tail(tail, window, matcher.overlap)
				}
			}
			position += int64(len(bytes))
			if end {
				line := lineRange{start: start, end: position, head: viewJSFromBytes(head), matches: matches}
				// The read buffer is reused and a matching line is handed over where it lies in it: the
				// text of a line is valid until the iterator advances; any line that is not whole in
				// the buffer is re-read by position on demand.
				if matches && position-start == int64(len(bytes)) {
					line.bytes, line.hasBytes = bytes, true
				}
				if !yield(line, nil) {
					return
				}
				position++
				start = position
				head = head[:0]
				tail = tail[:0]
				decoder = utf8Decoder{}
				matches = matcher == nil
			}
		}
	}
}

// viewJSFromBytes is jsFromBytes without a copy for well-formed UTF-8: the result aliases p and is
// valid while p is.
func viewJSFromBytes(p []byte) string {
	if utf8.Valid(p) {
		return unsafeString(p)
	}
	return jsFromBytes(p)
}

// rangeText is `rangeText` (session-lines.ts:61-85): the text of one record.
func rangeText(ctx context.Context, f *os.File, line lineRange) (string, error) {
	if err := throwIfAborted(ctx); err != nil {
		return "", err
	}
	if line.hasBytes {
		return viewJSFromBytes(line.bytes), nil
	}
	buffer := make([]byte, line.end-line.start)
	offset := 0
	for offset < len(buffer) {
		if err := throwIfAborted(ctx); err != nil {
			return "", err
		}
		// Positioned reads may be short; each continues the same selected record.
		n, err := f.ReadAt(buffer[offset:], line.start+int64(offset))
		if n == 0 {
			if err != nil && !errors.Is(err, io.EOF) {
				return "", nodeFsError(err, "read", "")
			}
			return "", errors.New("Session file changed while reading a history entry.")
		}
		offset += n
	}
	return jsFromBytes(buffer), nil
}

// sessionLines is `sessionLines` (session-lines.ts:86-107). Irrelevant lines retain only their
// prefix, byte range and needle overlap, not their payload. matcher nil is undefined. An error
// (open, read, abort) is yielded once as (zero, err).
func sessionLines(ctx context.Context, file string, matcher *lineMatcher) iter.Seq2[sessionLine, error] {
	return func(yield func(sessionLine, error) bool) {
		f, err := os.Open(file)
		if err != nil {
			yield(sessionLine{}, nodeFsError(err, "open", file))
			return
		}
		defer f.Close()
		// One closure serves every line: it reads the line the loop is on.
		var current lineRange
		text := func() (string, error) { return rangeText(ctx, f, current) }
		for line, err := range lineRanges(ctx, f, file, matcher) {
			if err != nil {
				yield(sessionLine{}, err)
				return
			}
			current = line
			if !yield(sessionLine{head: line.head, matches: line.matches, text: text}, nil) {
				return
			}
		}
	}
}
