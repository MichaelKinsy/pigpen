package shell

import (
	"strings"
	"unicode/utf8"
)

// upstream: packages/utils/src/sanitize-text.ts (Bun.stripANSI + C0/C1 strip),
// packages/tui/src/utils.ts (replaceTabs)

// streamSanitizer is an incremental VT/ANSI + control-byte + UTF-8 sanitizer.
// It matches whole-input `sanitizeText` across arbitrary chunk boundaries
// regardless of escape-sequence length (e.g. long OSC 8 hyperlinks, OSC 52
// payloads, multi-kilobyte Sixel/Kitty DCS/APC graphics):
//   - Inside CSI / OSC / DCS / PM / APC / SOS / charset sequences, payload
//     bytes are discarded on the fly in O(1) memory without buffering.
//   - VT string recovery (U2):
//   - `ESC \` (7-bit ST), `0x9c` (raw 8-bit ST), and `0xC2 0x9C` (UTF-8 U+009C
//     ST) terminate OSC/DCS/PM/APC/SOS; BEL (`0x07`) also terminates OSC.
//   - `ESC` followed by anything other than `\` inside a string sequence ends
//     the string and re-enters the escape state (matching VT100/xterm/Bun.stripANSI).
//   - CAN (`0x18`) and SUB (`0x1a`) immediately abort any in-progress CSI,
//     ESC, charset, or string sequence and return to ground.
//   - Raw 8-bit C1 introducers (`0x9b` CSI, `0x9d` OSC, `0x90` DCS, `0x98` SOS,
//     `0x9e` PM, `0x9f` APC) and their UTF-8 encodings (`0xC2 0x90..0x9F`)
//     enter the corresponding CSI/OSC/string states so their parameters are
//     stripped rather than leaked as text (matching `Bun.stripANSI`).
//   - Unterminated string sequences: matching `Bun.stripANSI` (verified at
//     omp v19.1.1 `packages/utils/src/sanitize-text.ts:37`), legitimate
//     multi-line Sixel DCS and OSC payloads span newlines until ST/BEL/CAN/SUB/ESC;
//     an unterminated string sequence without any terminator is discarded to
//     EOF (same for streamed and whole input).
//   - Incomplete trailing multi-byte UTF-8 sequences (1–3 bytes) are held in
//     `utf8Buf` until the completing bytes arrive (or dropped on `Flush`).
//   - Valid UTF-8 with no ESC/control bytes takes a zero-allocation fast path.
type streamSanitizer struct {
	state   sanitizeState
	utf8Buf [4]byte
	utf8Len int
}

type sanitizeState uint8

const (
	sanGround   sanitizeState = iota
	sanEsc                    // saw ESC (0x1b)
	sanCSIParam               // CSI <params 0x30-0x3F>*
	sanCSIInter               // CSI ... <intermediates 0x20-0x2F>*
	sanOSC                    // OSC ... (BEL | ST | CAN | SUB | ESC<non-\>)
	sanOSCEsc                 // saw ESC inside OSC
	sanOSCC2                  // saw 0xC2 inside OSC (checking for UTF-8 U+009C ST)
	sanStr                    // DCS/PM/APC/SOS ... (ST | CAN | SUB | ESC<non-\>)
	sanStrEsc                 // saw ESC inside DCS/PM/APC/SOS
	sanStrC2                  // saw 0xC2 inside DCS/PM/APC/SOS (checking for UTF-8 U+009C ST)
	sanCharset                // ESC ( / ) / * / + / - / . / / (waiting for 1 designator byte)
)

// sanitizeText strips ANSI/VT escape sequences (7-bit ESC and 8-bit/UTF-8 C1
// CSI, OSC, DCS/PM/APC/SOS, 2-byte ESC, 3-byte charset), removes C0/C1 control
// characters (keeping \t and \n), and drops invalid UTF-8 bytes, matching omp's
// sanitizeText (`packages/utils/src/sanitize-text.ts:25-40`).
func sanitizeText(text string) string {
	if text == "" {
		return ""
	}
	var st streamSanitizer
	out := st.Feed(text)
	if tail := st.Flush(); tail != "" {
		return out + tail
	}
	return out
}

// Feed sanitizes one chunk of raw output, carrying parser state and any
// incomplete trailing UTF-8 bytes (1-3 bytes) into the next Feed call.
func (s *streamSanitizer) Feed(chunk string) string {
	if chunk == "" {
		return ""
	}
	// Fast path: in ground state with no pending UTF-8 bytes and a clean chunk,
	// return chunk verbatim without allocating.
	if s.state == sanGround && s.utf8Len == 0 && isCleanSanitizedChunk(chunk) {
		return chunk
	}

	var out strings.Builder
	out.Grow(len(chunk) + s.utf8Len)

	for i := 0; i < len(chunk); i++ {
		s.stepByte(chunk[i], &out)
	}
	return out.String()
}

