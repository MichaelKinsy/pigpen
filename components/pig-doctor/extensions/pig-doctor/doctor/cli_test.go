//go:build unix

package doctor

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, rc *realCase, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	testOptionsHook = func(o *Options) { o.Procs = rc.procs }
	t.Cleanup(func() { testOptionsHook = nil })
	env := []string{"HOME=" + rc.user, "PIG_HOME=" + rc.home, "PATH=" + rc.bin + ":/usr/bin:/bin"}
	code := Run(args, IO{In: strings.NewReader(stdin), Out: &out, Err: &errb}, env)
	return code, out.String(), errb.String()
}

func TestCLICheckIsTheDefault(t *testing.T) {
	rc := newRealCase(t)
	before := snapshot(t, rc.root)
	code, out, errs := runCLI(t, rc, "")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "ERRORS") || !strings.Contains(out, "unknown, left alone") {
		t.Errorf("%s", out)
	}
	if d := diffSnapshots(before, snapshot(t, rc.root)); len(d) != 0 {
		t.Errorf("check changed the tree: %v", d)
	}
	code2, out2, _ := runCLI(t, rc, "", "check")
	if code2 != 0 || out2 != out {
		t.Error("`check` and no argument are the same")
	}
}

func TestCLIStrictAndJSON(t *testing.T) {
	rc := newRealCase(t)
	if code, _, _ := runCLI(t, rc, "", "check", "--strict"); code != 1 {
		t.Errorf("--strict with errors should exit 1, got %d", code)
	}
	code, out, _ := runCLI(t, rc, "", "check", "--json")
	if code != 0 {
		t.Fatal(code)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rep.Findings) == 0 || len(rep.Unknown) != 6 {
		t.Errorf("%+v", rep)
	}
	f := newFixture(t)
	rcClean := &realCase{fixture: f}
	if code, _, _ := runCLI(t, rcClean, "", "check", "--strict"); code != 0 {
		t.Errorf("clean home: exit %d", code)
	}
}

func TestCLIFixDryRun(t *testing.T) {
	rc := newRealCase(t)
	before := snapshot(t, rc.root)
	code, out, errs := runCLI(t, rc, "", "fix", "--dry-run")
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	if !strings.Contains(out, "remove packages[0]") || !strings.Contains(out, "DRY RUN") {
		t.Errorf("%s", out)
	}
	if d := diffSnapshots(before, snapshot(t, rc.root)); len(d) != 0 {
		t.Errorf("dry run changed the tree: %v", d)
	}
}

func TestCLIFixYesAppliesSafeGroupsOnly(t *testing.T) {
	rc := newRealCase(t)
	code, out, errs := runCLI(t, rc, "", "fix", "--yes")
	if code != 0 {
		t.Fatalf("%d %s\n%s", code, errs, out)
	}
	if exists(filepath.Join(rc.home, "kagent-pr-agent")) {
		t.Error("safe group should be applied")
	}
	if !exists(filepath.Join(rc.home, "extensions")) || !exists(filepath.Join(rc.home, "launch-workers-agent", "auth.json")) {
		t.Error("--yes must not cover confirm-level or credential groups")
	}
	if !strings.Contains(out, "skipped") || !strings.Contains(out, "pig-doctor restore") {
		t.Errorf("summary should mention skipped groups and how to restore:\n%s", out)
	}
}

func TestCLIInteractiveConfirmPerGroup(t *testing.T) {
	rc := newRealCase(t)
	// Answer no to everything except the legacy group.
	code, out, errs := runCLI(t, rc, "", "fix", "--group", "legacy")
	_ = errs
	if code == 0 && exists(filepath.Join(rc.home, "extensions")) == false {
		t.Errorf("no answer must mean no: %s", out)
	}
	code, out, _ = runCLI(t, rc, "y\n", "fix", "--group", "legacy")
	if code != 0 || exists(filepath.Join(rc.home, "extensions")) {
		t.Errorf("y should apply the legacy group (exit %d):\n%s", code, out)
	}
	if !strings.Contains(out, "Apply group") {
		t.Errorf("prompt missing:\n%s", out)
	}
}

