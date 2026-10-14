package persistence

import (
	"bytes"
	"context"
	"testing"

	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/schedule"
)

func TestEveryDJAndShowUsesGlobalModelAndIgnoresProfileInput(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	repo, err := NewDJs(db, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	settings := NewProducer(repo)
	s, err := settings.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.CurationModel = "gemma4:12b"
	if err = settings.Configure(ctx, s, producer.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	p, err := (djs.Service{Repo: repo}).Save(ctx, djs.Profile{Name: "Global DJ", Model: "obsolete-openai-model", Prompt: "Keep editorial choices"}, djs.KeyChange{})
	if err != nil || p.Model != "gemma4:12b" {
		t.Fatal(p, err)
	}
	profiles, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range profiles {
		if v.Model != "gemma4:12b" {
			t.Fatal("profile retained an override", v)
		}
	}
	if _, err = db.Exec(`INSERT INTO stations(id,name,description,timezone,locale,stream_slug) VALUES('station','Station','','UTC','en','station'); INSERT INTO playlists(id,station_id,name,description) VALUES('pool','station','Pool','')`); err != nil {
		t.Fatal(err)
	}
	shows := Schedule{DB: db}
	if err = shows.Save(ctx, schedule.Show{ID: "show", StationID: "station", Name: "Programme", DJID: p.ID, PlaylistID: "pool", Day: 0, Start: 540, End: 600}); err != nil {
		t.Fatal(err)
	}
	before, err := shows.Get(ctx, "station", "show")
	if err != nil || before.DJ.Model != "gemma4:12b" {
		t.Fatal(before, err)
	}
	s.CurationModel = "new-global-model"
	if err = settings.Configure(ctx, s, producer.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	p.Model = "another-obsolete-model"
	p, err = (djs.Service{Repo: repo}).Save(ctx, p, djs.KeyChange{})
	if err != nil || p.Model != "new-global-model" || p.Prompt != "Keep editorial choices" {
		t.Fatal(p, err)
	}
	after, err := shows.Get(ctx, "station", "show")
	if err != nil || after.DJ.Model != "new-global-model" || after.DJ.AIRevision <= before.DJ.AIRevision {
		t.Fatal("schedule did not resolve changed global settings", after, err)
	}
	var columns int
	err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('djs') WHERE name='model'`).Scan(&columns)
	if err != nil || columns != 0 {
		t.Fatal("obsolete persisted override remains", columns, err)
	}
}
