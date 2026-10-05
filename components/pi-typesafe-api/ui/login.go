package ui

import (
	"context"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	pitypesafe "github.com/MichaelKinsy/pigpen/components/pi-typesafe-api"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// LoginResult is a completed login.
type LoginResult struct {
	// Path is where the verified key was saved.
	Path string
	// Models is the number of models the key can access; it proves the API accepted the key.
	Models int
}

// LoginOptions configure the login helpers.
type LoginOptions struct {
	// HTTPClient is a transport injection for offline tests.
	HTTPClient typesafe.HTTPDoer
}

// LoginWithPrompt asks for a TypeSafe key (hidden input), verifies it against api.typesafe.ai with a model
// listing, and stores it for every consumer of this package. It returns nil when the user cancels and a
// *pitypesafe.IntegrationError for an invalid or unverifiable key. It refuses when TYPESAFE_API_KEY is set,
// because the environment would shadow the stored key. Other backends have no login: their key is an
// environment variable only.
func LoginWithPrompt(ctx sdk.Context, opts LoginOptions) (*LoginResult, error) {
	if pitypesafe.EnvKeySet() {
		return nil, &pitypesafe.IntegrationError{Code: pitypesafe.CodeConfiguration, Message: "TYPESAFE_API_KEY is set in the environment and takes precedence over a stored key. Unset it before logging in interactively."}
	}
	if !ctx.HasUI() {
		return nil, &pitypesafe.IntegrationError{Code: pitypesafe.CodeConfiguration, Message: "Logging in needs an interactive session. Set TYPESAFE_API_KEY in the environment instead."}
	}
	entered, ok, err := PromptForAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	key, err := pitypesafe.NormalizeAPIKey(entered)
	if err != nil {
		return nil, err
	}
	// Verify before saving so a bad paste fails here, not on first use.
	client, err := pitypesafe.New(pitypesafe.Options{APIKey: key, HTTPClient: opts.HTTPClient})
	if err != nil {
		return nil, err
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		return nil, err
	}
	path, err := pitypesafe.StoreAPIKey(key)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Path: path, Models: len(models)}, nil
}

// EnsureResult is the outcome of EnsureAPIKey: the source of the key in use, and the login when one ran.
type EnsureResult struct {
	Source pitypesafe.KeySource
	Login  *LoginResult
}

// EnsureAPIKey uses the configured key for the backend if there is one; otherwise it runs the login prompt. A
// nil result means the user cancelled. A store that must not be read returns a configuration error with the
// reason instead of prompting, so a permissions problem stays visible. A backend without a login store (every
// backend except TypeSafe) returns a configuration error naming its environment variable instead of
// prompting, because the prompt would verify the key against the wrong service and store it where the
// TypeSafe key lives. backend nil means the default.
func EnsureAPIKey(ctx sdk.Context, backend any, opts LoginOptions) (*EnsureResult, error) {
	if backend == nil {
		backend = pitypesafe.DefaultBackend
	}
	situation, err := pitypesafe.KeySituationFor(backend)
	if err != nil {
		return nil, err
	}
	switch situation.Kind {
	case pitypesafe.KeyEnvironment:
		return &EnsureResult{Source: pitypesafe.SourceEnvironment}, nil
	case pitypesafe.KeyStored:
		return &EnsureResult{Source: pitypesafe.SourceStored}, nil
	case pitypesafe.KeyNotRequired:
		return &EnsureResult{}, nil
	case pitypesafe.KeyUnusable:
		return nil, &pitypesafe.IntegrationError{Code: pitypesafe.CodeConfiguration, Message: situation.Reason}
	}
	config, err := pitypesafe.ResolveBackend(backend)
	if err != nil {
		return nil, err
	}
	if !pitypesafe.UsesTypeSafeKey(config.BackendConfig) {
		return nil, &pitypesafe.IntegrationError{Code: pitypesafe.CodeConfiguration, Message: "No " + config.Label + " key. Set " + config.KeyEnv + " in the environment; /typesafe login stores a TypeSafe key only."}
	}
	login, err := LoginWithPrompt(ctx, opts)
	if err != nil || login == nil {
		return nil, err
	}
	return &EnsureResult{Source: pitypesafe.SourceStored, Login: login}, nil
}
