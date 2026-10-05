package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/pigpen/ahp/internal/channels"
	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

// TerminalOptions configures a TerminalService.
type TerminalOptions struct {
	// DefaultWorkingDirectory is where a terminal starts when the client names no cwd.
	DefaultWorkingDirectory string
	// Shell is the program to run; empty means $SHELL, then sh.
	Shell string
	Log   func(string)
	// Spawn opens the pseudoterminal; nil means SpawnPty.
	Spawn PtySpawner
	// Clock schedules output batching; nil means the wall clock.
	Clock Clock
}

const (
	defaultCols          = 80
	defaultRows          = 24
	maxDimension         = 65535
	outputBatchDelay     = 8 * time.Millisecond
	outputBatchThreshold = 16 * 1024
)

var terminalChannelPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:/[^/?#]+$`)

type terminalEntry struct {
	pty PtyProcess

	// guarded by TerminalService.mu
	pending    string
	flushTimer Stopper
	detached   bool // listeners are gone (exited or removed)
}

// TerminalService owns the PTYs behind client-owned terminal channels (port of
// terminal-service.ts). The PTY is the client's shell on this machine: the extension only builds a
// service when the user explicitly enabled terminals, and only for the client that claimed it.
// It implements host.TerminalHandler.
type TerminalService struct {
	host  *host.Host
	opts  TerminalOptions
	spawn PtySpawner
	clock Clock

	createMu sync.Mutex // serialises check-and-spawn so duplicate creates spawn once

	mu      sync.Mutex
	entries map[string]*terminalEntry
	closed  bool

	unhookAction    func()
	unhookValidator func()
}

// NewTerminalService creates the service and hooks it to client actions on terminal channels.
func NewTerminalService(h *host.Host, opts TerminalOptions) *TerminalService {
	s := &TerminalService{host: h, opts: opts, spawn: opts.Spawn, clock: opts.Clock, entries: map[string]*terminalEntry{}}
	if s.spawn == nil {
		s.spawn = SpawnPty
	}
	if s.clock == nil {
		s.clock = realClock{}
	}
	s.unhookValidator = h.AddClientActionValidator(s.validateClientAction)
	s.unhookAction = h.OnClientAction(s.applyClientAction)
	return s
}

func validDimension(v int64) bool { return v > 0 && v <= maxDimension }

func dimension(value *int64, fallback int64, name string) (int64, error) {
	result := fallback
	if value != nil {
		result = *value
	}
	if !validDimension(result) {
		return 0, wire.InvalidParams(fmt.Sprintf("%s must be an integer between 1 and %d", name, maxDimension))
	}
	return result, nil
}

func defaultShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "sh"
}

func (s *TerminalService) assertAvailable(channel string) error {
	s.mu.Lock()
	_, taken := s.entries[channel]
	s.mu.Unlock()
	if taken || s.host.Store().Has(channel) {
		return wire.Coded(wire.CodeAlreadyExists, "Terminal already exists: "+channel)
	}
	return nil
}

