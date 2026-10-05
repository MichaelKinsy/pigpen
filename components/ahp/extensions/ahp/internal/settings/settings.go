// Package settings reads and writes the listener settings file (port of pi-ahp's
// src/host/direct-settings.ts).
//
// Unlike the standalone upstream server, this extension never opens a listener on its own: the
// file is only loaded (and, if absent, created) after the user explicitly asked for one.
package settings

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
)

// DefaultHost is where the listener binds unless told otherwise: loopback only.
const DefaultHost = "127.0.0.1"

// Direct is the endpoint configuration of the direct listener.
type Direct struct {
	Port  int     `json:"port"`
	Token *string `json:"token"`
	Host  string  `json:"host"`
}

// Error is a problem with the settings file, naming the file.
type Error struct{ Path, Detail string }

func (e *Error) Error() string { return e.Path + ": " + e.Detail }

func freePort() (int, error) {
	l, err := net.Listen("tcp", net.JoinHostPort(DefaultHost, "0"))
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// LoadDirect reads the settings at path. A missing file is created with a free loopback port and
// a fresh token (only a missing file mints a token); an existing file is taken literally, with
// only the optional fields defaulted, and a malformed one is an error rather than an invitation
// to invent replacements.
func LoadDirect(path string) (Direct, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		port, err := freePort()
		if err != nil {
			return Direct{}, err
		}
		token, err := newToken()
		if err != nil {
			return Direct{}, err
		}
		settings := Direct{Port: port, Token: &token, Host: DefaultHost}
		return settings, write(settings, path)
	}
	if err != nil {
		return Direct{}, err
	}

	var stored map[string]json.RawMessage
	if err := json.Unmarshal(raw, &stored); err != nil || stored == nil {
		detail := "not an object"
		if err != nil {
			detail = err.Error()
		}
		return Direct{}, &Error{path, fmt.Sprintf("not valid JSON (%s)", detail)}
	}
	rawPort, hasPort := stored["port"]
	if !hasPort {
		return Direct{}, &Error{path, "no `port`. Set one, or delete the file to have it chosen."}
	}
	var portValue float64
	if err := json.Unmarshal(rawPort, &portValue); err != nil || portValue != math.Trunc(portValue) || portValue < 1 || portValue > 65535 {
		return Direct{}, &Error{path, fmt.Sprintf("`port` must be an integer 1-65535, got %s", rawPort)}
	}
	out := Direct{Port: int(portValue), Host: DefaultHost}
	if rawToken, ok := stored["token"]; ok && string(rawToken) != "null" {
		var token string
		if err := json.Unmarshal(rawToken, &token); err != nil {
			return Direct{}, &Error{path, fmt.Sprintf("`token` must be a string or null, got %s", rawToken)}
		}
		out.Token = &token // absent means absent
	}
	if rawHost, ok := stored["host"]; ok {
		var host string
		if err := json.Unmarshal(rawHost, &host); err != nil {
			return Direct{}, &Error{path, fmt.Sprintf("`host` must be a string, got %s", rawHost)}
		}
		out.Host = host
	}
	return out, nil
}

func write(s Direct, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// Owner-only: the token is a bearer credential for the endpoint.
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
