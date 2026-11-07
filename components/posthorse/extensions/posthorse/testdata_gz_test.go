package posthorse

// Oracle data of the differential tests: JSON generated from the unmodified original under Node 24 by
// port/tools/gen-testdata.sh and shipped gzipped in testdata/. A missing file is a failure, never a skip.

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// testdataGz returns the decompressed bytes of testdata/name (a .json.gz file).
func testdataGz(t testing.TB, name string) []byte {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("oracle data %s is missing (regenerate with port/tools/gen-testdata.sh): %v", name, err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("oracle data %s: %v", name, err)
	}
	var out bytes.Buffer
	if _, err := io.Copy(&out, reader); err != nil {
		t.Fatalf("oracle data %s: %v", name, err)
	}
	return out.Bytes()
}
