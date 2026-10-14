// Package artwork validates and normalizes raster assets before persistence.
package artwork

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
)

func Normalize(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > 5<<20 {
		return nil, errors.New("Choose an image smaller than 5 MB.")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, errors.New("Choose a valid JPEG or PNG image.")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 8000 || cfg.Height > 8000 || int64(cfg.Width)*int64(cfg.Height) > 12000000 {
		return nil, errors.New("Choose an image no larger than 12 megapixels.")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("The image could not be read.")
	}
	w, h := cfg.Width, cfg.Height
	if max(w, h) > 768 {
		w = max(1, w*768/max(cfg.Width, cfg.Height))
		h = max(1, h*768/max(cfg.Width, cfg.Height))
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, a := src.At(src.Bounds().Min.X+x*cfg.Width/w, src.Bounds().Min.Y+y*cfg.Height/h).RGBA()
			dst.SetRGBA(x, y, color.RGBA{R: uint8((r + 65535 - a) >> 8), G: uint8((g + 65535 - a) >> 8), B: uint8((b + 65535 - a) >> 8), A: 255})
		}
	}
	var out bytes.Buffer
	if jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}) != nil {
		return nil, errors.New("The image could not be saved.")
	}
	return out.Bytes(), nil
}