func TestCLICredentialsNeedTheTypedPhrase(t *testing.T) {
	rc := newRealCase(t)
	code, out, _ := runCLI(t, rc, "y\n", "fix", "--group", "credentials")
	if !exists(filepath.Join(rc.home, "launch-workers-agent", "auth.json")) {
		t.Fatalf("a plain y must not delete credentials (exit %d):\n%s", code, out)
	}
	if !strings.Contains(out, "remove credentials") {
		t.Errorf("prompt should ask for the phrase:\n%s", out)
	}
	code, out, _ = runCLI(t, rc, "remove credentials\n", "fix", "--group", "credentials")
	if code != 0 || exists(filepath.Join(rc.home, "launch-workers-agent", "auth.json")) {
		t.Errorf("typed phrase should delete the copy (exit %d):\n%s", code, out)
	}
	if strings.Contains(out, canary) {
		t.Error("leak")
	}
}

func TestCLICredentialFlagWithYes(t *testing.T) {
	rc := newRealCase(t)
	if code, _, _ := runCLI(t, rc, "", "fix", "--yes", "--group", "credentials"); code != 0 || !exists(filepath.Join(rc.home, "launch-workers-agent", "auth.json")) {
		t.Errorf("--yes alone never covers credentials (exit %d)", code)
	}
	if code, out, _ := runCLI(t, rc, "", "fix", "--yes", "--confirm-credentials", "--group", "credentials"); code != 0 || exists(filepath.Join(rc.home, "launch-workers-agent", "auth.json")) {
		t.Errorf("--confirm-credentials is the explicit confirmation (exit %d)\n%s", code, out)
	}
}

func TestCLIRefusesWhileLocked(t *testing.T) {
	rc := newRealCase(t)
	rc.holdLock(filepath.Join(rc.agent, "settings.json.lock"))
	code, out, errs := runCLI(t, rc, "", "fix", "--yes")
	if code != 3 || !strings.Contains(errs+out, "refusing") {
		t.Errorf("exit %d\n%s\n%s", code, out, errs)
	}
}

func TestCLIRestore(t *testing.T) {
	rc := newRealCase(t)
	_, out, _ := runCLI(t, rc, "", "fix", "--yes", "--group", "packages")
	ts := ""
	for _, w := range strings.Fields(out) {
		if strings.HasPrefix(w, "20") && strings.Contains(w, "T") && strings.HasSuffix(strings.TrimRight(w, ".)"), "Z") {
			ts = strings.TrimRight(w, ".)")
		}
	}
	if ts == "" {
		t.Fatalf("no timestamp in output:\n%s", out)
	}
	code, out, _ := runCLI(t, rc, "", "backups")
	if code != 0 || !strings.Contains(out, ts) {
		t.Errorf("%s", out)
	}
	code, out, errs := runCLI(t, rc, "", "restore", ts)
	if code != 0 || !strings.Contains(rc.readSettings(), "utils/pig-stuff") {
		t.Errorf("restore failed (exit %d): %s %s", code, out, errs)
	}
	if code, _, _ := runCLI(t, rc, "", "restore", "bogus"); code == 0 {
		t.Error("restore bogus should fail")
	}
}

func TestCLIUsage(t *testing.T) {
	rc := newRealCase(t)
	for _, args := range [][]string{{"explode"}, {"fix", "--nope"}, {"restore"}, {"check", "--orphan-days", "x"}} {
		if code, _, errs := runCLI(t, rc, "", args...); code != 2 || errs == "" {
			t.Errorf("%v: exit %d %q", args, code, errs)
		}
	}
	if code, out, _ := runCLI(t, rc, "", "--help"); code != 0 || !strings.Contains(out, "pig-doctor check") {
		t.Errorf("help: %d %s", code, out)
	}
}

func TestCLIHomeResolution(t *testing.T) {
	rc := newRealCase(t)
	for _, tc := range []struct {
		env  []string
		want string
	}{
		{[]string{"PIG_HOME=/a", "HOME=/u"}, "/a"},
		{[]string{"XDG_CONFIG_HOME=/x", "HOME=/u"}, "/x/pig"},
		{[]string{"HOME=/u"}, "/u/.pig"},
		{[]string{"PIG_HOME=/a", "PIG_CODING_AGENT_DIR=/z", "HOME=/u"}, "/a"},
	} {
		o, err := optionsFromEnv(tc.env)
		if err != nil || o.Home != tc.want {
			t.Errorf("%v: %v %v", tc.env, o.Home, err)
		}
	}
	o, _ := optionsFromEnv([]string{"PIG_HOME=/a", "PIG_CODING_AGENT_DIR=/z", "HOME=/u"})
	if o.AgentDir != "/z" || o.UserHome != "/u" {
		t.Errorf("%+v", o)
	}
	if _, err := optionsFromEnv([]string{}); err == nil {
		t.Error("no HOME and no PIG_HOME must be an error, not a guess")
	}
	_ = rc
}
