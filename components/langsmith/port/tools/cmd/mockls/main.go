// Command mockls is a loopback LangSmith endpoint that records every request as JSONL.
//
//	mockls -log requests.jsonl [-addr 127.0.0.1:0] [-config cfg.json] [-gzip] [-multipart=false] [-port-file F]
//
// It prints "mockls listening http://127.0.0.1:PORT" on stdout once it accepts connections and stops on SIGINT or
// SIGTERM (or when stdin reaches EOF with -stdin-lifetime).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lstools/internal/mockls"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Stdin)) }

func run(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	fs := flag.NewFlagSet("mockls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		addr      = fs.String("addr", "127.0.0.1:0", "listen address (loopback only)")
		logPath   = fs.String("log", "", "request log (JSONL), truncated at start; empty: no log")
		cfgPath   = fs.String("config", "", "config file (JSON); see internal/mockls/config.go")
		portFile  = fs.String("port-file", "", "write the chosen port here once listening")
		gzipFlag  = fs.Bool("gzip", false, "advertise instance_flags.gzip_body_enabled in /info (ignored with -config info)")
		multipart = fs.Bool("multipart", true, "advertise batch_ingest_config.use_multipart_endpoint in /info (ignored with -config info)")
		stdinLife = fs.Bool("stdin-lifetime", false, "stop when stdin reaches EOF")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := &mockls.Config{}
	if *cfgPath != "" {
		var err error
		if cfg, err = mockls.LoadConfig(*cfgPath); err != nil {
			fmt.Fprintln(stderr, "mockls:", err)
			return 2
		}
	}
	if len(cfg.Info) == 0 && (*gzipFlag || !*multipart) {
		var info map[string]any
		_ = json.Unmarshal([]byte(mockls.DefaultInfo), &info)
		info["batch_ingest_config"].(map[string]any)["use_multipart_endpoint"] = *multipart
		info["instance_flags"].(map[string]any)["gzip_body_enabled"] = *gzipFlag
		cfg.Info, _ = json.Marshal(info)
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		fmt.Fprintln(stderr, "mockls: -addr:", err)
		return 2
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		fmt.Fprintln(stderr, "mockls: -addr must be a loopback address")
		return 2
	}
	var logw io.Writer
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			fmt.Fprintln(stderr, "mockls:", err)
			return 2
		}
		defer f.Close()
		logw = f
	}
	srv, err := mockls.New(cfg, logw)
	if err != nil {
		fmt.Fprintln(stderr, "mockls:", err)
		return 2
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(stderr, "mockls:", err)
		return 2
	}
	base := "http://" + ln.Addr().String()
	srv.SetBase(base)
	if *portFile != "" {
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		if err := os.WriteFile(*portFile, []byte(port+"\n"), 0o600); err != nil {
			fmt.Fprintln(stderr, "mockls:", err)
			return 2
		}
	}
	hs := &http.Server{Handler: srv}
	done := make(chan error, 1)
	go func() { done <- hs.Serve(ln) }()
	fmt.Fprintln(stdout, "mockls listening", base)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	eof := make(chan struct{})
	if *stdinLife {
		go func() { _, _ = io.Copy(io.Discard, stdin); close(eof) }()
	}
	select {
	case <-sig:
	case <-eof:
	case err := <-done:
		fmt.Fprintln(stderr, "mockls:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := hs.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		_ = hs.Close()
	}
	st := srv.Stats()
	fmt.Fprintf(stderr, "mockls: %d requests %v\n", st.Requests, st.ByRoute)
	if err := srv.LogError(); err != nil {
		fmt.Fprintln(stderr, "mockls: log:", err)
		return 1
	}
	return 0
}
