package resourceserver

import (
	"net/http"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// Challenge is the WWW-Authenticate value of a 401 response.
const Challenge = "Bearer"

// BearerToken returns the token of an "Authorization: Bearer <token>" header. There must be exactly one
// Authorization header, with the scheme "Bearer" (any case) and one token that has no whitespace. Anything else is
// token_invalid.
func BearerToken(h http.Header) (string, error) {
	values := h.Values("Authorization")
	if len(values) != 1 {
		return "", profile.NewError(profile.TokenInvalid, profile.FlagResourceServer, "")
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", profile.NewError(profile.TokenInvalid, profile.FlagResourceServer, "")
	}
	return token, nil
}

// Authenticate verifies the bearer token of a request.
func (v *Verifier) Authenticate(r *http.Request) (Principal, error) {
	token, err := BearerToken(r.Header)
	if err != nil {
		return Principal{}, v.err(profile.TokenInvalid)
	}
	return v.Verify(r.Context(), token)
}

// StatusFor returns the HTTP status a failed Verify maps to: 503 when the JWKS is unavailable or the request was
// cancelled, otherwise 401.
func StatusFor(err error) int {
	switch profile.CodeOf(err) {
	case profile.JWKSUnavailable, profile.Cancelled:
		return http.StatusServiceUnavailable
	}
	return http.StatusUnauthorized
}

// WriteError writes the response for a failed Verify: the status of StatusFor, a "WWW-Authenticate: Bearer" header
// on a 401, and the fixed text of the error's code as the body. The body never echoes the request.
func WriteError(w http.ResponseWriter, err error) {
	status := StatusFor(err)
	code := profile.CodeOf(err)
	if !code.Valid() {
		code = profile.TokenInvalid
	}
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", Challenge)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(string(code) + ": " + code.Text() + "\n"))
}
