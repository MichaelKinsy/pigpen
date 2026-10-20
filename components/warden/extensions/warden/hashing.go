package warden

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"unicode/utf8"
)

// Content hashing for the capability index (src/hashing.ts). The hash is the first 16 hex characters of a SHA-256,
// so an index written by the original reads the same here.

// fileContentHash is the hash of a file's content read as UTF-8, or "missing" when it cannot be read.
func fileContentHash(path string) string {
	raw, err := readWholeFile(path)
	if err != nil {
		return "missing"
	}
	sum := sha256.Sum256([]byte(nodeUTF8(raw)))
	return hex.EncodeToString(sum[:])[:16]
}

// readWholeFile reads a file with the four system calls of Node's readFileSync (open, fstat, one read of the stated
// size, close); os.ReadFile adds a read that finds the end.
func readWholeFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	if !st.Mode().IsRegular() || size <= 0 {
		return io.ReadAll(f)
	}
	buf := make([]byte, size)
	n := 0
	for n < len(buf) {
		m, err := f.Read(buf[n:])
		n += m
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
	}
	return buf[:n], nil
}

// toolSourceHash is the sourceHash of a tool: it has no file, so the name and the description are hashed.
func toolSourceHash(name, description string) string {
	sum := sha256.Sum256([]byte(name + ":" + description))
	return hex.EncodeToString(sum[:])[:16]
}

// nodeUTF8 is Buffer.toString("utf-8"): each maximal invalid subsequence becomes one U+FFFD (the WHATWG decoder),
// so the text hashed or parsed is the text Node would see. A byte-order mark is kept.
func nodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	out := make([]byte, 0, len(b)+8)
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			out = append(out, c)
			i++
			continue
		}
		var need int
		lo, hi := byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c >= 0xE0 && c <= 0xEF:
			need = 2
			if c == 0xE0 {
				lo = 0xA0
			} else if c == 0xED {
				hi = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need = 3
			if c == 0xF0 {
				lo = 0x90
			} else if c == 0xF4 {
				hi = 0x8F
			}
		default:
			out = append(out, "\uFFFD"...)
			i++
			continue
		}
		j, ok := i+1, true
		for k := 0; k < need; k++ {
			if j >= len(b) || b[j] < lo || b[j] > hi {
				ok = false
				break
			}
			lo, hi = 0x80, 0xBF
			j++
		}
		if ok {
			out = append(out, b[i:j]...)
		} else {
			out = append(out, "\uFFFD"...)
		}
		i = j
	}
	return string(out)
}
