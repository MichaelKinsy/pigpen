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
// cannot be recovered). 64 megapixels is 8192×8192: at most 512 MiB even at 16 bits per channel.
const maxDecodePixels = 1 << 26

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
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
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
