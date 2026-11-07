package langsmith

// Bridge for the external test package (fakehost_test.go and the twins are package langsmith_test): it exports what
// the twins need from the unexported port, and builds the extension against a loopback LangSmith endpoint the way
// upstream's mockClient builds its client (autoBatchTracing: false).

import (
	"context"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// TestOptions configures NewTestExtension.
type TestOptions struct {
	// Env is process.env for getConfig (vi.stubEnv); nothing else is read from the process.
	Env  map[string]string
	Home string
	Cwd  string
	// ServerURL is the loopback LangSmith endpoint (the client's apiUrl); Direct posts `POST /runs` and
	// `PATCH /runs/<id>` instead of batching (mockClient's autoBatchTracing: false).
	ServerURL string
	Direct    bool
	// PiVersion is Pi's VERSION; "" is a host that reports none.
	PiVersion string
}

// TestExtension is the extension under test with the handles a test needs.
type TestExtension struct {
	Ext *sdk.Extension
	// Flush is client.awaitPendingTraceBatches (nil without a client).
	Flush func(ctx context.Context) error
	// Close stops the client's goroutines.
	Close func()
	// Enabled is the resolved config.enabled.
	Enabled bool
	// ProjectName is the resolved config.project.
	ProjectName string
}

// NewTestExtension builds the extension from an environment, as the factory does after reading it.
func NewTestExtension(o TestOptions) (*TestExtension, error) {
	lookup := func(k string) (string, bool) { v, ok := o.Env[k]; return v, ok }
	cfg, err := getConfig(&configOptions{Home: o.Home, Cwd: o.Cwd, Lookup: lookup})
	if err != nil {
		return nil, err
	}
	te := &TestExtension{Close: func() {}, Enabled: cfg.Enabled, ProjectName: cfg.Project}
	var cl *client
	if cfg.Enabled {
		opts := clientOptions{APIKey: cfg.APIKey, APIURL: cfg.APIURL}
		if o.ServerURL != "" {
			opts.APIURL = &o.ServerURL
		}
		if o.Direct {
			opts.AutoBatchTracing = new(false)
		}
		if cl, err = newClient(opts); err != nil {
			return nil, err
		}
		te.Close = cl.close
		te.Flush = cl.awaitPendingTraceBatches
	}
	var pv *string
	if o.PiVersion != "" {
		pv = &o.PiVersion
	}
	te.Ext = newExtension(cfg, cl, pv)
	return te, nil
}

// Stringify is JSON.stringify of a value the port built.
func Stringify(v any) string {
	b, ok, err := stringify(v)
	if err != nil {
		panic(err)
	}
	if !ok {
		return "undefined"
	}
	return string(b)
}

// ConvertMessages, ConvertToolOutputs, NormalizeContentPart, ExtractUsageMetadata and ConvertProviderPayload expose
// the converters.
func ConvertMessages(messages []any) ([]any, error) { return convertMessages(messages) }
func ConvertToolOutputs(result any) any             { return convertToolOutputs(result) }
func NormalizeContentPart(part any) any             { return normalizeContentPart(part) }
func ExtractUsageMetadata(message any) (any, error) { return extractUsageMetadata(message) }
func ConvertProviderPayload(p any) (any, error)     { return convertProviderPayload(p) }

// ParseJSONOrdered is JSON.parse into ordered objects.
func ParseJSONOrdered(text string) (any, bool) { return parseJSONOrdered(text) }
