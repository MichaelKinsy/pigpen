package settings_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/settings"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

// Twins of upstream test/direct-settings.test.ts.

func fixture(t *testing.T) (path string, write func(any)) {
	path = filepath.Join(t.TempDir(), "nested", "settings.json")
	return path, func(v any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		var raw []byte
		if s, ok := v.(string); ok {
			raw = []byte(s)
		} else {
			raw, _ = json.Marshal(v)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirectListenerSettings(t *testing.T) {
	twin.Run(t, "direct-settings", "creates a private, reusable endpoint configuration on first use", func(t *testing.T) {
		path, _ := fixture(t)
		s, err := settings.LoadDirect(path)
		if err != nil {
			t.Fatal(err)
		}
		if s.Host != "127.0.0.1" || s.Port < 1 || s.Port > 65535 {
			t.Fatalf("settings = %+v", s)
		}
		if s.Token == nil || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(*s.Token) {
			t.Fatalf("token = %v", s.Token)
		}
		raw, _ := os.ReadFile(path)
		var onDisk settings.Direct
		if err := json.Unmarshal(raw, &onDisk); err != nil || !reflect.DeepEqual(onDisk, s) {
			t.Fatalf("on disk %+v (%v), returned %+v", onDisk, err, s)
		}
		if runtime.GOOS != "windows" {
			if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
				t.Fatalf("mode = %v; the token is a bearer credential", info.Mode().Perm())
			}
		}
		again, err := settings.LoadDirect(path)
		if err != nil || !reflect.DeepEqual(again, s) {
			t.Fatalf("second load %+v (%v)", again, err)
		}
	})

	twin.Run(t, "direct-settings", "takes an existing file literally and defaults only optional fields", func(t *testing.T) {
		path, write := fixture(t)
		write(map[string]any{"port": 12345})
		got, err := settings.LoadDirect(path)
		if err != nil || got.Port != 12345 || got.Token != nil || got.Host != "127.0.0.1" {
			t.Fatalf("got %+v (%v)", got, err)
		}
		write(map[string]any{"port": 23456, "token": nil, "host": "0.0.0.0"})
		got, err = settings.LoadDirect(path)
		if err != nil || got.Port != 23456 || got.Token != nil || got.Host != "0.0.0.0" {
			t.Fatalf("got %+v (%v)", got, err)
		}
	})

	twin.Run(t, "direct-settings", "rejects malformed or incomplete files instead of inventing replacements", func(t *testing.T) {
		path, write := fixture(t)
		cases := []struct {
			value   any
			message string
		}{
			{"not json", `not valid JSON`},
			{map[string]any{}, "no `port`"},
			{map[string]any{"port": 0}, `integer 1-65535`},
			{map[string]any{"port": 65536}, `integer 1-65535`},
			{map[string]any{"port": 1.5}, `integer 1-65535`},
			{map[string]any{"port": 12345, "token": 42}, `string or null`},
			{map[string]any{"port": 12345, "host": 42}, `host.*string`},
		}
		for _, c := range cases {
			write(c.value)
			_, err := settings.LoadDirect(path)
			var se *settings.Error
			if !errors.As(err, &se) || !regexp.MustCompile(c.message).MatchString(se.Error()) {
				t.Fatalf("%v: err = %v, want /%s/", c.value, err, c.message)
			}
		}
	})
}
