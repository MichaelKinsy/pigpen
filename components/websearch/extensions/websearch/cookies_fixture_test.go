package websearch

// Fixtures of the browser cookie twins: synthetic Chromium cookie databases (built the way the
// upstream tests build them, with python3's sqlite3 module) and canned `security`, `secret-tool`,
// `kwallet-query` and `powershell.exe` programs behind the host command runner. `sqlite3` and
// `python3` run for real, but only against the synthetic databases; no real browser profile is
// ever read. Every password and cookie value in this file is synthetic.

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

const fixtureScript = `
import json, sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute("create table meta (key text, value integer)")
c.execute("insert into meta values ('version', 24)")
c.execute("create table cookies (name text, value text, host_key text, encrypted_value blob, expires_utc integer)")
for row in json.loads(sys.argv[2]):
    encrypted = bytes.fromhex(row[3][4:]) if isinstance(row[3], str) and row[3].startswith('hex:') else row[3]
    c.execute("insert into cookies values (?, ?, ?, ?, ?)", [row[0], row[1], row[2], encrypted, row[4]])
c.commit()
c.close()
`

const authFixtureScript = `
import json, sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute("create table meta (key text, value integer)")
c.execute("insert into meta values ('version', 24)")
c.execute("create table cookies (name text, value text, host_key text, path text, encrypted_value blob, expires_utc integer, top_frame_site_key text)")
for row in json.loads(sys.argv[2]):
    c.execute("insert into cookies values (?, ?, ?, ?, ?, ?, ?)", row)
c.commit()
c.close()
`

func needPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("fixture creation requires python3")
	}
}

// browserBase is the profile root of a browser on a platform under home.
func browserBase(home, browser, goos string) string {
	switch goos {
	case "darwin":
		switch browser {
		case "Brave":
			return filepath.Join(home, "Library", "Application Support", "BraveSoftware", "Brave-Browser")
		case "Helium":
			return filepath.Join(home, "Library", "Application Support", "net.imput.helium")
		case "Arc":
			return filepath.Join(home, "Library", "Application Support", "Arc", "User Data")
		}
		return filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	case "windows":
		if browser == "Edge" {
			return filepath.Join(home, "AppData", "Local", "Microsoft", "Edge", "User Data")
		}
		return filepath.Join(home, "AppData", "Local", "Google", "Chrome", "User Data")
	}
	if browser == "Chromium" {
		return filepath.Join(home, ".config", "chromium")
	}
	return filepath.Join(home, ".config", "google-chrome")
}

type fixtureOptions struct {
	browser     string
	goos        string
	windowsKey  []byte
	profileRoot string
}

