package catalogue_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

type provider struct {
	browse    func(string, int, int) (sources.Page, error)
	supported bool
}

func (p provider) Profile() sources.Profile {
	return sources.Profile{Capabilities: sources.Capabilities{Browse: p.supported}}
}
func (p provider) Browse(_ context.Context, o string, n, c int) (sources.Page, error) {
	return p.browse(o, n, c)
}
func (p provider) Get(context.Context, string) (content.AudioItem, error) {
	return content.AudioItem{}, sources.ErrUnsupported
}
func (p provider) Resolve(context.Context, string) ([]content.Resource, error) {
	return nil, sources.ErrUnsupported
}
func setup(t *testing.T) (catalogue.Sync, persistence.Catalogue, sources.Config) {
	t.Helper()
	db, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	_, err = (station.Service{Repo: persistence.Stations{DB: db}}).Save(ctx, station.Station{Name: "Test", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	src, err := (sources.Service{Repo: persistence.Sources{DB: db}}).Save(ctx, sources.Config{Name: "Music", Kind: "upnp", Endpoint: "http://server:9790/desc"})
	if err != nil {
		t.Fatal(err)
	}
	cat := persistence.Catalogue{DB: db}
	return catalogue.Sync{Sources: persistence.Sources{DB: db}, Catalogue: cat, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, cat, src
}
func track(id string) *content.AudioItem {
	return &content.AudioItem{SourceItemID: id, Title: "Track " + id, Kind: content.Track, Origin: content.Original}
}
func TestTreeImportAndIdempotence(t *testing.T) {
	s, cat, c := setup(t)
	p := provider{supported: true, browse: func(o string, n, count int) (sources.Page, error) {
		if o == "0" {
			return sources.Page{Total: 2, Entries: []sources.Entry{{ContainerID: "album"}, {ContainerID: "album"}}}, nil
		}
		return sources.Page{Total: 3, Entries: []sources.Entry{{ContainerID: "0"}, {Item: track("one")}, {Item: track("one")}}}, nil
	}}
	s.Factory = func(context.Context, sources.Config) (sources.AudioSource, error) { return p, nil }
	for i := 0; i < 2; i++ {
		n, err := s.Run(context.Background(), c.ID)
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	got, _ := cat.Search(context.Background(), catalogue.Query{Limit: 50})
	if got.Total != 1 || got.Items[0].SourceID != c.ID || got.Items[0].ID != content.ID(c.ID, "one") {
		t.Fatal(got)
	}
}
func TestImportFailurePreservesCatalogue(t *testing.T) {
	s, cat, c := setup(t)
	a := *track("old")
	a.SourceID = c.ID
	a.ID = content.ID(c.ID, a.SourceItemID)
	if err := cat.Replace(context.Background(), c, []content.AudioItem{a}); err != nil {
		t.Fatal(err)
	}
	s.MaxSnapshotBytes = 512
	tests := map[string]func(string, int, int) (sources.Page, error){"byte limit": func(string, int, int) (sources.Page, error) {
		a := track("large")
		a.Album = strings.Repeat("a", 1000)
		return sources.Page{Total: 1, Entries: []sources.Entry{{Item: a}}}, nil
	},
		"offline": func(string, int, int) (sources.Page, error) { return sources.Page{}, errors.New("offline") }, "stalled": func(string, int, int) (sources.Page, error) { return sources.Page{Total: 2}, nil }, "invalid counts": func(string, int, int) (sources.Page, error) {
			return sources.Page{Total: 0, Entries: []sources.Entry{{Item: track("one")}}}, nil
		}, "changed": func(_ string, n, _ int) (sources.Page, error) {
			return sources.Page{Total: 2, UpdateID: string(rune('a' + n)), Entries: []sources.Entry{{Item: track("one")}}}, nil
		}, "bad item": func(string, int, int) (sources.Page, error) {
			a := track("bad")
			a.Title = ""
			return sources.Page{Total: 1, Entries: []sources.Entry{{Item: a}}}, nil
		}}
	for name, browse := range tests {
		t.Run(name, func(t *testing.T) {
			s.Factory = func(context.Context, sources.Config) (sources.AudioSource, error) {
				return provider{supported: true, browse: browse}, nil
			}
			if _, err := s.Run(context.Background(), c.ID); err == nil {
				t.Fatal("failure accepted")
			}
			got, _ := cat.Search(context.Background(), catalogue.Query{Limit: 50})
			if got.Total != 1 || got.Items[0].Title != "Track old" {
				t.Fatal("old catalogue lost")
			}
		})
	}
}
func TestFactoryFailureUnsupportedAndCancellation(t *testing.T) {
	s, _, c := setup(t)
	s.Factory = func(context.Context, sources.Config) (sources.AudioSource, error) { return nil, errors.New("factory") }
	if _, err := s.Run(context.Background(), c.ID); err == nil {
		t.Fatal("factory error hidden")
	}
	s.Factory = func(context.Context, sources.Config) (sources.AudioSource, error) { return provider{}, nil }
	if _, err := s.Run(context.Background(), c.ID); !errors.Is(err, sources.ErrUnsupported) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Run(ctx, c.ID); err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := s.Run(context.Background(), "missing"); !errors.Is(err, sources.ErrNotFound) {
		t.Fatal(err)
	}
}
