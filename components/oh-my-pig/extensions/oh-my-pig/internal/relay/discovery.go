package relay

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// upstream: packages/coding-agent/src/tools/browser/relay/discovery.ts:1-42
// Deliberate security difference from omp: each endpoint file holds a 256-bit random
// bearer token (mode 0600 inside a 0700 directory on Unix; protected by user profile
// ACLs on Windows) required on every relay HTTP/WebSocket endpoint.

const relayTokenBytes = 32 // 256 bits (>= 128 bits required)

var (
	endpointFileRE = regexp.MustCompile(`^(\d+)-(\d+)$`)

	// ErrRelayTokenMissing indicates no live, safely-permissioned endpoint file matches the requested port.
	ErrRelayTokenMissing = errors.New("no readable relay auth token found for endpoint")
	// ErrRelayTokenUnsafePermissions indicates a Unix endpoint file or directory is accessible to group/other or owned by another user.
	ErrRelayTokenUnsafePermissions = errors.New("relay endpoint file has unsafe permissions")
)

// GenerateRelayToken returns a cryptographically random hex token (256 bits).
func GenerateRelayToken() (string, error) {
	var buf [relayTokenBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate relay auth token: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// PublishRelayEndpoint publishes an OS-assigned (or fixed) loopback port and its
// bearer token for this native host's lifetime by writing `<pid>-<port>` (mode 0600)
// inside EndpointsDir() (mode 0700). The returned cleanup removes the file.
// upstream: discovery.ts:11-15 (extended with bearer token file content)
func PublishRelayEndpoint(port int, token string) (func() error, error) {
	if token == "" {
		return nil, errors.New("cannot publish relay endpoint without auth token")
	}
	dir := EndpointsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dir, 0o700)
	}
	file := filepath.Join(dir, fmt.Sprintf("%d-%d", os.Getpid(), port))
	// Write atomically via sibling temp file with 0600 permissions so no reader sees a partial token.
	if err := writeFileAtomic(file, []byte(token+"\n"), 0o600); err != nil {
		return nil, err
	}
	return func() error {
		err := os.Remove(file)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}, nil
}

// DiscoveredEndpoint describes a live local relay endpoint and its bearer token.
type DiscoveredEndpoint struct {
	PID     int
	Port    int
	URL     string
	Token   string
	Started int64
}

// DiscoverRelayEndpointEntries returns live, safely-permissioned local relay endpoints
// sorted most recently started first.
func DiscoverRelayEndpointEntries() []DiscoveredEndpoint {
	dir := EndpointsDir()
	if !endpointDirSafe(dir) {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var live []DiscoveredEndpoint
	for _, entry := range entries {
		m := endpointFileRE.FindStringSubmatch(entry.Name())
		if len(m) != 3 {
			continue
		}
		pid, err1 := strconv.Atoi(m[1])
		port, err2 := strconv.Atoi(m[2])
		if err1 != nil || err2 != nil || pid < 1 || port < 1 || port > 65535 {
			continue
		}
		if !processAlive(pid) {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		token, modNano, err := readEndpointTokenFile(fullPath)
		if err != nil || token == "" {
			continue
		}
		live = append(live, DiscoveredEndpoint{
			PID:     pid,
			Port:    port,
			URL:     fmt.Sprintf("http://127.0.0.1:%d", port),
			Token:   token,
			Started: modNano,
		})
	}
	sort.Slice(live, func(i, j int) bool {
		return live[i].Started > live[j].Started
	})
	return live
}

// DiscoverRelayEndpoints discovers live local relay hosts under EndpointsDir(),
// returning http://127.0.0.1:<port> URLs sorted most recently started first.
// upstream: discovery.ts:18-42
func DiscoverRelayEndpoints() []string {
	entries := DiscoverRelayEndpointEntries()
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.URL
	}
	return out
}

// RelayTokenForURL finds the bearer token for cdpURL from its matching live endpoint
// file under EndpointsDir(). Refuses endpoint files with unsafe permissions on Unix.
func RelayTokenForURL(cdpURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(cdpURL))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid relay URL %q", cdpURL)
	}
	portStr := u.Port()
	if portStr == "" {
		return "", fmt.Errorf("relay URL %q does not specify a port; cannot locate endpoint token under %s", cdpURL, EndpointsDir())
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid port in relay URL %q", cdpURL)
	}
	dir := EndpointsDir()
	if !endpointDirSafe(dir) {
		if _, statErr := os.Stat(dir); statErr == nil {
			return "", fmt.Errorf("%w: endpoints directory %s must be owned by current user with mode 0700", ErrRelayTokenUnsafePermissions, dir)
		}
		return "", fmt.Errorf("%w: no readable endpoint file for port %d in %s (is Chrome running with the %s extension?)", ErrRelayTokenMissing, port, dir, ExtensionName)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("%w: cannot read %s: %v", ErrRelayTokenMissing, dir, err)
	}
	var (
		bestToken   string
		bestStarted int64
		unsafeErr   error
	)
	for _, entry := range entries {
		m := endpointFileRE.FindStringSubmatch(entry.Name())
		if len(m) != 3 {
			continue
		}
		pid, err1 := strconv.Atoi(m[1])
		epPort, err2 := strconv.Atoi(m[2])
		if err1 != nil || err2 != nil || epPort != port || pid < 1 {
			continue
		}
		if !processAlive(pid) {
			continue
		}
		tok, modNano, rerr := readEndpointTokenFile(filepath.Join(dir, entry.Name()))
		if rerr != nil {
			if errors.Is(rerr, ErrRelayTokenUnsafePermissions) {
				unsafeErr = rerr
			}
			continue
		}
		if tok != "" && (bestToken == "" || modNano > bestStarted) {
			bestToken = tok
			bestStarted = modNano
		}
	}
	if bestToken != "" {
		return bestToken, nil
	}
	if unsafeErr != nil {
		return "", fmt.Errorf("%w for port %d in %s", unsafeErr, port, dir)
	}
	return "", fmt.Errorf("%w: no readable endpoint file for port %d in %s (configure Chrome with the %s extension, or check permissions)", ErrRelayTokenMissing, port, dir, ExtensionName)
}

func readEndpointTokenFile(path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, ErrRelayTokenUnsafePermissions
	}
	if err := checkEndpointFileSafe(info); err != nil {
		return "", 0, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	tok := strings.TrimSpace(string(raw))
	if len(tok) < 32 { // >= 128 bits hex-encoded
		return "", 0, ErrRelayTokenMissing
	}
	return tok, info.ModTime().UnixNano(), nil
}
