package relay

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/tools/browser/relay/server.ts:1-263

const (
	wsKeepaliveInterval = 30 * time.Second
)

// extensionOriginRE matches `chrome-extension://<32-char a-p id>`.
// upstream: server.ts:44
var extensionOriginRE = regexp.MustCompile(`^chrome-extension://([a-p]{32})$`)

// RelayUnavailableInfo is the JSON body of 503 /json/version before an extension connects.
// upstream: server.ts:47-55
type RelayUnavailableInfo struct {
	OmpRelayVersion    string `json:"ompRelayVersion"`
	OmpRelayInstance   string `json:"ompRelayInstance"`
	Error              string `json:"error"`
	OmpRelayCdpClients int    `json:"ompRelayCdpClients"`
}

// RestartRequest is passed to RelayServerOptions.OnRestart on POST /omp/relay/restart.
type RestartRequest struct {
	Version  string
	IdleOnly bool
}

// RelayServerOptions configures StartRelayServer.
// upstream: server.ts:27-41
type RelayServerOptions struct {
	Port                int
	ChatOnly            bool
	OnRestart           func(RestartRequest) bool
	DisableGroup        bool
	Group               *GroupConfig
	InstallRegistryFile string
	InstalledStamp      func(extensionID string) (*RelayExtensionStamp, error)
	UpdateExtension     func(extensionID string) (bool, error)
	PanelSources        *PanelSources
	Log                 func(message string, data map[string]any)
	// AuthToken sets the bearer token required on every HTTP/WebSocket request.
	// When empty, StartRelayServer generates a fresh 256-bit random token.
	AuthToken string
}

// RelayServer is a running HTTP + WebSocket loopback relay server.
// upstream: server.ts:58-62
type RelayServer struct {
	Bridge    *RelayBridge
	Port      int
	Instance  string
	AuthToken string

	ln         net.Listener
	httpServer *http.Server
	mu         sync.Mutex
	sockets    map[*wsRelaySocket]struct{}
	stopCh     chan struct{}
	stopOnce   sync.Once
}

type wsRelaySocket struct {
	ws *WSConn
}

func (s *wsRelaySocket) Send(text string) {
	_ = s.ws.WriteText([]byte(text))
}

func (s *wsRelaySocket) Close() {
	_ = s.ws.Close()
}

// isWSAuthority returns true when raw can serve as the authority of a ws:// URL:
// no whitespace, slashes, userinfo, fragments, or control characters, and URL-parseable.
// upstream: server.ts:82-89
func isWSAuthority(raw string) bool {
	if raw == "" {
		return false
	}
	for i := range len(raw) {
		c := raw[i]
		if c <= 0x20 || c == '/' || c == '\\' || c == '@' || c == '#' || c == '?' || c == 0x7f {
			return false
		}
	}
	u, err := url.Parse("ws://" + raw)
	return err == nil && u.Host != ""
}

func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}

