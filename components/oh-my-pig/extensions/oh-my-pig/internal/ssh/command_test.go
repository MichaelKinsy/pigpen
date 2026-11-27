package ssh

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestSSHConfigWriterAndCommand(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, ".omp", "ssh.json")

	// 1. ValidateHostName.
	if err := ValidateHostName("prod-1.internal_a"); err != nil {
		t.Fatalf("expected valid host name, got %v", err)
	}
	if err := ValidateHostName("bad host!"); err == nil {
		t.Fatalf("expected invalid host name error")
	}
	if err := ValidateHostName(""); err == nil {
		t.Fatalf("expected empty host name error")
	}

	// 2. Read missing config returns empty map.
	cfg, err := ReadConfigFile(cfgPath)
	if err != nil || len(cfg.Hosts) != 0 {
		t.Fatalf("expected empty config, got %+v err=%v", cfg, err)
	}

	// 3. AddHost writes atomically and rejects duplicates.
	if err := AddHost(cfgPath, "prod", HostConfigEntry{
		Host:     "10.0.0.5",
		Username: "deploy",
		Port:     2222,
	}); err != nil {
		t.Fatalf("AddHost failed: %v", err)
	}
	if err := AddHost(cfgPath, "prod", HostConfigEntry{Host: "10.0.0.5"}); err == nil {
		t.Fatalf("expected duplicate host error")
	}

	// 4. Read back and verify.
	cfg, err = ReadConfigFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cfg.Hosts["prod"]
	if !ok || got.Host != "10.0.0.5" || got.Username != "deploy" || got.Port != 2222 {
		t.Fatalf("unexpected stored host: %+v", got)
	}

	// 5. RemoveHost removes cleanly and errors on missing.
	if err := RemoveHost(cfgPath, "prod"); err != nil {
		t.Fatalf("RemoveHost failed: %v", err)
	}
	if err := RemoveHost(cfgPath, "prod"); err == nil {
		t.Fatalf("expected missing host error")
	}

	// 6. Command registration does not panic.
	ext := sdk.New("test")
	registerSSHCommand(ext)
}

func TestSSHConfigWriterLosslessPreservesUntouchedEntriesAndTopLevelKeys(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "ssh.json")
	initial := `{
  "version": 2,
  "hosts": {
    "legacy": {
      "host": "old.example",
      "key": "~/.ssh/old_key",
      "compat": false,
      "port": "2222",
      "note": "x"
    }
  }
}
`
	if err := os.WriteFile(cfgPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	// Add a new host.
	if err := AddHost(cfgPath, "fresh", HostConfigEntry{Host: "10.0.0.8", Username: "ops"}); err != nil {
		t.Fatalf("AddHost failed: %v", err)
	}
	// Remove the newly added host.
	if err := RemoveHost(cfgPath, "fresh"); err != nil {
		t.Fatalf("RemoveHost failed: %v", err)
	}

	rawBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(rawBytes, &doc); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if doc["version"] != float64(2) {
		t.Fatalf("top-level version lost: %#v", doc)
	}
	hostsMap, ok := doc["hosts"].(map[string]any)
	if !ok {
		t.Fatalf("missing hosts map: %#v", doc)
	}
	legacy, ok := hostsMap["legacy"].(map[string]any)
	if !ok {
		t.Fatalf("legacy host missing: %#v", hostsMap)
	}
	wantLegacy := map[string]any{
		"host":   "old.example",
		"key":    "~/.ssh/old_key",
		"compat": false,
		"port":   "2222",
		"note":   "x",
	}
	if !reflect.DeepEqual(legacy, wantLegacy) {
		t.Fatalf("legacy host mutated:\n got: %#v\nwant: %#v", legacy, wantLegacy)
	}
}

func TestSSHUserScopePathMatchesDiscoverHosts(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	// Ensure PI_CODING_AGENT_DIR / PI_CONFIG_DIR don't leak from outer env.
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("PI_CONFIG_DIR", "")

	userPath := sshConfigPath("user", cwd, home)
	if err := AddHost(userPath, "usrbox", HostConfigEntry{Host: "10.0.0.9", Username: "alice"}); err != nil {
		t.Fatalf("AddHost user scope failed: %v", err)
	}

	hosts, _ := DiscoverHosts(ConfigOptions{
		Cwd:                 cwd,
		Home:                home,
		DisableSystemConfig: true,
	})
	var found bool
	for _, h := range hosts {
		if h.Name == "usrbox" && h.Host == "10.0.0.9" && h.Username == "alice" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("DiscoverHosts did not find user-scope host usrbox written to %s; got %+v", userPath, hosts)
	}

	// Also verify PI_CODING_AGENT_DIR override matches DiscoverHosts.
	customAgentDir := filepath.Join(tmp, "custom-agent")
	t.Setenv("PI_CODING_AGENT_DIR", customAgentDir)
	customUserPath := sshConfigPath("user", cwd, home)
	if err := AddHost(customUserPath, "envbox", HostConfigEntry{Host: "10.0.0.10"}); err != nil {
		t.Fatalf("AddHost custom agent dir failed: %v", err)
	}
	hosts2, _ := DiscoverHosts(ConfigOptions{
		Cwd:                 cwd,
		Home:                home,
		DisableSystemConfig: true,
	})
	var foundEnv bool
	for _, h := range hosts2 {
		if h.Name == "envbox" && h.Host == "10.0.0.10" {
			foundEnv = true
			break
		}
	}
	if !foundEnv {
		t.Fatalf("DiscoverHosts did not find PI_CODING_AGENT_DIR host envbox written to %s; got %+v", customUserPath, hosts2)
	}
}

func TestParseSSHCommandArgsWindowsPathsAndApostrophes(t *testing.T) {
	// 1. Backslashes stay literal (Windows key paths).
	gotWin := parseSSHCommandArgs(`add winbox --host 10.0.0.5 --key C:\Users\me\.ssh\id_ed25519 --scope user`)
	wantWin := []string{"add", "winbox", "--host", "10.0.0.5", "--key", `C:\Users\me\.ssh\id_ed25519`, "--scope", "user"}
	if !reflect.DeepEqual(gotWin, wantWin) {
		t.Fatalf("windows path mangled:\n got: %#v\nwant: %#v", gotWin, wantWin)
	}

	// 2. Mid-word apostrophe does not open a quote and swallow trailing flags.
	gotApos := parseSSHCommandArgs(`add box --host 10.0.0.5 --desc don't --scope user`)
	wantApos := []string{"add", "box", "--host", "10.0.0.5", "--desc", "don't", "--scope", "user"}
	if !reflect.DeepEqual(gotApos, wantApos) {
		t.Fatalf("mid-word apostrophe swallowed flags:\n got: %#v\nwant: %#v", gotApos, wantApos)
	}

	// 3. Quoted description with spaces still parses as one token.
	gotQuoted := parseSSHCommandArgs(`add prod --host 10.0.0.5 --desc "my prod box" --scope project`)
	wantQuoted := []string{"add", "prod", "--host", "10.0.0.5", "--desc", "my prod box", "--scope", "project"}
	if !reflect.DeepEqual(gotQuoted, wantQuoted) {
		t.Fatalf("quoted arg failed:\n got: %#v\nwant: %#v", gotQuoted, wantQuoted)
	}
}
