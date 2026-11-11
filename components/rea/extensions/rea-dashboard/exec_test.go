package readashboard

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// scriptServer is a server whose "node" is /bin/sh and whose "rea.mjs" is body.
func scriptServer(t *testing.T, body string) reaServer {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	script := filepath.Join(t.TempDir(), "rea.sh")
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return reaServer{Present: true, Node: "/bin/sh", Entry: script}
}

func TestExecREARunsOneShotWithTheProcessEnvironment(t *testing.T) {
	t.Setenv("REA_TEST_SETTING", "from-the-host")
	srv := scriptServer(t, `echo "args=$*"; echo "env=${REA_TEST_SETTING-unset}"`)
	out, truncated, err := execREA(t.Context(), srv, []string{"doctor", "--json"}, 1024)
	if err != nil || truncated {
		t.Fatalf("err=%v truncated=%v", err, truncated)
	}
	if got := string(out); got != "args=doctor --json\nenv=from-the-host\n" {
		t.Fatalf("output %q", got)
	}
}

func TestExecREACapsOutputAndStopsTheProcess(t *testing.T) {
	srv := scriptServer(t, `yes aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`)
	start := time.Now()
	out, truncated, err := execREA(t.Context(), srv, []string{"inspect-artifact", "--json", "/x"}, inspectLimit)
	if err != nil || !truncated || len(out) != inspectLimit {
		t.Fatalf("len=%d truncated=%v err=%v", len(out), truncated, err)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatal("the process was not stopped")
	}
}

func TestExecREAReportsFailureWithStderr(t *testing.T) {
	srv := scriptServer(t, `echo "partial"; echo "bad things" >&2; exit 3`)
	out, _, err := execREA(t.Context(), srv, []string{"doctor"}, 1024)
	if err == nil || !strings.Contains(err.Error(), "bad things") || string(out) != "partial\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestExecREARequiresARegisteredServer(t *testing.T) {
	if _, _, err := execREA(t.Context(), reaServer{}, []string{"doctor"}, 10); err == nil {
		t.Fatal("ran without a server")
	}
}

func TestRunInspectRefusesNonPaths(t *testing.T) {
	fr := &fakeRunner{t: t}
	for _, target := range []string{"https://example.test", "-rf", "relative/path", ""} {
		res := runInspect(t.Context(), fr.run, testServer, target)
		if res.OK || len(res.Summary) == 0 {
			t.Errorf("%q: %+v", target, res)
		}
	}
	if _, inspect := fr.counts(); inspect != 0 {
		t.Fatalf("ran %d inspections", inspect)
	}
}

func TestFormatInspect(t *testing.T) {
	res := formatInspect(readTestdata(t, "inspect-artifact.json"), false)
	if !res.OK || !strings.HasPrefix(res.Summary[0], "evidence ev_") || !strings.Contains(strings.Join(res.Summary, "\n"), "limitation:") || len(res.Raw) < 10 {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "subject: ls") {
		t.Fatalf("summary = %v", res.Summary)
	}

	errRes := formatInspect([]byte(`{"error":"Analysis failed","code":"target_unavailable","message":"Cannot open artifact"}`), false)
	if errRes.OK || !strings.Contains(errRes.Summary[0], "Analysis failed") || !strings.Contains(strings.Join(errRes.Summary, "\n"), "Cannot open artifact") {
		t.Fatalf("error result = %+v", errRes)
	}
	if cut := formatInspect([]byte(`{"evidence_id":"x"`), true); cut.OK || !cut.Truncated || !strings.Contains(cut.Summary[0], "64 KB") {
		t.Fatalf("truncated = %+v", cut)
	}
	if text := formatInspect([]byte("plain\ntext"), false); text.OK || len(text.Raw) != 2 {
		t.Fatalf("text = %+v", text)
	}
	if empty := formatInspect(nil, false); empty.OK || empty.Summary[0] != "no output" {
		t.Fatalf("empty = %+v", empty)
	}
}

func TestFindREAAndPayloadDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "payload.json"), []byte(`{"target":"linux/amd64","rea":{"package":"rea-agents","version":"6.8.0"},"node":{"version":"24.22.2"},"treeSha256":"abc","files":3,"bytes":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(dir, "node", "bin", "node")
	srv := findREA(registered(node, filepath.Join(dir, "rea", "scripts", "rea.mjs")))
	if !srv.Present || srv.Dir != dir || srv.Payload.Rea.Version != "6.8.0" || srv.Payload.TreeSha256 != "abc" || srv.Entry != filepath.Join(dir, "rea", "scripts", "rea.mjs") || !srv.runnable() {
		t.Fatalf("server = %+v", srv)
	}
	// Windows layout: node\node.exe.
	if got := payloadDir(filepath.Join(dir, "node", "node.exe")); got != dir {
		t.Fatalf("windows layout: %q", got)
	}
	// A command outside a payload still runs, but has no payload description.
	other := findREA(registered("/usr/bin/node", "/x/rea.mjs"))
	if !other.Present || other.Dir != "" || other.Err == "" || !other.runnable() {
		t.Fatalf("other = %+v", other)
	}
	// No rea server.
	if findREA(nil).Present {
		t.Fatal("present without servers")
	}
}
