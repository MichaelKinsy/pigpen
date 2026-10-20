package websearch

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// decodeText against the original's readTextResponseWithLimit: testdata/decode-text.json is recorded by
// port/record-decode-text.mjs.
func TestDecodeTextEqualsTheOriginal(t *testing.T) {
	data, err := os.ReadFile("testdata/decode-text.json")
	noErr(t, err)
	var cases []struct {
		Sample      string `json:"sample"`
		Hex         string `json:"hex"`
		ContentType string `json:"contentType"`
		Text        string `json:"text"`
	}
	noErr(t, json.Unmarshal(data, &cases))
	if len(cases) < 500 {
		t.Fatalf("%d cases", len(cases))
	}
	bad, recorded := 0, 0
	for _, c := range cases {
		raw, err := hex.DecodeString(c.Hex)
		noErr(t, err)
		got := decodeText(raw, c.ContentType)
		if got == c.Text {
			continue
		}
		if decodeTextRecordedDifference(c.Sample, c.ContentType) {
			recorded++
			continue
		}
		bad++
		if bad <= 40 {
			t.Errorf("%s as %q: %q, want %q", c.Sample, c.ContentType, got, c.Text)
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d cases differ", bad, len(cases))
	}
	if recorded == 0 {
		t.Fatalf("the recorded differences no longer differ: remove decodeTextRecordedDifference and its PORT.md entry")
	}
}

// decodeTextRecordedDifference marks the one family that differs from TextDecoder: bytes that are not text of the
// declared legacy multi-byte encoding (another encoding's bytes, or malformed input), where golang.org/x/text
// replaces and re-synchronizes after an invalid trail byte differently from the Encoding Standard and has no
// mapping for the Shift_JIS, Big5 and GBK private-use areas. Text of the declared encoding and ASCII decode
// identically; port/PORT.md lists the difference.
func decodeTextRecordedDifference(sample, contentType string) bool {
	native := map[string]string{"shift_jis": "shift_jis", "euc-jp": "euc-jp", "gbk": "gbk", "gb2312": "gbk", "gb18030": "gbk", "big5": "big5", "euc-kr": "euc-kr"}
	for label, own := range native {
		if strings.HasSuffix(contentType, "charset="+label) {
			return sample != "ascii" && sample != "empty" && sample != own
		}
	}
	return false
}
