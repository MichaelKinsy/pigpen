package profile

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func env(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func writeProfile(t *testing.T, agent, pkg, content string) string {
	t.Helper()
	dir := filepath.Join(agent, Dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, pkg+".json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustLoad(t *testing.T, content string, kv ...string) Flags {
	t.Helper()
	agent := t.TempDir()
	writeProfile(t, agent, "websearch", content)
	f, err := Load("websearch", agent, env(kv...))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f
}

func misconfigured(t *testing.T, content string, kv ...string) {
	t.Helper()
	agent := t.TempDir()
	writeProfile(t, agent, "websearch", content)
	f, err := Load("websearch", agent, env(kv...))
	if !IsCode(err, ProfileMisconfigured) {
		t.Fatalf("%s: err = %v, want profile_misconfigured", content, err)
	}
	if f.Any() {
		t.Fatalf("%s: a failed load left a flag on", content)
	}
	if f.Status().LastError != ProfileMisconfigured {
		t.Fatalf("%s: status does not report the error", content)
	}
}

// ---- flag-off: the library does nothing unless a profile enables it ----

func TestNoFileMeansEveryFlagOff(t *testing.T) {
	f, err := Load("websearch", t.TempDir(), env())
	if err != nil || f.Any() {
		t.Fatalf("flags=%+v err=%v", f, err)
	}
	st := f.Status()
	if st.ProfileFound || st.LastError != "" || st.Package != "websearch" {
		t.Fatalf("%+v", st)
	}
	if got := st.String(); got != "profile=absent credentialFile=off egressPolicy=off audit=off resourceServer=off policyFailClosed=off headless=off last_error=none" {
		t.Fatal(got)
	}
}

func TestNoAgentDirAndNoEnvironmentMeansEveryFlagOff(t *testing.T) {
	if f, err := Load("websearch", "", env()); err != nil || f.Any() {
		t.Fatalf("flags=%+v err=%v", f, err)
	}
	if f, err := Load("websearch", "", nil); err != nil || f.Any() {
		t.Fatalf("nil getenv: flags=%+v err=%v", f, err)
	}
}

func TestEmptyProfileMeansEveryFlagOff(t *testing.T) {
	for _, c := range []string{`{}`, `{"schema":1}`, `{"headless":{"enabled":false},"audit":{"enabled":false}}`, `{"headless":{}}`, `{"credentialFile":null}`} {
		f := mustLoad(t, c)
		if f.Any() {
			t.Errorf("%s turned a flag on", c)
		}
		if !f.Status().ProfileFound {
			t.Errorf("%s: not reported found", c)
		}
	}
}

func TestFlagOffLoadOpensTheProfileOnceAndStartsNothing(t *testing.T) {
	agent := t.TempDir()
	writeProfile(t, agent, "websearch", `{}`)
	opens := 0
	counting := func(p string, max int64) ([]byte, error) {
		opens++
		return readFile(p, max)
	}
	before := runtime.NumGoroutine()
	for _, dir := range []string{agent, t.TempDir()} { // a present file, then a missing one
		opens = 0
		if _, err := load("websearch", dir, env(), counting); err != nil {
			t.Fatal(err)
		}
		if opens != 1 {
			t.Fatalf("opens = %d, want 1", opens)
		}
	}
	if after := runtime.NumGoroutine(); after != before {
		t.Fatalf("goroutines %d -> %d", before, after)
	}
}

func TestProfileIsNeverReadFromTheWorkingDirectoryOrAProjectDirectory(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, rel := range []string{Dir, filepath.Join(".pig", Dir), filepath.Join(".pig", "agent", Dir)} {
		if err := os.MkdirAll(rel, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rel, "websearch.json"), []byte(`{"headless":{"enabled":true}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := Load("websearch", t.TempDir(), env())
	if err != nil || f.Headless {
		t.Fatalf("read a profile from the working directory: %+v %v", f, err)
	}
	f, err = Load("websearch", "", env())
	if err != nil || f.Headless {
		t.Fatalf("no agent dir read the working directory: %+v %v", f, err)
	}
	// A relative agent directory or profile path would resolve against the working directory: refused.
	if _, err := Load("websearch", ".", env()); !IsCode(err, ProfileMisconfigured) {
		t.Fatalf("relative agent dir: %v", err)
	}
	if _, err := Load("websearch", "", env("PIGPEN_WEBSEARCH_ENTERPRISE", filepath.Join(Dir, "websearch.json"))); !IsCode(err, ProfileMisconfigured) {
		t.Fatalf("relative profile path: %v", err)
	}
}

func TestAgentDir(t *testing.T) {
	if AgentDir(env("PIG_CODING_AGENT_DIR", "/a", "PIG_HOME", "/h")) != "/a" {
		t.Fatal("PIG_CODING_AGENT_DIR wins")
	}
	if got := AgentDir(env("PIG_HOME", "/h")); got != filepath.Join("/h", "agent") {
		t.Fatal(got)
	}
	if AgentDir(env("PI_CODING_AGENT_DIR", "/pi", "HOME", "/home/x")) != "" || AgentDir(nil) != "" {
		t.Fatal("fell back to a Pi directory or HOME")
	}
}

// ---- parse errors are load errors ----

func TestParseErrorsAreLoadErrors(t *testing.T) {
	for _, c := range []string{
		``, `{`, `[]`, `null`, `"x"`, `{"headless":true}`, `{"headless":{"enabled":"yes"}}`, `{"headless":{"enabled":true,"extra":1}}`,
		`{"unknown":{}}`, `{"headless":{"enabled":true},"headless":{"enabled":false}}`, `{"headless":{"enabled":true,"enabled":false}}`,
		`{"headless":{"enabled":true}} {}`, `{"headless":{"enabled":true}} x`, `{"schema":2}`, `{"schema":"1"}`,
		"\xef\xbb\xbf{}", `{"headless":{"enabled":true},}`, `{'headless':{}}`,
		// encoding/json folds key case; a key must name a field exactly, or it would fill or shadow it unseen
		`{"Headless":{"enabled":true}}`, `{"headless":{"Enabled":true}}`, `{"headless":{"enabled":false},"HEADLESS":{"enabled":true}}`,
		`{"egressPolicy":{"enabled":true,"allow":["a.example"],"Allow":["evil.example"]}}`, "{\"\u017fchema\":1}",
	} {
		misconfigured(t, c)
	}
}

func TestFileThatIsTooLargeOrNotRegularIsALoadError(t *testing.T) {
	agent := t.TempDir()
	writeProfile(t, agent, "websearch", `{"audit":{"enabled":true},"pad":"`+strings.Repeat("x", MaxProfileBytes)+`"}`)
	if _, err := Load("websearch", agent, env()); !IsCode(err, ProfileMisconfigured) {
		t.Fatalf("oversize: %v", err)
	}
	agent = t.TempDir()
	if err := os.MkdirAll(filepath.Join(agent, Dir, "websearch.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("websearch", agent, env()); !IsCode(err, ProfileMisconfigured) {
		t.Fatalf("directory: %v", err)
	}
}

func TestUnreadableProfileIsALoadError(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("permissions are not enforced")
	}
	agent := t.TempDir()
	p := writeProfile(t, agent, "websearch", `{}`)
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("websearch", agent, env()); !IsCode(err, ProfileMisconfigured) {
		t.Fatalf("err = %v", err)
	}
}

func TestProfileIsReachedThroughASymlink(t *testing.T) {
	shared := t.TempDir()
	writeProfile(t, shared, "websearch", `{"headless":{"enabled":true}}`)
	agent := t.TempDir()
	if err := os.Symlink(filepath.Join(shared, Dir), filepath.Join(agent, Dir)); err != nil {
		t.Skip("symlinks unavailable")
	}
	f, err := Load("websearch", agent, env())
	if err != nil || !f.Headless {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestInvalidPackageNameIsRefused(t *testing.T) {
	for _, p := range []string{"", "../x", "A", "a b", "a/b"} {
		if _, err := Load(p, t.TempDir(), env()); !IsCode(err, ProfileMisconfigured) {
			t.Errorf("%q: %v", p, err)
		}
	}
}

// ---- environment: on only ----

func TestEnvironmentVariableNamesAnotherFile(t *testing.T) {
	other := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(other, []byte(`{"headless":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	agent := t.TempDir()
	writeProfile(t, agent, "websearch", `{}`)
	f, err := Load("websearch", agent, env("PIGPEN_WEBSEARCH_ENTERPRISE", other))
	if err != nil || !f.Headless {
		t.Fatalf("%+v %v", f, err)
	}
	// A file that was named and is missing is an error, not "no profile".
	if _, err := Load("websearch", agent, env("PIGPEN_WEBSEARCH_ENTERPRISE", other+".missing")); !IsCode(err, ProfileMisconfigured) {
		t.Fatalf("named but missing: %v", err)
	}
}

func TestEnvironmentVariablesUseThePackagePrefix(t *testing.T) {
	if EnvPrefix("pig-doctor") != "PIGPEN_PIG_DOCTOR_ENTERPRISE" || EnvPrefix("a2a") != "PIGPEN_A2A_ENTERPRISE" {
		t.Fatal(EnvPrefix("pig-doctor"))
	}
	want := map[string]string{FlagCredentialFile: "CREDENTIAL_FILE", FlagEgressPolicy: "EGRESS_POLICY", FlagAudit: "AUDIT",
		FlagResourceServer: "RESOURCE_SERVER", FlagPolicyFailClosed: "POLICY_FAIL_CLOSED", FlagHeadless: "HEADLESS"}
	for flag, suffix := range want {
		if EnvFlagName(flag) != suffix {
			t.Errorf("%s -> %s", flag, EnvFlagName(flag))
		}
	}
	// Another Package's variable, and an existing variable of a Package, do nothing.
	agent := t.TempDir()
	f, err := Load("websearch", agent, env("PIGPEN_A2A_ENTERPRISE_HEADLESS", "1", "PIG_A2A_HEADLESS", "1", "PIGPEN_WEBSEARCH_HEADLESS", "1"))
	if err != nil || f.Any() {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestEnvironmentCanTurnAFlagOnAndNeverOff(t *testing.T) {
	// on, with no file
	f, err := Load("websearch", t.TempDir(), env("PIGPEN_WEBSEARCH_ENTERPRISE_HEADLESS", "1", "PIGPEN_WEBSEARCH_ENTERPRISE_POLICY_FAIL_CLOSED", "TRUE", "PIGPEN_WEBSEARCH_ENTERPRISE_AUDIT", "true"))
	if err != nil || !f.Headless || !f.PolicyFailClosed || f.Audit == nil || f.Audit.Sink != SinkStderr {
		t.Fatalf("%+v %v", f, err)
	}
	if f.Status().ProfileFound {
		t.Fatal("no file was found")
	}
	// on over a file that has the flag off, keeping the file's configuration
	f = mustLoad(t, `{"audit":{"enabled":false,"sink":"session-file","required":true}}`, "PIGPEN_WEBSEARCH_ENTERPRISE_AUDIT", "1")
	if f.Audit == nil || f.Audit.Sink != SinkSessionFile || !f.Audit.Required {
		t.Fatalf("%+v", f.Audit)
	}
	// never off
	for _, off := range []string{"0", "false", "FALSE", "", "no", "off"} {
		f = mustLoad(t, `{"headless":{"enabled":true}}`, "PIGPEN_WEBSEARCH_ENTERPRISE_HEADLESS", off)
		if !f.Headless {
			t.Errorf("value %q turned a flag off", off)
		}
	}
	// an off-value does not turn a flag on
	for _, v := range []string{"0", "false", "FALSE", "no", "off", ""} {
		f, err = Load("websearch", t.TempDir(), env("PIGPEN_WEBSEARCH_ENTERPRISE_HEADLESS", v))
		if err != nil || f.Headless {
			t.Errorf("value %q: %+v %v", v, f, err)
		}
	}
	// any other value is a mistake the Package cannot read: it fails closed instead of leaving the flag off
	for _, v := range []string{"yes", "on", "enabled", "2", " 1", "true "} {
		f, err = Load("websearch", t.TempDir(), env("PIGPEN_WEBSEARCH_ENTERPRISE_HEADLESS", v))
		if !IsCode(err, ProfileMisconfigured) || f.Any() {
			t.Errorf("value %q: %+v %v", v, f, err)
		}
	}
}

func TestEnvironmentOnWithoutConfigurationFailsClosed(t *testing.T) {
	for _, flag := range []string{"CREDENTIAL_FILE", "EGRESS_POLICY", "RESOURCE_SERVER"} {
		_, err := Load("websearch", t.TempDir(), env("PIGPEN_WEBSEARCH_ENTERPRISE_"+flag, "1"))
		if !IsCode(err, ProfileMisconfigured) {
			t.Errorf("%s: %v", flag, err)
		}
	}
}

// ---- per-flag configuration ----

const goodCred = `{"enabled":true,"path":"/run/secrets/auth.json","provider":"search","origin":"https://api.example.com"}`

func TestCredentialFileConfiguration(t *testing.T) {
	f := mustLoad(t, `{"credentialFile":`+goodCred+`}`)
	c := f.CredentialFile
	if c == nil || c.Path != "/run/secrets/auth.json" || c.Provider != "search" || c.Origin != "https://api.example.com:443" ||
		c.Header != "Authorization" || c.Scheme != "Bearer" || c.ExpiryMargin != 30*time.Second {
		t.Fatalf("%+v", c)
	}
	f = mustLoad(t, `{"credentialFile":{"enabled":true,"path":"/p","provider":"x","origin":"http://127.0.0.1:9","header":"x-api-key","expiryMarginSeconds":5}}`)
	c = f.CredentialFile
	if c.Header != "X-Api-Key" || c.Scheme != "" || c.ExpiryMargin != 5*time.Second || c.Origin != "http://127.0.0.1:9" {
		t.Fatalf("%+v", c)
	}
	f = mustLoad(t, `{"credentialFile":{"enabled":true,"path":"/p","provider":"x","origin":"https://a.example","header":"X-Token","scheme":"Token"}}`)
	if f.CredentialFile.Scheme != "Token" {
		t.Fatalf("%+v", f.CredentialFile)
	}
}

func TestCredentialFileMisconfigured(t *testing.T) {
	bad := []string{
		`{"enabled":true}`,
		`{"enabled":true,"provider":"x","origin":"https://a.example"}`,
		`{"enabled":true,"path":"/p","origin":"https://a.example"}`,
		`{"enabled":true,"path":"/p","provider":"x"}`,
		`{"enabled":true,"path":"auth.json","provider":"x","origin":"https://a.example"}`,
		`{"enabled":true,"path":"/p","provider":"a b","origin":"https://a.example"}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"http://api.example.com"}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"https://u:p@a.example"}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"https://a.example/v1"}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"ftp://a.example"}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"https://a.example","header":"Host"}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"https://a.example","header":"Bad Header"}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"https://a.example","header":"X\r\nInjected"}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"https://a.example","scheme":"Be arer"}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"https://a.example","expiryMarginSeconds":-1}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"https://a.example","expiryMarginSeconds":3601}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"https://a.example","expiryMarginSeconds":1.5}`,
		`{"enabled":true,"path":"/p","provider":"x","origin":"https://a.example","tokenEnv":"KEY"}`,
	}
	for _, c := range bad {
		misconfigured(t, `{"credentialFile":`+c+`}`)
	}
	// Off, the same configuration is not looked at.
	if f := mustLoad(t, `{"credentialFile":{"enabled":false,"path":"relative"}}`); f.CredentialFile != nil {
		t.Fatal("a disabled flag is on")
	}
}

func testCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEgressPolicyConfiguration(t *testing.T) {
	ca := testCA(t)
	f := mustLoad(t, `{"egressPolicy":{"enabled":true,"allow":["API.Example.com.","`+".Example.org"+`","api.example.com","proxy.internal"],
		"allowCIDRs":["10.0.0.0/8","::ffff:192.168.1.7/120","fd00::/8"],"proxy":"HTTP://Proxy.Internal:3128","noProxy":["localhost",".internal"],"caBundle":"`+ca+`"}}`)
	e := f.EgressPolicy
	if e == nil || strings.Join(e.Allow, ",") != "api.example.com,.example.org,proxy.internal" {
		t.Fatalf("%+v", e)
	}
	var cidrs []string
	for _, p := range e.AllowCIDRs {
		cidrs = append(cidrs, p.String())
	}
	if strings.Join(cidrs, ",") != "10.0.0.0/8,192.168.1.0/24,fd00::/8" {
		t.Fatalf("%v", cidrs)
	}
	if e.Proxy != "http://proxy.internal:3128" || strings.Join(e.NoProxy, ",") != "localhost,.internal" || e.CABundle != ca {
		t.Fatalf("%+v", e)
	}
	if f := mustLoad(t, `{"egressPolicy":{"enabled":true,"allow":["a.example"]}}`); f.EgressPolicy.Proxy != "" || f.EgressPolicy.CABundle != "" {
		t.Fatalf("%+v", f.EgressPolicy)
	}
}

func TestEgressPolicyMisconfigured(t *testing.T) {
	notPEM := filepath.Join(t.TempDir(), "not.pem")
	if err := os.WriteFile(notPEM, []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := []string{
		`{"enabled":true}`,
		`{"enabled":true,"allow":[]}`,
		`{"enabled":true,"allow":["*.example.com"]}`,
		`{"enabled":true,"allow":["example.com:443"]}`,
		`{"enabled":true,"allow":["https://example.com"]}`,
		`{"enabled":true,"allow":[""]}`,
		`{"enabled":true,"allow":["."]}`,
		`{"enabled":true,"allow":["a.example"],"allowCIDRs":["not a cidr"]}`,
		`{"enabled":true,"allow":["a.example"],"allowCIDRs":["0.0.0.0/0"]}`,
		`{"enabled":true,"allow":["a.example"],"allowCIDRs":["::/0"]}`,
		`{"enabled":true,"allow":["a.example"],"allowCIDRs":["10.0.0.1"]}`,
		`{"enabled":true,"allow":["a.example","p"],"proxy":"ftp://p:1"}`,
		`{"enabled":true,"allow":["a.example","p"],"proxy":"http://u:p@p:1"}`,
		`{"enabled":true,"allow":["a.example","p"],"proxy":"http://p"}`,
		`{"enabled":true,"allow":["a.example","p"],"proxy":"http://p:1/path"}`,
		`{"enabled":true,"allow":["a.example","p"],"proxy":"http://p:0"}`,
		`{"enabled":true,"allow":["a.example","p"],"proxy":"p:3128"}`,
		`{"enabled":true,"allow":["a.example"],"noProxy":["*"]}`,
		`{"enabled":true,"allow":["a.example"],"caBundle":"relative.pem"}`,
		`{"enabled":true,"allow":["a.example"],"caBundle":"` + filepath.Join(t.TempDir(), "missing.pem") + `"}`,
		`{"enabled":true,"allow":["a.example"],"caBundle":"` + notPEM + `"}`,
		`{"enabled":true,"allow":["a.example"],"caBundle":"` + t.TempDir() + `"}`,
		`{"enabled":true,"allow":["a.example"],"proxyFromEnvironment":true}`,
		// the proxy is a destination: it must be listed
		`{"enabled":true,"allow":["a.example"],"proxy":"http://proxy.internal:3128"}`,
		`{"enabled":true,"allow":["a.example",".proxy.internal"],"proxy":"http://proxy.internal:3128"}`,
		`{"enabled":true,"allow":["a.example"],"proxy":"http://10.0.0.1:3128","allowCIDRs":["10.0.0.0/8"]}`,
	}
	for _, c := range bad {
		misconfigured(t, `{"egressPolicy":`+c+`}`)
	}
}

func TestACABundleNamedByARelativePathIsRefusedEvenWhenItExists(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	data, err := os.ReadFile(testCA(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "ca.pem"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	misconfigured(t, `{"egressPolicy":{"enabled":true,"allow":["a.example"],"caBundle":"ca.pem"}}`)
}

func TestAuditConfiguration(t *testing.T) {
	if f := mustLoad(t, `{"audit":{"enabled":true}}`); f.Audit == nil || f.Audit.Sink != SinkStderr || f.Audit.Required {
		t.Fatalf("%+v", f.Audit)
	}
	if f := mustLoad(t, `{"audit":{"enabled":true,"sink":"session-file","required":true}}`); f.Audit.Sink != SinkSessionFile || !f.Audit.Required {
		t.Fatalf("%+v", f.Audit)
	}
	for _, c := range []string{`"sink":"/tmp/x"`, `"sink":"stdout"`, `"sink":""`, `"required":"yes"`, `"arguments":true`, `"includeArguments":true`} {
		misconfigured(t, `{"audit":{"enabled":true,`+c+`}}`)
	}
}

func TestResourceServerConfiguration(t *testing.T) {
	f := mustLoad(t, `{"resourceServer":{"enabled":true,"issuer":"https://idp.example/","audience":"a2a","jwksURL":"https://idp.example/jwks.json"}}`)
	r := f.ResourceServer
	if r == nil || r.Issuer != "https://idp.example/" || r.Audience != "a2a" || strings.Join(r.Algorithms, ",") != "RS256" ||
		r.Leeway != 60*time.Second || r.SubjectClaim != "sub" || r.TenantClaim != "tenant" {
		t.Fatalf("%+v", r)
	}
	f = mustLoad(t, `{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"http://localhost:8080/k","algorithms":["ES256","RS256","ES256"],"leewaySeconds":300,"subjectClaim":"user","tenantClaim":"https://example.com/tenant"}}`)
	r = f.ResourceServer
	if strings.Join(r.Algorithms, ",") != "ES256,RS256" || r.Leeway != 300*time.Second || r.SubjectClaim != "user" || r.TenantClaim != "https://example.com/tenant" {
		t.Fatalf("%+v", r)
	}
}

func TestResourceServerRejectsEachBadSetting(t *testing.T) {
	for _, c := range []string{
		`{"resourceServer":{"enabled":true}}`,
		`{"resourceServer":{"enabled":true,"audience":"a","jwksURL":"https://x.example/k"}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","jwksURL":"https://x.example/k"}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a"}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"http://idp.example/k"}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"https://u:p@idp.example/k"}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"https://idp.example/k?x=1"}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"https://idp.example/k","algorithms":["HS256"]}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"https://idp.example/k","algorithms":["none"]}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"https://idp.example/k","algorithms":["rs256"]}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"https://idp.example/k","leewaySeconds":301}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"https://idp.example/k","leewaySeconds":-1}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"https://idp.example/k","subjectClaim":""}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"https://idp.example/k","tenantClaim":"a b"}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i\n","audience":"a","jwksURL":"https://idp.example/k"}}`,
		`{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"https://idp.example/k","jwksDiscovery":true}}`,
	} {
		misconfigured(t, c)
	}
	for _, a := range AllowedAlgorithms() {
		if strings.HasPrefix(a, "HS") || a == "none" {
			t.Fatalf("%s is allowed", a)
		}
	}
}

func TestPolicyFailClosedAndHeadlessConfiguration(t *testing.T) {
	f := mustLoad(t, `{"policyFailClosed":{"enabled":true},"headless":{"enabled":true}}`)
	if !f.PolicyFailClosed || f.PolicyFile != "" || !f.Headless {
		t.Fatalf("%+v", f)
	}
	f = mustLoad(t, `{"policyFailClosed":{"enabled":true,"policyFile":"/etc/policy.cedar"}}`)
	if f.PolicyFile != "/etc/policy.cedar" {
		t.Fatal(f.PolicyFile)
	}
	misconfigured(t, `{"policyFailClosed":{"enabled":true,"policyFile":"policy.cedar"}}`)
	misconfigured(t, `{"policyFailClosed":{"enabled":true,"failOpen":true}}`)
	misconfigured(t, `{"headless":{"enabled":true,"answer":"allow"}}`)
}

// ---- Status, Require, CheckInsecureNoAuth ----

func TestStatusHoldsNoSecretAndNoPath(t *testing.T) {
	ca := testCA(t)
	f := mustLoad(t, `{"credentialFile":{"enabled":true,"path":"/run/secrets/SECRETPATH.json","provider":"SECRETPROVIDER","origin":"https://secret-host.example"},
		"egressPolicy":{"enabled":true,"allow":["secret-host.example","secret-proxy"],"proxy":"http://secret-proxy:3128","caBundle":"`+ca+`"},
		"resourceServer":{"enabled":true,"issuer":"SECRETISSUER","audience":"SECRETAUD","jwksURL":"https://secret-host.example/k"},
		"policyFailClosed":{"enabled":true,"policyFile":"/etc/SECRETPOLICY.cedar"},"audit":{"enabled":true},"headless":{"enabled":true}}`)
	s := f.Status().String()
	for _, secret := range []string{"SECRET", "secret", "/run", "/etc", "https://", "proxy", ca} {
		if strings.Contains(s, secret) {
			t.Fatalf("status leaks %q: %s", secret, s)
		}
	}
	if s != "profile=found credentialFile=on egressPolicy=on audit=on resourceServer=on policyFailClosed=on headless=on last_error=none" {
		t.Fatal(s)
	}
	// A misconfigured file also reports no content.
	agent := t.TempDir()
	writeProfile(t, agent, "websearch", `{"credentialFile":{"enabled":true,"path":"/SECRETPATH"}}`)
	bad, err := Load("websearch", agent, env())
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(bad.Status().String(), "SECRET") {
		t.Fatalf("%v / %s", err, bad.Status())
	}
	if !strings.Contains(bad.Status().String(), "last_error=profile_misconfigured") || !bad.Status().ProfileFound {
		t.Fatal(bad.Status())
	}
	if !strings.Contains(err.Error(), FlagCredentialFile) {
		t.Fatalf("the flag is not named: %v", err)
	}
}

func TestRequireRejectsAFlagThePackageDoesNotSupport(t *testing.T) {
	f := mustLoad(t, `{"headless":{"enabled":true},"audit":{"enabled":true}}`)
	if err := f.Require(FlagHeadless, FlagAudit); err != nil {
		t.Fatal(err)
	}
	err := f.Require(FlagAudit)
	var pe *Error
	if !IsCode(err, ProfileMisconfigured) || !asError(err, &pe) || pe.Flag != FlagHeadless || pe.Package != "websearch" {
		t.Fatalf("%v", err)
	}
	if err := (Flags{}).Require(); err != nil {
		t.Fatal("the flag-off profile has no unsupported flag")
	}
}

func TestInsecureNoAuthWithResourceServerIsMisconfigured(t *testing.T) {
	f := mustLoad(t, `{"resourceServer":{"enabled":true,"issuer":"i","audience":"a","jwksURL":"https://idp.example/k"}}`)
	if err := f.CheckInsecureNoAuth(true); !IsCode(err, ProfileMisconfigured) {
		t.Fatal(err)
	}
	if err := f.CheckInsecureNoAuth(false); err != nil {
		t.Fatal(err)
	}
	if err := (Flags{}).CheckInsecureNoAuth(true); err != nil {
		t.Fatal("insecureNoAuth alone is not the profile's business")
	}
}
