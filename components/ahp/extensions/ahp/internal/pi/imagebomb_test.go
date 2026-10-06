package pi

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
)

// bombPNG streams a w×h 8-bit grayscale PNG of zeros through zlib without ever holding the
// pixels, so the test itself stays small: about 400 KiB for 20000×20000.
func bombPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(kind string, body []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(body)))
		out.Write(n[:])
		out.WriteString(kind)
		out.Write(body)
		crc := crc32.NewIEEE()
		crc.Write([]byte(kind))
		crc.Write(body)
		binary.BigEndian.PutUint32(n[:], crc.Sum32())
		out.Write(n[:])
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8] = 8 // bit depth; colour type 0 is grayscale
	chunk("IHDR", ihdr)
	var idat bytes.Buffer
	// BestSpeed: about the same size for all-zero rows, and several times faster (this runs under -race too).
	zw, err := zlib.NewWriterLevel(&idat, zlib.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	row := make([]byte, 1+w) // filter byte 0 plus zero pixels
	for y := 0; y < h; y++ {
		if _, err := zw.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	chunk("IDAT", idat.Bytes())
	chunk("IEND", nil)
	return out.Bytes()
}

// A client-supplied 20000×20000 PNG (400 MP, about 400 KiB on the wire) must be refused from its
// header, before the decoder allocates the pixel buffer.
func TestPrepareImagesRefusesDecodeBomb(t *testing.T) {
	raw := bombPNG(t, 20000, 20000)
	if len(raw) > 2<<20 {
		t.Fatalf("fixture is %d KiB, expected a small bomb", len(raw)>>10)
	}
	data := base64.StdEncoding.EncodeToString(raw)
	for _, autoResize := range []bool{true, false} {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := PrepareImagesForPi([]mapper.Image{{Type: "image", Data: data, MimeType: "image/png"}}, autoResize)
		runtime.ReadMemStats(&after)
		if autoResize && (err == nil || !strings.Contains(err.Error(), "too large")) {
			t.Fatalf("autoResize=%v: want a too-large refusal, got %v", autoResize, err)
		}
		if grown := after.TotalAlloc - before.TotalAlloc; grown > 32<<20 {
			t.Fatalf("autoResize=%v: refusing allocated %d MiB (want under 32)", autoResize, grown>>20)
		}
	}
}

// Images at or under the cap still work.
func TestPrepareImagesAcceptsLargeButBoundedPNG(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(bombPNG(t, 3000, 3000))
	out, err := PrepareImagesForPi([]mapper.Image{{Type: "image", Data: data, MimeType: "image/png"}}, true)
	if err != nil || len(out) != 1 {
		t.Fatalf("%v %v", out, err)
	}
}
