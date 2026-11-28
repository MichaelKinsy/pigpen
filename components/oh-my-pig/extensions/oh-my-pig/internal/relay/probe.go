package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// upstream: packages/coding-agent/src/tools/browser/relay/probe.ts:1-186

const (
	// helloWindow is how long a relay that is up may go without its browser's hello.
	// upstream: probe.ts:15
	helloWindow = 10 * time.Second

	// restartWindow is how long a restarted relay may take to come back.
	// upstream: probe.ts:20
	restartWindow = 20 * time.Second

	// probeTimeout is the per-request HTTP timeout when probing /json/version.
	// upstream: probe.ts:21
	probeTimeout = 2 * time.Second

	// probePollInterval is the sleep between /json/version polls.
	// upstream: probe.ts:22
	probePollInterval = 150 * time.Millisecond

	// UpdateRelayExtension describes how the user replaces an out-of-date extension.
	// upstream: probe.ts:163
	UpdateRelayExtension = "Run `oh-my-pig browser-relay install` and reload the oh-my-pig extension in Chrome."
)

// RelayVersion is the current binary's version reported on `/json/version` and used
// for extension/relay version comparisons. Defaults to surface.Version; tests may override.
var RelayVersion = surface.Version

// RelayWaitOutcome is the result of WaitForRelayExtension.
// upstream: probe.ts:25-37
type RelayWaitOutcome string

const (
	RelayOutcomeReady             RelayWaitOutcome = "ready"
	RelayOutcomeUnreachable       RelayWaitOutcome = "unreachable"
	RelayOutcomeNoExtension       RelayWaitOutcome = "no-extension"
	RelayOutcomeOutdatedRelay     RelayWaitOutcome = "outdated-relay"
	RelayOutcomeIdleOlderRelay    RelayWaitOutcome = "idle-older-relay"
	RelayOutcomeOutdatedExtension RelayWaitOutcome = "outdated-extension"
)

// WaitOptions configures WaitForRelayExtension.
type WaitOptions struct {
	Restarted bool
	// HelloWindow overrides helloWindow when > 0 (used in tests).
	HelloWindow time.Duration
	// RestartWindow overrides restartWindow when > 0 (used in tests).
	RestartWindow time.Duration
}

// probeHTTPClient bypasses HTTP_PROXY/HTTPS_PROXY for loopback probes.
var probeHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: probeTimeout,
		}).DialContext,
	},
}

type probeResponse struct {
	Status int
	Body   string
}

func probeHTTP(ctx context.Context, rawURL string, timeout time.Duration, token string) *probeResponse {
	if timeout <= 0 {
		timeout = probeTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := probeHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil
	}
	return &probeResponse{Status: resp.StatusCode, Body: string(body)}
}

// RelayVersionOf returns ompRelayVersion from a parsed /json/version body, or "".
// upstream: probe.ts:40-42
func RelayVersionOf(parsed map[string]any) string {
	if parsed == nil {
		return ""
	}
	v, _ := parsed["ompRelayVersion"].(string)
	return v
}

// RelayCdpClientsOf returns ompRelayCdpClients from a parsed /json/version body, or nil.
// upstream: probe.ts:45-49
func RelayCdpClientsOf(parsed map[string]any) *int {
	if parsed == nil {
		return nil
	}
	raw, ok := parsed["ompRelayCdpClients"]
	if !ok {
		return nil
	}
	switch n := raw.(type) {
	case float64:
		v := int(n)
		return &v
	case int:
		v := n
		return &v
	}
	return nil
}

func parseVersionBody(body string) map[string]any {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return nil
	}
	return parsed
}

func relayInstanceAt(ctx context.Context, cdpURL string) *string {
	tok, _ := RelayTokenForURL(cdpURL)
	resp := probeHTTP(ctx, strings.TrimRight(cdpURL, "/")+"/json/version", probeTimeout, tok)
	if resp == nil {
		return nil
	}
	parsed := parseVersionBody(resp.Body)
	if parsed != nil {
		if inst, ok := parsed["ompRelayInstance"].(string); ok {
			return &inst
		}
	}
	empty := ""
	return &empty
}

