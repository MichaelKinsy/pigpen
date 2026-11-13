// Command mockllm is a hermetic deterministic mock model server plus a tiny MCP
// server, used to prove agent harnesses without model credentials.
//
//	mockllm                       # model API on :8080 and MCP on :8081
//	mockllm -model-addr :9000 -mcp-addr ""
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

func main() {
	modelAddr := flag.String("model-addr", envOr("MOCK_MODEL_ADDR", ":8080"), "model API listen address, empty disables")
	mcpAddr := flag.String("mcp-addr", envOr("MOCK_MCP_ADDR", ":8081"), "MCP listen address, empty disables")
	healthcheck := flag.String("healthcheck", "", "GET this URL and exit 0 on 200 (for scratch images)")
	flag.Parse()
	if *healthcheck != "" {
		c := http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get(*healthcheck)
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	cfg := loadConfig()
	var wg sync.WaitGroup
	serve := func(addr string, h http.Handler, name string) {
		if addr == "" {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			logJSON(map[string]any{"event": "listen", "service": name, "addr": addr, "chunk_delay_ms": cfg.ChunkDelay.Milliseconds(), "ttft_ms": cfg.TTFT.Milliseconds()})
			srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
			fmt.Fprintln(os.Stderr, name, srv.ListenAndServe())
			os.Exit(1)
		}()
	}
	s := &server{cfg: cfg}
	serve(*modelAddr, s.mux(), "model")
	mm := http.NewServeMux()
	mm.HandleFunc("/mcp", mcpHandler)
	mm.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	serve(*mcpAddr, mm, "mcp")
	wg.Wait()
}
