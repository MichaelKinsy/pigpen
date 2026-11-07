package mockls

import (
	"bytes"
	"errors"
	"mime"
	"strings"
	"unicode/utf8"

	"lstools/internal/reqlog"
)

// parseMultipart splits a multipart body on its boundary, keeping each part's raw header block and bytes exactly.
// It accepts what the SDK sends (CRLF framing, "--boundary" delimiters) and reports anything else as an error, so
// a port that frames its body differently is noticed here and not papered over.
func parseMultipart(body []byte, boundary string) ([]reqlog.Part, error) {
	delim := []byte("--" + boundary)
	if !bytes.HasPrefix(body, delim) {
		return nil, errors.New("body does not start with the boundary delimiter")
	}
	var parts []reqlog.Part
	rest := body[len(delim):]
	for {
		if bytes.HasPrefix(rest, []byte("--")) {
			// Close delimiter; only CRLF (or nothing) may follow.
			tail := rest[2:]
			if len(tail) != 0 && string(tail) != "\r\n" {
				return parts, errors.New("bytes after the close delimiter")
			}
			return parts, nil
		}
		if !bytes.HasPrefix(rest, []byte("\r\n")) {
			return parts, errors.New("delimiter not followed by CRLF")
		}
		rest = rest[2:]
		hdrEnd := bytes.Index(rest, []byte("\r\n\r\n"))
		if hdrEnd < 0 {
			return parts, errors.New("part without a header terminator")
		}
		header := string(rest[:hdrEnd])
		rest = rest[hdrEnd+4:]
		end := bytes.Index(rest, append([]byte("\r\n"), delim...))
		if end < 0 {
			return parts, errors.New("part without a closing delimiter")
		}
		content := rest[:end]
		rest = rest[end+2+len(delim):]
		p := reqlog.Part{Header: header, Bytes: bytes.Clone(content)}
		for _, line := range strings.Split(header, "\r\n") {
			name, val, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			val = strings.TrimSpace(val)
			switch strings.ToLower(name) {
			case "content-type":
				p.ContentType = val
			case "content-disposition":
				if _, params, err := mime.ParseMediaType(val); err == nil {
					p.Name = params["name"]
					p.Filename = params["filename"]
				}
			}
		}
		if utf8.Valid(content) {
			p.Text = string(content)
		}
		parts = append(parts, p)
	}
}
