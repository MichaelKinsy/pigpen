package pi_typesafe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	pitypesafe "github.com/MichaelKinsy/pigpen/components/pi-typesafe-api"
	"github.com/MichaelKinsy/pigpen/components/pi-typesafe-api/hostmodel"
	"github.com/MichaelKinsy/pigpen/components/pi-typesafe-api/ui"
)

// actions are the /typesafe subcommands the usage text lists, in the original's order.
var actions = []string{"login", "logout", "setup", "status", "enable", "disable", "test", "playground"}

// backendAction is this port's addition (the own-model backend): it is completed but not part of the usage text.
const backendAction = "backend"

func (s *state) registerCommand() {
	s.ext.RegisterCommand("typesafe", sdk.CommandOptions{
		Description: "TypeSafe login, consent, usage, sample test, and JSON playground",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			var items []sdk.AutocompleteItem
			for _, a := range append(append([]string(nil), actions...), backendAction) {
				if strings.HasPrefix(a, prefix) {
					items = append(items, sdk.AutocompleteItem{Value: a, Label: a})
				}
			}
			return items, nil
		},
		Handler: s.command,
	})
}

func (s *state) reporter(ctx sdk.Context) func(text, level string) {
	return func(text, level string) {
		if ctx.HasUI() {
			ctx.Notify(text, level)
			return
		}
		_ = ctx.SendMessage(statusType, text, true, sdk.SendMessageOptions{})
	}
}

func contains(list []string, v string) bool {
	for _, e := range list {
		if e == v {
			return true
		}
	}
	return false
}

func (s *state) command(ctx sdk.Context, args string) error {
	// The original takes the whole argument text as the action (args.trim() || "status"), so trailing words make
	// an unknown action; only this port's backend action takes an argument.
	text := strings.TrimSpace(args)
	fields := strings.Fields(text)
	action := text
	if action == "" {
		action = "status"
	}
	if len(fields) > 0 && fields[0] == backendAction {
		action = backendAction
	}
	report := s.reporter(ctx)
	err := s.run(ctx, action, fields, report)
	if err != nil {
		report(pitypesafe.SafeError(err, s.currentBackend()).Message, "error")
	}
	return nil
}

