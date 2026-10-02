package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Additions: the capability keys are the port's own (upstream has a bare port/token/host file).

func writeSettings(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAccessDefaultsToNothing(t *testing.T) {
	// a freshly created file (port and token only) grants no capability
	path := filepath.Join(t.TempDir(), "ahp", "settings.json")
	if _, err := LoadDirect(path); err != nil {
		t.Fatal(err)
	}
	a, err := LoadAccess(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.Filesystem.Enabled || a.Terminals.Enabled || a.AllowSessionDeletion || len(a.AllowedOrigins) != 0 {
		t.Fatalf("%+v", a)
	}
}

func TestAccessReadsCapabilities(t *testing.T) {
	a, err := LoadAccess(writeSettings(t, `{"port": 1, "allowedOrigins": ["https://vscode.dev"], "filesystem": {"enabled": true, "roots": ["/work"]}, "terminals": {"enabled": true}, "allowSessionDeletion": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !a.Filesystem.Enabled || !reflect.DeepEqual(a.Filesystem.Roots, []string{"/work"}) || a.Filesystem.Unrestricted || !a.Terminals.Enabled || !a.AllowSessionDeletion || !reflect.DeepEqual(a.AllowedOrigins, []string{"https://vscode.dev"}) {
		t.Fatalf("%+v", a)
	}
}

func TestAccessRejectsWrongTypes(t *testing.T) {
	for _, body := range []string{`{"filesystem": true}`, `{"terminals": {"enabled": "yes"}}`, `{"allowedOrigins": "x"}`, `{"filesystem": {"roots": "/work"}}`} {
		if _, err := LoadAccess(writeSettings(t, body)); err == nil {
			t.Errorf("%s must be refused", body)
		}
	}
}