// createCookieFixture is createFixture of chrome-cookie-extraction.test.mjs: a Cookies database
// of the 5-column schema. A row is [name, value, host, encrypted ("hex:..." or nil), expires].
func createCookieFixture(t *testing.T, home, profile string, rows [][]any, o fixtureOptions) {
	t.Helper()
	if o.goos == "" {
		o.goos = runtime.GOOS
	}
	if o.browser == "" {
		o.browser = "Chrome"
	}
	base := browserBase(home, o.browser, o.goos)
	db := filepath.Join(base, profile, "Cookies")
	if o.goos == "windows" {
		db = filepath.Join(base, profile, "Network", "Cookies")
	}
	runPython(t, fixtureScript, db, rows)
	if o.windowsKey != nil {
		protected := append([]byte("DPAPI"), []byte("protected")...)
		state, _ := json.Marshal(map[string]any{"os_crypt": map[string]any{"encrypted_key": base64Std(protected)}})
		if err := os.WriteFile(filepath.Join(base, "Local State"), state, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// createAuthFixture is createCookieFixture of auth-fetch.test.mjs: the 7-column schema with
// paths and a partition key, under the platform's Chrome.
func createAuthFixture(t *testing.T, home string, rows [][]any) {
	t.Helper()
	profileDir := browserBase(home, "Chrome", runtime.GOOS)
	runPython(t, authFixtureScript, filepath.Join(profileDir, "Default", "Cookies"), rows)
}

func runPython(t *testing.T, script, db string, rows [][]any) {
	t.Helper()
	needPython(t)
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("python3", "-c", script, db, string(payload)).CombinedOutput(); err != nil {
		t.Fatalf("python3 fixture: %v\n%s", err, out)
	}
}

func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func hostHash(hostKey string) []byte {
	if hostKey == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(hostKey))
	return sum[:]
}

// encryptLinuxCookie is encryptLinuxCookie of the upstream test: v11, AES-128-CBC with the
// pbkdf2 key of the password, optionally preceded by the host hash Chromium 24+ adds.
func encryptLinuxCookie(value, password, version, hostKey string) string {
	key, _ := pbkdf2.Key(sha1.New, password, []byte("saltysalt"), 1, 16)
	plaintext := append(hostHash(hostKey), []byte(value)...)
	pad := 16 - len(plaintext)%16
	plaintext = append(plaintext, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(key)
	out := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, bytes.Repeat([]byte{0x20}, 16)).CryptBlocks(out, plaintext)
	return hex.EncodeToString(append([]byte(version), out...))
}

// encryptWindowsCookie is encryptWindowsCookie of the upstream test (AES-256-GCM, nonce of 7s).
func encryptWindowsCookie(value string, key []byte, version, hostKey string) string {
	nonce := bytes.Repeat([]byte{7}, 12)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	sealed := gcm.Seal(nil, nonce, append(hostHash(hostKey), []byte(value)...), nil)
	return hex.EncodeToString(append(append([]byte(version), nonce...), sealed...))
}

func googleLinuxRows(password, one, two string) [][]any {
	host := ".google.com"
	return [][]any{
		{"__Secure-1PSID", "", host, "hex:" + encryptLinuxCookie(one, password, "v11", host), 1},
		{"__Secure-1PSIDTS", "", host, "hex:" + encryptLinuxCookie(two, password, "v11", host), 2},
	}
}

func plainGoogleRows(one, two string) [][]any {
	return [][]any{
		{"__Secure-1PSID", one, ".google.com", nil, 1},
		{"__Secure-1PSIDTS", two, ".google.com", nil, 2},
	}
}

// shims is the set of programs the host runs for the extension in one test.
type shims struct {
	mu       sync.Mutex
	handlers map[string]func(n int, args []string) ExecResult
	counts   map[string]int
	args     map[string][][]string
	// noSQL makes sqlite3 and python3 unavailable.
	noSQL bool
}

// installShims routes the host command runner to the shims, pins the platform and clears the
// cookie state; the restore functions run at cleanup. Browser cookie access is switched on by
// the environment, as the upstream tests do.
func installShims(t *testing.T, goos string) *shims {
	t.Helper()
	s := &shims{handlers: map[string]func(int, []string) ExecResult{}, counts: map[string]int{}, args: map[string][][]string{}}
	resetCookieState()
	t.Cleanup(resetCookieState)
	t.Cleanup(SetExecRunner(s.run))
	prevOS, prevLook := cookieGOOS, cookieLookPath
	cookieGOOS = goos
	cookieLookPath = s.lookPath
	t.Cleanup(func() { cookieGOOS, cookieLookPath = prevOS, prevLook })
	t.Setenv("PI_ALLOW_BROWSER_COOKIES", "1")
	unsetenv(t, "KDE_FULL_SESSION", "XDG_CURRENT_DESKTOP", "LOCALAPPDATA")
	return s
}

func (s *shims) on(command string, h func(n int, args []string) ExecResult) {
	s.mu.Lock()
	s.handlers[command] = h
	s.mu.Unlock()
}

// password is a canned program that prints a synthetic password.
func (s *shims) password(command, value string) {
	s.on(command, func(int, []string) ExecResult { return ExecResult{Stdout: value} })
}

func (s *shims) count(command string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[command]
}

func (s *shims) lastArgs(command string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := s.args[command]
	if len(calls) == 0 {
		return nil
	}
	return calls[len(calls)-1]
}

func (s *shims) lookPath(name string) (string, error) {
	s.mu.Lock()
	_, ok := s.handlers[name]
	noSQL := s.noSQL
	s.mu.Unlock()
	if ok {
		return "/shim/" + name, nil
	}
	if (name == "sqlite3" || name == "python3") && !noSQL {
		return exec.LookPath(name)
	}
	return "", errors.New("not found")
}

func (s *shims) run(ctx context.Context, command string, args []string, timeout time.Duration) (ExecResult, error) {
	s.mu.Lock()
	h, ok := s.handlers[command]
	s.counts[command]++
	n := s.counts[command]
	s.args[command] = append(s.args[command], append([]string(nil), args...))
	noSQL := s.noSQL
	s.mu.Unlock()
	if ok {
		return h(n, args), nil
	}
	if (command == "sqlite3" || command == "python3") && !noSQL {
		cmd := exec.CommandContext(ctx, command, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			code = 1
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			}
		}
		return ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: code}, nil
	}
	return ExecResult{Code: 1}, nil
}
