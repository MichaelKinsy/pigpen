package ahp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/ahp/internal/settings"
	"github.com/MichaelKinsy/pigpen/ahp/internal/tunnel"
)

// prepareTunnel is the setup half of pi-ahp's `pi-ahp-tunnel` (src/bin/tunnel.ts): require a
// login, fold the requested display name, reuse this host's tunnel or create one, and make sure
// its fixed port forwards HTTP. It returns the tunnel id. log receives the same lines the
// upstream program writes to standard error.
func prepareTunnel(cli *tunnel.CLI, requested string, log func(string)) (string, error) {
	if err := cli.RequireLogin(); err != nil {
		return "", err
	}
	name, haveName := "", false
	if requested != "" {
		name, haveName = tunnel.NameLabel(requested)
		if !haveName {
			log("ignoring --name " + requested + ": nothing left after removing characters a label cannot hold")
		}
	}

	existing, err := cli.FindTunnel()
	if err != nil {
		return "", err
	}
	var id string
	if existing != nil {
		id = existing.TunnelID
		current, _ := tunnel.DisplayLabel(existing.Labels)
		renamed := haveName && name != current
		if renamed {
			if err := cli.Rename(id, current, name); err != nil {
				return "", err
			}
		}
		suffix := ""
		if renamed {
			suffix = ", renamed to " + name
		}
		log("tunnel: " + id + " (reused" + suffix + ")")
	} else {
		id, err = cli.CreateTunnel(name)
		if err != nil {
			return "", err
		}
		suffix := ""
		if haveName {
			suffix = ", named " + name
		}
		log("tunnel: " + id + " (new" + suffix + ")")
	}
	if err := cli.EnsurePort(id); err != nil {
		return "", err
	}
	return id, nil
}

// hostStopTimeout is how long `devtunnel host` gets to take the stop request before it is killed:
// a child that will not take SIGTERM should not wedge the session, an orphaned relay is the lesser
// failure.
var hostStopTimeout = 5 * time.Second

// hostedTunnel is a running `devtunnel host <id>`, supervised until it exits or is stopped.
type hostedTunnel struct {
	id   string
	cmd  *exec.Cmd
	done chan struct{} // closed when the process has exited and its output is drained

	mu       sync.Mutex
	stopping bool
	exitErr  error
}

// hostCommand builds the command that forwards a tunnel to the local listener. A variable so a
// test can substitute a harmless process.
var hostCommand = func(id string) *exec.Cmd { return exec.Command("devtunnel", "host", id) }

// hostTunnel starts `devtunnel host <id>`. Its output lines go to output (the command's own
// words are the only place the connect address is printed); onExit runs once when the process
// has ended for any reason other than stop.
func hostTunnel(id string, output func(line string), onExit func(failure string)) (*hostedTunnel, error) {
	cmd := hostCommand(id)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("`devtunnel` not found on PATH")
		}
		return nil, fmt.Errorf("devtunnel host failed: %w", err)
	}
	h := &hostedTunnel{id: id, cmd: cmd, done: make(chan struct{})}
	var readers sync.WaitGroup
	for _, r := range []io.Reader{stdout, stderr} {
		readers.Add(1)
		go func() {
			defer readers.Done()
			scanner := bufio.NewScanner(r)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				if line := strings.TrimRight(scanner.Text(), "\r"); line != "" {
					output(line)
				}
			}
		}()
	}
	go func() {
		readers.Wait()
		err := cmd.Wait()
		h.mu.Lock()
		h.exitErr = err
		stopping := h.stopping
		h.mu.Unlock()
		close(h.done)
		if stopping {
			return
		}
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				onExit(fmt.Sprintf("devtunnel host exited (%d); shutting down", exit.ExitCode()))
				return
			}
			onExit("devtunnel host failed: " + err.Error())
			return
		}
		onExit("devtunnel host exited (0); shutting down")
	}()
	return h, nil
}

// stop asks the process to end and waits for it, killing it if it does not.
func (h *hostedTunnel) stop() {
	h.mu.Lock()
	h.stopping = true
	h.mu.Unlock()
	select {
	case <-h.done:
		return
	default:
	}
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Signal(stopSignal)
	}
	select {
	case <-h.done:
	case <-time.After(hostStopTimeout):
		if h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
		}
		<-h.done
	}
}

