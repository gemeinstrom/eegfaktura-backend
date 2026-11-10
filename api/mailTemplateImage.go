package api

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"image/png"
)

// Size limits of an uploaded mail image in pixels (the editor scales images to
// 1200 px wide); they also guard the decoder against decompression bombs.
const (
	maxMailImageWidth  = 1200
	maxMailImageHeight = 4000
	maxMailImagePixels = 4_800_000
	mailJpegQuality    = 80
)

// reencodeMailImage decodes an uploaded PNG or JPEG and encodes it again in the
// same format, so only pixels are stored and sent: EXIF/GPS, comments and data
// hidden in or appended to the file are dropped. The dimensions are checked on
// the header before decoding. msg is the German answer for a refused image.
func reencodeMailImage(content []byte, mimeType string) (out []byte, msg string) {
	decodeConfig, decode := png.DecodeConfig, png.Decode
	if mimeType == "image/jpeg" {
		decodeConfig, decode = jpeg.DecodeConfig, jpeg.Decode
	}
	cfg, err := decodeConfig(bytes.NewReader(content))
	if err != nil {
		return nil, "Das Bild konnte nicht gelesen werden."
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxMailImageWidth || cfg.Height > maxMailImageHeight ||
		int64(cfg.Width)*int64(cfg.Height) > maxMailImagePixels {
		return nil, fmt.Sprintf("Das Bild ist zu groß: höchstens %d Pixel breit und %d Pixel hoch (dieses: %d × %d).",
			maxMailImageWidth, maxMailImageHeight, cfg.Width, cfg.Height)
	}
	img, err := decode(bytes.NewReader(content))
	if err != nil {
		return nil, "Das Bild konnte nicht gelesen werden."
	}
	var buf bytes.Buffer
	if mimeType == "image/jpeg" {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: mailJpegQuality})
	} else {
		err = png.Encode(&buf, img)
	}
	if err != nil {
		return nil, "Das Bild konnte nicht verarbeitet werden."
	}
	return buf.Bytes(), ""
}
