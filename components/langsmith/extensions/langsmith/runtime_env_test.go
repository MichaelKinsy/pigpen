package langsmith

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

type envScenario struct {
	Name     string             `json:"name"`
	Env      []string           `json:"env"`
	Stderr   string             `json:"stderr"`
	Metadata [][2]string        `json:"metadata"`
	Vars     [][2]string        `json:"vars"`
	Runtime  [][2]string        `json:"runtime"`
	Shas     [][2]string        `json:"shas"`
	LsVar    map[string]*string `json:"lsVar"`
	Otel     bool               `json:"otel"`
	Mode     struct {
		Ok    string `json:"ok"`
		Error string `json:"error"`
	} `json:"mode"`
}

type envVectors struct {
	SDKVersion string        `json:"sdk_version"`
	Scenarios  []envScenario `json:"scenarios"`
}

func loadEnv(t testing.TB) *envVectors {
	var v envVectors
	loadWireVectors(t, "wire-env.json", &v)
	if len(v.Scenarios) < 25 {
		t.Fatalf("vectors: %d scenarios", len(v.Scenarios))
	}
	return &v
}

func entries(t *testing.T, m *omap) [][2]string {
	t.Helper()
	out := make([][2]string, 0, len(m.keys))
	for _, k := range m.keys {
		s, ok := m.vals[k].(string)
		if !ok {
			t.Fatalf("value of %q is %T", k, m.vals[k])
		}
		out = append(out, [2]string{k, s})
	}
	return out
}

func eqEntries(a, b [][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lookupIn(environ []string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		for _, kv := range environ {
			if k, v, ok := strings.Cut(kv, "="); ok && k == name {
				return v, true
			}
		}
		return "", false
	}
}

func TestSDKVersionConstant(t *testing.T) {
	if v := loadEnv(t); v.SDKVersion != sdkVersion {
		t.Fatalf("sdkVersion %q, JS __version__ %q", sdkVersion, v.SDKVersion)
	}
}

func TestLangSmithEnvVarsMetadataVectors(t *testing.T) {
	for _, sc := range loadEnv(t).Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			got := entries(t, langSmithEnvVarsMetadataFrom(sc.Env))
			if !eqEntries(got, sc.Metadata) {
				t.Errorf("metadata: got %v want %v", got, sc.Metadata)
			}
			vars := entries(t, langSmithEnvFrom(sc.Env, true))
			if !eqEntries(vars, sc.Vars) {
				t.Errorf("vars: got %v want %v", vars, sc.Vars)
			}
		})
	}
}

func TestRuntimeEnvironmentVectors(t *testing.T) {
	for _, sc := range loadEnv(t).Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			lookup := lookupIn(sc.Env)
			if got := entries(t, shasFrom(lookup)); !eqEntries(got, sc.Shas) {
				t.Errorf("shas: got %v want %v", got, sc.Shas)
			}
			// Same keys in the same order as the SDK; the values of runtime/sdk/sdk_version describe this process (D1).
			want := make([][2]string, len(sc.Runtime))
			copy(want, sc.Runtime)
			for i := range want {
				switch want[i][0] {
				case "runtime":
					want[i][1] = "go"
				case "sdk":
					want[i][1] = "pigpen-langsmith-go"
				case "sdk_version":
					want[i][1] = portVersion
				}
			}
			if got := entries(t, runtimeEnvFrom(lookup)); !eqEntries(got, want) {
				t.Errorf("runtime: got %v want %v", got, want)
			}
		})
	}
}

// withEnv replaces the LangSmith/LangChain/OTEL variables of the process with the scenario's.
func withEnv(t *testing.T, environ []string) {
	t.Helper()
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "LANGSMITH_") || strings.HasPrefix(k, "LANGCHAIN_") || k == "OTEL_ENABLED" {
			os.Unsetenv(k)
			t.Cleanup(func() { os.Setenv(k, v) })
		}
	}
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if k == "OTEL_ENABLED" || strings.HasPrefix(k, "LANGSMITH_") || strings.HasPrefix(k, "LANGCHAIN_") {
			t.Setenv(k, v)
		}
	}
}

