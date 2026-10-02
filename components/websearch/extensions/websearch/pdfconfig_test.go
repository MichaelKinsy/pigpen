package websearch

import "testing"

// Twins of pdf-config.test.mjs.
func TestUpstream_pdf_config(t *testing.T) {
	const F = "pdf-config"
	read := func(t *testing.T, config string) pdfConfig {
		t.Helper()
		_, dir := isolate(t)
		unsetenv(t, "DATALAB_MODE")
		if config != "" {
			writeConfig(t, dir, config)
		}
		ResetCaches()
		c, err := loadPDFConfig()
		noErr(t, err)
		return c
	}
	tw(t, F, "pdf.enabled defaults to true and accepts false", func(t *testing.T) {
		if !read(t, "").Enabled || read(t, `{"pdf":{"enabled":false}}`).Enabled || !read(t, `{"pdf":{"enabled":"false"}}`).Enabled {
			t.Fatal("enabled")
		}
	})
	tw(t, F, "pdf config reloads after the config file changes", func(t *testing.T) {
		_, dir := isolate(t)
		var got []bool
		for _, c := range []string{`{"pdf":{"enabled":false}}`, `{"pdf":{"enabled":true}}`} {
			writeConfig(t, dir, c)
			cfg, err := loadPDFConfig()
			noErr(t, err)
			got = append(got, cfg.Enabled)
		}
		if got[0] || !got[1] {
			t.Fatal(got)
		}
	})
	tw(t, F, "pdf.maxSizeMB defaults to 20 and accepts values through 50", func(t *testing.T) {
		if read(t, "").MaxSizeMB != 20 || read(t, `{"pdf":{"maxSizeMB":30}}`).MaxSizeMB != 30 || read(t, `{"pdf":{"maxSizeMB":50}}`).MaxSizeMB != 50 {
			t.Fatal("maxSizeMB")
		}
	})
	tw(t, F, "pdf.maxSizeMB caps values above 50 and rejects invalid values", func(t *testing.T) {
		for cfg, want := range map[string]float64{`{"pdf":{"maxSizeMB":80}}`: 50, `{"pdf":{"maxSizeMB":0}}`: 20, `{"pdf":{"maxSizeMB":-1}}`: 20, `{"pdf":{"maxSizeMB":"50"}}`: 20} {
			if got := read(t, cfg).MaxSizeMB; got != want {
				t.Fatalf("%s -> %v", cfg, got)
			}
		}
	})
	tw(t, F, "pdf.maxPages defaults to 100 and accepts positive integer values", func(t *testing.T) {
		if read(t, "").MaxPages != 100 {
			t.Fatal("default")
		}
		for cfg, want := range map[string]int{`{"pdf":{"maxPages":25}}`: 25, `{"pdf":{"maxPages":2.8}}`: 2, `{"pdf":{"maxPages":0}}`: 100, `{"pdf":{"maxPages":-1}}`: 100, `{"pdf":{"maxPages":"25"}}`: 100} {
			if got := read(t, cfg).MaxPages; got != want {
				t.Fatalf("%s -> %v", cfg, got)
			}
		}
	})
	tw(t, F, "pdf.provider defaults to auto and validates explicit providers", func(t *testing.T) {
		if read(t, "").Provider != "auto" {
			t.Fatal("default")
		}
		for cfg, want := range map[string]string{`{"pdf":{"provider":"gemini"}}`: "gemini", `{"pdf":{"provider":"datalab"}}`: "datalab", `{"pdf":{"provider":"unpdf"}}`: "unpdf", `{"pdf":{"provider":"gemini2"}}`: "auto"} {
			if got := read(t, cfg).Provider; got != want {
				t.Fatalf("%s -> %v", cfg, got)
			}
		}
	})
	tw(t, F, "pdf.datalabMode defaults to balanced and validates modes", func(t *testing.T) {
		if read(t, "").DatalabMode != "balanced" {
			t.Fatal("default")
		}
		for cfg, want := range map[string]string{`{"pdf":{"datalabMode":"fast"}}`: "fast", `{"pdf":{"datalabMode":"accurate"}}`: "accurate", `{"pdf":{"datalabMode":"ultra"}}`: "balanced"} {
			if got := read(t, cfg).DatalabMode; got != want {
				t.Fatalf("%s -> %v", cfg, got)
			}
		}
	})
	tw(t, F, "pdf.datalabTimeoutMs defaults to 120000 and caps at 300000", func(t *testing.T) {
		if read(t, "").DatalabTimeoutMs != 120000 {
			t.Fatal("default")
		}
		for cfg, want := range map[string]float64{`{"pdf":{"datalabTimeoutMs":5000}}`: 5000, `{"pdf":{"datalabTimeoutMs":999999}}`: 300000, `{"pdf":{"datalabTimeoutMs":-1}}`: 120000} {
			if got := read(t, cfg).DatalabTimeoutMs; got != want {
				t.Fatalf("%s -> %v", cfg, got)
			}
		}
	})
	// The environment default and its validation (datalab-pdf-extract.ts:116), not in the upstream file.
	t.Run("DATALAB_MODE environment default is validated", func(t *testing.T) {
		isolate(t)
		t.Setenv("DATALAB_MODE", " Accurate ")
		c, err := loadPDFConfig()
		noErr(t, err)
		if c.DatalabMode != "accurate" {
			t.Fatal(c.DatalabMode)
		}
		t.Setenv("DATALAB_MODE", "ultra")
		_, err = loadPDFConfig()
		wantErr(t, err, `Failed to parse datalab mode: expected "fast", "balanced", or "accurate", got "ultra"`)
	})
	tskip(t, F, "PDF streamed byte enforcement allows the exact limit", "PDF text extraction is deferred (docs/PORT.md); the streamed size limit ships with it")
	tskip(t, F, "PDF streamed byte enforcement rejects a headerless response above the limit", "PDF text extraction is deferred (docs/PORT.md); the streamed size limit ships with it")
}
