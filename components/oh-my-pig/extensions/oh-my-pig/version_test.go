package ohmypig

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// The banner and the welcome card print surface.Version; the Package declares
// its version in package.json two directories up. They must not drift.
func TestVersionMatchesPackageJSON(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "package.json"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("package.json is not beside this module in this layout")
	}
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != surface.Version {
		t.Fatalf("surface.Version = %q, %s package.json version = %q", surface.Version, pkg.Name, pkg.Version)
	}
}
