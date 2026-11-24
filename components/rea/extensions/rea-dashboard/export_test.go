package readashboard

import (
	"context"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// WithRunner builds the extension with run in place of the embedded node, so host tests need no REA payload.
func WithRunner(run func(ctx context.Context, node, entry string, args []string, limit int) ([]byte, bool, error)) *sdk.Extension {
	return build(func(ctx context.Context, srv reaServer, args []string, limit int) ([]byte, bool, error) {
		return run(ctx, srv.Node, srv.Entry, args, limit)
	}, time.Now)
}

// DeriveRequest is the message of the Derive piglet action, for host tests that check which name and target it carries.
func DeriveRequest(name, target string) string { return deriveRequest(name, target) }
