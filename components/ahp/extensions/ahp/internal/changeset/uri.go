// Package changeset is the git changeset service of pi-ahp (src/pi/changeset-uri.ts,
// git-changes.ts, changeset-service.ts): it advertises two static changesets per session, the
// latest commit and the uncommitted work, computes them with the git command line, keeps them
// fresh while a client watches, and serves their immutable git content as git-blob: resources.
package changeset

import (
	"strings"
	"unicode/utf8"

	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

const changesetScheme = "ahp-changeset:"

// Kind says which static changeset a channel is.
type Kind string

// The two changesets this host advertises.
const (
	LatestCommit Kind = "latest-commit"
	Uncommitted  Kind = "uncommitted"
)

// Kinds lists every changeset kind in catalogue order.
var Kinds = []Kind{LatestCommit, Uncommitted}

// ParsedURI is a recognised changeset channel.
type ParsedURI struct {
	SessionID string
	Kind      Kind
}

// URI is the channel of a session's changeset, `ahp-changeset:/<encoded session id>/<kind>`.
func URI(sessionID string, kind Kind) string {
	return changesetScheme + "/" + encodeURIComponent(sessionID) + "/" + string(kind)
}

// ParseURI recognises only the channels URI builds: anything else (an unknown kind, an extra
// segment, an encoded slash, a query) is not a channel this host advertised.
func ParseURI(uri string) (ParsedURI, bool) {
	rest, ok := strings.CutPrefix(uri, changesetScheme+"/")
	if !ok {
		return ParsedURI{}, false
	}
	encoded, kindText, ok := strings.Cut(rest, "/")
	if !ok || encoded == "" {
		return ParsedURI{}, false
	}
	kind := Kind(kindText)
	if kind != LatestCommit && kind != Uncommitted {
		return ParsedURI{}, false
	}
	id, ok := decodeURIComponent(encoded)
	if !ok || id == "" || strings.Contains(id, "/") {
		return ParsedURI{}, false
	}
	if URI(id, kind) != uri {
		return ParsedURI{}, false
	}
	return ParsedURI{SessionID: id, Kind: kind}, true
}

// Catalogue is the session's changeset list: Latest Commit only once the repository has a commit.
func Catalogue(sessionID string, hasCommit bool) []ahptypes.Changeset {
	var out []ahptypes.Changeset
	if hasCommit {
		description := "Changes introduced by the current HEAD commit"
		out = append(out, ahptypes.Changeset{
			Label: "Latest Commit", Description: &description, ChangeKind: "branch",
			UriTemplate: URI(sessionID, LatestCommit),
		})
	}
	description := "Current staged, unstaged, and untracked changes"
	return append(out, ahptypes.Changeset{
		Label: "Uncommitted Changes", Description: &description, ChangeKind: "uncommitted",
		UriTemplate: URI(sessionID, Uncommitted),
	})
}

const hexDigits = "0123456789ABCDEF"

// encodeURIComponent is JavaScript's function of that name.
func encodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("-_.!~*'()", c) >= 0:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}

// decodeURIComponent is JavaScript's function of that name: a malformed escape, or escapes that
// do not decode to valid UTF-8, fail.
func decodeURIComponent(s string) (string, bool) {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b = append(b, s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", false
		}
		hi, lo := unhex(s[i+1]), unhex(s[i+2])
		if hi < 0 || lo < 0 {
			return "", false
		}
		b = append(b, byte(hi<<4|lo))
		i += 2
	}
	if !utf8.Valid(b) {
		return "", false
	}
	return string(b), true
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
