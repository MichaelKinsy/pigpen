package pi_permission_system

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"unicode/utf8"
)

// File access the way the original's node:fs calls behave: UTF-8 decoding with U+FFFD for malformed bytes, Node's error wording,
// and `String(statSync(path).mtimeMs)` stamps.

// decodeUTF8Lossy is TextDecoder's UTF-8 decoder (the WHATWG algorithm Node's readFileSync(path, "utf-8") uses): each maximal
// malformed subpart becomes one U+FFFD. A byte-order mark stays in the text.
func decodeUTF8Lossy(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	var needed, seen int
	var cp rune
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(b); {
		c := b[i]
		if needed == 0 {
			switch {
			case c <= 0x7F:
				out.WriteByte(c)
			case c >= 0xC2 && c <= 0xDF:
				needed, cp = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				}
				if c == 0xED {
					upper = 0x9F
				}
				needed, cp = 2, rune(c&0xF)
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				}
				if c == 0xF4 {
					upper = 0x8F
				}
				needed, cp = 3, rune(c&0x7)
			default:
				out.WriteRune(utf8.RuneError)
			}
			i++
			continue
		}
		if c < lower || c > upper {
			cp, needed, seen = 0, 0, 0
			lower, upper = 0x80, 0xBF
			out.WriteRune(utf8.RuneError)
			continue // reprocess the byte
		}
		lower, upper = 0x80, 0xBF
		cp = cp<<6 | rune(c&0x3F)
		seen++
		i++
		if seen == needed {
			out.WriteRune(cp)
			cp, needed, seen = 0, 0, 0
		}
	}
	if needed != 0 {
		out.WriteRune(utf8.RuneError)
	}
	return out.String()
}

var nodeErrnos = []struct {
	errno syscall.Errno
	code  string
	desc  string
}{
	{syscall.ENOENT, "ENOENT", "no such file or directory"},
	{syscall.EACCES, "EACCES", "permission denied"},
	{syscall.EPERM, "EPERM", "operation not permitted"},
	{syscall.EISDIR, "EISDIR", "illegal operation on a directory"},
	{syscall.ENOTDIR, "ENOTDIR", "not a directory"},
	{syscall.ELOOP, "ELOOP", "too many symbolic links encountered"},
	{syscall.ENAMETOOLONG, "ENAMETOOLONG", "name too long"},
	{syscall.EMFILE, "EMFILE", "too many open files"},
	{syscall.EEXIST, "EEXIST", "file already exists"},
	{syscall.ENOSPC, "ENOSPC", "no space left on device"},
	{syscall.EROFS, "EROFS", "read-only file system"},
}

// nodeFsMessage is the `message` of the error a failed fs call throws: "CODE: description, syscall 'path'" (a read of a directory
// carries no path).
func nodeFsMessage(err error, syscallName, path string) string {
	for _, e := range nodeErrnos {
		if errors.Is(err, e.errno) {
			m := e.code + ": " + e.desc + ", " + syscallName
			if path != "" {
				m += " '" + path + "'"
			}
			return m
		}
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// readTextFile is readFileSync(path, "utf-8"); the error is the one Node would throw, as its message.
func readTextFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", errors.New(nodeFsMessage(err, "open", path))
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.IsDir() {
		return "", errors.New(nodeFsMessage(syscall.EISDIR, "read", ""))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New(nodeFsMessage(err, "read", ""))
	}
	return decodeUTF8Lossy(b), nil
}

// pathExists is existsSync: false for anything stat cannot reach.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// fileStamp is `String(statSync(path).mtimeMs)`, "missing" when stat fails.
func fileStamp(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	t := st.ModTime()
	return jsNumber(float64(t.Unix())*1e3 + float64(t.Nanosecond())/1e6)
}
