package powerline_footer

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

// The welcome golden: testdata/welcome-states.json drawn by the ORIGINAL welcome.ts (port/drive/welcome-drive.mjs, recorded into
// testdata/welcome-golden.json) and by the port. The box, its centering and truncation, the countdown line, the estimate and the
// discovery of recent sessions must be identical, line for line.

type welcomeStateFile struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	Sessions  []struct{ Name, TimeAgo string }
	Counts    loadedCountsJSON `json:"counts"`
	Tokens    any              `json:"tokens"`
	Countdown int              `json:"countdown"`
	Widths    []int            `json:"widths"`
	Files     []welcomeFileFixture
	Legacy    []welcomeFileFixture `json:"legacy"`
	MaxCount  int                  `json:"maxCount"`
	CustomDir bool                 `json:"customAgentDir"`
}

type loadedCountsJSON struct {
	ContextFiles    int `json:"contextFiles"`
	Skills          int `json:"skills"`
	PromptTemplates int `json:"promptTemplates"`
}

type welcomeFileFixture struct {
	Dir    string `json:"dir"`
	Name   string `json:"name"`
	Header string `json:"header"`
	Age    int64  `json:"age"`
	Body   int    `json:"body"`
}

func writeWelcomeFiles(t *testing.T, root string, files []welcomeFileFixture, now time.Time) {
	t.Helper()
	for _, f := range files {
		dir := filepath.Join(root, f.Dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, f.Name)
		body := make([]byte, f.Body)
		for i := range body {
			body[i] = 'z'
		}
		if err := os.WriteFile(path, append([]byte(f.Header+"\n"), body...), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := now.Add(-time.Duration(f.Age) * time.Millisecond)
		if err := os.Chtimes(path, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWelcomeMatchesTheOriginal(t *testing.T) {
	var states []welcomeStateFile
	readTestJSON(t, "testdata/welcome-states.json", &states)
	var golden map[string]json.RawMessage
	readTestJSON(t, "testdata/welcome-golden.json", &golden)
	if len(golden) != len(states) {
		t.Fatalf("golden has %d states, the state file %d: record it again", len(golden), len(states))
	}
	for _, s := range states {
		t.Run(s.Name, func(t *testing.T) {
			raw, ok := golden[s.Name]
			if !ok {
				t.Fatalf("no golden for %s", s.Name)
			}
			if s.Kind == "sessions" {
				var want []recentSession
				var wantJSON []struct{ Name, TimeAgo string }
				if err := json.Unmarshal(raw, &wantJSON); err != nil {
					t.Fatal(err)
				}
				for _, w := range wantJSON {
					want = append(want, recentSession{w.Name, w.TimeAgo})
				}
				if want == nil {
					want = []recentSession{}
				}
				home := t.TempDir()
				t.Setenv("HOME", home)
				setenv(t, "PI_CODING_AGENT_DIR", nil)
				agentSessions := filepath.Join(home, ".pi", "agent", "sessions")
				if s.CustomDir {
					t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent-dir"))
					agentSessions = filepath.Join(home, "agent-dir", "sessions")
				}
				if err := os.MkdirAll(agentSessions, 0o755); err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				writeWelcomeFiles(t, agentSessions, s.Files, now)
				writeWelcomeFiles(t, filepath.Join(home, ".pi", "sessions"), s.Legacy, now)
				got, err := getRecentSessions(t.Context(), s.MaxCount)
				if err != nil {
					t.Fatal(err)
				}
				eq(t, got, want, "recent sessions")
				return
			}
			var want map[string][]string
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			var tokens *float64
			switch v := s.Tokens.(type) {
			case float64:
				tokens = &v
			case string:
				nan := math.NaN()
				tokens = &nan
			}
			var sessions []recentSession
			for _, x := range s.Sessions {
				sessions = append(sessions, recentSession{x.Name, x.TimeAgo})
			}
			counts := loadedCounts{s.Counts.ContextFiles, s.Counts.Skills, s.Counts.PromptTemplates}
			var render func(int) []string
			if s.Kind == "overlay" {
				c := newWelcomeComponent(s.Model, s.Provider, sessions, counts, tokens)
				c.setCountdown(s.Countdown)
				render = c.render
			} else {
				render = newWelcomeHeader(s.Model, s.Provider, sessions, counts, tokens).render
			}
			var widths []string
			for w := range want {
				widths = append(widths, w)
			}
			sort.Strings(widths)
			if len(widths) != len(s.Widths) {
				t.Fatalf("golden has %d widths, the state %d", len(widths), len(s.Widths))
			}
			for _, w := range s.Widths {
				key := jsNumber(float64(w))
				got := render(w)
				if !reflect.DeepEqual(got, want[key]) {
					t.Errorf("width %d:\n got %q\nwant %q", w, got, want[key])
				}
			}
		})
	}
}

func readTestJSON(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
