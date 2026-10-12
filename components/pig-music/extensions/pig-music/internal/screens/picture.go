package screens

import (
	"image"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
)

// picture is the cover as the art cache returns it: the picture with the file it was decoded from.
func picture(img image.Image, data []byte) image.Image {
	return art.Picture{Image: img, Data: data, MimeType: "image/png"}
}
