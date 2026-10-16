package pig_music_test

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// cells is a terminal row as a terminal draws it: each cell's character and its attributes (SGR state and hyperlink) after
// every escape before it. Two rows with the same cells look the same, whatever their bytes.
type cell struct {
	text  string // the grapheme; "" for the right half of a wide one
	style style
}

type style struct {
	bold, faint, italic, underline, blink, reverse, conceal, strike bool
	fg, bg, link                                                    string
}

// cellsOf parses one row: SGR and OSC 8 escapes change the style, every other escape is an error.
func cellsOf(row string) ([]cell, error) {
	var out []cell
	var st style
	for i := 0; i < len(row); {
		if row[i] == 0x1b {
			n, err := escape(row[i:], &st)
			if err != nil {
				return nil, fmt.Errorf("at byte %d: %w", i, err)
			}
			i += n
			continue
		}
		g, w := ansi.FirstGraphemeCluster(row[i:], ansi.GraphemeWidth)
		out = append(out, cell{text: g, style: st})
		for range w - 1 {
			out = append(out, cell{style: st})
		}
		i += len(g)
	}
	return out, nil
}

func escape(s string, st *style) (int, error) {
	switch {
	case strings.HasPrefix(s, "\x1b["):
		end := strings.IndexByte(s, 'm')
		if end < 0 || strings.ContainsFunc(s[2:end], func(r rune) bool { return !(r >= '0' && r <= '9' || r == ';' || r == ':') }) {
			return 0, fmt.Errorf("an escape that is not SGR: %q", s[:min(len(s), 12)])
		}
		sgr(s[2:end], st)
		return end + 1, nil
	case strings.HasPrefix(s, "\x1b]8;"):
		end := strings.IndexAny(s, "\x07")
		term := 1
		if stEnd := strings.Index(s, "\x1b\\"); stEnd >= 0 && (end < 0 || stEnd < end) {
			end, term = stEnd, 2
		}
		if end < 0 {
			return 0, fmt.Errorf("an unterminated OSC 8")
		}
		parts := strings.SplitN(s[4:end], ";", 2)
		st.link = ""
		if len(parts) == 2 {
			st.link = parts[1]
		}
		return end + term, nil
	}
	return 0, fmt.Errorf("an escape that is not SGR or OSC 8: %q", s[:min(len(s), 12)])
}

func sgr(params string, st *style) {
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		sub := strings.Split(fields[i], ":")
		code, err := strconv.Atoi(sub[0])
		if sub[0] == "" {
			code, err = 0, nil
		}
		if err != nil {
			continue
		}
		color := func() string { // 38/48: 5;n or 2;r;g;b, either form
			if len(sub) > 1 {
				return strings.Join(sub[1:], ":")
			}
			if i+1 < len(fields) && fields[i+1] == "5" && i+2 < len(fields) {
				c := "5:" + fields[i+2]
				i += 2
				return c
			}
			if i+1 < len(fields) && fields[i+1] == "2" && i+4 < len(fields) {
				c := "2:" + strings.Join(fields[i+2:i+5], ":")
				i += 4
				return c
			}
			return "?"
		}
		switch {
		case code == 0:
			link := st.link
			*st = style{link: link}
		case code == 1:
			st.bold = true
		case code == 2:
			st.faint = true
		case code == 3:
			st.italic = true
		case code == 4:
			st.underline = len(sub) < 2 || sub[1] != "0"
		case code == 5 || code == 6:
			st.blink = true
		case code == 7:
			st.reverse = true
		case code == 8:
			st.conceal = true
		case code == 9:
			st.strike = true
		case code == 21:
			st.underline = true
		case code == 22:
			st.bold, st.faint = false, false
		case code == 23:
			st.italic = false
		case code == 24:
			st.underline = false
		case code == 25:
			st.blink = false
		case code == 27:
			st.reverse = false
		case code == 28:
			st.conceal = false
		case code == 29:
			st.strike = false
		case code >= 30 && code <= 37, code >= 90 && code <= 97:
			st.fg = strconv.Itoa(code)
		case code == 38:
			st.fg = color()
		case code == 39:
			st.fg = ""
		case code >= 40 && code <= 47, code >= 100 && code <= 107:
			st.bg = strconv.Itoa(code)
		case code == 48:
			st.bg = color()
		case code == 49:
			st.bg = ""
		}
	}
}

// sameCells reports whether two rows draw the same cells, and where the first difference is.
func sameCells(a, b string) (bool, string) {
	ca, err := cellsOf(a)
	if err != nil {
		return false, "first row: " + err.Error()
	}
	cb, err := cellsOf(b)
	if err != nil {
		return false, "second row: " + err.Error()
	}
	if len(ca) != len(cb) {
		return false, fmt.Sprintf("%d cells against %d", len(ca), len(cb))
	}
	for i := range ca {
		if ca[i] != cb[i] {
			return false, fmt.Sprintf("cell %d: %+v against %+v", i, ca[i], cb[i])
		}
	}
	return true, ""
}
