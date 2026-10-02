// Package pi_typesafe is a Go port of pi-typesafe's extension half
// (https://github.com/DevMortimer/pi-typesafe, MIT, Ryan Gapac): the typesafe_evaluate tool that hands
// batched Choice, Score and Noul questions to Jev, the /typesafe command (login, consent, usage, sample
// test, JSON playground), and the result renderers. The typed API other extensions build on is the library
// package components/pi-typesafe.
//
// The tool is off until the operator runs /typesafe enable or sets PI_TYPESAFE_ENABLED=1. Content submitted
// goes to api.typesafe.ai, unless the own-model backend is selected (PI_TYPESAFE_BACKEND=ownmodel or
// /typesafe backend ownmodel): then it goes to the provider of the model PiG is configured with.
package pi_typesafe

import (
	"context"
	"os"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	pitypesafe "github.com/MichaelKinsy/pigpen/components/pi-typesafe-api"
	"github.com/MichaelKinsy/pigpen/components/pi-typesafe-api/hostmodel"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/ownmodel"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

const (
	toolName    = "typesafe_evaluate"
	entryType   = "typesafe-result"
	statusType  = "typesafe-status"
	envEnabled  = "PI_TYPESAFE_ENABLED"
	envBackend  = "PI_TYPESAFE_BACKEND"
	typesafeMsg = "Submitted state and questions will be sent to api.typesafe.ai and may incur charges. Do not include secrets. The extension does not collect files or conversation history. Results are model judgments, not proof or authorization."
)

// Options inject the parts a test or an embedder replaces; the zero value is the production extension.
type Options struct {
	// HTTPClient is the transport for the TypeSafe API.
	HTTPClient typesafe.HTTPDoer
	// Ledger is the usage ledger; nil uses the store next to the key.
	Ledger pitypesafe.UsageLedger
	// Evaluator replaces the own-model evaluator (which reads PiG's configured model).
	Evaluator typesafe.Evaluator
}

// Extension returns the extension.
func Extension() *sdk.Extension { return New(Options{}) }

// state is one session's mutable configuration.
type state struct {
	mu        sync.Mutex
	opts      Options
	ext       *sdk.Extension
	enabled   bool
	backend   string
	client    *pitypesafe.TypeSafe
	calledOut string
	// consent counts backend switches. Consent is per destination: a call or a dialog that was admitted under
	// one value must not be sent after it changed.
	consent int
}

// New returns the extension with injected parts.
func New(opts Options) *sdk.Extension {
	s := &state{opts: opts, ext: sdk.New("pi-typesafe")}
	s.enabled = os.Getenv(envEnabled) == "1"
	s.backend = backendFromEnv()
	s.registerTool()
	s.ext.EntryRenderer(entryType, s.renderEntry)
	s.ext.OnSessionStart(s.onSessionStart)
	s.registerCommand()
	return s.ext
}

func backendFromEnv() string {
	if strings.TrimSpace(os.Getenv(envBackend)) == pitypesafe.BackendOwnModel {
		return pitypesafe.BackendOwnModel
	}
	return pitypesafe.BackendTypeSafe
}

func (s *state) isEnabled() bool        { s.mu.Lock(); defer s.mu.Unlock(); return s.enabled }
func (s *state) setEnabled(v bool)      { s.mu.Lock(); s.enabled = v; s.mu.Unlock() }
func (s *state) currentBackend() string { s.mu.Lock(); defer s.mu.Unlock(); return s.backend }

// admit reads the consent gate and the destination it was given for under one lock.
func (s *state) admit() (enabled bool, consent int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled, s.consent
}

// consentNow is the current destination's consent generation.
func (s *state) consentNow() int { s.mu.Lock(); defer s.mu.Unlock(); return s.consent }

// errDestinationChanged stops a call or a dialog whose destination changed after it was admitted.
var errDestinationChanged = &pitypesafe.IntegrationError{Code: pitypesafe.CodeConfiguration, Message: "The judgment backend changed after this request was admitted; nothing was sent. Run /typesafe enable to consent to the new destination."}

// clientFor is getClient for a request admitted under consent generation consent: it fails with
// errDestinationChanged when a backend switch happened since, so the request cannot reach a destination
// nobody consented to. A switch after it returns does not affect the request (in-flight requests continue).
func (s *state) clientFor(consent int) (*pitypesafe.TypeSafe, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consent != consent {
		return nil, errDestinationChanged
	}
	return s.clientLocked()
}

// clientLocked builds the client on first use (s.mu held); a new session or a login clears it.
func (s *state) clientLocked() (*pitypesafe.TypeSafe, error) {
	if s.client != nil {
		return s.client, nil
	}
	options := pitypesafe.Options{Backend: s.backend, HTTPClient: s.opts.HTTPClient, Ledger: s.opts.Ledger}
	if s.backend == pitypesafe.BackendOwnModel {
		options.Evaluator = s.opts.Evaluator
		if options.Evaluator == nil {
			options.Evaluator = hostmodel.New(hostmodel.Options{AnswerMode: ownmodel.Probabilities, MalformedRetries: 1})
		}
	}
	client, err := pitypesafe.New(options)
	if err != nil {
		return nil, err
	}
	s.client = client
	return client, nil
}

func (s *state) resetClient() { s.mu.Lock(); s.client = nil; s.mu.Unlock() }

func (s *state) peekClient() *pitypesafe.TypeSafe { s.mu.Lock(); defer s.mu.Unlock(); return s.client }

// callOut says something once per distinct degradation per session: a long run must not bury the reason in
// repeated notices.
func (s *state) callOut(ctx sdk.Context, key, text string) {
	s.mu.Lock()
	if s.calledOut == key {
		s.mu.Unlock()
		return
	}
	s.calledOut = key
	s.mu.Unlock()
	// Reporting must never replace the failure it describes, and a headless run may have no message channel.
	if ctx.HasUI() {
		ctx.Notify(text, "warning")
		return
	}
	_ = ctx.SendMessage(statusType, text, true, sdk.SendMessageOptions{})
}

func (s *state) onSessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	s.mu.Lock()
	s.enabled = os.Getenv(envEnabled) == "1"
	if backend := backendFromEnv(); backend != s.backend {
		s.backend = backend
		s.consent++
	}
	s.client = nil
	s.calledOut = ""
	backend := s.backend
	s.mu.Unlock()
	// An enabled extension with no usable key used to look exactly like a working one. Say it at startup; an
	// unverified-but-present key stays quiet, because the first request is what proves it.
	state, err := pitypesafe.GetAuthState(pitypesafe.AuthOptions{Backend: backend})
	if err != nil {
		return nil, nil
	}
	report, err := pitypesafe.DescribeAuth(state)
	if err == nil && s.isEnabled() && report.Level == pitypesafe.LevelError {
		s.callOut(ctx, "start:"+string(report.Level), "TypeSafe is enabled but judgments are skipped. "+report.Text)
	}
	return nil, nil
}

// goContext adapts a request Context to a context.Context that ends when the request is cancelled and carries
// the Context for the own-model backend.
func goContext(ctx sdk.Context) (context.Context, context.CancelFunc) {
	c, cancel := context.WithCancel(hostmodel.WithContext(context.Background(), ctx))
	done := ctx.Done()
	go func() {
		select {
		case <-done:
			cancel()
		case <-c.Done():
		}
	}()
	return c, cancel
}
