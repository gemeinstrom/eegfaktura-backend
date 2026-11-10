package api

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodedPNG(t *testing.T, b []byte) *image.NRGBA {
	img, err := png.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	out := image.NewNRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out
}

func encodePNG(t *testing.T, w, h int) []byte {
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))))
	return buf.Bytes()
}

// jpegWithExif is a JPEG with an APP1 Exif segment holding GPS data right after SOI, as a phone writes it.
func jpegWithExif(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for x := 0; x < 40; x++ {
		img.Set(x, x%30, color.RGBA{R: 200, A: 255})
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	exif := []byte("Exif\x00\x00GPSLatitude 48/1 12/1 0/1 N GPSLongitude 16/1 22/1 0/1 E")
	out := []byte{0xFF, 0xD8, 0xFF, 0xE1, byte((len(exif) + 2) >> 8), byte(len(exif) + 2)}
	out = append(out, exif...)
	return append(out, buf.Bytes()[2:]...)
}

// Uploaded images are re-encoded: only pixels are stored, metadata and hidden data are dropped.
func TestMailTemplates_ImagesReencoded(t *testing.T) {
	c := newMailTemplateClient(t)
	stored := func(name string, content []byte) []byte {
		w := c.upload("admin", mtTenant, "zp-active", "image", name, content)
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		f := decode[map[string]interface{}](t, w)
		w = c.do("admin", mtTenant, "GET", "/eeg/mailtemplates/zp-active/files/"+f["id"].(string), nil, "")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, float64(len(w.Body.Bytes())), f["size"], "the size is the stored one")
		return w.Body.Bytes()
	}

	withExif := jpegWithExif(t)
	require.True(t, bytes.Contains(withExif, []byte("GPSLatitude")))
	got := stored("foto.jpg", withExif)
	assert.False(t, bytes.Contains(got, []byte("Exif")), "no EXIF")
	assert.False(t, bytes.Contains(got, []byte("GPS")), "no GPS")
	assert.False(t, bytes.Contains(got, []byte{0xFF, 0xE1}), "no APP1 segment")
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(got))
	require.NoError(t, err)
	assert.Equal(t, 40, cfg.Width)
	assert.Equal(t, 30, cfg.Height)

	trailer := []byte("<?php echo 'hidden'; ?>")
	got = stored("a.png", append(append([]byte{}, logoPNG(t)...), trailer...))
	assert.False(t, bytes.Contains(got, trailer), "data after IEND is dropped")
	assert.Equal(t, decodedPNG(t, logoPNG(t)), decodedPNG(t, got), "PNG stays lossless")

	// the editor's largest image passes
	stored("max.png", encodePNG(t, 1200, 100))

	for name, content := range map[string][]byte{
		"too wide":           encodePNG(t, 1201, 1),
		"too high":           encodePNG(t, 1, 4001),
		"corrupt after IHDR": append(append([]byte{}, logoPNG(t)[:33]...), []byte("this is not image data at all")...),
		"corrupt header":     append([]byte("\x89PNG\r\n\x1a\n"), []byte("garbage garbage garbage")...),
		"truncated jpeg":     withExif[:len(withExif)/2],
	} {
		t.Run(name, func(t *testing.T) {
			w := c.upload("admin", mtTenant, "zp-active", "image", "x.png", content)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			msg := decode[mtErrors](t, w).Errors[0].Message
			if name == "too wide" || name == "too high" {
				assert.Contains(t, msg, "höchstens 1200 Pixel breit und 4000 Pixel hoch")
			} else {
				assert.Equal(t, "Das Bild konnte nicht gelesen werden.", msg)
			}
		})
	}
}
