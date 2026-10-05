// Package gate is an end-to-end test fixture: pig runs its tool_call hook after it has announced
// the tool (tool_execution_start) and before it runs the tool, so a hook that blocks holds the tool
// while the adapter reacts to the announcement.
package gate

import (
	"net"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension holds every write until the test closes the connection it opened to the address in
// <cwd>/gate.addr.
func Extension() *sdk.Extension {
	e := sdk.New("gate")
	e.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, data map[string]any) (any, error) {
		if data["toolName"] != "write" {
			return nil, nil
		}
		addr, err := os.ReadFile(filepath.Join(ctx.Cwd(), "gate.addr"))
		if err != nil {
			return nil, err
		}
		conn, err := net.Dial("tcp", strings.TrimSpace(string(addr)))
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		// The test closes its end to release the write; Read returns then.
		_, _ = conn.Read(make([]byte, 1))
		return nil, nil
	})
	return e
}
