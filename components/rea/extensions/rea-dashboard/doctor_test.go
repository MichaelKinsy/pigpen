package readashboard

import (
	"strings"
	"testing"
)

func TestParseRealDoctorSample(t *testing.T) {
	report, err := parseDoctor(readTestdata(t, "doctor.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Healthy || report.REAVersion != "6.8.0" || report.Tools != 229 {
		t.Fatalf("report = %+v", report)
	}
	var ids []string
	for _, p := range report.Providers {
		ids = append(ids, p.ID)
		if p.State != provNotConfigured {
			t.Errorf("%s: state %v, want not configured (nothing is configured in the sample)", p.ID, p.State)
		}
		if p.Label == "" || len(p.Checks) == 0 {
			t.Errorf("%s: %+v", p.ID, p)
		}
	}
	if strings.Join(ids, ",") != "hopper,ghidra,ida,jadx,kuna" {
		t.Fatalf("providers = %v", ids)
	}
	byID := map[string]provider{}
	for _, p := range report.Providers {
		byID[p.ID] = p
	}
	if !strings.Contains(byID["ghidra"].Detail, "GHIDRA_INSTALL_DIR") || !strings.Contains(byID["hopper"].Remediation, "HOPPER_LAUNCHER_PATH") {
		t.Fatalf("details: ghidra=%q hopper=%q", byID["ghidra"].Detail, byID["hopper"].Remediation)
	}
	// node and host are healthy; the skill and seven client registrations drift.
	if report.FailedOther != 8 {
		t.Fatalf("other failures = %d", report.FailedOther)
	}
	ready, failing, none := report.summary()
	if ready != 0 || failing != 0 || none != 5 {
		t.Fatalf("summary = %d %d %d", ready, failing, none)
	}
	// Every provider the doctor reports has its variables listed.
	for _, p := range report.Providers {
		if len(providerEnv[p.ID]) == 0 {
			t.Errorf("no environment variables for %s", p.ID)
		}
	}
}

func TestParseDoctorStates(t *testing.T) {
	report, err := parseDoctor([]byte(`{
	  "healthy": true,
	  "checks": [
	    {"name":"node","ok":true,"classification":"healthy"},
	    {"name":"hopper","ok":true,"classification":"healthy","detail":"/opt/Hopper"},
	    {"name":"hopper-version","ok":true,"classification":"healthy"},
	    {"name":"ghidra","ok":false,"classification":"missing_analysis_engine"}
	  ],
	  "hopperPath": "/opt/Hopper/launcher",
	  "providerInspections": [
	    {"id":"ghidra","configured":true,"available":false,"providerVersion":null,"checks":[{"name":"java","ok":false,"code":"missing_java","detail":"JAVA_HOME is not set","remediation":null,"classification":"missing_dependency"}]},
	    {"id":"kuna","configured":true,"available":true,"providerVersion":"1.833","checks":[{"name":"installation","ok":true,"code":null,"detail":"ok","remediation":null}]},
	    {"id":"jadx","configured":null,"available":null,"providerVersion":null,"checks":[]},
	    {"id":"rizin","configured":false,"available":false,"providerVersion":null,"checks":[]}
	  ],
	  "identity": {"cli_package_version":"9.9.9","catalog":{"counts":{"mcp_tools":3}}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]provState{"hopper": provReady, "ghidra": provFailing, "kuna": provReady, "jadx": provUnknown, "rizin": provNotConfigured}
	for _, p := range report.Providers {
		if want[p.ID] != p.State {
			t.Errorf("%s = %v, want %v", p.ID, p.State, want[p.ID])
		}
		delete(want, p.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing providers %v", want)
	}
	byID := map[string]provider{}
	for _, p := range report.Providers {
		byID[p.ID] = p
	}
	if byID["hopper"].Detail != "/opt/Hopper/launcher" || byID["kuna"].Version != "1.833" || byID["ghidra"].Detail != "JAVA_HOME is not set" {
		t.Fatalf("details: %+v", byID)
	}
	if report.Providers[0].ID != "hopper" || report.Providers[len(report.Providers)-1].ID != "rizin" {
		t.Fatalf("order: %v", report.Providers)
	}
}

func TestParseDoctorHopperConfiguredButBroken(t *testing.T) {
	report, err := parseDoctor([]byte(`{"checks":[{"name":"hopper","ok":false,"classification":"missing_analysis_engine","detail":"/opt/gone"}],"identity":{"cli_package_version":"1"}}`))
	if err != nil || report.Providers[0].State != provFailing {
		t.Fatalf("report = %+v err=%v", report, err)
	}
}

func TestParseDoctorRejectsOtherJSON(t *testing.T) {
	for _, in := range []string{``, `not json`, `{}`, `[]`, `{"error":"Analysis failed","code":"x"}`} {
		if _, err := parseDoctor([]byte(in)); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestEnvVarsMatchReaSchemaNames(t *testing.T) {
	// A few names taken from REA's src/config/environment.ts and its Kuna/JADX configuration.
	for id, name := range map[string]string{
		"ghidra": "GHIDRA_INSTALL_DIR", "hopper": "HOPPER_LAUNCHER_PATH", "ida": "REA_IDA_MCP_CONFIG",
		"jadx": "REA_JADX_MCP_JAR", "kuna": "REA_KUNA_COMMAND", "jeb": "REA_JEB_MCP_URL",
	} {
		found := false
		for _, e := range providerEnv[id] {
			found = found || e.Name == name
		}
		if !found {
			t.Errorf("%s lacks %s", id, name)
		}
	}
}

func TestRunDoctorUsesReportDespiteExitStatus(t *testing.T) {
	fr := &fakeRunner{t: t}
	report, err := runDoctor(t.Context(), fr.run, testServer)
	if err != nil || report.REAVersion != "6.8.0" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	fr.failure = errTest
	if _, err := runDoctor(t.Context(), fr.run, testServer); err == nil {
		t.Fatal("failure not reported")
	}
}

var errTest = &testError{"boom"}

type testError struct{ s string }

func (e *testError) Error() string { return e.s }
