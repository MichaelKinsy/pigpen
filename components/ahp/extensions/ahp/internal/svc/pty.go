package svc

import (
	"errors"
	"unicode/utf8"
)

// PtyOptions describe the pseudoterminal to open.
type PtyOptions struct {
	Name       string // $TERM
	Cols, Rows int
	Cwd        string
	Env        []string // extra environment entries; nil inherits the host process environment
}

// PtyHandlers receive the process's output and exit. OnData gets whole UTF-8 text (a multi-byte
// character is never split across two calls); OnExit is called once, after the last OnData.
type PtyHandlers struct {
	OnData func(data string)
	OnExit func(exitCode int)
}

// PtyProcess is a running child on a pseudoterminal.
type PtyProcess interface {
	Write(data string) error
	Resize(cols, rows int) error
	Kill() error
}

// PtySpawner starts file on a new pseudoterminal.
type PtySpawner func(file string, args []string, opts PtyOptions, h PtyHandlers) (PtyProcess, error)

// ErrPtyUnsupported is returned by the default spawner on platforms without pseudoterminals.
var ErrPtyUnsupported = errors.New("pseudoterminals are not supported on this platform")

// utf8Decoder turns a byte stream into strings without splitting a character: an incomplete
// trailing sequence is held back until the next chunk (or flushed at the end).
type utf8Decoder struct{ held []byte }

func (d *utf8Decoder) Write(chunk []byte) string {
	buf := append(d.held, chunk...)
	d.held = nil
	// hold back an incomplete trailing sequence (at most 3 bytes)
	for back := 1; back <= 3 && back <= len(buf); back++ {
		b := buf[len(buf)-back]
		if b < 0x80 {
			break
		}
		if utf8.RuneStart(b) {
			if !utf8.FullRune(buf[len(buf)-back:]) {
				d.held = append([]byte(nil), buf[len(buf)-back:]...)
				buf = buf[:len(buf)-back]
			}
			break
		}
	}
	return string(buf)
}

func (d *utf8Decoder) Flush() string {
	out := string(d.held)
	d.held = nil
	return out
}
