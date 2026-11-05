package hc

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestParseListStringStatus(t *testing.T) {
	out := []byte(`{"hidden_finished":3,"terminals":[
	 {"address":"t5","agent":"pig","cwd":"/x","name":"a","state":"live","status":"working","turn":2},
	 {"address":"t6","name":"b","state":"live","status":null,"turn":0},
	 {"address":"t7","name":"c","state":"finished","turn":1}]}`)
	ts, err := ParseList(out)
	if err != nil || len(ts) != 2 || ts[0].Status != "working" || ts[0].Turn != 2 || ts[1].Status != "" {
		t.Fatalf("%+v %v", ts, err)
	}
}

func TestStatusObject(t *testing.T) {
	if s := statusOf([]byte(`{"app":"pig","state":"blocked","kind":"question"}`)); s != "blocked" {
		t.Fatal(s)
	}
}

func TestEnvironCarriesTerminalSocket(t *testing.T) {
	env := Environ([]string{"A=1", "HC_TERMINAL_SOCKET=/s/hc.sock"})
	if !slices.Contains(env, "HC_SOCKET=/s/hc.sock") {
		t.Fatalf("%v", env)
	}
	env = Environ([]string{"HC_SOCKET=/mine", "HC_TERMINAL_SOCKET=/s/hc.sock"})
	if slices.Contains(env, "HC_SOCKET=/s/hc.sock") {
		t.Fatalf("overrode HC_SOCKET: %v", env)
	}
}

func TestGetStatusMs(t *testing.T) {
	var w wireTerm
	if err := json.Unmarshal([]byte(`{"address":"t5","status":{"app":"pig","state":"blocked"},"status_ms":1793837040000,"turn":1}`), &w); err != nil {
		t.Fatal(err)
	}
	if tm := w.term(); tm.Status != "blocked" || tm.StatusMs != 1793837040000 {
		t.Fatalf("%+v", tm)
	}
}

func TestSendErrorKinds(t *testing.T) {
	dir := t.TempDir()
	script := dir + "/hc"
	os.WriteFile(script, []byte("#!/bin/sh\necho '{\"error\":{\"kind\":\"status_mismatch\",\"message\":\"t1 is done, not idle\"}}'; exit 10\n"), 0o755)
	c := &CLI{Bin: script, Env: os.Environ()}
	if err := c.Send("t1", "hi", "idle"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("got %v", err)
	}
	os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > "+dir+"/args; echo '{}'\n"), 0o755)
	if err := c.Send("t1", "hi", "idle"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dir + "/args"); strings.TrimSpace(string(b)) != "send --json --if idle t1 -- hi" {
		t.Fatalf("args %q", b)
	}
}
