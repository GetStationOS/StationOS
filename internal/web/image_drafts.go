package web

import (
	"context"
	"encoding/base64"
	"errors"
	"stationos.local/stationos/internal/artwork"
	"stationos.local/stationos/internal/producer"
	"time"
)

func (s *Server) imageGenerationAvailable(ctx context.Context) bool {
	if s.d.Images == nil {
		return false
	}
	available, err := s.d.Images.Available(ctx)
	if err != nil {
		s.d.Logger.Warn("image generation configuration unavailable", "error", err)
	}
	return err == nil && available
}

func decodeImageDraft(value string) ([]byte, error) {
	if len(value) > 7<<20 {
		return nil, errors.New("Generated image is too large")
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New("Generated image could not be read; generate it again")
	}
	return artwork.Normalize(data)
}

// Image generation only prepares a normalized draft, never an entity write.
func (s *Server) generateImageDraft(parent context.Context, request producer.ImageRequest) (string, error) {
	if !s.imageGenerationAvailable(parent) {
		return "", errors.New("Image generation is unavailable. Configure artwork in Assistant settings")
	}
	select {
	case s.imageSlots <- struct{}{}:
		defer func() { <-s.imageSlots }()
	default:
		return "", errors.New("An image is already being generated. Wait for it to finish")
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
	defer cancel()
	image, err := s.d.Images.Generate(ctx, request)
	if err != nil {
		return "", err
	}
	data, err := artwork.Normalize(image.Data)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
