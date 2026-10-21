package warden

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// The single-pass check against the two gated halves it fronts, over generated texts built from every literal in every
// case, with and without the characters that precede them, near misses, `.env` variants and noise.
func TestSensitivePathAgreesWithTheFullCheck(t *testing.T) {
	if sensitiveLiterals() == nil {
		t.Fatal("the single-pass check is off: reSensitiveRest's gate no longer gives one folded set")
	}
	vocab := []string{" ", "\n", "x", "/", "~", "=", ":", "(", "\"", "'", ";", "|", "&", ")", "-", "_", ".", "a", "src/main.go", "/home/user/project/", "file_name.go",
		".env", ".ENV", ".Env", "env", ".en", ".e", ".env.local", ".env.example", ".env.SAMPLE", ".env.template.x", ".env.dist", ".env.production", ".env-x", ".envrc",
		"ssh/id_rsa", "ssh/id_", "ssh/authorized_keys", "ssh/known_hosts", ".ssh/id_ed25519", "ssh/", "aws/credentials", ".aws/credentials", "aws/", "gnupg/", ".gnupg/", "gnupg",
		"netrc", ".netrc", "npmrc", ".npmrc", "pypirc", ".pypirc", "netrcx", "docker/config.json", ".docker/config.json", "kube/config", ".kube/config", ".kube/configs",
		"pi/agent/auth.json", "pig/agent/auth.json", "pi/agent/pi-typesafe/auth.json", ".pi/agent/auth.json",
		"cert.pem", "a.P12", "k.pfx", "store.keystore", "x.jks", "pem", ".pem", "a.pemx", "_.jks",
		"id_rsa", "id_ed25519", "id_ecdsa", "id_dsa", "ID_RSA", "id_", "id_x", "xid_rsa", "id", "rsa",
		"SSH/ID_RSA", "Aws/Credentials", "NetRC", "KUBE/CONFIG",
		"é", "日本", "\u212a", "ſ", "\xff", "\u0130", "\u212aube/config", "ſsh/id_rsa"}
	r := rand.New(rand.NewPCG(77, 77))
	ascii, sensitive := 0, 0
	for i := range 200000 {
		text := genText(r, vocab, 6)
		want := sensitivePathFull(text)
		if got := SensitivePath(text); got != want {
			t.Fatalf("case %d %q: got %v want %v", i, text, got, want)
		}
		if isASCII(text) {
			ascii++
			if want && !sensitiveMayMatch(text) {
				t.Fatalf("case %d %q: the single pass rejects a match", i, text)
			}
		}
		if want {
			sensitive++
		}
	}
	if ascii < 20000 || sensitive < 20000 {
		t.Fatalf("generator too narrow: %d ASCII, %d sensitive", ascii, sensitive)
	}
	for _, text := range []string{"", ".", ".e", ".env", "a", "id", "id_", "pem", strings.Repeat("export const a = 1;\n", 600), strings.Repeat("x", 4000) + ".env", ".env" + strings.Repeat("x", 4000)} {
		if got, want := SensitivePath(text), sensitivePathFull(text); got != want {
			t.Errorf("%.40q: got %v want %v", text, got, want)
		}
	}
}