func (s *state) run(ctx sdk.Context, action string, fields []string, report func(string, string)) error {
	backend := s.currentBackend()
	switch action {
	case "status":
		return s.status(ctx, report)
	case "logout":
		removed := pitypesafe.ClearStoredAPIKey()
		pitypesafe.ClearAuthState()
		s.resetClient()
		s.setEnabled(false)
		if removed {
			report("Removed the stored key at "+pitypesafe.CredentialsPath()+". TypeSafe is disabled.", "info")
		} else {
			text := "No stored key to remove."
			if pitypesafe.EnvKeySet() {
				text += " TYPESAFE_API_KEY is still set in the environment."
			}
			report(text, "info")
		}
		return nil
	case "disable":
		s.setEnabled(false)
		report("TypeSafe disabled for future agent calls. In-flight requests are not cancelled.", "info")
		return nil
	case backendAction:
		return s.chooseBackend(fields, report)
	}
	if !contains(actions, action) {
		report("Usage: /typesafe "+strings.Join(actions, " | "), "warning")
		return nil
	}
	if !ctx.HasUI() {
		report("This command needs interactive Pi. For headless tool use, explicitly set PI_TYPESAFE_ENABLED=1 and TYPESAFE_API_KEY before launching Pi.", "warning")
		return nil
	}
	situation, err := pitypesafe.KeySituationFor(backend)
	if err != nil {
		return err
	}
	if backend == pitypesafe.BackendOwnModel && (action == "login" || action == "setup") {
		report("The own-model backend needs no key: it answers with the model PiG is configured with. Use /typesafe backend typesafe to use the TypeSafe API.", "info")
		return nil
	}
	if action == "login" || (action == "setup" && situation.Kind == pitypesafe.KeyMissing) {
		if pitypesafe.EnvKeySet() {
			report("TYPESAFE_API_KEY is set in the environment and takes precedence over a stored key. Unset it before using /typesafe login.", "warning")
			return nil
		}
		login, err := ui.LoginWithPrompt(ctx, ui.LoginOptions{HTTPClient: s.opts.HTTPClient})
		if err != nil {
			return err
		}
		if login == nil {
			report("Login cancelled; nothing was saved.", "info")
			return nil
		}
		s.resetClient()
		plural := "s"
		if login.Models == 1 {
			plural = ""
		}
		report(fmt.Sprintf("Key verified (%d model%s available) and saved to %s with owner-only permissions. Run /typesafe enable to allow agent tool calls.", login.Models, plural, login.Path), "info")
		return nil
	}
	if action == "setup" {
		current := "missing"
		switch situation.Kind {
		case pitypesafe.KeyEnvironment, pitypesafe.KeyStored:
			current = "configured via " + pitypesafe.KeySourceLabel(situation)
		case pitypesafe.KeyUnusable:
			current = "unusable — " + situation.Reason
		}
		report("Key "+current+". Run /typesafe test for one sample request or /typesafe enable to allow agent tool calls.", "info")
		return nil
	}
	if action == "enable" {
		switch situation.Kind {
		case pitypesafe.KeyMissing:
			report("Run /typesafe login first: no API key is configured.", "warning")
			return nil
		case pitypesafe.KeyUnusable:
			report("The stored key cannot be used. "+situation.Reason, "warning")
			return nil
		}
		ok, err := ctx.Confirm("Enable TypeSafe for this session?", disclosure(backend))
		if err != nil {
			return err
		}
		if ok {
			s.setEnabled(true)
			report(fmt.Sprintf("TypeSafe enabled. Up to %d attempts in this session; /typesafe disable stops future agent calls.", pitypesafe.DefaultMaxRequests), "info")
		}
		return nil
	}
	return s.sample(ctx, action)
}

// sample is /typesafe test and /typesafe playground: validate, confirm, send, and show the result without
// putting it in the model's context.
func (s *state) sample(ctx sdk.Context, action string) error {
	var request any = mustParse(sampleJSON)
	if action == "playground" {
		pretty := indent(sampleJSON)
		text, ok, err := ctx.Editor("TypeSafe request JSON · edit state and questions", pretty)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		parsed, err := pitypesafe.ParseJSON([]byte(text))
		if err != nil {
			s.reporter(ctx)("Invalid JSON. Keep quoted strings on one line; nothing was sent.", "error")
			return nil
		}
		request = parsed
	}
	validated, err := pitypesafe.PrepareEvaluationRequest(request, pitypesafe.PrepareOptions{})
	if err != nil {
		return err
	}
	consent := s.consentNow()
	ok, err := ctx.Confirm("Send this TypeSafe request?", disclosure(s.currentBackend()))
	if err != nil || !ok {
		return err
	}
	// The operator confirmed one destination; a backend switch while the dialog was open voids that.
	client, err := s.clientFor(consent)
	if err != nil {
		return err
	}
	goCtx, cancel := goContext(ctx)
	defer cancel()
	result, err := client.EvaluateRaw(goCtx, validated)
	if err != nil {
		return err
	}
	// Playground results stay out of LLM context; the agent tool returns its own results normally.
	details, err := result.DetailsJSON()
	if err != nil {
		return err
	}
	return ctx.AppendEntry(entryType, json.RawMessage(details))
}

func mustParse(text string) any {
	v, err := pitypesafe.ParseJSON([]byte(text))
	if err != nil {
		panic(err)
	}
	return v
}

// indent is JSON.stringify(value, null, 2).
func indent(compact string) string {
	var b bytes.Buffer
	if err := json.Indent(&b, []byte(compact), "", "  "); err != nil {
		return compact
	}
	return b.String()
}

