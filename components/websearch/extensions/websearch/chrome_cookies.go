package websearch

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

// CookieMap maps a cookie name to its value.
type CookieMap = map[string]string

// BrowserCookieAttempt is one profile tried (BrowserCookieAttemptDiagnostic upstream).
type BrowserCookieAttempt struct {
	Browser string `json:"browser"`
	Profile string `json:"profile"`
	Status  string `json:"status"`
}

// BrowserCookieDiagnosticDetails is the last extraction's diagnostic with its attempts.
type BrowserCookieDiagnosticDetails struct {
	Message  string                 `json:"message"`
	Attempts []BrowserCookieAttempt `json:"attempts"`
}

// BrowserCookieOptions are the options of getBrowserCookiesForHosts.
type BrowserCookieOptions struct {
	Hosts           []string
	Browser         string
	Profile         string
	RequiredCookies []string
	CookieNames     []string
	RequiredLabel   string
	RequestURL      *url.URL
}

// BrowserCookies is a successful extraction.
type BrowserCookies struct {
	Cookies      CookieMap
	Warnings     []string
	CookieHeader string
}

// GoogleCookieOptions are the options of getGoogleCookies.
type GoogleCookieOptions struct {
	Browser         string
	Profile         string
	RequiredCookies []string
}

var (
	cookieGOOS     = runtime.GOOS
	cookieLookPath = exec.LookPath
	cookieNow      = time.Now
)

var errCookiesStub = errors.New("browser cookie extraction is not implemented")

// GetGoogleCookies is getGoogleCookies.
func GetGoogleCookies(context.Context, GoogleCookieOptions) (*BrowserCookies, error) {
	return nil, errCookiesStub
}

// GetBrowserCookiesForHosts is getBrowserCookiesForHosts: nil, nil when no cookies were
// obtained (the diagnostic says why).
func GetBrowserCookiesForHosts(context.Context, BrowserCookieOptions) (*BrowserCookies, error) {
	return nil, errCookiesStub
}

// LastGoogleCookieDiagnostic is getLastGoogleCookieDiagnostic ("" for null).
func LastGoogleCookieDiagnostic() string { return "" }

// LastBrowserCookieDiagnostic is getLastBrowserCookieDiagnostic.
func LastBrowserCookieDiagnostic() string { return "" }

// LastGoogleCookieDiagnosticDetails is getLastGoogleCookieDiagnosticDetails.
func LastGoogleCookieDiagnosticDetails() *BrowserCookieDiagnosticDetails { return nil }

func resetCookieState() {}

// pbkdf2SHA1 is the Chromium safe-storage key.
func pbkdf2SHA1(password string, iterations int) []byte { return nil }
