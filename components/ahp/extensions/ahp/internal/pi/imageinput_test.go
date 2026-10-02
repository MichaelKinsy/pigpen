package pi

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

// Twins of upstream test/image-input.test.ts (support/images.ts fixtures).

const twoPixelBMP = "Qk1GAAAAAAAAADYAAAAoAAAAAgAAAAIAAAABABgAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAD/AAD/AAAAAP8AAP8AAA=="

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestImageInput(t *testing.T) {
	twin.Run(t, "image-input", "converts an unsupported image type even when auto-resize is disabled", func(t *testing.T) {
		prepared, err := PrepareImagesForPi([]mapper.Image{{Type: "image", Data: twoPixelBMP, MimeType: "image/bmp"}}, false)
		if err != nil || len(prepared) != 1 {
			t.Fatalf("%v %v", prepared, err)
		}
		if prepared[0].MimeType != "image/png" {
			t.Fatalf("mime %s", prepared[0].MimeType)
		}
		raw, _ := base64.StdEncoding.DecodeString(prepared[0].Data)
		if !bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatalf("not a PNG: %x", raw[:8])
		}
	})

	twin.Run(t, "image-input", "fails when image bytes cannot be prepared", func(t *testing.T) {
		_, err := PrepareImagesForPi([]mapper.Image{{Type: "image", Data: base64.StdEncoding.EncodeToString([]byte("not an image")), MimeType: "image/png"}}, true)
		if err == nil || !strings.Contains(err.Error(), "could not be prepared") {
			t.Fatalf("%v", err)
		}
	})
}

func TestImageInputAdditions(t *testing.T) {
	// Additions: the resize and validation paths the standard-library codecs stand in for.
	if got, err := PrepareImagesForPi(nil, true); got != nil || err != nil {
		t.Fatalf("no images: %v %v", got, err)
	}
	same, err := PrepareImagesForPi([]mapper.Image{{Type: "image", Data: onePixelPNG, MimeType: "IMAGE/PNG; charset=x"}}, true)
	if err != nil || same[0].Data != onePixelPNG || same[0].MimeType != "image/png" {
		t.Fatalf("a small PNG must pass through: %+v %v", same, err)
	}
	if _, err := PrepareImagesForPi([]mapper.Image{{Type: "image", Data: onePixelPNG, MimeType: "text/plain"}}, true); err == nil {
		t.Fatal("a non-image MIME type must be rejected")
	}
	if _, err := PrepareImagesForPi([]mapper.Image{{Type: "image", Data: "!!!", MimeType: "image/tiff"}}, false); err == nil {
		t.Fatal("an undecodable conversion must fail")
	}
}
