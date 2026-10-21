package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
	"time"
)

func TestRecoveredIncidentsAgreeWithDashboardAndCurrentHealth(t *testing.T) {
	_, stations, src, cat := setupWeb(t, "")
	ctx := context.Background()
	if err := stations.Save(ctx, station.Station{ID: "radio", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	store := persistence.Operations{DB: stations.DB}
	for i := 0; i < 4; i++ {
		e := operations.Event{ID: fmt.Sprint("failure", i), StationID: "radio", Started: time.Now(), Kind: "failure", Outcome: "observed", IncidentCode: fmt.Sprint("issue", i), Severity: "warning", Reason: "Old issue"}
		if err := store.Save(ctx, e); err != nil {
			t.Fatal(err)
		}
		e.ID = fmt.Sprint("recovery", i)
		e.Recover = true
		if err := store.Save(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{d: Dependencies{Stations: stations, Sources: src, Catalogue: cat, Operations: store}}
	summary := s.operationalSummary(httptest.NewRequest("GET", "/producer", nil), "radio")
	if summary.Active != 0 || summary.NeedsReview != 0 || !summary.Healthy {
		t.Fatalf("recovered incidents counted as active: %+v", summary)
	}
	v := stationDirectoryView{}
	if err := s.loadStationCards(httptest.NewRequest("GET", "/producer", nil), &v); err != nil {
		t.Fatal(err)
	}
	if v.Dashboard.Attention != 0 || len(v.StationCards) != 1 || v.StationCards[0].NeedsAttention {
		t.Fatalf("false attention state: %+v", v.Dashboard)
	}
	active, err := store.Incidents(ctx, "radio", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewIncidents(active, "")) != summary.Active {
		t.Fatal("dashboard and active page disagree")
	}
	tone, _ := stationHealth(broadcast.State{Status: "Dormant", PresenterStatus: "Unavailable", CurationStatus: "Unavailable", Error: "old error", Current: content.AudioItem{ID: "old"}}, summary)
	if tone != "neutral" {
		t.Fatal("dormant station retained old runtime warning", tone)
	}
	e := operations.Event{ID: "new", StationID: "radio", Started: time.Now(), Kind: "failure", Outcome: "observed", IncidentCode: "new", Severity: "warning", Reason: "Current problem"}
	if err := store.Save(ctx, e); err != nil {
		t.Fatal(err)
	}
	summary = s.operationalSummary(httptest.NewRequest("GET", "/producer", nil), "radio")
	tone, label := stationHealth(broadcast.State{Status: "Dormant"}, summary)
	if summary.Active != 1 || tone != "warning" || !strings.Contains(label, "1 active incident") {
		t.Fatal(summary, tone, label)
	}
	if tone, _ := stationHealth(broadcast.State{Status: "Broadcasting"}, operationSummary{}); tone != "unknown" {
		t.Fatal("unknown monitoring shown as healthy")
	}
}

func TestUpcomingArtworkUsesCheckedCatalogueRoute(t *testing.T) {
	_, stations, src, cat := setupWeb(t, "")
	ctx := context.Background()
	source, err := (sources.Service{Repo: src}).Save(ctx, sources.Config{Name: "Music", Kind: "upnp", Endpoint: "http://server/desc"})
	if err != nil {
		t.Fatal(err)
	}
	item := content.AudioItem{ID: content.ID(source.ID, "opening"), SourceID: source.ID, SourceItemID: "opening", Title: "Opening song", Artist: "Opening artist", Artwork: "http://server/album.png", Kind: content.Track, Origin: content.Original}
	if err := cat.Replace(ctx, source, []content.AudioItem{item}); err != nil {
		t.Fatal(err)
	}
	s := &Server{d: Dependencies{Catalogue: cat}}
	tracks := s.followingArtwork(ctx, "radio", []episodes.Track{{ID: item.ID, Title: item.Title, Artist: item.Artist}, {ID: "missing", Title: "Missing song"}})
	if len(tracks) != 2 || tracks[0].Artwork != sourceCoverURL(source.ID, item) || tracks[1].Artwork != "" || tracks[1].Item.Title != "Missing song" {
		t.Fatalf("incorrect catalogue artwork projection: %+v", tracks)
	}
	h, err := New(Dependencies{Stations: stations, Sources: src, Catalogue: cat, ArtworkFactory: func(context.Context, sources.Config) (sources.AudioSource, error) { return artworkSource{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if w := request(h, "GET", tracks[0].Artwork, nil, nil); w.Code != 200 {
		t.Fatal("opening artwork route failed", w.Code)
	}
}
