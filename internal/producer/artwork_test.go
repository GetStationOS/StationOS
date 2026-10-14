package producer_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/station"
)

type testImages struct {
	mu      sync.Mutex
	calls   []producer.ImageRequest
	err     error
	started chan struct{}
}

func (g *testImages) Generate(ctx context.Context, request producer.ImageRequest) (producer.Image, error) {
	g.mu.Lock()
	g.calls = append(g.calls, request)
	err := g.err
	started := g.started
	g.mu.Unlock()
	if started != nil {
		close(started)
		<-ctx.Done()
		return producer.Image{}, ctx.Err()
	}
	if err != nil {
		return producer.Image{}, err
	}
	pic := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			pic.Set(x, y, color.RGBA{R: 20, G: 160, B: 140, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, pic)
	return producer.Image{Data: buf.Bytes(), Usage: producer.Usage{Input: 30, Output: 50}}, nil
}
func TestNewAssistantEntitiesAutomaticallySaveArtworkAndReuseIt(t *testing.T) {
	_, _, stations, _, _, repo := fixture(t)
	images := &testImages{}
	registry := producer.NewRegistry(producer.Dependencies{Stations: stations, DJs: repo, Images: images})
	st := call(t, registry, "save_station", map[string]string{"name": "Logo Radio", "description": "Late-night jazz"}).Data.(station.Station)
	dj := call(t, registry, "save_dj", map[string]string{"name": "Morgan Field", "model": "model", "background": "Fictional jazz historian", "vibe": "Relaxed and reflective"}).Data.(djs.Profile)
	if st.LogoVersion == "" || !strings.HasPrefix(dj.PortraitURL, "/djs/"+dj.ID+"/photo?v=") {
		t.Fatal("artwork was not attached", st, dj)
	}
	logo, err := stations.Logo(context.Background(), st.ID)
	if err != nil {
		t.Fatal(err)
	}
	photo, err := repo.Photo(context.Background(), dj.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{logo, photo} {
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || format != "jpeg" || cfg.Width != 64 {
			t.Fatal(cfg, format, err)
		}
	}
	call(t, registry, "save_station", map[string]string{"id": st.ID, "description": "Updated description"})
	call(t, registry, "save_dj", map[string]string{"id": dj.ID, "vibe": "An updated voice direction"})
	call(t, registry, "generate_station_logo", map[string]string{"id": st.ID})
	call(t, registry, "generate_dj_photo", map[string]string{"id": dj.ID})
	if len(images.calls) != 2 || images.calls[0].Description != "Late-night jazz" || images.calls[1].Background != "Fictional jazz historian" {
		t.Fatal("reuse generated unnecessary images", images.calls)
	}
	call(t, registry, "generate_dj_photo", map[string]any{"id": dj.ID, "replace": true, "brief": "Outdoor portrait"})
	if len(images.calls) != 3 || images.calls[2].Brief != "Outdoor portrait" {
		t.Fatal(images.calls)
	}
}
func TestArtworkFailureKeepsCreatedEntityAndRetriesArtworkOnly(t *testing.T) {
	_, _, stations, _, _, repo := fixture(t)
	ctx := context.Background()
	images := &testImages{err: errors.New("Image quota unavailable")}
	registry := producer.NewRegistry(producer.Dependencies{Stations: stations, DJs: repo, Images: images})
	result := call(t, registry, "save_station", map[string]string{"name": "Keep Radio"})
	st := result.Data.(station.Station)
	if result.ArtworkError == "" || st.ID == "" || st.LogoVersion != "" {
		t.Fatal(result)
	}
	all, _ := stations.List(ctx)
	if len(all) != 1 {
		t.Fatal(all)
	}
	images.err = nil
	repaired := call(t, registry, "generate_station_logo", map[string]string{"id": st.ID}).Data.(station.Station)
	all, _ = stations.List(ctx)
	if len(all) != 1 || repaired.ID != st.ID || repaired.LogoVersion == "" {
		t.Fatal("artwork retry duplicated station", all, repaired)
	}
	images.err = errors.New("Image quota unavailable")
	failed := registry.Call(ctx, "generate_station_logo", []byte(`{"id":"`+st.ID+`","replace":true}`))
	kept, _ := stations.Get(ctx, st.ID)
	if failed.Error == "" || kept.LogoVersion != repaired.LogoVersion {
		t.Fatal("failed replacement erased existing artwork", failed, kept)
	}
}
func TestInterruptedAutomaticArtworkHasCheckpointedCreationID(t *testing.T) {
	store, _, stations, _, _, repo := fixture(t)
	ctx := context.Background()
	_ = store.SaveSettings(ctx, "model", producer.KeyChange{Value: "test-key"})
	images := &testImages{started: make(chan struct{})}
	registry := producer.NewRegistry(producer.Dependencies{Stations: stations, DJs: repo, Images: images})
	service, err := producer.New(ctx, store, registry, &scripted{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	id, _ := service.Create(ctx, "")
	_ = service.Start(ctx, id, producer.ID(), "Create the station with a logo")
	select {
	case <-images.started:
	case <-time.After(2 * time.Second):
		t.Fatal("automatic logo not started")
	}
	saved, _ := stations.List(ctx)
	conversation, _ := store.Get(ctx, id)
	if len(saved) != 1 || !strings.Contains(string(conversation.Transcript), saved[0].ID) {
		t.Fatal("creation ID not checkpointed before image call", saved, conversation)
	}
	service.Close()
	conversation, _ = store.Get(ctx, id)
	if conversation.Status != "failed" || !strings.Contains(string(conversation.Transcript), saved[0].ID) {
		t.Fatal("interrupted logo lost station identity", conversation)
	}
}
