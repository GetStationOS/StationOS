package producer_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

func fixture(t *testing.T) (producer.Store, *producer.Registry, persistence.Stations, persistence.Playlists, persistence.Schedule, persistence.DJs) {
	t.Helper()
	db, e := persistence.Open(context.Background(), ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	dj, e := persistence.NewDJs(db, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	st := persistence.Stations{DB: db}
	pl := persistence.Playlists{DB: db}
	sh := persistence.Schedule{DB: db}
	r := producer.NewRegistry(producer.Dependencies{Stations: st, Sources: persistence.Sources{DB: db}, Catalogue: persistence.Catalogue{DB: db}, Playlists: pl, DJs: dj, Schedule: sh, Broadcast: persistence.Broadcast{DB: db}, Mixer: persistence.Mixer{DB: db}, Audience: persistence.Audience{DB: db}})
	return persistence.NewProducer(dj), r, st, pl, sh, dj
}
func call(t *testing.T, r *producer.Registry, name string, args any) producer.Result {
	t.Helper()
	b, _ := json.Marshal(args)
	v := r.Call(context.Background(), name, b)
	if v.Error != "" {
		t.Fatalf("%s: %s", name, v.Error)
	}
	return v
}
func TestProducerToolsCreateAndManageStation(t *testing.T) {
	_, r, st, pl, sh, dj := fixture(t)
	ctx := context.Background()
	v := call(t, r, "save_station", map[string]any{"name": "Evening Jazz"}).Data.(station.Station)
	pool := call(t, r, "save_playlist", map[string]any{"station_id": v.ID, "name": "Jazz"}).Data
	raw, _ := json.Marshal(pool)
	var p struct{ ID string }
	_ = json.Unmarshal(raw, &p)
	call(t, r, "add_playlist_rule", map[string]any{"playlist_id": p.ID, "rule": map[string]string{"kind": "genre", "value": "Jazz"}})
	call(t, r, "assign_default_playlist", map[string]string{"station_id": v.ID, "playlist_id": p.ID})
	if result := r.Call(ctx, "assign_default_playlist", []byte(`{"station_id":"`+v.ID+`"}`)); result.Error == "" {
		t.Fatal("omitted playlist cleared assignment")
	}
	configured, e := (persistence.Broadcast{DB: st.DB}).Assignment(ctx, v.ID)
	if e != nil || configured != p.ID {
		t.Fatal("default was changed", configured, e)
	}
	presenter := call(t, r, "save_dj", map[string]string{"name": "Maya Night"}).Data.(djs.Profile)
	if presenter.HasKey {
		t.Fatal("agent assigned a key")
	}
	call(t, r, "create_shows", map[string]any{"show": map[string]any{"station_id": v.ID, "name": "Late Jazz", "dj_id": presenter.ID, "playlist_id": p.ID, "start": 1320, "end": 60}, "days": []int{4, 6}})
	shows, e := sh.List(ctx, v.ID)
	if e != nil || len(shows) != 2 {
		t.Fatal(shows, e)
	}
	bad := r.Call(ctx, "create_shows", []byte(`{"show":{"station_id":"`+v.ID+`","name":"Conflicting","dj_id":"`+presenter.ID+`","playlist_id":"`+p.ID+`","start":1320,"end":60},"days":[2,4]}`))
	if bad.Error == "" {
		t.Fatal("accepted overlapping shows")
	}
	shows, _ = sh.List(ctx, v.ID)
	if len(shows) != 2 {
		t.Fatal("partial days committed")
	}
	if v.Timezone != "Europe/London" {
		t.Fatal(v)
	}
	call(t, r, "save_station", map[string]string{"id": v.ID, "description": "Updated description"})
	updated, _ := st.Get(ctx, v.ID)
	if updated.Name != v.Name || updated.StreamSlug != v.StreamSlug {
		t.Fatal("patch overwrote settings", updated)
	}
	call(t, r, "save_dj", map[string]string{"id": presenter.ID, "vibe": "Relaxed"})
	updatedDJ, _ := dj.Get(ctx, presenter.ID)
	if updatedDJ.Name != presenter.Name || updatedDJ.Voice != presenter.Voice {
		t.Fatal("DJ patch lost fields")
	}
	if r.Call(ctx, "save_dj", []byte(`{"id":"`+presenter.ID+`","api_key":"hidden"}`)).Error == "" {
		t.Fatal("credential exposed in schema")
	}
	if r.Call(ctx, "delete_playlist", []byte(`{"id":"`+p.ID+`"}`)).Error == "" {
		t.Fatal("deleted assigned playlist")
	}
	for _, s := range shows {
		call(t, r, "delete_show", map[string]string{"station_id": v.ID, "id": s.ID})
	}
	call(t, r, "delete_dj", map[string]string{"id": presenter.ID})
	call(t, r, "delete_station", map[string]string{"id": v.ID})
	if _, e = pl.Get(ctx, p.ID); e == nil {
		t.Fatal("station playlist survived deletion")
	}
}
func TestDiscoveryUsesCatalogueAndPreservesUnknownDuration(t *testing.T) {
	_, r, st, _, _, _ := fixture(t)
	db := st.DB
	ctx := context.Background()
	src := sources.Config{ID: "library", Name: "Music", Kind: "upnp", Endpoint: "http://server/desc", Revision: 1}
	if e := (persistence.Sources{DB: db}).Save(ctx, src); e != nil {
		t.Fatal(e)
	}
	item := content.AudioItem{ID: content.ID(src.ID, "one"), SourceID: src.ID, SourceItemID: "one", Title: "A song", Artist: "Performer", Composer: "Composer", Genre: "Jazz", Kind: content.Track, Origin: content.Original}
	if e := (persistence.Catalogue{DB: db}).Replace(ctx, src, []content.AudioItem{item}); e != nil {
		t.Fatal(e)
	}
	result := call(t, r, "search_music", map[string]any{"genre": "Jazz", "limit": 1})
	b, _ := json.Marshal(result)
	if !strings.Contains(string(b), `"Seconds":null`) || !strings.Contains(string(b), `"Performers":"Performer"`) {
		t.Fatal(string(b))
	}
	call(t, r, "explore_music", map[string]any{"kind": "genres", "limit": 1})
	if e := (catalogue.Query{Limit: 101}).Validate(); e == nil {
		t.Fatal("fixture query validation")
	}
}

type scripted struct {
	mu       sync.Mutex
	requests int
	block    <-chan struct{}
}

func (m *scripted) Respond(ctx context.Context, model, key, prompt string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	m.mu.Lock()
	m.requests++
	n := m.requests
	m.mu.Unlock()
	if m.block != nil {
		select {
		case <-m.block:
		case <-ctx.Done():
			return producer.Response{}, ctx.Err()
		}
	}
	if n == 1 {
		item := json.RawMessage(`{"type":"function_call","call_id":"create-once","name":"save_station","arguments":"{\"name\":\"Agent Radio\"}"}`)
		return producer.Response{Items: []producer.Item{item}, Calls: []producer.Call{{ID: "create-once", Name: "save_station", Arguments: []byte(`{"name":"Agent Radio"}`)}}}, nil
	}
	return producer.Response{Text: "Created Agent Radio."}, nil
}
func waitDone(t *testing.T, store producer.Store, id string) producer.Conversation {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		c, e := store.Get(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if c.Status != "running" {
			return c
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("run did not finish")
	return producer.Conversation{}
}
func TestSavedConversationActsWithoutApprovalAndDoesNotReplay(t *testing.T) {
	store, r, st, _, _, _ := fixture(t)
	ctx := context.Background()
	if e := store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"}); e != nil {
		t.Fatal(e)
	}
	model := &scripted{}
	svc, e := producer.New(ctx, store, r, model)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(svc.Close)
	id, e := svc.Create(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	request := producer.ID()
	if e = svc.Start(ctx, id, request, "Create a station"); e != nil {
		t.Fatal(e)
	}
	c := waitDone(t, store, id)
	if c.Status != "idle" || len(c.Messages) != 4 || c.Messages[2].URL == "" {
		t.Fatal(c)
	}
	if e = svc.Start(ctx, id, request, "Create a station"); !errors.Is(e, producer.ErrDuplicate) {
		t.Fatal("duplicate replay", e)
	}
	stations, _ := st.List(ctx)
	if len(stations) != 1 {
		t.Fatal(stations)
	}
	if e = svc.Start(ctx, id, producer.ID(), "sk-abcdefghijklmnop"); e == nil {
		t.Fatal("accepted secret in chat")
	}
	settings, _ := store.Settings(ctx)
	if !settings.HasKey {
		t.Fatal("credential missing")
	}
}
func TestBusyAndShutdownLeaveDurableFailure(t *testing.T) {
	store, r, _, _, _, _ := fixture(t)
	ctx := context.Background()
	_ = store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"})
	svc, e := producer.New(ctx, store, r, &scripted{block: make(chan struct{})})
	if e != nil {
		t.Fatal(e)
	}
	id, _ := svc.Create(ctx, "")
	if e = svc.Start(ctx, id, producer.ID(), "Create station"); e != nil {
		t.Fatal(e)
	}
	if e = svc.Start(ctx, id, producer.ID(), "Another message"); !errors.Is(e, producer.ErrBusy) {
		t.Fatal(e)
	}
	svc.Close()
	c, _ := store.Get(ctx, id)
	if c.Status != "failed" {
		t.Fatal(c.Status)
	}
}

type failAfterWrite struct{ scripted }

func (m *failAfterWrite) Respond(ctx context.Context, model, key, prompt string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	m.mu.Lock()
	n := m.requests
	m.mu.Unlock()
	if n == 1 {
		m.mu.Lock()
		m.requests++
		m.mu.Unlock()
		return producer.Response{}, errors.New("Simulated model outage")
	}
	return m.scripted.Respond(ctx, model, key, prompt, input, tools)
}
func TestFailedModelRetainsCommittedActionsAndRecoveryContext(t *testing.T) {
	store, r, stations, _, _, _ := fixture(t)
	ctx := context.Background()
	_ = store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"})
	svc, e := producer.New(ctx, store, r, &failAfterWrite{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(svc.Close)
	id, _ := svc.Create(ctx, "")
	if e = svc.Start(ctx, id, producer.ID(), "Create station"); e != nil {
		t.Fatal(e)
	}
	c := waitDone(t, store, id)
	items, _ := stations.List(ctx)
	if c.Status != "failed" || len(items) != 1 || len(c.Messages) != 4 || c.Messages[2].URL == "" {
		t.Fatal(c, items)
	}
	if e = svc.Start(ctx, id, producer.ID(), "Continue from saved state"); e != nil {
		t.Fatal(e)
	}
	c = waitDone(t, store, id)
	items, _ = stations.List(ctx)
	if c.Status != "idle" || len(items) != 1 || !strings.Contains(string(c.Transcript), "Saved station") {
		t.Fatal("recovery replayed or lost committed actions", c, items)
	}
}