// loadTunnelAccess reads the capability opt-ins of the settings file when there is one. A tunnel
// does not read or modify the direct listener's endpoint settings, so it neither creates the file
// nor needs it: without one nothing beyond the session is exposed.
func loadTunnelAccess(path string) (settings.Access, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return settings.Access{}, nil
		}
		return settings.Access{}, err
	}
	return settings.LoadAccess(path)
}

// hostOutputNotices is how many lines of `devtunnel host` are shown as notifications; the first
// ones carry the connect address, the rest only go to the log.
const hostOutputNotices = 12

// startTunnel serves /ahp tunnel [name], the counterpart of running `pi-ahp-tunnel [--name <label>]`:
// find or create this host's dev tunnel, serve the session on the fixed port 31546 without a URL
// token (Dev Tunnels controls remote access), and run `devtunnel host` to forward it. Setup talks to
// the CLI, which can take seconds, so it runs in the background and reports through notifications.
func (rt *runtime) startTunnel(ctx sdk.Context, requested string) error {
	rt.mu.Lock()
	switch {
	case rt.server != nil:
		rt.mu.Unlock()
		return errors.New("the AHP listener is already running; /ahp stop it first (a tunnel serves its own listener on 127.0.0.1:" + strconv.Itoa(tunnel.Port) + ", without a token)")
	case rt.tunnelStarting:
		rt.mu.Unlock()
		return errors.New("the dev tunnel is already starting")
	}
	if rt.live.ID() == "" {
		rt.mu.Unlock()
		return errors.New("no PiG session is running yet")
	}
	rt.tunnelStarting = true
	rt.mu.Unlock()
	go rt.runTunnel(ctx, requested)
	return nil
}

func (rt *runtime) stillStarting() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.tunnelStarting
}

func (rt *runtime) runTunnel(ctx sdk.Context, requested string) {
	notify := func(message, level string) {
		defer func() { _ = recover() }()
		ctx.Notify(message, level)
	}
	fail := func(err error) {
		rt.stop()
		notify("AHP tunnel: "+err.Error(), "error")
	}
	id, err := prepareTunnel(tunnel.NewCLI(rt.devtunnel), requested, func(line string) { notify(line, "info") })
	if err != nil {
		if rt.stillStarting() {
			fail(err)
		}
		return
	}
	if !rt.stillStarting() {
		return // /ahp stop during setup
	}
	access, err := loadTunnelAccess(settingsPath(&ctx))
	if err != nil {
		fail(err)
		return
	}
	// The host must be up before the tunnel forwards to it, or the first client through the relay
	// hits a closed port.
	if _, err := rt.listen(ctx, access, "127.0.0.1", tunnel.Port, ""); err != nil {
		fail(err)
		return
	}
	var shown int
	var shownMu sync.Mutex
	hosted, err := hostTunnel(id, func(line string) {
		fmt.Fprintf(os.Stderr, "ahp: devtunnel host: %s\n", line)
		shownMu.Lock()
		shown++
		show := shown <= hostOutputNotices
		shownMu.Unlock()
		if show {
			notify("devtunnel host: "+line, "info")
		}
	}, func(failure string) {
		notify(failure, "error")
		rt.stop()
	})
	if err != nil {
		fail(err)
		return
	}
	rt.mu.Lock()
	if !rt.tunnelStarting {
		rt.mu.Unlock()
		hosted.stop()
		rt.stop()
		return
	}
	rt.tunnelStarting = false
	rt.tunnel, rt.tunnelID = hosted, id
	rt.addr = "AHP dev tunnel " + id + ": listening on ws://127.0.0.1:" + strconv.Itoa(tunnel.Port) +
		" (no URL token; remote access is controlled by Microsoft Dev Tunnels)"
	notice := rt.addr
	rt.mu.Unlock()
	notify(notice, "info")
	notify("Connect from a Dev Tunnel-capable AHP client signed in to the same account.", "info")
}
