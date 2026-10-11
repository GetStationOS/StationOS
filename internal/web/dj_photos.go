package web

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"stationos.local/stationos/internal/djs"
)

func (s *Server) readDJPhoto(r *http.Request) (*djs.PhotoChange, error) {
	fail := func(message string) (*djs.PhotoChange, error) {
		return nil, errors.Join(djs.ErrInvalid, errors.New(message))
	}
	clear := r.PostForm.Get("clear_photo") == "yes" || r.PostForm.Get("photo_removed") == "yes"
	if r.MultipartForm == nil {
		if clear {
			return &djs.PhotoChange{Clear: true}, nil
		}
		return nil, nil
	}
	file, header, err := r.FormFile("photo")
	if errors.Is(err, http.ErrMissingFile) {
		if clear {
			return &djs.PhotoChange{Clear: true}, nil
		}
		return nil, nil
	}
	if err != nil {
		return fail("Choose a JPEG or PNG photo.")
	}
	defer file.Close()
	if clear {
		return fail("Choose either a replacement photo or removal.")
	}
	if header.Size > 5<<20 {
		return fail("Choose a photo smaller than 5 MB.")
	}
	data, err := io.ReadAll(io.LimitReader(file, (5<<20)+1))
	if err != nil || len(data) > 5<<20 {
		return fail("Choose a photo smaller than 5 MB.")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return fail("Choose a valid JPEG or PNG photo.")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 8000 || cfg.Height > 8000 || int64(cfg.Width)*int64(cfg.Height) > 12000000 {
		return fail("Choose a photo no larger than 12 megapixels.")
	}
	select {
	case s.artworkSlots <- struct{}{}:
		defer func() { <-s.artworkSlots }()
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fail("The photo could not be read. Choose another JPEG or PNG.")
	}
	w, h := cfg.Width, cfg.Height
	if max(w, h) > 768 {
		w = max(1, w*768/max(cfg.Width, cfg.Height))
		h = max(1, h*768/max(cfg.Width, cfg.Height))
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	// Resize without a new dependency; flatten transparency and discard metadata.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(src.Bounds().Min.X+x*cfg.Width/w, src.Bounds().Min.Y+y*cfg.Height/h)
			r, g, b, a := c.RGBA()
			dst.SetRGBA(x, y, color.RGBA{R: uint8((r + 65535 - a) >> 8), G: uint8((g + 65535 - a) >> 8), B: uint8((b + 65535 - a) >> 8), A: 255})
		}
	}
	var out bytes.Buffer
	if err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return fail("The photo could not be saved.")
	}
	return &djs.PhotoChange{Data: out.Bytes()}, nil
}
func (s *Server) djPhoto(w http.ResponseWriter, r *http.Request) {
	reader, ok := s.d.DJs.(djs.PhotoReader)
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := reader.Photo(r.Context(), r.PathValue("id"))
	if errors.Is(err, djs.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Write(data)
}
