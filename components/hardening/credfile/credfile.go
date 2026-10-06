// Package credfile reads a credential from an auth.json-shaped file on every request and keeps nothing.
//
// The file is the one PiG writes: a JSON object keyed by provider, each entry either
//
//	{"type":"oauth","access":"<token>","expires":<ms since the epoch>,"refresh":"..."}
//	{"type":"api_key","key":"<key>"}
//
// The host rotates the file by an atomic rename between turns. This package opens it once per request, checks the
// opened descriptor, reads a bounded amount and parses it. It never uses a "refresh" value (the field is not even
// decoded), never resolves a "key" as an environment variable or a command, and never retries a partial read.
package credfile

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/MichaelKinsy/pigpen/components/hardening/internal/hostname"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/safefile"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/strictjson"
	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// MaxFileBytes caps the credential file (64 KiB).
const MaxFileBytes = 64 << 10

// maxTokenBytes caps one credential value: a header value must stay small.
const maxTokenBytes = 8 << 10

// Source reads one provider's credential from one file. The zero Source is unusable: every call returns
// ProfileMisconfigured.
type Source struct {
	pkg      string
	path     string
	provider string
	origin   hostname.Origin
	header   string
	scheme   string
	margin   time.Duration
	now      func() time.Time
	open     safefile.Opener
	ok       bool
}

// New returns a Source for the credentialFile configuration of Package pkg. A nil configuration (the flag is off)
// is ProfileMisconfigured: a caller asks for a Source only when the flag is on.
func New(pkg string, cfg *profile.CredentialFile) (Source, error) {
	if cfg == nil {
		return Source{}, profile.NewError(profile.ProfileMisconfigured, profile.FlagCredentialFile, pkg)
	}
	o, err := hostname.ParseOrigin(cfg.Origin)
	if err != nil || cfg.Path == "" || cfg.Provider == "" || cfg.Header == "" || cfg.ExpiryMargin < 0 {
		return Source{}, profile.NewError(profile.ProfileMisconfigured, profile.FlagCredentialFile, pkg)
	}
	return Source{pkg: pkg, path: cfg.Path, provider: cfg.Provider, origin: o, header: cfg.Header, scheme: cfg.Scheme,
		margin: cfg.ExpiryMargin, now: time.Now, open: os.OpenFile, ok: true}, nil
}

// WithClock returns a copy of s that reads the time from now (for tests).
func (s Source) WithClock(now func() time.Time) Source { s.now = now; return s }

func (s Source) err(code profile.Code) *profile.Error {
	return profile.NewError(code, profile.FlagCredentialFile, s.pkg)
}

// entry is one provider's record. It has no field for "refresh" on purpose.
type entry struct {
	Type    *string  `json:"type"`
	Access  *string  `json:"access"`
	Key     *string  `json:"key"`
	Expires *float64 `json:"expires"`
}

// Token opens, checks and parses the file, and returns the credential. It keeps nothing: the next call reads the
// file again.
//
// Failures: the file is missing, unreadable, not a regular file after symlinks, or larger than 64 KiB
// (CredentialUnavailable); it does not parse, has no entry for the provider, has an unknown type, or an unusable
// value (CredentialMalformed); an OAuth entry expires at or before now plus the margin (CredentialExpired); the
// context is cancelled (Cancelled).
func (s Source) Token(ctx context.Context) (string, error) {
	if !s.ok {
		return "", profile.NewError(profile.ProfileMisconfigured, profile.FlagCredentialFile, s.pkg)
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", s.err(profile.Cancelled).WithCause(err)
		}
	}
	data, err := safefile.ReadWith(s.open, s.path, MaxFileBytes)
	if err != nil {
		return "", s.err(profile.CredentialUnavailable)
	}
	var doc map[string]json.RawMessage
	if err := strictjson.Decode(data, &doc, false); err != nil || doc == nil {
		return "", s.err(profile.CredentialMalformed)
	}
	raw, ok := doc[s.provider]
	if !ok {
		return "", s.err(profile.CredentialMalformed)
	}
	var e entry
	// strictjson, not json.Unmarshal: encoding/json would fill a field from a key that matches it only by case
	// ("Access" after "access" would win).
	if err := strictjson.Decode(raw, &e, false); err != nil || e.Type == nil {
		return "", s.err(profile.CredentialMalformed)
	}
	var token string
	switch *e.Type {
	case "oauth":
		if e.Access == nil || e.Expires == nil {
			return "", s.err(profile.CredentialMalformed)
		}
		token = *e.Access
		if !usable(token) {
			return "", s.err(profile.CredentialMalformed)
		}
		// The margin makes a token count as expired slightly early, never late.
		deadline := float64(s.now().Add(s.margin).UnixMilli())
		if *e.Expires <= deadline {
			return "", s.err(profile.CredentialExpired)
		}
	case "api_key":
		if e.Key == nil {
			return "", s.err(profile.CredentialMalformed)
		}
		token = *e.Key
		// PiG resolves a key that starts with "!" by running a command. This package never runs one.
		if !usable(token) || token[0] == '!' {
			return "", s.err(profile.CredentialMalformed)
		}
	default:
		return "", s.err(profile.CredentialMalformed)
	}
	return token, nil
}

// usable reports whether v can be a header value: printable ASCII with no space, not empty, not huge.
func usable(v string) bool {
	if v == "" || len(v) > maxTokenBytes {
		return false
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

// Header returns the header name and value to send: "Authorization" and "Bearer <token>" by default.
func (s Source) Header(ctx context.Context) (name, value string, err error) {
	token, err := s.Token(ctx)
	if err != nil {
		return "", "", err
	}
	if s.scheme != "" {
		token = s.scheme + " " + token
	}
	return s.header, token, nil
}

// Origin returns the bound origin as "scheme://host:port".
func (s Source) Origin() string { return s.origin.String() }

// RoundTripper returns a transport that adds the credential to requests for the bound origin only, reading the
// file once per request. A request for another origin never carries it, and its copy of the credential header is
// removed, so a redirect to another origin cannot carry the credential even when the client copies headers. When the
// credential is unavailable the request fails with the typed error and nothing is sent. next defaults to
// http.DefaultTransport.
func (s Source) RoundTripper(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &transport{s: s, next: next}
}

type transport struct {
	s    Source
	next http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	o, err := hostname.OriginOf(req.URL)
	if err == nil && t.s.ok && o == t.s.origin {
		name, value, err := t.s.Header(req.Context())
		if err != nil {
			closeBody(req)
			return nil, err
		}
		clone := req.Clone(req.Context())
		clone.Header.Set(name, value)
		return t.next.RoundTrip(clone)
	}
	if t.s.ok && req.Header.Get(t.s.header) != "" {
		clone := req.Clone(req.Context())
		clone.Header.Del(t.s.header)
		return t.next.RoundTrip(clone)
	}
	return t.next.RoundTrip(req)
}

func closeBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}