// Create implements host.TerminalHandler.
func (s *TerminalService) Create(_ context.Context, params ahptypes.CreateTerminalParams, clientID string) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("Terminal service is closed")
	}
	channel := params.Channel
	if !terminalChannelPattern.MatchString(channel) {
		return wire.InvalidParams("createTerminal requires a URI channel")
	}
	if kind, ok := wire.KindOf(channel); ok && kind != wire.KindTerminal {
		return wire.InvalidParams(fmt.Sprintf("Channel belongs to %s, not terminal: %s", kind, channel))
	}
	if _, ok := wire.SessionIDFromURI(channel, host.ProviderScheme); ok {
		return wire.InvalidParams(fmt.Sprintf("Channel uses the %s session scheme: %s", host.ProviderScheme, channel))
	}
	if err := s.assertAvailable(channel); err != nil {
		return err
	}
	claim, ok := params.Claim.Value.(*ahptypes.TerminalClientClaim)
	if !ok {
		return wire.InvalidParams("Only client-owned terminals are supported")
	}
	if clientID == "" || claim.ClientId != clientID {
		return wire.InvalidParams("Terminal claim must match the initialized client")
	}
	cols, err := dimension(params.Cols, defaultCols, "cols")
	if err != nil {
		return err
	}
	rows, err := dimension(params.Rows, defaultRows, "rows")
	if err != nil {
		return err
	}
	cwdURI := wire.PathToFileURI(s.opts.DefaultWorkingDirectory)
	if params.Cwd != nil {
		cwdURI = *params.Cwd
	}
	if len(cwdURI) < 7 || cwdURI[:7] != "file://" {
		return wire.InvalidParams("Terminal cwd must be a file: URI")
	}
	cwd, err := wire.FileURIToPath(cwdURI)
	if err != nil {
		return wire.InvalidParams("Invalid terminal cwd: " + cwdURI)
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return wire.NotFound(cwdURI)
	}
	if !info.IsDir() {
		return wire.InvalidParams("Terminal cwd is not a directory: " + cwdURI)
	}

	s.createMu.Lock()
	defer s.createMu.Unlock()
	if err := s.assertAvailable(channel); err != nil {
		return err
	}
	shell := s.opts.Shell
	if shell == "" {
		shell = defaultShell()
	}
	title := filepath.Base(shell)
	if params.Name != nil {
		title = *params.Name
	}
	entry := &terminalEntry{}
	handlers := PtyHandlers{
		OnData: func(data string) { s.onData(channel, entry, data) },
		OnExit: func(code int) { s.onExit(channel, entry, code) },
	}
	pty, err := s.spawn(shell, []string{}, PtyOptions{Name: "xterm-256color", Cols: int(cols), Rows: int(rows), Cwd: cwd}, handlers)
	if err != nil {
		return wire.Coded(wire.CodeInternalError, "Cannot start terminal: "+err.Error())
	}
	stateCwd := wire.PathToFileURI(cwd)
	isPty := true
	state := ahptypes.TerminalState{
		Title: title, Cwd: &stateCwd, Cols: &cols, Rows: &rows, Content: []ahptypes.TerminalContentPart{},
		Lifecycle: ahptypes.TerminalLifecycleState{Value: &ahptypes.TerminalRunningLifecycleState{Status: ahptypes.TerminalLifecycleStatusRunning}},
		Claim:     ahptypes.TerminalClaim{Value: &ahptypes.TerminalClientClaim{Kind: ahptypes.TerminalClaimKindClient, ClientId: clientID}},
		IsPty:     &isPty,
	}
	entry.pty = pty
	s.mu.Lock()
	s.entries[channel] = entry
	s.mu.Unlock()
	if err := s.host.Store().Create(channel, &state, wire.KindTerminal); err != nil {
		s.mu.Lock()
		delete(s.entries, channel)
		s.mu.Unlock()
		_ = pty.Kill()
		return err
	}
	s.publishCatalogue()
	return nil
}

// Dispose implements host.TerminalHandler; disposing an unknown terminal is a no-op.
func (s *TerminalService) Dispose(_ context.Context, channel string) error {
	s.mu.Lock()
	entry := s.entries[channel]
	s.mu.Unlock()
	if entry != nil {
		s.remove(channel, entry, true)
	}
	return nil
}

// Shutdown kills every running PTY and stops listening; further calls do nothing.
func (s *TerminalService) Shutdown() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	type pair struct {
		channel string
		entry   *terminalEntry
	}
	var all []pair
	for channel, entry := range s.entries {
		all = append(all, pair{channel, entry})
	}
	s.mu.Unlock()
	s.unhookValidator()
	s.unhookAction()
	for _, p := range all {
		s.remove(p.channel, p.entry, false)
	}
	if len(all) > 0 {
		s.publishCatalogue()
	}
}

func (s *TerminalService) entry(channel string) *terminalEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[channel]
}

// ── client actions ──────────────────────────────────────────────────────

