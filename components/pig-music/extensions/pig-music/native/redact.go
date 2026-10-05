// Package native is the pure-Go playback engine: WaxTap resolves YouTube
// streams, WaxFlow demuxes and decodes them, oto plays them, and a detached
// `pigmusic serve` process owns the queue so that playback survives the
// extension restarting. It needs no external program on a desktop.
package native

import (
	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"os"
	"strings"
	"unicode"
)

var (
	urlRe = lazyre.New(`https?://[^\s"'<>)\]]+`)
	// IPv4 with an optional port. A version such as 2026.08.19 has three parts and does not match.
	ip4Re = lazyre.New(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`)
	// Bracketed IPv6 (as net errors print it), a bare compressed one (with ::), and bare groups of three or more hex fields.
	ip6Re = lazyre.New(`\[[0-9a-fA-F:.]+\](?::\d+)?|\b(?:[0-9a-fA-F]{1,4}:){1,7}(?::[0-9a-fA-F]{1,4}){1,7}\b|\b(?:[0-9a-fA-F]{1,4}:){3,7}[0-9a-fA-F]{1,4}\b`)
)

// Redact removes what must never reach a log or a message: URLs (signed stream
// URLs carry the listener's address and a signature) and IP addresses.
// Control characters are removed too (see Clean): network text must not reach a terminal as an escape sequence.
func Redact(s string) string {
	s = urlRe.ReplaceAllString(s, "<url>")
	s = ip6Re.ReplaceAllString(s, "<ip>")
	return Clean(ip4Re.ReplaceAllString(s, "<ip>"))
}

// Clean makes text from the network safe to draw: tabs and line breaks become a
// space, every other control character U+FFFD, and the bidirectional overrides
// (which reorder what follows them) are dropped.
func Clean(s string) string {
	clean := true
	for _, r := range s {
		if unicode.IsControl(r) || isBidi(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteByte(' ')
		case unicode.IsControl(r):
			b.WriteRune('\uFFFD')
		case isBidi(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isBidi(r rune) bool { return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) }

// WaxTap writes the raw responses and stream URLs it handles to a directory when
// these variables name one. The player must never do that, whatever the
// environment of whoever started it says.
var waxtapDumpVars = []string{"WAXTAP_DUMP_DIR", "WAXTAP_SABR_DUMP_DIR"}

// SanitizeEnvironment unsets WaxTap's debug dump variables in this process.
func SanitizeEnvironment() {
	for _, k := range waxtapDumpVars {
		_ = os.Unsetenv(k)
	}
}

// CleanEnv returns env without those variables, for a process that is started.
func CleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		for _, k := range waxtapDumpVars {
			if strings.HasPrefix(kv, k+"=") {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}

type redacted struct {
	msg string
	err error
}

func (r *redacted) Error() string { return r.msg }
func (r *redacted) Unwrap() error { return r.err }

// RedactError returns err with a redacted message and the same chain for errors.Is and As.
func RedactError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if clean := Redact(msg); clean != msg {
		return &redacted{msg: clean, err: err}
	}
	return err
}
