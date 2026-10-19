package persistence

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"stationos.local/stationos/internal/changefeed"
	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

func TestEpisodePersistenceIsolationInvalidationAndCommitNotification(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "episodes.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	bus := &changefeed.Bus{}
	changes, stop := bus.Watch("episodes")
	defer stop()
	st := Stations{DB: db}
	if err = st.Save(ctx, station.Station{ID: "s", Name: "Station", Timezone: "Europe/London", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	if err = (Playlists{DB: db}).Save(ctx, playlists.Playlist{ID: "pool", StationID: "s", Name: "Pool"}); err != nil {
		t.Fatal(err)
	}
	shows := Schedule{DB: db}
	show := schedule.Show{ID: "show", StationID: "s", Name: "Show", DJID: "default-pop", PlaylistID: "pool", Day: 0, Start: 540, End: 600, MusicDirection: "Night drives"}
	if err = shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	show, err = shows.Get(ctx, "s", "show")
	if err != nil || show.MusicDirection != "Night drives" {
		t.Fatal(show, err)
	}
	loc, _ := time.LoadLocation("Europe/London")
	all := schedule.Occurrences([]schedule.Show{show}, time.Now(), loc)
	o := all[1]
	repo := Episodes{DB: db, Changes: bus}
	if err = repo.Update(ctx, o, func(e *episodes.Episode) error {
		e.Theme = "First episode"
		e.Notes = "Producer direction"
		e.Note("DJ planning", "Retained context")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
	default:
		t.Fatal("no committed notification")
	}
	if err = repo.Update(ctx, o, func(*episodes.Episode) error { return errors.New("reject") }); err == nil {
		t.Fatal("invalid update saved")
	}
	select {
	case <-changes:
		t.Fatal("failed mutation published")
	default:
	}
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo.DB = db
	shows.DB = db
	loaded, err := repo.Get(ctx, o.Key)
	if err != nil || loaded.Theme != "First episode" || loaded.Notes != "Producer direction" || len(loaded.Turns) != 1 {
		t.Fatal(loaded, err)
	}
	if _, err = repo.Get(ctx, all[2].Key); !errors.Is(err, episodes.ErrNotFound) {
		t.Fatal("cross-episode leakage", err)
	}
	show.MusicDirection = "Changed"
	if err = shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	if err = repo.Update(ctx, o, func(e *episodes.Episode) error { e.Theme = "Stale generation"; return nil }); !errors.Is(err, episodes.ErrStale) {
		t.Fatal("stale worker saved", err)
	}
	revised, err := shows.Get(ctx, "s", "show")
	if err != nil {
		t.Fatal(err)
	}
	var revisedOccurrence schedule.Occurrence
	for _, candidate := range schedule.Occurrences([]schedule.Show{revised}, o.Start, loc) {
		if candidate.Start.Equal(o.Start) {
			revisedOccurrence = candidate
		}
	}
	if err = repo.Update(ctx, revisedOccurrence, func(*episodes.Episode) error { return nil }); err != nil {
		t.Fatal(err)
	}
	continued, err := repo.Get(ctx, revisedOccurrence.Key)
	if err != nil || continued.Notes != "Producer direction" || len(continued.Turns) != 1 || continued.Status != "attention" {
		t.Fatal("show revision lost episode session", continued, err)
	}
	if err = shows.Delete(ctx, "s", "show"); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Get(ctx, o.Key); !errors.Is(err, episodes.ErrNotFound) {
		t.Fatal("episode not deleted with show", err)
	}
}
