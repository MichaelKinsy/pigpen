// Package wire holds the protocol-level helpers of the AHP host: channel URI classification,
// protocol errors, version negotiation, the client-dispatchable action table and file: URI
// conversion. It has no I/O. Ports pi-ahp src/core/channels.ts, uri.ts and src/protocol/*.
package wire

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ResourceWatchScheme prefixes every resource watch channel URI ("<scheme>/<id>").
const ResourceWatchScheme = "ahp-resource-watch:"

// RootChannel is the singleton root channel.
const RootChannel = "ahp-root://"

// ChannelKind classifies a channel URI by the reducer that serves it.
type ChannelKind string

const (
	KindRoot          ChannelKind = "root"
	KindSession       ChannelKind = "session"
	KindChat          ChannelKind = "chat"
	KindTerminal      ChannelKind = "terminal"
	KindChangeset     ChannelKind = "changeset"
	KindResourceWatch ChannelKind = "resourceWatch"
)

// JSON-RPC and AHP error codes (pinned spec: errors.generated.go).
const (
	CodeParseError                 = -32700
	CodeInvalidRequest             = -32600
	CodeMethodNotFound             = -32601
	CodeInvalidParams              = -32602
	CodeInternalError              = -32603
	CodeSessionNotFound            = -32001
	CodeProviderNotFound           = -32002
	CodeSessionAlreadyExists       = -32003
	CodeTurnInProgress             = -32004
	CodeUnsupportedProtocolVersion = -32005
	CodeAuthRequired               = -32007
	CodeNotFound                   = -32008
	CodePermissionDenied           = -32009
	CodeAlreadyExists              = -32010
	// CodeConflict is the code of the npm 0.9.0 package pi-ahp was written against (errors.ts,
	// AhpErrorCodes.Conflict); the pinned spec commit has no such code, see PORT.md.
	CodeConflict = -32011
)

// ProtocolVersion is the one AHP version this host speaks (pinned: 0.9.0).
const ProtocolVersion = "0.9.0"

// Error is a protocol error: handlers return it and the router turns it into a JSON-RPC error response.
type Error struct {
	Code    int
	Message string
	Data    any
}

func (e *Error) Error() string { return e.Message }

const (
	sessionScheme       = "ahp-session:"
	chatScheme          = "ahp-chat:"
	terminalScheme      = "ahp-terminal:"
	changesetScheme     = "ahp-changeset:"
	resourceWatchScheme = "ahp-resource-watch:"
)

// KindOf classifies a channel URI, false for a scheme this host does not serve. Deliberately
// strict: it drives subscribe, where guessing wrong would create a channel with the wrong reducer.
func KindOf(uri string) (ChannelKind, bool) {
	switch {
	case uri == RootChannel:
		return KindRoot, true
	case strings.HasPrefix(uri, sessionScheme+"/"):
		return KindSession, true
	case strings.HasPrefix(uri, chatScheme+"/"):
		return KindChat, true
	case strings.HasPrefix(uri, terminalScheme+"/"):
		return KindTerminal, true
	case strings.HasPrefix(uri, changesetScheme+"/"):
		return KindChangeset, true
	case strings.HasPrefix(uri, resourceWatchScheme+"/"):
		return KindResourceWatch, true
	}
	return "", false
}

// ActionBelongsToChannel reports whether an action type's namespace belongs to a channel kind.
func ActionBelongsToChannel(actionType string, kind ChannelKind) bool {
	return strings.HasPrefix(actionType, string(kind)+"/")
}

// IsChatChannel reports whether the URI is `ahp-chat:/…`.
func IsChatChannel(uri string) bool { return strings.HasPrefix(uri, chatScheme+"/") }

// SessionURI returns `ahp-session:/<id>`.
func SessionURI(id string) string { return sessionScheme + "/" + id }

// ChatURI returns `ahp-chat:/<id>`.
func ChatURI(id string) string { return chatScheme + "/" + id }

