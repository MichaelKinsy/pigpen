package pi

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
)

// Port of image-input.ts. Upstream calls Pi's convertToPng / resizeImage (WASM codecs); those are
// not part of the PiG SDK, so the standard library's PNG, JPEG and GIF codecs plus a small BMP
// decoder stand in. WebP cannot be decoded here: it passes through unchanged when the header is
// valid and is never resized (see PORT.md).

const (
	maxImageDimension = 2000
	maxImageBytes     = 4_500_000
	// maxDecodePixels bounds what a client image may declare before it is decoded: the decoders
	// allocate the whole pixel buffer from the header, so 420 KiB of PNG can ask for 20000×20000
	// pixels (hundreds of MiB, seconds of CPU). Same value and name as websearch's imageproc.go
	// (a separate module; websearch's TestDecodePixelCapMatchesAHP keeps them equal).
	maxDecodePixels = 1 << 26
)

var supportedInlineTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// PrepareImagesForPi validates, converts and (when autoResize) bounds client images before they
// enter the agent. It returns nil for no images.
func PrepareImagesForPi(images []mapper.Image, autoResize bool) ([]mapper.Image, error) {
	if len(images) == 0 {
		return nil, nil
	}
	out := make([]mapper.Image, 0, len(images))
	for i, img := range images {
		prepared, err := prepareImage(img, autoResize, i)
		if err != nil {
			return nil, err
		}
		out = append(out, prepared)
	}
	return out, nil
}

func prepareImage(img mapper.Image, autoResize bool, index int) (mapper.Image, error) {
	data := img.Data
	mimeType := mapper.NormalizeImageMimeType(img.MimeType)
	if mimeType == "" {
		return mapper.Image{}, fmt.Errorf("Image %d has an invalid MIME type: %s", index+1, img.MimeType)
	}
	if !supportedInlineTypes[mimeType] {
		converted, err := convertToPNG(data, mimeType)
		if err != nil {
			return mapper.Image{}, fmt.Errorf("Image %d could not be converted from %s: %w", index+1, mimeType, err)
		}
		data, mimeType = converted, "image/png"
	}
	if !autoResize {
		return mapper.Image{Type: "image", Data: data, MimeType: mimeType}, nil
	}
	resized, resizedType, err := resizeImage(data, mimeType)
	if err != nil {
		return mapper.Image{}, fmt.Errorf("Image %d could not be prepared for inline model input: %w", index+1, err)
	}
	return mapper.Image{Type: "image", Data: resized, MimeType: resizedType}, nil
}

// checkDecodeSize reads only the header and refuses images declaring more than maxDecodePixels.
func checkDecodeSize(raw []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		return fmt.Errorf("image too large to process (%d×%d pixels; the limit is %d megapixels)", cfg.Width, cfg.Height, maxDecodePixels/(1<<20))
	}
	return nil
}

func decodeAny(raw []byte, mimeType string) (image.Image, error) {
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif":
		if err := checkDecodeSize(raw); err != nil {
			return nil, err
		}
	}
	switch mimeType {
	case "image/png":
		return png.Decode(bytes.NewReader(raw))
	case "image/jpeg":
		return jpeg.Decode(bytes.NewReader(raw))
	case "image/gif":
		return gif.Decode(bytes.NewReader(raw))
	case "image/bmp", "image/x-ms-bmp":
		return decodeBMP(raw)
	}
	return nil, fmt.Errorf("no decoder for %s", mimeType)
}

func encodePNG(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func convertToPNG(data, mimeType string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}
	img, err := decodeAny(raw, mimeType)
	if err != nil {
		return "", err
	}
	return encodePNG(img)
}

func resizeImage(data, mimeType string) (string, string, error) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", "", err
	}
	if mimeType == "image/webp" {
		if len(raw) < 12 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WEBP" {
			return "", "", errors.New("not a WebP image")
		}
		return data, mimeType, nil
	}
	img, err := decodeAny(raw, mimeType)
	if err != nil {
		return "", "", err
	}
	b := img.Bounds()
	if b.Dx() <= maxImageDimension && b.Dy() <= maxImageDimension && len(raw) <= maxImageBytes {
		return data, mimeType, nil
	}
	scale := 1.0
	if b.Dx() > maxImageDimension {
		scale = float64(maxImageDimension) / float64(b.Dx())
	}
	if s := float64(maxImageDimension) / float64(b.Dy()); s < scale {
		scale = s
	}
	w, h := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	for {
		small := boxResize(img, w, h)
		if mimeType == "image/jpeg" {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: 85}); err != nil {
				return "", "", err
			}
			if buf.Len() <= maxImageBytes || w <= 1 {
				return base64.StdEncoding.EncodeToString(buf.Bytes()), "image/jpeg", nil
			}
		} else {
			encoded, err := encodePNG(small)
			if err != nil {
				return "", "", err
			}
			if base64.StdEncoding.DecodedLen(len(encoded)) <= maxImageBytes || w <= 1 {
				return encoded, "image/png", nil
			}
		}
		w, h = max(1, w*3/4), max(1, h*3/4)
	}
}

// boxResize averages source pixels into each destination pixel.
func boxResize(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+(y+1)*b.Dy()/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < w; x++ {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+(x+1)*b.Dx()/w
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, a, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					pr, pg, pb, pa := src.At(xx, yy).RGBA()
					r, g, bl, a, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa), n+1
				}
			}
			dst.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(a / n)})
		}
	}
	return dst
}

// decodeBMP reads uncompressed 24- and 32-bit BMPs (BITMAPINFOHEADER), the shapes clients send.
func decodeBMP(raw []byte) (image.Image, error) {
	if len(raw) < 54 || raw[0] != 'B' || raw[1] != 'M' {
		return nil, errors.New("not a BMP")
	}
	offset := int(binary.LittleEndian.Uint32(raw[10:14]))
	width := int(int32(binary.LittleEndian.Uint32(raw[18:22])))
	height := int(int32(binary.LittleEndian.Uint32(raw[22:26])))
	bpp := int(binary.LittleEndian.Uint16(raw[28:30]))
	compression := binary.LittleEndian.Uint32(raw[30:34])
	topDown := height < 0
	if topDown {
		height = -height
	}
	if width <= 0 || height <= 0 || width > 1<<15 || height > 1<<15 || (compression != 0 && compression != 3) || (bpp != 24 && bpp != 32) {
		return nil, fmt.Errorf("unsupported BMP (bpp=%d compression=%d)", bpp, compression)
	}
	stride := (width*bpp/8 + 3) &^ 3
	if offset < 0 || offset+stride*height > len(raw) {
		return nil, errors.New("truncated BMP")
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.Transparent, image.Point{}, draw.Src)
	for y := 0; y < height; y++ {
		row := y
		if !topDown {
			row = height - 1 - y
		}
		line := raw[offset+row*stride:]
		for x := 0; x < width; x++ {
			p := line[x*bpp/8:]
			a := uint8(255)
			if bpp == 32 {
				a = p[3]
				if a == 0 && compression == 0 {
					a = 255 // BI_RGB 32-bit files rarely carry a real alpha channel
				}
			}
			img.SetNRGBA(x, y, color.NRGBA{R: p[2], G: p[1], B: p[0], A: a})
		}
	}
	return img, nil
}
