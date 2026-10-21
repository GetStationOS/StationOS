package web

import (
	"context"
	"fmt"
	"log/slog"
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
	if summary.Active != 1 || tone != "neutral" || strings.Contains(label, "active incident") {
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

func TestCurrentHealthDoesNotInferFaultsFromReviewedHistoricalIncidents(t *testing.T) {
	summary := operationSummary{Available: true, Healthy: true, Active: 4}
	state := broadcast.State{Status: "Broadcasting", Current: content.AudioItem{ID: "now"}, PresenterStatus: "Ready", CurationStatus: "Preparing"}
	tone, label := stationHealth(state, summary)
	if tone != "healthy" || strings.Contains(label, "active incident") {
		t.Fatal("historical backlog is not current health", tone, label)
	}
	for _, bad := range []broadcast.State{{Status: "Retrying"}, {Status: "Unavailable"}, {Status: "Broadcasting", Current: content.AudioItem{ID: "now"}, PresenterStatus: "Unavailable"}, {Status: "Broadcasting", Current: content.AudioItem{ID: "now"}, CurationStatus: "Unavailable"}} {
		if tone, _ := stationHealth(bad, summary); tone != "warning" {
			t.Fatal("current runtime failure was hidden", bad)
		}
	}
}

func TestLiveIncidentViewIsObservationalAndIgnoresOldProgrammeFaults(t *testing.T) {
	_, stations, src, cat := setupWeb(t, "")
	ctx := context.Background()
	if err := stations.Save(ctx, station.Station{ID: "radio", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	store := persistence.Operations{DB: stations.DB}
	for i, code := range []string{"programme", "episode-plan:old:2026-10-19T18:00:00Z", "episode-plan:older:2026-10-19T19:00:00Z", "episode-plan:ended:2026-10-19T20:00:00Z"} {
		if err := store.Save(ctx, operations.Event{ID: fmt.Sprint("old", i), StationID: "radio", Kind: "failure", Outcome: "observed", Started: time.Now().Add(-24 * time.Hour), IncidentCode: code, Severity: "warning", Reason: "Historical planning or fallback fault"}); err != nil {
			t.Fatal(err)
		}
	}
	manager := broadcast.New(persistence.Broadcast{DB: stations.DB}, tickingEncoder{}, slog.Default(), broadcast.Options{})
	defer manager.Close()
	h, err := New(Dependencies{Stations: stations, Sources: src, Catalogue: cat, Operations: store, Broadcast: manager})
	if err != nil {
		t.Fatal(err)
	}
	active := request(h, "GET", "/producer/stations/radio/incidents", nil, nil).Body.String()
	if !strings.Contains(active, "No active incidents") || strings.Contains(active, "Mark reviewed") || strings.Contains(active, ">Needs review</a>") {
		t.Fatal("incident UI imposed historical faults or manual review", active)
	}
	all := request(h, "GET", "/producer/stations/radio/incidents?review=all", nil, nil).Body.String()
	if strings.Count(all, `<tr id="incident-`) != 4 || !strings.Contains(all, "Historical") {
		t.Fatal("lost historical observations", all)
	}
	home := request(h, "GET", "/producer", nil, nil).Body.String()
	if strings.Contains(home, `class="dashboard-attention"`) {
		t.Fatal("historical records caused false station attention", home)
	}
}
