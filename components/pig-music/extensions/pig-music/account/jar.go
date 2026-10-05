// Package account talks to YouTube Music as the signed-in user: the library (liked songs, playlists) from InnerTube's browse
// endpoint, signed with the SAPISIDHASH header that the web client itself sends. The cookies it needs are held in memory
// only (Jar), obtained once per process after the consent that cookies/ records, and are never written anywhere, logged or
// printed. Search, details and playback never use this package.
package account

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Cookie is one cookie that a browser sends to music.youtube.com.
type Cookie struct{ Name, Value, Domain string }

// Jar is the signed-in session's cookies, in memory. It has no method that writes it out.
type Jar []Cookie

// ParseNetscape reads yt-dlp's cookie file format (also the one with "#HttpOnly_" lines) and keeps the unexpired cookies that a
// browser would send to music.youtube.com: those of youtube.com and of music.youtube.com itself. The cookies of google.com
// (whose SAPISID has another value) and of other YouTube hosts are dropped at once.
func ParseNetscape(r io.Reader, now time.Time) (Jar, error) {
	var jar Jar
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		if !sentTo(musicHost, f[0], f[1], f[2]) {
			continue
		}
		domain := strings.TrimPrefix(f[0], ".")
		if exp, err := strconv.ParseInt(f[4], 10, 64); err == nil && exp != 0 && exp < now.Unix() {
			continue
		}
		jar = append(jar, Cookie{Name: f[5], Value: f[6], Domain: domain})
	}
	return jar, sc.Err()
}

// musicHost is the one host the jar's cookies are sent to.
const musicHost = "music.youtube.com"

// sentTo is the cookie domain and path match of RFC 6265 for a request to host (the browse endpoint's path): a host-only
// cookie (include-subdomains FALSE, no leading dot) goes to its own host only, a domain cookie to the domain and its subdomains.
func sentTo(host, domain, includeSubdomains, path string) bool {
	d := strings.ToLower(domain)
	sub := strings.EqualFold(includeSubdomains, "TRUE") || strings.HasPrefix(d, ".")
	d = strings.TrimPrefix(d, ".")
	if d != host && !(sub && strings.HasSuffix(host, "."+d)) {
		return false
	}
	return path == "" || path == "/" || strings.HasPrefix("/youtubei/v1/browse", path)
}

func (j Jar) value(name string) string {
	for _, c := range j {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// SignedIn reports whether the jar holds the cookie the signature is made from.
func (j Jar) SignedIn() bool { return j.value("SAPISID") != "" || j.value("__Secure-3PAPISID") != "" }

// Header is the Cookie header: every cookie of the jar as name=value.
func (j Jar) Header() string {
	parts := make([]string, 0, len(j))
	for _, c := range j {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// Authorization is the SAPISIDHASH header of the web client: sha1 of "<unix time> <cookie> <origin>", once for each of the
// three cookies the account has (SAPISID, __Secure-1PAPISID, __Secure-3PAPISID).
func (j Jar) Authorization(origin string, now time.Time) (string, bool) {
	ts := strconv.FormatInt(now.Unix(), 10)
	sapisid := j.value("SAPISID")
	if sapisid == "" {
		sapisid = j.value("__Secure-3PAPISID")
	}
	if sapisid == "" {
		return "", false
	}
	sign := func(secret string) string {
		sum := sha1.Sum([]byte(ts + " " + secret + " " + origin))
		return ts + "_" + hex.EncodeToString(sum[:])
	}
	parts := []string{"SAPISIDHASH " + sign(sapisid)}
	if v := j.value("__Secure-1PAPISID"); v != "" {
		parts = append(parts, "SAPISID1PHASH "+sign(v))
	}
	if v := j.value("__Secure-3PAPISID"); v != "" {
		parts = append(parts, "SAPISID3PHASH "+sign(v))
	}
	return strings.Join(parts, " "), true
}

// String names the cookies and never shows a value, so a Jar that reaches a log or an error says nothing secret.
func (j Jar) String() string {
	names := make([]string, 0, len(j))
	for _, c := range j {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return fmt.Sprintf("%d cookies (%s)", len(j), strings.Join(names, ","))
}

// GoString is String for %#v.
func (j Jar) GoString() string { return j.String() }
