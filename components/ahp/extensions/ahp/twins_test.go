package ahp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestEveryUpstreamCaseHasATwin is the Skill's "exact 1:1 case mapping" gate. Every leaf case of
// the upstream test suite (testdata/upstream-tests.json, produced by running the pinned oracle) must
// have a twin(twin.Run) or a named skipped twin (twin.Skip) with the same file and title.
func TestEveryUpstreamCaseHasATwin(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "upstream-tests.json"))
	if err != nil {
		t.Fatal(err)
	}
	var upstream []struct {
		File  string `json:"file"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &upstream); err != nil {
		t.Fatal(err)
	}

	call := regexp.MustCompile(`twin\.(Run|Skip)\(\s*t,\s*"((?:[^"\\]|\\.)*)",\s*"((?:[^"\\]|\\.)*)"(?:,\s*"((?:[^"\\]|\\.)*)")?`)
	have := map[string]string{} // "file\x00title" -> "run" | "skip: reason"
	err = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, "_test.go") || strings.Contains(path, "third_party") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range call.FindAllStringSubmatch(string(src), -1) {
			file, _ := strconv.Unquote(`"` + m[2] + `"`)
			title, _ := strconv.Unquote(`"` + m[3] + `"`)
			key := file + "\x00" + title
			if _, dup := have[key]; dup {
				t.Errorf("duplicate twin for %s: %q", file, title)
			}
			if m[1] == "Skip" {
				reason, _ := strconv.Unquote(`"` + m[4] + `"`)
				if strings.TrimSpace(reason) == "" {
					t.Errorf("skipped twin without a reason: %s: %q", file, title)
				}
				have[key] = "skip: " + reason
			} else {
				have[key] = "run"
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{}
	var missing []string
	for _, c := range upstream {
		key := c.File + "\x00" + c.Title
		want[key] = true
		if _, ok := have[key]; !ok {
			missing = append(missing, c.File+": "+c.Title)
		}
	}
	var extra []string
	skipped := 0
	for key, status := range have {
		if !want[key] {
			extra = append(extra, strings.ReplaceAll(key, "\x00", ": "))
		}
		if strings.HasPrefix(status, "skip") {
			skipped++
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	t.Logf("upstream cases: %d, twins: %d run + %d skipped, missing: %d", len(upstream), len(have)-skipped, skipped, len(missing))
	if len(missing) > 0 {
		t.Errorf("%d upstream cases have no twin:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("%d twins name no upstream case:\n  %s", len(extra), strings.Join(extra, "\n  "))
	}
}
