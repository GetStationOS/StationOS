package persistence

import (
	"context"
	"path/filepath"
	"stationos.local/stationos/internal/station"
	"testing"
	"time"
)

func TestRotationPersistsThroughRestartAndCatalogueClear(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rotation.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	stations := Stations{db}
	history := Broadcast{db}
	for _, id := range []string{"a", "b"} {
		if err = stations.Save(ctx, station.Station{ID: id, Name: id, Timezone: "UTC"}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Truncate(time.Second)
	if err = history.RecordPlay(ctx, "a", "exact-track", now); err != nil {
		t.Fatal(err)
	}
	if err = history.RecordPlay(ctx, "a", "exact-track", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	db.Exec(`DELETE FROM catalogue_items`)
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	history = Broadcast{db}
	stations = Stations{db}
	rotation, err := history.Rotation(ctx, "a", now)
	if err != nil || rotation.Interval != 3*time.Hour || rotation.LastTrackID != "exact-track" || !rotation.LastPlayed["exact-track"].Equal(now) {
		t.Fatal(rotation, err)
	}
	other, err := history.Rotation(ctx, "b", now)
	if err != nil || len(other.LastPlayed) != 0 || other.LastTrackID != "" {
		t.Fatal("station history leaked", other, err)
	}
	st, _ := stations.Get(ctx, "a")
	st.RepeatMinutes = 240
	if err = stations.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	rotation, err = history.Rotation(ctx, "a", now.Add(3*time.Hour))
	if err != nil || rotation.Interval != 4*time.Hour || len(rotation.LastPlayed) != 1 {
		t.Fatal("changed interval forgot history", rotation, err)
	}
	rotation, err = history.Rotation(ctx, "a", now.Add(7*24*time.Hour))
	if err != nil || len(rotation.LastPlayed) != 1 || !rotation.LastPlayed["exact-track"].Equal(now) || rotation.LastTrackID != "exact-track" {
		t.Fatal("last-played timestamp outside the repeat window was lost", rotation, err)
	}
	if err = stations.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM station_track_plays`).Scan(&count); err != nil || count != 0 {
		t.Fatal("history not removed with station", count, err)
	}
}