// Flush finishes the stream on EOF/Dump, discarding any incomplete trailing
// escape sequence or incomplete UTF-8 lead/continuation bytes.
func (s *streamSanitizer) Flush() string {
	s.state = sanGround
	s.utf8Len = 0
	return ""
}

func (s *streamSanitizer) flushBadUTF8() {
	s.utf8Len = 0
}

// enterC1Control transitions into the appropriate VT state for a C1 control
// codepoint (0x80..0x9F), returning true if it entered a multi-byte sequence
// state (CSI/OSC/DCS/SOS/PM/APC); other C1 codes are simply dropped.
func (s *streamSanitizer) enterC1Control(c1 byte) bool {
	switch c1 {
	case 0x9B: // CSI
		s.state = sanCSIParam
		return true
	case 0x9D: // OSC
		s.state = sanOSC
		return true
	case 0x90, 0x98, 0x9E, 0x9F: // DCS, SOS, PM, APC
		s.state = sanStr
		return true
	default:
		return false
	}
}

func (s *streamSanitizer) stepByte(b byte, out *strings.Builder) {
	switch s.state {
	case sanGround:
		if b == 0x1b {
			s.flushBadUTF8()
			s.state = sanEsc
			return
		}
		if b < 0x80 {
			s.flushBadUTF8()
			if !isControlByte(b) {
				out.WriteByte(b)
			}
			return
		}
		s.feedHighByte(b, out)

	case sanEsc:
		if b == 0x18 || b == 0x1a {
			// CAN / SUB abort escape sequence.
			s.state = sanGround
			return
		}
		switch b {
		case '[':
			s.state = sanCSIParam
		case ']':
			s.state = sanOSC
		case 'P', '^', '_', 'X':
			s.state = sanStr
		case '(', ')', '*', '+', '-', '.', '/':
			s.state = sanCharset
		default:
			if b >= 0x20 && b <= 0x7E {
				// 2-byte ESC sequence (e.g. ESC c, ESC 7, ESC =, ESC >): consumed.
				s.state = sanGround
			} else if b == 0x1b {
				// Back-to-back ESC: stay in sanEsc for the new ESC.
				s.state = sanEsc
			} else {
				// Unrecognized ESC + control/high byte: drop ESC and reprocess b.
				s.state = sanGround
				s.stepByte(b, out)
			}
		}

	case sanCSIParam:
		if b == 0x18 || b == 0x1a {
			// CAN / SUB abort CSI sequence.
			s.state = sanGround
			return
		}
		if b == 0x1b {
			// New ESC aborts CSI and enters sanEsc.
			s.state = sanEsc
			return
		}
		switch {
		case b >= 0x30 && b <= 0x3F:
			// parameter byte; stay in sanCSIParam
		case b >= 0x20 && b <= 0x2F:
			s.state = sanCSIInter
		case b >= 0x40 && b <= 0x7E:
			// final byte terminates CSI
			s.state = sanGround
		default:
			// Aborted/malformed CSI: leave CSI and reprocess b in ground state.
			s.state = sanGround
			s.stepByte(b, out)
		}

	case sanCSIInter:
		if b == 0x18 || b == 0x1a {
			s.state = sanGround
			return
		}
		if b == 0x1b {
			s.state = sanEsc
			return
		}
		switch {
		case b >= 0x20 && b <= 0x2F:
			// intermediate byte; stay in sanCSIInter
		case b >= 0x40 && b <= 0x7E:
			// final byte terminates CSI
			s.state = sanGround
		default:
			s.state = sanGround
			s.stepByte(b, out)
		}

	case sanOSC:
		switch b {
		case 0x07, 0x9c, 0x18, 0x1a:
			// BEL, raw 8-bit ST (0x9c), CAN, or SUB terminates/aborts OSC.
			s.state = sanGround
		case 0x1b:
			s.state = sanOSCEsc
		case 0xC2:
			// Might be UTF-8 U+009C (0xC2 0x9C) ST.
			s.state = sanOSCC2
		}

	case sanOSCEsc:
		if b == '\\' {
			// 7-bit ST (ESC \) terminates OSC.
			s.state = sanGround
			return
		}
		// Any ESC not followed by '\' ends the OSC string and re-enters sanEsc
		// for the new escape sequence (matching VT100/xterm/Bun.stripANSI).
		s.state = sanEsc
		s.stepByte(b, out)

	case sanOSCC2:
		if b == 0x9C {
			// UTF-8 U+009C ST terminates OSC.
			s.state = sanGround
			return
		}
		// Not U+009C: reprocess b in sanOSC (in case b is BEL/ESC/CAN/SUB/0x9c/0xC2).
		s.state = sanOSC
		s.stepByte(b, out)

	case sanStr:
		switch b {
		case 0x9c, 0x18, 0x1a:
			// Raw 8-bit ST (0x9c), CAN, or SUB terminates/aborts DCS/PM/APC/SOS.
			s.state = sanGround
		case 0x1b:
			s.state = sanStrEsc
		case 0xC2:
			// Might be UTF-8 U+009C (0xC2 0x9C) ST.
			s.state = sanStrC2
		}

	case sanStrEsc:
		if b == '\\' {
			// 7-bit ST (ESC \) terminates DCS/PM/APC/SOS.
			s.state = sanGround
			return
		}
		// Any ESC not followed by '\' ends the string and re-enters sanEsc.
		s.state = sanEsc
		s.stepByte(b, out)

	case sanStrC2:
		if b == 0x9C {
			// UTF-8 U+009C ST terminates DCS/PM/APC/SOS.
			s.state = sanGround
			return
		}
		s.state = sanStr
		s.stepByte(b, out)

	case sanCharset:
		if b == 0x18 || b == 0x1a {
			s.state = sanGround
			return
		}
		if b == 0x1b {
			s.state = sanEsc
			return
		}
		// 3-byte charset designation `ESC ( X`: consume one byte and return to ground.
		s.state = sanGround
	}
}

