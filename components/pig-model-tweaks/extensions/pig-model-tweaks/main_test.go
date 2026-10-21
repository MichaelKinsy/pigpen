package pigmodeltweaks

import (
	"os"
	"testing"
)

// The tests give the extension a home through PIG_HOME. A host's own answer
// (PIG_EXT_HOST_INFO, which sdk.AgentDir reads) must not leak in from the
// environment the tests run in.
func TestMain(m *testing.M) {
	_ = os.Unsetenv("PIG_EXT_HOST_INFO")
	os.Exit(m.Run())
}
