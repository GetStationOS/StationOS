package persistence

import (
	"context"
	"errors"
	"path/filepath"
	"stationos.local/stationos/internal/mixer"
	"stationos.local/stationos/internal/station"
	"testing"
)

func TestMixerPersistsIndependentLevelsAndCascades(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mixer.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	st := Stations{DB: db}
	if err = st.Save(ctx, station.Station{ID: "one", Name: "One", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if err = st.Save(ctx, station.Station{ID: "two", Name: "Two", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	repo := Mixer{DB: db}
	v, err := repo.Get(ctx, "one")
	if err != nil || v != mixer.Default() {
		t.Fatal(v, err)
	}
	want := mixer.Levels{Music: 0, DJ: 275}
	if err = repo.Save(ctx, "one", want); err != nil {
		t.Fatal(err)
	}
	if err = repo.Save(ctx, "one", mixer.Levels{Music: 20, DJ: 401}); err == nil {
		t.Fatal("invalid gain accepted")
	}
	v, err = repo.Get(ctx, "one")
	if err != nil || v != want {
		t.Fatal("failed save changed levels", v, err)
	}
	v, err = repo.Get(ctx, "two")
	if err != nil || v != mixer.Default() {
		t.Fatal("changed other station", v, err)
	}
	if err = repo.Save(ctx, "missing", want); !errors.Is(err, station.ErrNotFound) {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo = Mixer{DB: db}
	v, err = repo.Get(ctx, "one")
	if err != nil || v != want {
		t.Fatal("restart lost gains", v, err)
	}
	if err = (Stations{DB: db}).Delete(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM station_mixer`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
