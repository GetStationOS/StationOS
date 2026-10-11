package persistence

import (
	"context"
	"errors"
	"path/filepath"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
	"testing"
)

func TestScheduleOwnershipOverlapAndDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "schedule.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := Stations{DB: db}
	pl := Playlists{DB: db}
	repo := Schedule{DB: db}
	dj, _ := NewDJs(db, make([]byte, 32))
	for _, id := range []string{"s", "other"} {
		if err = st.Save(ctx, station.Station{ID: id, Name: id, Timezone: "Europe/London", Locale: "en"}); err != nil {
			t.Fatal(err)
		}
		if err = pl.Save(ctx, playlists.Playlist{ID: id + "-pool", StationID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	show := schedule.Show{ID: "show", StationID: "s", Name: "Morning", DJID: "default-pop", PlaylistID: "s-pool", Day: 0, Start: 540, End: 600}
	if err = repo.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	profile, err := dj.Get(ctx, show.DJID)
	if err != nil {
		t.Fatal(err)
	}
	profile.ConversationSpeed = 1.15
	if err = dj.Save(ctx, profile, djs.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.Get(ctx, "s", "show")
	if err != nil || loaded.DJ.ConversationSpeed != 1.15 {
		t.Fatal("scheduled DJ lost speed", loaded.DJ, err)
	}
	wrong := show
	wrong.ID = "wrong"
	wrong.PlaylistID = "other-pool"
	if err = repo.Save(ctx, wrong); !errors.Is(err, schedule.ErrInvalid) {
		t.Fatal(err)
	}
	overlap := show
	overlap.ID = "overlap"
	overlap.Start = 555
	if err = repo.Save(ctx, overlap); !errors.Is(err, schedule.ErrOverlap) {
		t.Fatal(err)
	}
	if _, err = repo.Get(ctx, "other", "show"); !errors.Is(err, schedule.ErrNotFound) {
		t.Fatal(err)
	}
	if err = repo.Delete(ctx, "other", "show"); !errors.Is(err, schedule.ErrNotFound) {
		t.Fatal(err)
	}
	if err = pl.Delete(ctx, "s-pool"); !errors.Is(err, playlists.ErrInUse) {
		t.Fatal(err)
	}
	if err = dj.Delete(ctx, "default-pop"); !errors.Is(err, djs.ErrInUse) {
		t.Fatal(err)
	}
	if err = st.Delete(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if _, err = dj.Get(ctx, "default-pop"); err != nil {
		t.Fatal("station deletion removed DJ", err)
	}
	if err = Integrity(ctx, db); err != nil {
		t.Fatal(err)
	}
}
func TestDefaultDJsAreEditableAndSeededOnlyOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "default.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := NewDJs(db, make([]byte, 32))
	profiles, err := repo.List(ctx)
	if err != nil || len(profiles) != 3 {
		t.Fatal(profiles, err)
	}
	for _, p := range profiles {
		if err = p.Validate(); err != nil || p.HasKey || p.Prompt == "" || p.Vibe == "" {
			t.Fatal(p, err)
		}
	}
	p, _ := repo.Get(ctx, "default-pop")
	p.Name = "My Pop DJ"
	if err = repo.Save(ctx, p, djs.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	if err = repo.Delete(ctx, "default-jazz"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo, _ = NewDJs(db, make([]byte, 32))
	profiles, err = repo.List(ctx)
	if err != nil || len(profiles) != 2 {
		t.Fatal("restart reseeded profiles", profiles, err)
	}
	p, _ = repo.Get(ctx, "default-pop")
	if p.Name != "My Pop DJ" {
		t.Fatal("restart overwrote edit")
	}
}