func TestEnvAccessorsAndTracingModeVectors(t *testing.T) {
	for _, sc := range loadEnv(t).Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			withEnv(t, sc.Env)
			for name, want := range sc.LsVar {
				got, ok := getLangSmithEnvironmentVariable(name)
				if want == nil && ok || want != nil && (!ok || got != *want) {
					t.Errorf("getLangSmithEnvironmentVariable(%s): %q,%v want %v", name, got, ok, want)
				}
			}
			if got := getOtelEnabled(); got != sc.Otel {
				t.Errorf("getOtelEnabled: %v want %v", got, sc.Otel)
			}
			var buf bytes.Buffer
			old := wireWarnWriter
			wireWarnWriter = &buf
			defer func() { wireWarnWriter = old }()
			mode, err := resolveTracingMode(nil)
			switch {
			case sc.Mode.Error != "":
				if err == nil || err.Error() != sc.Mode.Error {
					t.Errorf("resolveTracingMode error: %v want %q", err, sc.Mode.Error)
				}
			case err != nil || mode != sc.Mode.Ok:
				t.Errorf("resolveTracingMode: %q, %v want %q", mode, err, sc.Mode.Ok)
			}
			if buf.String() != sc.Stderr {
				t.Errorf("warning: got %q want %q", buf.String(), sc.Stderr)
			}
			// the real-process accessors agree with the scenario-driven ones
			if got := entries(t, getLangSmithEnvVarsMetadata()); !eqEntries(got, sc.Metadata) {
				t.Errorf("getLangSmithEnvVarsMetadata: got %v want %v", got, sc.Metadata)
			}
		})
	}
}

func TestResolveTracingModeExplicit(t *testing.T) {
	withEnv(t, []string{"LANGSMITH_TRACING_MODE=bogus"})
	m := "otel"
	if got, err := resolveTracingMode(&m); err != nil || got != "otel" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestGetRuntimeEnvironmentCachedAndTruthful(t *testing.T) {
	a, b := getRuntimeEnvironment(), getRuntimeEnvironment()
	if a != b {
		t.Fatal("not cached")
	}
	if a.keys[0] != "library" || a.vals["library"] != "langsmith" || a.vals["runtime"] != "go" ||
		a.vals["sdk"] != "pigpen-langsmith-go" || a.vals["sdk_version"] != portVersion {
		t.Fatalf("%v", entries(t, a))
	}
}

func TestIsSensitiveEnvVarName(t *testing.T) {
	for k, want := range map[string]bool{
		"LANGSMITH_API_KEY": true, "x_Secret": true, "TOKEN": true, "my_PassWord": true, "a_pwd": true,
		"EMAIL_ADDR": true, "LANGSMITH_CREDENTIALS": true, "LANGSMITH_PROJECT": false, "LANGSMITH_PI_DEBUG": false,
		"\u212Aey": true, "ÉCOLE": false,
	} {
		if got := isSensitiveEnvVarName(k); got != want {
			t.Errorf("%q: got %v want %v", k, got, want)
		}
	}
}

func TestMaskSecret(t *testing.T) {
	for in, want := range map[string]string{"abcd": "abcd", "abcdef": "ab**ef", "\U0001F600\U0001F600": "\U0001F600\U0001F600"} {
		if got, ok := maskSecret(in); !ok || got != want {
			t.Errorf("%q: %q %v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "abc", "\U0001F600", "\u00e9\U0001F600"} {
		if _, ok := maskSecret(in); ok {
			t.Errorf("%q must fail (fewer than 4 UTF-16 units)", in)
		}
	}
}

func BenchmarkGetLangSmithEnvVarsMetadata(b *testing.B) {
	b.Setenv("LANGSMITH_PI_PROJECT", "p")
	b.Setenv("LANGSMITH_API_KEY", "abcdefgh")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		getLangSmithEnvVarsMetadata()
	}
}
