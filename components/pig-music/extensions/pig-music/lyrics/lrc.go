// Package lyrics finds a track's lyrics (time-synced ones from LRCLIB, YouTube Music's own as a fallback), parses them and
// keeps them on disk by video ID. It reads no account and sends no cookie.
package lyrics

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/lazyre"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// MaxLines bounds what one lyrics sheet may hold, whatever a server sends.
const MaxLines = 2000

// MaxWords bounds the timed words a whole sheet keeps: a line repeated by many stamps shares the budget, and the lines past it
// are swept by character instead.
const MaxWords = 20000

// maxOffsetTag bounds an [offset:] tag; anything further out is not a real correction.
const maxOffsetTag = time.Hour

var (
	stampRe = lazyre.New(`^\[(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?\]`)
	wordRe  = lazyre.New(`<(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?>`)
	tagRe   = lazyre.New(`^\[([A-Za-z]+):([^\]]*)\]\s*$`)
)

func stamp(min, sec, frac string) time.Duration {
	m, _ := strconv.Atoi(min)
	s, _ := strconv.Atoi(sec)
	d := time.Duration(m)*time.Minute + time.Duration(s)*time.Second
	if frac != "" {
		f, _ := strconv.Atoi(frac)
		switch len(frac) {
		case 1:
			d += time.Duration(f) * 100 * time.Millisecond
		case 2:
			d += time.Duration(f) * 10 * time.Millisecond
		default:
			d += time.Duration(f) * time.Millisecond
		}
	}
	return d
}

// ParseLRC reads LRC text: "[mm:ss.xx]line", several stamps on one line, the [offset:ms] tag, and the enhanced form with
// "<mm:ss.xx>word" timings inside a line. Text without a single stamp is not synced and gives no lines.
func ParseLRC(text string) music.Lyrics {
	var lines []music.LyricLine
	offset := time.Duration(0)
	wordsLeft := MaxWords
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.TrimRight(raw, "\r ")
		if m := tagRe.FindStringSubmatch(raw); m != nil && !stampRe.MatchString(raw) {
			if strings.EqualFold(m[1], "offset") {
				if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(m[2], "+"))); err == nil {
					n = min(max(n, -int(maxOffsetTag/time.Millisecond)), int(maxOffsetTag/time.Millisecond))
					offset = time.Duration(n) * time.Millisecond
				}
			}
			continue
		}
		var stamps []time.Duration
		rest := raw
		for {
			m := stampRe.FindStringSubmatch(rest)
			if m == nil {
				break
			}
			stamps = append(stamps, stamp(m[1], m[2], m[3]))
			rest = rest[len(m[0]):]
		}
		if len(stamps) == 0 {
			continue
		}
		body, words := splitWords(strings.TrimSpace(rest))
		for _, at := range stamps {
			if len(lines) >= 4*MaxLines {
				break
			}
			ln := music.LyricLine{At: at, Text: body}
			if len(words) > 0 && len(words) <= wordsLeft {
				ln.Words = append([]music.LyricWord(nil), words...)
				wordsLeft -= len(words)
			}
			lines = append(lines, ln)
		}
		if len(lines) >= 4*MaxLines {
			break
		}
	}
	if len(lines) == 0 {
		return music.Lyrics{}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].At < lines[j].At })
	if len(lines) > MaxLines {
		lines = lines[:MaxLines]
	}
	for i := range lines {
		lines[i].At = max(lines[i].At-offset, 0)
		for w := range lines[i].Words {
			lines[i].Words[w].At = max(lines[i].Words[w].At-offset, 0)
		}
	}
	for i := 0; i+1 < len(lines); i++ {
		lines[i].End = lines[i+1].At
	}
	return music.Lyrics{Synced: true, Lines: lines}
}

// splitWords turns "<t>Hello <t>big" into the plain text and the timed words (each word keeps its trailing space).
func splitWords(s string) (string, []music.LyricWord) {
	locs := wordRe.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return s, nil
	}
	var words []music.LyricWord
	var text strings.Builder
	text.WriteString(s[:locs[0][0]])
	for i, loc := range locs {
		end := len(s)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		w := s[loc[1]:end]
		at := stamp(s[loc[2]:loc[3]], s[loc[4]:loc[5]], fragment(s, loc))
		text.WriteString(w)
		if strings.TrimSpace(w) != "" {
			words = append(words, music.LyricWord{At: at, Text: w})
		}
	}
	return strings.TrimSpace(text.String()), words
}

func fragment(s string, loc []int) string {
	if loc[6] < 0 {
		return ""
	}
	return s[loc[6]:loc[7]]
}

// ParsePlain reads unsynced lyrics: one line per line of text, blank lines kept as breaks.
func ParsePlain(text string) music.Lyrics {
	var lines []music.LyricLine
	for _, raw := range strings.Split(strings.TrimRight(text, "\r\n "), "\n") {
		lines = append(lines, music.LyricLine{Text: strings.TrimSpace(raw)})
		if len(lines) >= MaxLines {
			break
		}
	}
	return music.Lyrics{Lines: lines}
}