func (s *state) status(ctx sdk.Context, report func(string, string)) error {
	backend := s.currentBackend()
	state, err := pitypesafe.GetAuthState(pitypesafe.AuthOptions{Backend: backend})
	if err != nil {
		return err
	}
	auth, err := pitypesafe.DescribeAuth(state)
	if err != nil {
		return err
	}
	client := s.peekClient()
	session := fmt.Sprintf("Session 0/%d attempts; no client yet in this session.", pitypesafe.DefaultMaxRequests)
	today, blocked := "", ""
	if client != nil {
		spend := client.GetSpend()
		session = fmt.Sprintf("Session %d/%d attempts, %d successful, %d failed, %d input tokens (~$%.4f).",
			spend.Session.RequestsStarted, pitypesafe.DefaultMaxRequests, spend.Session.RequestsSucceeded, spend.Session.RequestsFailed, spend.Session.InputTokens, spend.Session.EstimatedUSD)
		today = fmt.Sprintf("Today %d requests (%d ok, %d failed), %d input tokens, ~$%.4f.",
			spend.Today.RequestsStarted, spend.Today.RequestsSucceeded, spend.Today.RequestsFailed, spend.Today.InputTokens, spend.Today.EstimatedUSD)
		if b := spend.Blocked; b != nil {
			blocked = fmt.Sprintf(" Cap reached: %s %s/%s on %s; no request will be submitted until the local day rolls over.", b.Cap, num(b.Used), num(b.Limit), b.Day)
		}
	}
	enabled := s.isEnabled()
	word := "disabled"
	if enabled {
		word = "enabled"
	}
	level := "info"
	if auth.Level == pitypesafe.LevelError && enabled {
		level = "warning"
	}
	model := pitypesafe.DefaultModelID(pitypesafe.DefaultBackend)
	if backend == pitypesafe.BackendOwnModel {
		model = "the model PiG is configured with"
		if ref, err := hostmodel.Active(ctx); err == nil {
			model = ref.Provider + "/" + ref.ID
		}
	}
	report(fmt.Sprintf("TypeSafe: %s. %s %s %s%s Model: %s. Session limits reset on session start/reload; daily counters persist and caps come from client options or PI_TYPESAFE_MAX_* environment variables. %s",
		word, auth.Text, session, today, blocked, model, disclosure(backend)), level)
	return nil
}

func num(v float64) string {
	b, _ := pitypesafe.EncodeJSON(v)
	return string(b)
}

// chooseBackend switches between the TypeSafe API and the own-model backend. Consent is per destination, so a
// switch disables the tool until the operator enables it again.
func (s *state) chooseBackend(fields []string, report func(string, string)) error {
	current := s.currentBackend()
	if len(fields) < 2 {
		report(fmt.Sprintf("Judgment backend: %s. Options: %s (api.typesafe.ai), %s (the model PiG is configured with). Set PI_TYPESAFE_BACKEND before launch to choose the startup backend.",
			current, pitypesafe.BackendTypeSafe, pitypesafe.BackendOwnModel), "info")
		return nil
	}
	choice := fields[1]
	if choice != pitypesafe.BackendTypeSafe && choice != pitypesafe.BackendOwnModel {
		report(fmt.Sprintf("Unknown backend %q. Options: %s, %s.", choice, pitypesafe.BackendTypeSafe, pitypesafe.BackendOwnModel), "warning")
		return nil
	}
	if choice == current {
		report("Backend is already "+choice+".", "info")
		return nil
	}
	s.mu.Lock()
	s.backend = choice
	s.consent++
	s.enabled = false
	s.client = nil
	s.calledOut = ""
	s.mu.Unlock()
	s.registerTool()
	destination := "api.typesafe.ai"
	if choice == pitypesafe.BackendOwnModel {
		destination = "the provider of the model PiG is configured with"
	}
	report(fmt.Sprintf("Backend set to %s for this session. TypeSafe is disabled: run /typesafe enable to consent to sending content to %s.", choice, destination), "info")
	return nil
}
