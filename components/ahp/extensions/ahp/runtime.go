package ahp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/compose"
	"github.com/MichaelKinsy/pigpen/ahp/internal/live"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/settings"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
	"github.com/MichaelKinsy/pigpen/ahp/internal/ws"
)

// Version is reported to clients as the server version.
const Version = "0.1.0"

// runtime owns the (optional) listener of one PiG process.
type runtime struct {
	live *live.Session

	mu      sync.Mutex
	server  *ws.Server
	built   *compose.Built
	addr    string
	ctx     sdk.Context
	haveCtx bool
}

func newRuntime() *runtime { return &runtime{live: live.New()} }

func (rt *runtime) status() string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.server == nil {
		return "AHP listener is off. /ahp start opens it (settings: " + settingsPath(nil) + ")"
	}
	return rt.addr
}

// listenNotice is the message printed when the listener is up. The first address is the form VS Code takes
// (ws://<host>:<port>?tkn=<token>) and is followed by a space: VS Code's SSH launcher (chat.sshRemoteAgentHostCommand) and
// "Agents: Add Remote Agent Host..." both read the first ws:// URL in the text, and the launcher's token runs to the next
// space or '&'. The generic form (/?token=) follows for other AHP clients; the listener takes either. hostport is the
// listener's address. A listener without a token has one address and no token form.
func listenNotice(hostport, token string) string {
	if token == "" {
		return "AHP listening on ws://" + hostport
	}
	escaped := url.QueryEscape(token)
	return "AHP listening on ws://" + hostport + "?tkn=" + escaped +
		" - for VS Code, paste it into \"Agents: Add Remote Agent Host...\"; other AHP clients can also use ws://" + hostport + "/?token=" + escaped
}

// settingsPath is the settings file: the --ahp-settings flag, else <pig home>/ahp/settings.json.
func settingsPath(ctx *sdk.Context) string {
	if ctx != nil {
		if v, err := ctx.GetFlag(flagSettings); err == nil {
			if path, ok := v.(string); ok && path != "" {
				return path
			}
		}
	}
	if home := os.Getenv("PIG_HOME"); home != "" {
		return filepath.Join(home, "ahp", "settings.json")
	}
	if dir, err := os.UserHomeDir(); err == nil {
		return filepath.Join(dir, ".pig", "ahp", "settings.json")
	}
	return filepath.Join(".pig", "ahp", "settings.json")
}

// onSessionStart opens the listener when --ahp was given. PiG emits session_shutdown before a
// replacement session starts (and re-runs the extension factory), so a listener opened with
// /ahp start has already been closed here; only --ahp brings it back for the new session.
func (rt *runtime) onSessionStart(ctx sdk.Context, id, cwd, reason string) {
	rt.mu.Lock()
	rt.ctx, rt.haveCtx = ctx, true
	rt.mu.Unlock()
	requested := false
	if v, err := ctx.GetFlag(flagStart); err == nil {
		requested, _ = v.(bool)
	}
	if requested {
		if addr, err := rt.start(ctx); err != nil {
			ctx.Notify("AHP: "+err.Error(), "error")
		} else {
			ctx.Notify(addr, "info")
		}
	}
}

func (rt *runtime) onSessionShutdown(ctx sdk.Context, reason string) {
	restarts := false
	if v, err := ctx.GetFlag(flagStart); err == nil {
		restarts, _ = v.(bool)
	}
	if notice := replacementNotice(reason, rt.stop(), restarts); notice != "" {
		ctx.Notify(notice, "warning")
	}
}

