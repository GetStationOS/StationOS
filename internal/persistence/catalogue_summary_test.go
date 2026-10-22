package persistence

import (
	"context"
	"encoding/json"
	"stationos.local/stationos/internal/catalogue"
	"testing"
	"time"
)

func TestCatalogueSummaryAggregatesOneHundredThousandTracks(t *testing.T) {
	db := database(t)
	seed(t, db)
	_, err := db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000) INSERT INTO catalogue_items(id,source_id,source_item_id,title,artist,album,metadata) SELECT 'track-'||x,'source',CAST(x AS TEXT),'Recording '||x,'Artist '||(x%100),'Album '||(x%1000),json_object('genre',CASE WHEN x%2=0 THEN 'Jazz' ELSE 'Blues' END,'duration_ns',180000000000) FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	repo := Catalogue{DB: db}
	start := time.Now()
	// Exercise the aggregate adapter directly: race instrumentation must not
	// turn this scale test into a test of the production service deadline.
	s, err := repo.Summary(context.Background(), catalogue.SummaryQuery{Limit: 2})
	if err != nil || s.Tracks != 100000 || s.Albums != 1000 || s.Artists != 100 || s.Genres != 2 || s.KnownSeconds != 18000000 || len(s.TopArtists) != 2 || s.TopGenres[0].Tracks != 50000 {
		t.Fatal(s, err)
	}
	raw, _ := json.Marshal(s)
	if len(raw) > 1500 {
		t.Fatal("summary grows with recording count", len(raw))
	}
	t.Log("100k catalogue aggregate", time.Since(start), "response bytes", len(raw))
	s, err = repo.Summary(context.Background(), catalogue.SummaryQuery{Genre: "Jazz", Limit: 1})
	if err != nil || s.Tracks != 50000 || s.Artists != 50 || s.Genres != 1 || s.TopGenres[0].Name != "Jazz" {
		t.Fatal("genre scope was not respected", s, err)
	}
	s, err = repo.Summary(context.Background(), catalogue.SummaryQuery{SourceID: "missing", Limit: 12})
	if err != nil || s.Tracks != 0 || len(s.TopGenres) != 0 {
		t.Fatal(s, err)
	}
}

// Keep the production service deadline covered with a small catalogue, rather
// than tying the instrumented 100k-row scale fixture to a wall-clock budget.
func TestCatalogueSummaryServiceMissingSource(t *testing.T) {
	db := database(t)
	seed(t, db)
	service := catalogue.SummaryService{Repo: Catalogue{DB: db}}
	s, err := service.Summarize(context.Background(), catalogue.SummaryQuery{SourceID: "missing"})
	if err != nil || s.Tracks != 0 || len(s.TopGenres) != 0 {
		t.Fatal(s, err)
	}
}