// StartRelayServer binds 127.0.0.1:<opts.Port> (0 for OS-assigned) and serves the
// browser relay HTTP discovery and WebSocket endpoints.
// upstream: server.ts:92-263
func StartRelayServer(opts RelayServerOptions) (*RelayServer, error) {
	logFn := opts.Log
	if logFn == nil {
		logFn = func(string, map[string]any) {}
	}
	var group *GroupConfig
	if !opts.DisableGroup {
		if opts.Group != nil {
			g := *opts.Group
			group = &g
		} else {
			g := DefaultGroupConfig
			group = &g
		}
	}
	installedStamp := opts.InstalledStamp
	if installedStamp == nil && !opts.ChatOnly {
		regFile := opts.InstallRegistryFile
		installedStamp = func(extensionID string) (*RelayExtensionStamp, error) {
			rows, err := ReadRelayInstalls(InstallLocations{RegistryFile: regFile})
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if !row.Missing && ExtensionIDForPath(row.Dir, runtime.GOOS) == extensionID {
					st := InstalledExtensionStamp(row.Dir)
					if st == nil {
						return nil, nil
					}
					var buildPtr *string
					if st.HasBuild {
						b := st.Build
						buildPtr = &b
					}
					return &RelayExtensionStamp{OmpVersion: st.Version, OmpBuild: buildPtr}, nil
				}
			}
			return nil, nil
		}
	}
	updateExt := opts.UpdateExtension
	if updateExt == nil && !opts.ChatOnly {
		regFile := opts.InstallRegistryFile
		updateExt = func(extensionID string) (bool, error) {
			ours, err := RegisteredRelayExtensionIDs(InstallLocations{RegistryFile: regFile})
			if err != nil {
				return false, err
			}
			if !slices.Contains(ours, extensionID) {
				logFn("connected extension is not a registered install; not updating it", map[string]any{"extensionId": extensionID})
				return false, nil
			}
			results, err := SyncRelayInstalls(InstallSyncOptions{
				InstallLocations: InstallLocations{RegistryFile: regFile, Version: RelayVersion},
				Mode:             SyncModeRelay,
			})
			if err != nil {
				return false, err
			}
			current := false
			failed := false
			for _, r := range results {
				if r.Status == SyncStatusFailed {
					failed = true
					logFn("extension install sync failed", map[string]any{"dir": r.Dir, "error": r.Error})
				} else if r.Status != SyncStatusMissing && r.OmpVersion == RelayVersion {
					current = true
				}
			}
			return current && !failed, nil
		}
	}
	bridge := NewRelayBridge(BridgeOptions{
		Log:             logFn,
		Group:           group,
		InstalledStamp:  installedStamp,
		UpdateExtension: updateExt,
		PanelSources:    opts.PanelSources,
	})

	token := opts.AuthToken
	if token == "" {
		var err error
		token, err = GenerateRelayToken()
		if err != nil {
			return nil, err
		}
	}

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	boundPort := ln.Addr().(*net.TCPAddr).Port

	srv := &RelayServer{
		Bridge:    bridge,
		Port:      boundPort,
		Instance:  randomUUID(),
		AuthToken: token,
		ln:        ln,
		sockets:   make(map[*wsRelaySocket]struct{}),
		stopCh:    make(chan struct{}),
	}

	srv.httpServer = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			srv.handleHTTP(w, r, opts)
		}),
	}
	go func() {
		_ = srv.httpServer.Serve(ln)
	}()

	go srv.keepaliveLoop()

	logFn("relay listening", map[string]any{"port": boundPort})
	return srv, nil
}

func (s *RelayServer) checkAuth(r *http.Request) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return false
	}
	got := auth[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.AuthToken)) == 1
}