func (s *TerminalService) validateClientAction(channel string, action host.ClientAction, clientID string) string {
	if kind, _ := s.host.Store().KindOf(channel); kind != wire.KindTerminal {
		return ""
	}
	state := s.host.Store().Terminal(channel)
	if state == nil || s.entry(channel) == nil {
		return "Terminal process is unavailable"
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(action.Raw, &fields)
	isString := func(name string) bool {
		var v string
		return json.Unmarshal(fields[name], &v) == nil && fields[name] != nil
	}
	isDim := func(name string) bool {
		var v float64
		return json.Unmarshal(fields[name], &v) == nil && v == math.Trunc(v) && validDimension(int64(v))
	}
	switch action.Type {
	case string(ahptypes.ActionTypeTerminalClaimed):
		return "This host does not support transferring terminal claims"
	case string(ahptypes.ActionTypeTerminalInput):
		if !isString("data") {
			return "Terminal input must be a string"
		}
	case string(ahptypes.ActionTypeTerminalResized):
		if !isDim("cols") || !isDim("rows") {
			return fmt.Sprintf("Terminal dimensions must be integers between 1 and %d", maxDimension)
		}
	case string(ahptypes.ActionTypeTerminalTitleChanged):
		if !isString("title") {
			return "Terminal title must be a string"
		}
	case string(ahptypes.ActionTypeTerminalCleared):
	default:
		return fmt.Sprintf("This host does not accept %s on terminal channels", action.Type)
	}
	claim, ok := state.Claim.Value.(*ahptypes.TerminalClientClaim)
	if !ok || claim.ClientId != clientID {
		return "This terminal is claimed by another client"
	}
	if action.Type == string(ahptypes.ActionTypeTerminalInput) || action.Type == string(ahptypes.ActionTypeTerminalResized) {
		if _, running := state.Lifecycle.Value.(*ahptypes.TerminalRunningLifecycleState); !running {
			return "Terminal process has exited"
		}
	}
	return ""
}

func (s *TerminalService) applyClientAction(channel string, action ahptypes.StateAction) {
	entry := s.entry(channel)
	if entry == nil {
		return
	}
	switch a := action.Value.(type) {
	case *ahptypes.TerminalInputAction:
		if err := entry.pty.Write(a.Data); err != nil && s.opts.Log != nil {
			s.opts.Log(fmt.Sprintf("terminal %s write failed: %v", channel, err))
		}
	case *ahptypes.TerminalResizedAction:
		if err := entry.pty.Resize(int(a.Cols), int(a.Rows)); err != nil && s.opts.Log != nil {
			s.opts.Log(fmt.Sprintf("terminal %s resize failed: %v", channel, err))
		}
	case *ahptypes.TerminalTitleChangedAction:
		s.publishCatalogue()
	case *ahptypes.TerminalClearedAction:
		s.discardPending(entry)
	}
}

// ── output ──────────────────────────────────────────────────────────────

func (s *TerminalService) onData(channel string, entry *terminalEntry, data string) {
	if data == "" {
		return
	}
	s.mu.Lock()
	if s.entries[channel] != entry {
		s.mu.Unlock()
		return
	}
	entry.pending += data
	if utf8.RuneCountInString(entry.pending) >= outputBatchThreshold {
		s.mu.Unlock()
		s.flushPending(channel, entry)
		return
	}
	if entry.flushTimer == nil {
		entry.flushTimer = s.clock.AfterFunc(outputBatchDelay, func() { s.flushPending(channel, entry) })
	}
	s.mu.Unlock()
}

func (s *TerminalService) flushPending(channel string, entry *terminalEntry) {
	s.mu.Lock()
	if entry.flushTimer != nil {
		entry.flushTimer.Stop()
		entry.flushTimer = nil
	}
	data := entry.pending
	entry.pending = ""
	live := s.entries[channel] == entry
	s.mu.Unlock()
	if !live || data == "" {
		return
	}
	s.host.DispatchServerAction(channel, ahptypes.StateAction{Value: &ahptypes.TerminalDataAction{Type: ahptypes.ActionTypeTerminalData, Data: data}})
}

func (s *TerminalService) discardPending(entry *terminalEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry.flushTimer != nil {
		entry.flushTimer.Stop()
		entry.flushTimer = nil
	}
	entry.pending = ""
}

func (s *TerminalService) onExit(channel string, entry *terminalEntry, exitCode int) {
	s.mu.Lock()
	live := s.entries[channel] == entry && !entry.detached
	s.mu.Unlock()
	if !live {
		return
	}
	state := s.host.Store().Terminal(channel)
	if state == nil {
		return
	}
	if _, exited := state.Lifecycle.Value.(*ahptypes.TerminalExitedLifecycleState); exited {
		return
	}
	s.flushPending(channel, entry)
	s.mu.Lock()
	entry.detached = true
	s.mu.Unlock()
	code := int64(exitCode)
	s.host.DispatchServerAction(channel, ahptypes.StateAction{Value: &ahptypes.TerminalExitedAction{Type: ahptypes.ActionTypeTerminalExited, ExitCode: &code}})
	s.publishCatalogue()
}

func (s *TerminalService) remove(channel string, entry *terminalEntry, notify bool) {
	s.mu.Lock()
	if s.entries[channel] != entry {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if notify {
		s.flushPending(channel, entry)
	} else {
		s.discardPending(entry)
	}
	state := s.host.Store().Terminal(channel)
	s.mu.Lock()
	delete(s.entries, channel)
	entry.detached = true
	s.mu.Unlock()
	if state != nil {
		if _, running := state.Lifecycle.Value.(*ahptypes.TerminalRunningLifecycleState); running {
			if notify {
				s.host.DispatchServerAction(channel, ahptypes.StateAction{Value: &ahptypes.TerminalExitedAction{Type: ahptypes.ActionTypeTerminalExited}})
			}
			if err := entry.pty.Kill(); err != nil && s.opts.Log != nil {
				s.opts.Log(fmt.Sprintf("cannot kill terminal %s: %v", channel, err))
			}
		}
	}
	s.host.DeleteChannel(channel)
	if notify {
		s.publishCatalogue()
	}
}

func (s *TerminalService) publishCatalogue() {
	s.mu.Lock()
	channels_ := make([]string, 0, len(s.entries))
	for channel := range s.entries {
		channels_ = append(channels_, channel)
	}
	s.mu.Unlock()
	sort.Strings(channels_)
	terminals := []ahptypes.TerminalInfo{}
	for _, resource := range channels_ {
		if state := s.host.Store().Terminal(resource); state != nil {
			terminals = append(terminals, channels.TerminalInfoOf(resource, state))
		}
	}
	s.host.DispatchServerAction(wire.RootChannel, ahptypes.StateAction{Value: &ahptypes.RootTerminalsChangedAction{Type: ahptypes.ActionTypeRootTerminalsChanged, Terminals: terminals}})
}