func readyOutcome(body string) RelayWaitOutcome {
	parsed := parseVersionBody(body)
	wantProto := fmt.Sprintf("%d", DiscardedTabsProtocolVersion)
	if parsed == nil {
		return RelayOutcomeOutdatedRelay
	}
	if v, ok := parsed["ompRelayDiscardedTabsProtocol"].(string); !ok || v != wantProto {
		return RelayOutcomeOutdatedRelay
	}
	if v, ok := parsed["ompExtensionDiscardedTabsProtocol"].(string); !ok || v != wantProto {
		// A relay from another version is the likelier culprit than the extension.
		if RelayVersionOf(parsed) != RelayVersion {
			return RelayOutcomeOutdatedRelay
		}
		return RelayOutcomeOutdatedExtension
	}
	clients := RelayCdpClientsOf(parsed)
	if IsOlderOmpVersion(RelayVersionOf(parsed), RelayVersion) && clients != nil && *clients == 0 {
		return RelayOutcomeIdleOlderRelay
	}
	return RelayOutcomeReady
}

// WaitForRelayExtension polls cdpURL/json/version until its browser says hello or a
// terminal outcome is observed.
// upstream: probe.ts:102-130
func WaitForRelayExtension(ctx context.Context, cdpURL string, opts *WaitOptions) (RelayWaitOutcome, error) {
	probeURL := strings.TrimRight(cdpURL, "/") + "/json/version"
	hWin := helloWindow
	rWin := restartWindow
	restarted := false
	if opts != nil {
		restarted = opts.Restarted
		if opts.HelloWindow > 0 {
			hWin = opts.HelloWindow
		}
		if opts.RestartWindow > 0 {
			rWin = opts.RestartWindow
		}
	}
	reachableBy := time.Now()
	if restarted {
		reachableBy = reachableBy.Add(rWin)
	}
	var helloBy time.Time
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		tok, _ := RelayTokenForURL(cdpURL)
		resp := probeHTTP(ctx, probeURL, probeTimeout, tok)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if resp == nil {
			if !time.Now().Before(reachableBy) {
				return RelayOutcomeUnreachable, nil
			}
		} else if resp.Status >= 200 && resp.Status < 300 {
			return readyOutcome(resp.Body), nil
		} else if resp.Status != http.StatusServiceUnavailable {
			return RelayOutcomeUnreachable, nil
		} else {
			if helloBy.IsZero() {
				helloBy = time.Now().Add(hWin)
			}
			if !time.Now().Before(helloBy) {
				parsed := parseVersionBody(resp.Body)
				if parsed != nil && RelayVersionOf(parsed) != RelayVersion {
					return RelayOutcomeOutdatedRelay, nil
				}
				return RelayOutcomeNoExtension, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(probePollInterval):
		}
	}
}

// RequestRelayRestart asks the native host serving cdpURL to exit so Chrome starts the
// next one. Returns true once the old server stops answering or is replaced.
// upstream: probe.ts:137-160
func RequestRelayRestart(ctx context.Context, cdpURL string, idleOnly bool) (bool, error) {
	before := relayInstanceAt(ctx, cdpURL)
	if before == nil {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	query := "version=" + url.QueryEscape(RelayVersion)
	if idleOnly {
		query += "&idle=1"
	}
	postURL := strings.TrimRight(cdpURL, "/") + RelayRestartPath + "?" + query
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, nil)
	if err != nil {
		return false, nil
	}
	if tok, terr := RelayTokenForURL(cdpURL); terr == nil && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := probeHTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return false, nil
	}
	deadline := time.Now().Add(restartWindow)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(probePollInterval):
		}
		after := relayInstanceAt(ctx, cdpURL)
		if after == nil || *after != *before {
			return true, nil
		}
	}
	return false, nil
}

// RelayExtensionNotice returns a user-facing notice when a connected extension was
// installed by an older version, or "" when current or unknown.
// upstream: probe.ts:172-186
func RelayExtensionNotice(ctx context.Context, cdpURL string) string {
	tok, _ := RelayTokenForURL(cdpURL)
	resp := probeHTTP(ctx, strings.TrimRight(cdpURL, "/")+"/json/version", probeTimeout, tok)
	if resp == nil || resp.Status < 200 || resp.Status >= 300 {
		return ""
	}
	parsed := parseVersionBody(resp.Body)
	if parsed == nil {
		return ""
	}
	raw, exists := parsed["ompExtensionVersion"]
	if !exists {
		return ""
	}
	installedBy, ok := raw.(string)
	if !ok || !IsOlderOmpVersion(installedBy, RelayVersion) {
		return ""
	}
	origin := "an older " + ExtensionName
	if installedBy != "" {
		origin = fmt.Sprintf("%s %s; this is %s %s", ExtensionName, installedBy, ExtensionName, RelayVersion)
	}
	return fmt.Sprintf("The %s extension is out of date (installed by %s). %s", ExtensionName, origin, UpdateRelayExtension)
}
