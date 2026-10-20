package websearch

import (
	"strings"
	"testing"
	"time"
)

// The Gemini files read web-search.json through the same stat-validated cache as ReadConfigRoot
// (they used to read and parse the file on every call).

func TestGeminiConfigSharesTheCacheWithReadConfigRoot(t *testing.T) {
	path := cacheConfigPath(t)
	writeAged(t, path, `{"provider":"aaaa","geminiApiKey":"k1"}`, time.Minute)
	before := configParseCount()
	for range 3 {
		if got := provider(t); got != "aaaa" {
			t.Fatalf("provider = %q", got)
		}
		cfg, err := loadGeminiCfg().lax()
		if err != nil || cfg["geminiApiKey"] != "k1" {
			t.Fatalf("gemini config = %v, %v", cfg, err)
		}
	}
	if n := configParseCount() - before; n != 1 {
		t.Fatalf("a quiet file was parsed %d times by 6 reads, want 1", n)
	}
}

func TestGeminiConfigSeesAnEditOfTheSameSize(t *testing.T) {
	path := cacheConfigPath(t)
	writeAged(t, path, `{"geminiApiKey":"aaaa"}`, time.Minute)
	if cfg, _ := loadGeminiCfg().lax(); cfg["geminiApiKey"] != "aaaa" {
		t.Fatalf("gemini config = %v", cfg)
	}
	writeAged(t, path, `{"geminiApiKey":"bbbb"}`, 30*time.Second)
	if cfg, _ := loadGeminiCfg().lax(); cfg["geminiApiKey"] != "bbbb" {
		t.Fatalf("an edit with the same size and a later mtime was not picked up: %v", cfg)
	}
}

func TestGeminiConfigRootThatIsNotAnObject(t *testing.T) {
	path := cacheConfigPath(t)
	writeAged(t, path, `["not","an","object"]`, time.Minute)
	for range 2 {
		c := loadGeminiCfg()
		if cfg, err := c.lax(); err != nil || len(cfg) != 0 {
			t.Fatalf("lax read of a non-object root = %v, %v; want an empty config", cfg, err)
		}
		if _, err := c.strict(); err == nil || !strings.Contains(err.Error(), "config root must be an object") {
			t.Fatalf("strict read of a non-object root: %v", err)
		}
		if _, err := ReadConfigRoot(); err == nil || !strings.HasPrefix(err.Error(), "Invalid config in ") {
			t.Fatalf("ReadConfigRoot of a non-object root: %v", err)
		}
	}
}

func TestGeminiConfigMissingAndMalformedFiles(t *testing.T) {
	path := cacheConfigPath(t)
	if cfg, err := loadGeminiCfg().lax(); err != nil || len(cfg) != 0 {
		t.Fatalf("missing file = %v, %v; want an empty config", cfg, err)
	}
	writeAged(t, path, `{"geminiApiKey":`, time.Minute)
	for range 2 {
		if _, err := loadGeminiCfg().lax(); err == nil || !strings.HasPrefix(err.Error(), "Failed to parse "+path+": ") {
			t.Fatalf("malformed file: %v", err)
		}
	}
}

func TestConfigPathFollowsResetCaches(t *testing.T) {
	first := cacheConfigPath(t)
	if got := ConfigPath(); got != first {
		t.Fatalf("ConfigPath = %q, want %q", got, first)
	}
	second := cacheConfigPath(t)
	if second == first {
		t.Fatal("test setup reused a directory")
	}
	if got := ConfigPath(); got != second {
		t.Fatalf("ConfigPath after ResetCaches = %q, want %q", got, second)
	}
}
