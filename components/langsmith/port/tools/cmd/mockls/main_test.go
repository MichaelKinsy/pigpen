package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lstools/internal/reqlog"
)

func TestRunServesLogsAndStopsOnStdinEOF(t *testing.T) {
	dir := t.TempDir()
	logPath, portFile := filepath.Join(dir, "req.jsonl"), filepath.Join(dir, "port")
	pr, pw := io.Pipe()
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"-log", logPath, "-port-file", portFile, "-gzip", "-multipart=false", "-stdin-lifetime"}, &out, &errb, pr)
	}()
	var port string
	for i := 0; i < 200 && port == ""; i++ {
		if b, err := os.ReadFile(portFile); err == nil && strings.HasSuffix(string(b), "\n") {
			port = strings.TrimSpace(string(b))
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if port == "" {
		t.Fatal("no port file")
	}
	resp, err := http.Get("http://127.0.0.1:" + port + "/info")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"gzip_body_enabled":true`) || !strings.Contains(string(body), `"use_multipart_endpoint":false`) {
		t.Fatalf("flags not reflected: %s", body)
	}
	pw.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not stop")
	}
	if !strings.Contains(out.String(), "mockls listening http://127.0.0.1:"+port) {
		t.Fatalf("stdout %q", out.String())
	}
	es, err := reqlog.ReadFile(logPath)
	if err != nil || len(es) != 1 || es[0].Route != "info" {
		t.Fatalf("%v %+v", err, es)
	}
}

func TestRefusesNonLoopback(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-addr", "0.0.0.0:0"}, &out, &errb, strings.NewReader("")); code != 2 || !strings.Contains(errb.String(), "loopback") {
		t.Fatalf("%d %s", code, errb.String())
	}
}
