package websearch

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the webp decoder
)

type processedImage struct {
	data          string
	mime          string
	width, height int
}

// maxDecodePixels bounds the images that are decoded for resizing. The decoders allocate the
// whole pixel buffer from the declared size before reading any pixel data, so a few kilobytes of
// PNG can otherwise ask for gigabytes and kill the extension process (a Go out-of-memory error
// cannot be recovered). 64 megapixels is 8192×8192: 256 MiB as RGBA, 512 MiB at 16 bits per
// channel, and about 1 GiB for a progressive JPEG (its coefficients are kept for the whole image:
// 130 bytes declaring 8192×8192 allocate 960 MiB before failing), so decodeSlot below lets only
// one full decode run at a time. Always check image.DecodeConfig against it before image.Decode.
//
// components/ahp/extensions/ahp/internal/pi/imageinput.go carries the same limit under the same
// name (the two are separate Go modules, so the value is shared by test, not by import):
// TestDecodePixelCapMatchesAHP in this package fails if the two drift apart.
const maxDecodePixels = 1 << 26

// decodeSlot admits one full decode and rescale at a time. fetch_content extracts up to three URLs
// at once, and each decode may cost up to about 1 GiB under maxDecodePixels; one at a time keeps
// that a bound on the process rather than a per-goroutine figure.
var decodeSlot = make(chan struct{}, 1)

// resizeImage validates an image and fits it inside maxW×maxH, standing in for pi's resizeImage.
// Images already inside the limit are returned as received; larger ones are scaled down and
// re-encoded (JPEG stays JPEG, everything else becomes PNG). nil, nil means "cannot decode".
func resizeImage(data []byte, mime string, maxW, maxH int) (*processedImage, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, nil
	}
	if cfg.Width <= maxW && cfg.Height <= maxH {
		return &processedImage{data: base64.StdEncoding.EncodeToString(data), mime: mime, width: cfg.Width, height: cfg.Height}, nil
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		return nil, fmt.Errorf("Image too large to process (%d×%d pixels; the limit is %d megapixels)", cfg.Width, cfg.Height, maxDecodePixels/(1<<20))
	}
	decodeSlot <- struct{}{}
	defer func() { <-decodeSlot }()
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil
	}
	if b := src.Bounds(); b.Dx() != cfg.Width || b.Dy() != cfg.Height {
		// The header and the pixels disagree: a crafted file, whatever the decoder made of it.
		return nil, nil
	}
	scale := float64(maxW) / float64(cfg.Width)
	if s := float64(maxH) / float64(cfg.Height); s < scale {
		scale = s
	}
	w, h := max(1, int(float64(cfg.Width)*scale)), max(1, int(float64(cfg.Height)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	var buf bytes.Buffer
	outMime := "image/png"
	if mime == "image/jpeg" {
		outMime = "image/jpeg"
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85})
	} else {
		err = png.Encode(&buf, dst)
	}
	if err != nil {
		return nil, fmt.Errorf("Could not encode image: %v", err)
	}
	return &processedImage{data: base64.StdEncoding.EncodeToString(buf.Bytes()), mime: outMime, width: w, height: h}, nil
}