func (s *streamSanitizer) feedHighByte(b byte, out *strings.Builder) {
	if s.utf8Len == 0 {
		// Raw 8-bit C1 control introducers (0x80..0x9F): enter CSI/OSC/DCS/etc.
		// states so raw `0x9b 31m red` strips its parameters just like UTF-8 U+009B.
		if b >= 0x80 && b <= 0x9F {
			s.enterC1Control(b)
			return
		}
		if utf8LeadLength(b) == 0 {
			// Invalid UTF-8 lead (0xA0-0xC1, 0xF5-0xFF): drop.
			return
		}
		s.utf8Buf[0] = b
		s.utf8Len = 1
		return
	}

	// Currently accumulating a multi-byte UTF-8 rune.
	if b&0xC0 != 0x80 {
		// Previous sequence was truncated; drop it and handle b fresh.
		s.utf8Len = 0
		if utf8LeadLength(b) > 0 {
			s.utf8Buf[0] = b
			s.utf8Len = 1
		}
		return
	}

	s.utf8Buf[s.utf8Len] = b
	s.utf8Len++
	expected := utf8LeadLength(s.utf8Buf[0])
	if s.utf8Len < expected {
		// Validate prefix early so overlong / surrogate / >U+10FFFF bytes are
		// dropped promptly and subsequent valid lead bytes aren't eaten.
		if !isValidUTF8Prefix(s.utf8Buf[:s.utf8Len]) {
			s.utf8Len = 0
		}
		return
	}

	// We have `expected` bytes; decode and verify valid UTF-8 and handle C1.
	r, size := utf8.DecodeRune(s.utf8Buf[:s.utf8Len])
	n := s.utf8Len
	s.utf8Len = 0
	if r == utf8.RuneError && size == 1 {
		return
	}
	if r >= 0x80 && r <= 0x9F {
		// UTF-8 C1 control character (U+0080..U+009F): enter CSI/OSC/DCS/etc.
		// if it's an introducer, otherwise drop.
		s.enterC1Control(byte(r))
		return
	}
	out.Write(s.utf8Buf[:n])
}

func isValidUTF8Prefix(p []byte) bool {
	if len(p) < 2 {
		return true
	}
	b0, b1 := p[0], p[1]
	switch {
	case b0 == 0xE0:
		return b1 >= 0xA0 && b1 <= 0xBF
	case b0 == 0xED:
		return b1 >= 0x80 && b1 <= 0x9F // reject UTF-16 surrogates U+D800..U+DFFF
	case b0 == 0xF0:
		return b1 >= 0x90 && b1 <= 0xBF
	case b0 == 0xF4:
		return b1 >= 0x80 && b1 <= 0x8F // reject > U+10FFFF
	default:
		return b1 >= 0x80 && b1 <= 0xBF
	}
}

func isCleanSanitizedChunk(text string) bool {
	for i := 0; i < len(text); {
		b := text[i]
		if b < 0x80 {
			if isControlByte(b) {
				return false
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			return false
		}
		if r >= 0x80 && r <= 0x9F {
			return false
		}
		i += size
	}
	return true
}

func isControlByte(b byte) bool {
	if b == '\t' || b == '\n' {
		return false
	}
	return b < 0x20 || b == 0x7f
}

func utf8LeadLength(b byte) int {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	default:
		return 0
	}
}

// replaceTabs expands tabs to 3 spaces, matching omp's replaceTabs for service logs.
// upstream: packages/coding-agent/src/launch/services.ts:291
func replaceTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	return strings.ReplaceAll(s, "\t", "   ")
}