func (rt *runtime) onModelChanged() {
	rt.mu.Lock()
	built := rt.built
	rt.mu.Unlock()
	if built == nil {
		return
	}
	built.Host.DispatchServerAction(wire.RootChannel, ahptypes.StateAction{Value: &ahptypes.RootAgentsChangedAction{
		Type: ahptypes.ActionTypeRootAgentsChanged, Agents: []ahptypes.AgentInfo{pi.BuildAgentInfo(rt.live.Models())},
	}})
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// start opens the listener and returns the notice to show (listenNotice). It is only ever called on the user's explicit request.
func (rt *runtime) start(ctx sdk.Context) (string, error) {
	rt.mu.Lock()
	if rt.server != nil {
		addr := rt.addr
		rt.mu.Unlock()
		return addr, nil
	}
	rt.mu.Unlock()
	if rt.live.ID() == "" {
		return "", errors.New("no PiG session is running yet")
	}
	path := settingsPath(&ctx)
	direct, err := settings.LoadDirect(path)
	if err != nil {
		return "", err
	}
	access, err := settings.LoadAccess(path)
	if err != nil {
		return "", err
	}
	token := ""
	if direct.Token != nil {
		token = *direct.Token
	}
	if err := requireTokenOffLoopback(path, direct.Host, token); err != nil {
		return "", err
	}

	cwd := rt.live.WorkingDirectory()
	sessionRoot := sessionRootOf(ctx)
	opts := compose.Options{
		ServerInfo:       &ahptypes.Implementation{Name: "pigpen-ahp", Version: strPtr(Version)},
		WorkingDirectory: cwd, SessionRoot: sessionRoot,
		Models:               rt.live.Models,
		CreateBackend:        rt.live.BackendFactory,
		CreateSessionManager: rt.createStore,
		DefaultSelection:     rt.live.CurrentSelection,
		DeleteFile:           rt.deleteFile(access),
		WrapStore:            rt.wrapStore,
		AdoptForeignTurns:    true,
		Log:                  func(format string, args ...any) { fmt.Fprintf(os.Stderr, "ahp: "+format+"\n", args...) },
	}
	opts.Filesystem, opts.Terminals = accessOptions(access, cwd)
	built, err := compose.Build(opts)
	if err != nil {
		return "", err
	}
	if err := rt.registerLiveSession(built, cwd); err != nil {
		built.Close()
		return "", err
	}
	server, err := ws.Serve(built.Host, ws.Options{
		Addr: net.JoinHostPort(direct.Host, strconv.Itoa(direct.Port)), Token: token, AllowedOrigins: access.AllowedOrigins,
	})
	if err != nil {
		built.Close()
		return "", err
	}
	addr := listenNotice(server.Addr().String(), token)
	rt.mu.Lock()
	rt.server, rt.built, rt.addr = server, built, addr
	rt.mu.Unlock()
	return addr, nil
}

// stop closes the listener and the services behind it; false when nothing was running.
func (rt *runtime) stop() bool {
	rt.mu.Lock()
	server, built := rt.server, rt.built
	rt.server, rt.built, rt.addr = nil, nil, ""
	rt.mu.Unlock()
	if server == nil {
		return false
	}
	_ = server.Close()
	built.Close()
	return true
}

func sessionRootOf(ctx sdk.Context) string {
	if f, err := ctx.GetSessionFile(); err == nil && f != nil {
		// <root>/<encoded cwd>/<file>.jsonl
		return filepath.Dir(filepath.Dir(*f))
	}
	return ""
}

func strPtr(s string) *string { return &s }

// createStore is the storage for sessions created through the host: only the running session
// exists, so a client cannot make new ones.
func (rt *runtime) createStore(cwd, id string) (pi.SessionStore, error) {
	if id == rt.live.ID() {
		return rt.live.NewStore(), nil
	}
	return nil, errors.New("this host serves the running PiG session only; a client cannot create sessions")
}

func (rt *runtime) wrapStore(sessionID string, opened pi.SessionStore) pi.SessionStore {
	if sessionID == rt.live.ID() {
		return rt.live.NewStore()
	}
	return opened
}

// deleteFile refuses to delete anything unless the settings allow it, and never the running
// session's file.
func (rt *runtime) deleteFile(access settings.Access) func(string) (pi.SessionFileDeletionResult, error) {
	return guardedDelete(access.AllowSessionDeletion, func() string { return rt.live.NewStore().File() }, pi.FileDeleter)
}

func guardedDelete(allow bool, liveFile func() string, remove func(string) (pi.SessionFileDeletionResult, error)) func(string) (pi.SessionFileDeletionResult, error) {
	return func(path string) (pi.SessionFileDeletionResult, error) {
		if !allow {
			return pi.SessionFileDeletionResult{Error: "deleting sessions from a client is disabled (settings: allowSessionDeletion)"}, nil
		}
		if live := liveFile(); live != "" && filepath.Clean(live) == filepath.Clean(path) {
			return pi.SessionFileDeletionResult{Error: "the running PiG session cannot be deleted from a client"}, nil
		}
		return remove(path)
	}
}

// accessOptions turns the capability settings into the optional services of the composition:
// nil (not exposed) unless the settings enable them. A filesystem without roots is confined to
// the working directory; only an explicit unrestricted lifts that.
func accessOptions(access settings.Access, cwd string) (*compose.FilesystemOptions, *compose.TerminalOptions) {
	var fs *compose.FilesystemOptions
	var terminals *compose.TerminalOptions
	if access.Filesystem.Enabled {
		roots := access.Filesystem.Roots
		if len(roots) == 0 && !access.Filesystem.Unrestricted {
			roots = []string{cwd}
		}
		fs = &compose.FilesystemOptions{Roots: roots, Unrestricted: access.Filesystem.Unrestricted}
	}
	if access.Terminals.Enabled {
		terminals = &compose.TerminalOptions{}
	}
	return fs, terminals
}

// replacementNotice is what the user is told when a running listener closes with its session
// (session_shutdown for new, resume, fork or reload). With --ahp the next session opens it
// again, so there is nothing to say; after /ahp start it stays closed until asked again.
func replacementNotice(reason string, running, restarts bool) string {
	if !running || restarts || reason == "quit" {
		return ""
	}
	return "AHP listener stopped: the session was replaced (" + reason + "). /ahp start serves the new session."
}

// requireTokenOffLoopback refuses to expose an unauthenticated listener beyond this machine.
func requireTokenOffLoopback(path, host, token string) error {
	if !isLoopback(host) && token == "" {
		return fmt.Errorf("%s: host %q is not loopback, so a token is required", path, host)
	}
	return nil
}

// registerLiveSession makes the running session available to clients: hydrated from its file
// when it has history on disk, created empty otherwise, then its agent attached so events are
// mirrored from the start.
func (rt *runtime) registerLiveSession(built *compose.Built, cwd string) error {
	id := rt.live.ID()
	session := wire.SessionURI(id)
	ctx := context.Background()
	hydrated, err := built.Sessions.Hydrator.Hydrate(ctx, session)
	if err != nil {
		return err
	}
	if !hydrated {
		if err := built.Sessions.Registry.Create(ctx, ahptypes.CreateSessionParams{Channel: session, WorkingDirectories: []ahptypes.URI{wire.PathToFileURI(cwd)}}); err != nil {
			return err
		}
	}
	return built.Sessions.Registry.EnsureBackend(session)
}