func (s *RelayServer) handleHTTP(w http.ResponseWriter, r *http.Request, opts RelayServerOptions) {
	fallback := net.JoinHostPort("127.0.0.1", strconv.Itoa(s.Port))
	rawHost := strings.TrimSpace(r.Host)
	host := fallback
	if isWSAuthority(rawHost) {
		host = rawHost
	}

	path := strings.TrimRight(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}

	// Every endpoint requires the per-relay bearer token in addition to Origin rules
	// (deliberate security tightening over omp; closes loopback relay to other local users/processes).
	if !s.checkAuth(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// WS /cdp (upstream: server.ts:154-164)
	if path == "/cdp" {
		if r.Header.Get("Origin") != "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		claim := r.URL.Query().Get("claim")
		ws, attempted, err := UpgradeWS(w, r, DefaultMaxWSMessageBytes)
		if !attempted {
			w.WriteHeader(http.StatusUpgradeRequired)
			_, _ = w.Write([]byte("websocket upgrade required"))
			return
		}
		if err != nil {
			return
		}
		s.serveWS(ws, "cdp", "", claim)
		return
	}

	// WS /ext (upstream: server.ts:165-176)
	if path == "/ext" && !opts.ChatOnly {
		origin := r.Header.Get("Origin")
		if origin != "" && !strings.HasPrefix(origin, "chrome-extension://") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		var extID string
		if m := extensionOriginRE.FindStringSubmatch(origin); len(m) == 2 {
			extID = m[1]
		}
		ws, attempted, err := UpgradeWS(w, r, DefaultMaxWSMessageBytes)
		if !attempted {
			w.WriteHeader(http.StatusUpgradeRequired)
			_, _ = w.Write([]byte("websocket upgrade required"))
			return
		}
		if err != nil {
			return
		}
		s.serveWS(ws, "ext", extID, "")
		return
	}

	// POST /omp/extensions/recheck (upstream: server.ts:177-183)
	if path == RelayRecheckPath {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte("Method not allowed"))
			return
		}
		if r.Header.Get("Origin") != "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		s.Bridge.RecheckExtensions()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// POST /omp/relay/restart (upstream: server.ts:184-193)
	if path == RelayRestartPath {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte("Method not allowed"))
			return
		}
		if r.Header.Get("Origin") != "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		restarting := false
		if opts.OnRestart != nil {
			q := r.URL.Query()
			restarting = opts.OnRestart(RestartRequest{
				Version:  q.Get("version"),
				IdleOnly: q.Get("idle") == "1",
			})
		}
		if restarting {
			w.WriteHeader(http.StatusAccepted)
		} else {
			w.WriteHeader(http.StatusConflict)
		}
		return
	}

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte("Method not allowed"))
		return
	}

	// GET /json/version (upstream: server.ts:195-209)
	if path == "/json/version" {
		if !s.Bridge.Ready() {
			info := RelayUnavailableInfo{
				OmpRelayVersion:    RelayVersion,
				OmpRelayInstance:   s.Instance,
				Error:              "relay extension is not connected",
				OmpRelayCdpClients: s.Bridge.CDPClientCount(),
			}
			writeJSON(w, http.StatusServiceUnavailable, info)
			return
		}
		payload := s.Bridge.VersionInfo(fmt.Sprintf("ws://%s/cdp", host))
		payload["ompRelayInstance"] = s.Instance
		writeJSON(w, http.StatusOK, payload)
		return
	}

	// GET /json or /json/list (upstream: server.ts:210-212)
	if path == "/json" || path == "/json/list" {
		writeJSON(w, http.StatusOK, s.Bridge.ListTargets())
		return
	}

	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("Not found"))
}

func (s *RelayServer) serveWS(ws *WSConn, role, extID, claim string) {
	sock := &wsRelaySocket{ws: ws}
	s.mu.Lock()
	select {
	case <-s.stopCh:
		s.mu.Unlock()
		_ = ws.Close()
		return
	default:
	}
	s.sockets[sock] = struct{}{}
	s.mu.Unlock()

	var connID int
	if role == "ext" {
		s.Bridge.ExtConnected(sock, extID)
	} else {
		connID = s.Bridge.CDPConnected(sock, claim)
	}

	defer func() {
		s.mu.Lock()
		delete(s.sockets, sock)
		s.mu.Unlock()
		if role == "ext" {
			s.Bridge.ExtClosed(sock)
		} else {
			s.Bridge.CDPClosed(connID)
		}
		_ = ws.Close()
	}()

	for {
		msg, err := ws.ReadMessage()
		if err != nil {
			return
		}
		text := string(msg)
		if role == "ext" {
			s.Bridge.ExtMessage(sock, text)
		} else {
			s.Bridge.CDPMessage(connID, text)
		}
	}
}

func (s *RelayServer) keepaliveLoop() {
	ticker := time.NewTicker(wsKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			socks := make([]*wsRelaySocket, 0, len(s.sockets))
			for sock := range s.sockets {
				socks = append(socks, sock)
			}
			s.mu.Unlock()
			for _, sock := range socks {
				_ = sock.ws.WritePing(nil)
			}
		}
	}
}

// Stop closes the listener and every active WebSocket connection immediately.
// upstream: server.ts:258-261
func (s *RelayServer) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		_ = s.httpServer.Close()
		_ = s.ln.Close()
		s.mu.Lock()
		socks := make([]*wsRelaySocket, 0, len(s.sockets))
		for sock := range s.sockets {
			socks = append(socks, sock)
		}
		clear(s.sockets)
		s.mu.Unlock()
		for _, sock := range socks {
			_ = sock.ws.Close()
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
