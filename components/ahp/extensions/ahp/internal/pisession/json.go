package pisession

import (
	"bufio"
	"encoding/json"
	"strings"
)

// ParseLine decodes one session-file line; blank and malformed lines report false.
func ParseLine(line string) (Entry, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, false
	}
	var e Entry
	if err := json.Unmarshal([]byte(line), &e); err != nil || e == nil {
		return nil, false
	}
	return e, true
}

func writeLine(w *bufio.Writer, e Entry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	return w.WriteByte('\n')
}
