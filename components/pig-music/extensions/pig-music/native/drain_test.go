package native

import (
	"errors"
	"io"
	"testing"
)

// drain reads a Decoded to its end.
func drain(t *testing.T, s Decoded) []float32 {
	t.Helper()
	var all []float32
	buf := make([]float32, 2*1000)
	for {
		n, err := s.Read(buf)
		all = append(all, buf[:2*n]...)
		if errors.Is(err, io.EOF) {
			return all
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if n == 0 {
			t.Fatal("Read returned 0 frames and no error")
		}
	}
}
