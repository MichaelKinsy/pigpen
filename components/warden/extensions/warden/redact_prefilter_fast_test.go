package warden

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// The single-pass credential prefilter against the scan it replaces, over generated texts built from the literals in
// every case, near misses of them, and noise.
func TestMayHaveCredentialASCIIAgreesWithTheScan(t *testing.T) {
	vocab := []string{" ", "\n", "x", "export const a = 1;\n", "ke", "key", "KEY", "kEy", "secre", "Secret", "SECRET", "passw", "PassWord", "passphrase", "Token", "toke", "credential", "CREDENTIAL", "authorization", "Authorization", "bearer", "BEARER", "bear",
		"PRIVATE KEY-----", "PRIVATE KEY----", "private key-----", "sk-", "sk", "SK-", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "ghx_", "github_pat_", "github_pa", "AKIA", "akia", "xox", "XOX", "AIza", "aiza", "eyJ", "eyj", "://", "@", "http://u@h", "a", "-", "_", "=", "é"}
	r := rand.New(rand.NewPCG(31, 31))
	for i := range 80000 {
		text := genText(r, vocab, 8)
		if !isASCII(text) {
			continue
		}
		if got, want := mayHaveCredentialASCII(text), mayHaveCredentialScan(text); got != want {
			t.Fatalf("case %d %q: got %v want %v", i, text, got, want)
		}
	}
	for _, text := range []string{"", "a", "ke", strings.Repeat("export const a = 1;\n", 600), "k", "sk-"} {
		if got, want := mayHaveCredentialASCII(text), mayHaveCredentialScan(text); got != want {
			t.Errorf("%q: got %v want %v", text, got, want)
		}
	}
}
