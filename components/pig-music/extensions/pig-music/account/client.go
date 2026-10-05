package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/native"
)

const (
	origin           = "https://music.youtube.com"
	defaultBrowseURL = "https://music.youtube.com/youtubei/v1/browse?prettyPrint=false"
)

var (
	// ErrAuth means the account did not accept the cookies (or there are none to sign with): the caller falls back to yt-dlp.
	ErrAuth = errors.New("YouTube Music did not accept the signed-in session")
	// ErrShape means the answer was in a layout this version does not know: the caller falls back to yt-dlp.
	ErrShape = errors.New("YouTube Music answered in a layout this version does not know")
)

// Client is a signed WEB_REMIX client. Jar supplies the cookies, once: the Client keeps them in memory and asks again only
// after an answer rejected them.
type Client struct {
	Jar       func(ctx context.Context) (Jar, error)
	HTTP      *http.Client
	BrowseURL string // tests
	Now       func() time.Time
	Timeout   time.Duration

	mu  sync.Mutex
	jar Jar
}

func (c *Client) cookies(ctx context.Context) (Jar, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.jar != nil {
		return c.jar, nil
	}
	if c.Jar == nil {
		return nil, fmt.Errorf("%w: no cookies are configured", ErrAuth)
	}
	jar, err := c.Jar(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		// The export failed (no YouTube session in the browser, a keyring that did not answer, a timeout): the caller treats
		// it as a refusal and lists with yt-dlp for the session, instead of exporting again before every listing. The
		// original error stays in the chain (errors.As still finds a consent error).
		return nil, fmt.Errorf("%w: %w", ErrAuth, err)
	}
	if !jar.SignedIn() {
		return nil, fmt.Errorf("%w: the browser has no signed-in YouTube session", ErrAuth)
	}
	c.jar = jar
	return jar, nil
}

// Drop forgets the cookies, so the next request obtains them again.
func (c *Client) Drop() {
	c.mu.Lock()
	c.jar = nil
	c.mu.Unlock()
}

// Browse sends one signed browse request: a browseId, or a continuation token.
func (c *Client) Browse(ctx context.Context, fields map[string]any) ([]byte, error) {
	jar, err := c.cookies(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	auth, _ := jar.Authorization(origin, now())
	fields["context"] = native.ClientContext()
	body, _ := json.Marshal(fields)
	url := c.BrowseURL
	if url == "" {
		url = defaultBrowseURL
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", native.UserAgent)
	req.Header.Set("Origin", origin)
	req.Header.Set("X-Origin", origin)
	req.Header.Set("X-Goog-AuthUser", "0")
	req.Header.Set("Authorization", auth)
	req.Header.Set("Cookie", jar.Header())
	client := c.HTTP
	if client == nil {
		client = native.SharedHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, native.Explain(native.RedactError(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, native.Explain(native.RedactError(err))
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return data, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		c.Drop()
		return nil, fmt.Errorf("%w (HTTP %d)", ErrAuth, resp.StatusCode)
	}
	return nil, fmt.Errorf("YouTube Music browse answered HTTP %d", resp.StatusCode)
}
