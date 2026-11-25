package hashline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Oracle-backed block-span regression test.
// Loads testdata/blockspans/*.json generated against real omp v19.1.1 via
// `python3 -B internal/hashline/gen/gen_blockspans.py` (offline via oracle.sh, 0 network).
//
// Invariant: for EVERY anchor in EVERY fixture:
//   - Either the port refuses (nil or single-line refusal via ResolveBlockEdits), OR
//   - The port's resolved multi-line span [Start, End] is EXACTLY equal to omp's span.
// Any WRONG span (different endpoints, or port resolves when omp refuses) fails the test.

type blockSpanFixture struct {
	Lang           string            `json:"lang"`
	Sample         string            `json:"sample,omitempty"`
	OmpTag         string            `json:"omp_tag"`
	Generator      string            `json:"generator"`
	Total          int               `json:"total"`
	OmpResolved    int               `json:"omp_resolved"`
	OmpRefused     int               `json:"omp_refused"`
	RefusalSamples map[string]string `json:"refusal_samples,omitempty"`
	Cases          []blockSpanCase   `json:"cases"`
}

type blockSpanCase struct {
	File        string   `json:"file"`
	Path        string   `json:"path,omitempty"`
	Anchor      uint32   `json:"anchor"`
	Constructed bool     `json:"constructed,omitempty"`
	Name        string   `json:"name,omitempty"`
	Span        []uint32 `json:"span,omitempty"`
	Refused     string   `json:"refused,omitempty"`
	RefusedKind string   `json:"refused_kind,omitempty"`
}

func TestBlockSpansAgainstRealOmpOracle(t *testing.T) {
	dir := filepath.Join("testdata", "blockspans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read blockspans dir: %v", err)
	}
	var jsonFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			jsonFiles = append(jsonFiles, e.Name())
		}
	}
	sort.Strings(jsonFiles)
	if len(jsonFiles) == 0 {
		t.Fatal("no blockspans fixture files found in testdata/blockspans/")
	}

	fileCache := make(map[string][]string)
	loadLines := func(rel string) []string {
		if lines, ok := fileCache[rel]; ok {
			return lines
		}
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		norm := NormalizeToLF(string(raw))
		lines := strings.Split(norm, "\n")
		fileCache[rel] = lines
		return lines
	}

	type langStats struct {
		lang                 string
		total                int
		ompResolved          int
		equal                int
		refusedWhereResolved int
		refusedBoth          int
		wrong                int
	}
	var allStats []langStats

	for _, name := range jsonFiles {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var fx blockSpanFixture
		if err := json.Unmarshal(raw, &fx); err != nil {
			t.Fatalf("unmarshal %s: %v", name, err)
		}

		subName := strings.TrimSuffix(name, ".json")
		t.Run(subName, func(t *testing.T) {
			st := langStats{lang: subName, total: len(fx.Cases)}
			var wrongs []string
			for _, c := range fx.Cases {
				lines := loadLines(c.File)
				vpath := c.Path
				if vpath == "" {
					vpath = strings.TrimSuffix(c.File, ".src")
				}
				span := ResolveBlockAt(vpath, lines, c.Anchor)
				portResolved := span != nil && span.End > span.Start
				ompResolved := len(c.Span) == 2
				if ompResolved {
					st.ompResolved++
				}

				if !portResolved {
					if ompResolved {
						st.refusedWhereResolved++
					} else {
						st.refusedBoth++
					}
					continue
				}
				// Port resolved a multi-line span: it MUST equal omp's exact span!
				if !ompResolved {
					st.wrong++
					reason := c.Refused
					if reason == "" {
						reason = c.RefusedKind
					}
					wrongs = append(wrongs, fmt.Sprintf("%s:%d: port resolved %d-%d, omp refused (%s)",
						c.File, c.Anchor, span.Start, span.End, reason))
					continue
				}
				if span.Start != c.Span[0] || span.End != c.Span[1] {
					st.wrong++
					wrongs = append(wrongs, fmt.Sprintf("%s:%d: port resolved %d-%d, omp resolved %d-%d",
						c.File, c.Anchor, span.Start, span.End, c.Span[0], c.Span[1]))
					continue
				}
				st.equal++
			}

			allStats = append(allStats, st)
			t.Logf("%-14s total=%-3d omp_resolved=%-3d equal=%-3d refused_where_omp_resolves=%-3d refused_both=%-3d WRONG=%d",
				st.lang, st.total, st.ompResolved, st.equal, st.refusedWhereResolved, st.refusedBoth, st.wrong)
			if st.wrong > 0 {
				limit := min(len(wrongs), 25)
				t.Errorf("%s: %d WRONG block span(s) (showing first %d):\n  %s",
					subName, st.wrong, limit, strings.Join(wrongs[:limit], "\n  "))
			}
		})
	}
}
