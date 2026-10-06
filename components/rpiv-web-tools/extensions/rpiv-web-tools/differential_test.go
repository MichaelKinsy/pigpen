package rpiv_web_tools

import (
	"encoding/json"
	"os"
	"testing"
)

// TestDifferentialAgainstTheOriginal compares htmlToText, extractTitle, truncateHead and formatSize with the
// answers of the original's own code (port/gen-differential.mjs runs it under Node and Pi): the pipeline that a
// scenario cannot reach, because web_fetch refuses the loopback servers the harness provides.
func TestDifferentialAgainstTheOriginal(t *testing.T) {
	data, err := os.ReadFile("testdata/differential.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		HTML []struct {
			In    string  `json:"in"`
			Text  string  `json:"text"`
			Title *string `json:"title"`
		} `json:"html"`
		Truncate []struct {
			In string `json:"in"`
			T  struct {
				Content               string  `json:"content"`
				Truncated             bool    `json:"truncated"`
				TruncatedBy           *string `json:"truncatedBy"`
				TotalLines            int     `json:"totalLines"`
				TotalBytes            int     `json:"totalBytes"`
				OutputLines           int     `json:"outputLines"`
				OutputBytes           int     `json:"outputBytes"`
				LastLinePartial       bool    `json:"lastLinePartial"`
				FirstLineExceedsLimit bool    `json:"firstLineExceedsLimit"`
				MaxLines              int     `json:"maxLines"`
				MaxBytes              int     `json:"maxBytes"`
			} `json:"t"`
		} `json:"truncate"`
		Sizes []struct {
			N int    `json:"n"`
			S string `json:"s"`
		} `json:"sizes"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	for i, c := range d.HTML {
		if got := htmlToText(c.In); got != c.Text {
			t.Errorf("html case %d: htmlToText(%q)\n got  %q\n want %q", i, c.In, got, c.Text)
		}
		want := ""
		if c.Title != nil {
			want = *c.Title
		}
		if got := extractTitle(c.In); got != want {
			t.Errorf("html case %d: extractTitle = %q, want %q", i, got, want)
		}
	}
	for i, c := range d.Truncate {
		got := truncateHead(c.In, defaultMaxLines, defaultMaxBytes)
		w := c.T
		same := got.Content == w.Content && got.Truncated == w.Truncated && got.TotalLines == w.TotalLines && got.TotalBytes == w.TotalBytes &&
			got.OutputLines == w.OutputLines && got.OutputBytes == w.OutputBytes && got.LastLinePartial == w.LastLinePartial &&
			got.FirstLineExceedsLimit == w.FirstLineExceedsLimit && got.MaxLines == w.MaxLines && got.MaxBytes == w.MaxBytes &&
			((got.TruncatedBy == nil) == (w.TruncatedBy == nil)) && (got.TruncatedBy == nil || *got.TruncatedBy == *w.TruncatedBy)
		if !same {
			t.Errorf("truncate case %d: got lines %d/%d bytes %d/%d by %v, want lines %d/%d bytes %d/%d by %v", i,
				got.OutputLines, got.TotalLines, got.OutputBytes, got.TotalBytes, got.TruncatedBy, w.OutputLines, w.TotalLines, w.OutputBytes, w.TotalBytes, w.TruncatedBy)
		}
	}
	for _, c := range d.Sizes {
		if got := formatSize(c.N); got != c.S {
			t.Errorf("formatSize(%d) = %q, want %q", c.N, got, c.S)
		}
	}
}