// ChatIDFromURI extracts `<id>` from `ahp-chat:/<id>`.
func ChatIDFromURI(uri string) (string, bool) {
	if !IsChatChannel(uri) {
		return "", false
	}
	id := uri[len(chatScheme)+1:]
	return id, id != ""
}

var singleSegment = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*):/([^/?#]+)$`)

// SessionIDFromURI identifies a session from its canonical scheme or from explicit provider
// aliases given as bare scheme names. Shape alone never makes an unknown URI a session.
func SessionIDFromURI(uri string, providerAliases ...string) (string, bool) {
	m := singleSegment.FindStringSubmatch(uri)
	if m == nil {
		return "", false
	}
	scheme := strings.ToLower(m[1])
	if scheme == "ahp-session" {
		return m[2], true
	}
	for _, alias := range providerAliases {
		if scheme == strings.ToLower(alias) {
			return m[2], true
		}
	}
	return "", false
}

func newError(code int, message string, data any) *Error {
	return &Error{Code: code, Message: message, Data: data}
}

// InvalidParams and the other constructors mirror pi-ahp's ProtocolError statics.
// Coded builds a protocol error with an explicit code.
func Coded(code int, message string) *Error { return newError(code, message, nil) }

func InvalidParams(message string) *Error { return newError(CodeInvalidParams, message, nil) }
func MethodNotFound(method string) *Error {
	return newError(CodeMethodNotFound, "Unknown method: "+method, nil)
}
func SessionNotFound(channel string) *Error {
	return newError(CodeSessionNotFound, "Session not found: "+channel, nil)
}
func SessionAlreadyExists(channel string) *Error {
	return newError(CodeSessionAlreadyExists, "Session already exists: "+channel, nil)
}
func ProviderNotFound(provider string) *Error {
	return newError(CodeProviderNotFound, "No agent for provider: "+provider, nil)
}
func NotFound(uri string) *Error { return newError(CodeNotFound, "Not found: "+uri, nil) }

// HostSupportedVersions is what this host can speak. Generated wire models do not down-convert
// required fields across minor versions, so it is the current version only.
func HostSupportedVersions() []string { return []string{ProtocolVersion} }

// NegotiateProtocolVersion picks the client's most-preferred version this host also speaks.
func NegotiateProtocolVersion(offered []string) (string, error) {
	if len(offered) == 0 {
		return "", InvalidParams("initialize requires a non-empty protocolVersions array of strings")
	}
	for _, candidate := range offered {
		for _, supported := range HostSupportedVersions() {
			if candidate == supported {
				return candidate, nil
			}
		}
	}
	return "", newError(CodeUnsupportedProtocolVersion,
		"No mutually supported protocol version. Offered: "+strings.Join(offered, ", "),
		map[string]any{"supportedVersions": HostSupportedVersions()})
}

// IsClientDispatchable reports whether a client may dispatch an action of this type.
func IsClientDispatchable(actionType string) bool { return clientDispatchable[actionType] }

// FileURIToPath converts a file: URI to a filesystem path. A value that is not a file: URI is
// returned unchanged: clients occasionally send a bare path where the protocol asks for a URI.
// A URI naming a remote host is an error (Node's fileURLToPath does the same).
func FileURIToPath(uri string) (string, error) {
	if !strings.HasPrefix(uri, "file://") {
		return uri, nil
	}
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("invalid file URL %q: %w", uri, err)
	}
	if u.Host != "" && u.Host != "localhost" {
		return "", fmt.Errorf("file URL host must be \"localhost\" or empty on %s: %q", runtime.GOOS, uri)
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
		return filepath.FromSlash(p), nil
	}
	if strings.Contains(u.EscapedPath(), "%2F") || strings.Contains(u.EscapedPath(), "%2f") {
		return "", errors.New("file URL path must not include encoded / characters")
	}
	return p, nil
}

// PathToFileURI converts an absolute path to a file: URI.
func PathToFileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
