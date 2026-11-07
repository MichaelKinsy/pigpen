package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lstools/internal/reqlog"
)

func writeLog(t *testing.T, dir, name, body string) string {
	t.Helper()
	e := reqlog.Entry{Seq: 1, Route: "runs", Method: "POST", Path: "/runs", Transfer: "content-length", ContentLength: int64(len(body)),
		Headers: map[string]string{"content-type": "application/json"}, Body: []byte(body)}
	line, _ := json.Marshal(e)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExitCodesAndReport(t *testing.T) {
	dir := t.TempDir()
	a := writeLog(t, dir, "a.jsonl", `{"id":"0198a000-0000-7000-8000-000000000001","name":"x"}`)
	same := writeLog(t, dir, "b.jsonl", `{"id":"01ffb111-1111-7111-8111-00000000000a","name":"x"}`)
	other := writeLog(t, dir, "c.jsonl", `{"id":"01ffb111-1111-7111-8111-00000000000a","name":"y"}`)

	var out, errb bytes.Buffer
	if code := run([]string{a, same}, &out, &errb); code != 0 || !strings.Contains(out.String(), "IDENTICAL") {
		t.Fatalf("code %d\n%s%s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{a, other}, &out, &errb); code != 1 || !strings.Contains(out.String(), "first difference") || !strings.Contains(out.String(), `\"y\"`) {
		t.Fatalf("code %d\n%s", code, out.String())
	}
	out.Reset()
	if code := run([]string{a}, &out, &errb); code != 2 {
		t.Fatalf("usage code %d", code)
	}
	if code := run([]string{a, filepath.Join(dir, "missing")}, &out, &errb); code != 2 {
		t.Fatalf("missing file code %d", code)
	}
	out.Reset()
	if code := run([]string{"-rules"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "N5") || !strings.Contains(out.String(), "N1") {
		t.Fatalf("rules: %d %s", code, out.String())
	}
}

func TestScenarioNormalizeRulesAreN7(t *testing.T) {
	dir := t.TempDir()
	a := writeLog(t, dir, "a.jsonl", `{"response":"resp_aaa111"}`)
	b := writeLog(t, dir, "b.jsonl", `{"response":"resp_bbb222"}`)
	sc := filepath.Join(dir, "sc.json")
	os.WriteFile(sc, []byte(`{"name":"x","normalize":[{"pattern":"resp_[a-z0-9]+","replacement":"<resp>"}]}`), 0o600)
	var out, errb bytes.Buffer
	if code := run([]string{a, b}, &out, &errb); code != 1 {
		t.Fatalf("without the rule: %d", code)
	}
	out.Reset()
	if code := run([]string{"-scenario", sc, a, b}, &out, &errb); code != 0 || !strings.Contains(out.String(), "N7  1 / 1") {
		t.Fatalf("with the rule: %d\n%s%s", code, out.String(), errb.String())
	}
}
