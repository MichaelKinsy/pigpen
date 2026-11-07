package posthorse

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// nsRealTemp is a temporary directory with every symlink resolved (macOS keeps /tmp behind one).
func nsRealTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// nsUnmarshal is JSON.parse into a rec.
func nsUnmarshal(text string, out *rec) error { return json.Unmarshal([]byte(text), out) }

// jsonDecode is JSON.parse into v.
func jsonDecode(data []byte, v any) error { return json.Unmarshal(data, v) }
