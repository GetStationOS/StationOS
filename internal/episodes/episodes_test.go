package episodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

type memoryStore struct {
	mu    sync.Mutex
	items map[string]Episode
}

func (m *memoryStore) Get(_ context.Context, key string) (Episode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[SessionKey(key)]
	if !ok {
		return e, ErrNotFound
	}
	raw, _ := json.Marshal(e)
	json.Unmarshal(raw, &e)
	return e, nil
}
func (m *memoryStore) Update(_ context.Context, o schedule.Occurrence, fn func(*Episode) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items = map[string]Episode{}
	}
	e, ok := m.items[SessionKey(o.Key)]
	if !ok {
		e = New(o)
	}
	if err := fn(&e); err != nil {
		return err
	}
	e.Version++
	m.items[SessionKey(o.Key)] = e
	return nil
}

type poolStub []content.AudioItem

func (p poolStub) Pool(context.Context, string) ([]content.AudioItem, error) { return p, nil }

type textStub struct {
	calls    int
	generate func(ai.TextRequest, int) (string, error)
}

func (t *textStub) Generate(_ context.Context, q ai.TextRequest) (string, error) {
	t.calls++
	return t.generate(q, t.calls)
}
func (*textStub) Identity(context.Context, string) (string, error) { return "test", nil }
func (*textStub) Kind(context.Context, string) (string, error)     { return "compatible", nil }
func recording(id string, seconds int) content.AudioItem {
	d := time.Duration(seconds) * time.Second
	return content.AudioItem{ID: id, Title: "Song " + id, Artist: "Artist " + id, Album: "Album " + id, Duration: &d, Artwork: "private-artwork", SourceID: "private-source"}
}
func futureEpisode() schedule.Occurrence {
	return schedule.Occurrence{Key: "episode", ShowID: "show", StationID: "station", Start: time.Now().Add(time.Hour), End: time.Now().Add(time.Hour + 6*time.Minute), DJ: djs.Profile{ID: "dj", Model: "test"}, MusicDirection: "Different artists"}
}
func TestEpisodeSearchesWholePlaylistAndRetainsSession(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	o := futureEpisode()
	pool := poolStub{}
	for i := 0; i < 33; i++ {
		pool = append(pool, recording(fmt.Sprintf("%02d", i), 165))
	}
	store.Update(ctx, o, func(e *Episode) error { e.Notes = "Explore the evening"; e.Theme = "Night drives"; return nil })
	text := &textStub{generate: func(q ai.TextRequest, n int) (string, error) {
		if strings.Contains(q.Input, "private-artwork") || strings.Contains(q.Input, "private-source") {
			t.Fatal("transport metadata leaked")
		}
		if !strings.Contains(q.Input, "Night drives") || !strings.Contains(q.Input, "Explore the evening") {
			t.Fatal("session context missing", q.Input)
		}
		if n == 1 {
			return `{"Action":"search","Offset":30}`, nil
		}
		if !strings.Contains(q.Input, "Song 31") {
			t.Fatal("whole-playlist search unavailable", q.Input)
		}
		return `{"Action":"plan","Tracks":["31","32"],"Theme":"Night drives"}`, nil
	}}
	service := &Service{Store: store, Pool: pool, Text: text}
	if err := service.Plan(ctx, o); err != nil {
		t.Fatal(err)
	}
	e, _ := store.Get(ctx, o.Key)
	if e.Status != "ready" || len(e.Plan) != 2 || len(e.Turns) != 3 {
		t.Fatal(e)
	}
	if err := service.Aired(ctx, o, pool[31], ""); err != nil {
		t.Fatal(err)
	}
	id, err := service.SelectTrack(ctx, production.SelectionRequest{Show: o, Current: pool[31], Candidates: pool})
	if err != nil || id != "32" {
		t.Fatal(id, err)
	}
	// A newly constructed service resumes the same session, a different dated key does not.
	restarted := &Service{Store: store}
	contextText, _ := restarted.Context(ctx, o)
	if !strings.Contains(contextText, "Song 31") {
		t.Fatal(contextText)
	}
	o.Key = "next-week"
	contextText, err = restarted.Context(ctx, o)
	if err != nil || contextText != "" {
		t.Fatal("episode context leaked", contextText, err)
	}
}
func TestPlanRejectsRepetitionAndPreservesProducerConstraints(t *testing.T) {
	a, b, c := recording("a", 165), recording("b", 165), recording("c", 165)
	b.Artist = a.Artist
	b.Album = a.Album
	pool := map[string]content.AudioItem{"a": a, "b": b, "c": c}
	offered := map[string]bool{"a": true, "b": true, "c": true}
	if _, err := validatePlan(command{Tracks: []string{"a", "b"}}, Episode{}, pool, offered, 360, "varied"); err == nil {
		t.Fatal("artist/album marathon accepted")
	}
	if _, err := validatePlan(command{Tracks: []string{"a", "b"}}, Episode{}, pool, offered, 360, "album"); err != nil {
		t.Fatal("album special rejected", err)
	}
	if _, err := validatePlan(command{Tracks: []string{"a", "c"}}, Episode{Excluded: []string{"c"}}, pool, offered, 360, "varied"); err == nil {
		t.Fatal("producer exclusion ignored")
	}
	if _, err := validatePlan(command{Tracks: []string{"a", "c"}}, Episode{Plan: []Track{{ID: "b", Locked: true}}}, pool, offered, 360, "album"); err == nil {
		t.Fatal("kept recording lost")
	}
	if _, err := validatePlan(command{Tracks: []string{"a", "c"}}, Episode{}, pool, map[string]bool{"a": true}, 360, "varied"); err == nil {
		t.Fatal("unoffered recording accepted")
	}
}
func TestExclusionsApplyToFallbackAndStaleEditsFail(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	o := futureEpisode()
	a, b := recording("a", 165), recording("b", 165)
	store.Update(ctx, o, func(e *Episode) error { e.Plan = []Track{Snapshot(a), Snapshot(b)}; return nil })
	service := &Service{Store: store}
	e, _ := store.Get(ctx, o.Key)
	if err := service.Edit(ctx, o, e.Version, "New direction", []string{"b"}, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Edit(ctx, o, e.Version, "Stale direction", nil, nil); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	got, err := service.FilterCandidates(ctx, o, []content.AudioItem{a, b})
	if err != nil || len(got) != 1 || got[0].ID != "b" {
		t.Fatal(got, err)
	}
	service.Aired(ctx, o, b, "")
	id, err := service.SelectTrack(ctx, production.SelectionRequest{Show: o, Candidates: []content.AudioItem{a, b}})
	if err == nil || id != "" {
		t.Fatal("excluded fallback became a choice", id, err)
	}
}

type stationStub struct {
	station.Repository
	values []station.Station
}

func (s stationStub) List(context.Context) ([]station.Station, error) { return s.values, nil }

type scheduleStub struct {
	schedule.Repository
	values []schedule.Show
}

func (s scheduleStub) List(context.Context, string) ([]schedule.Show, error) { return s.values, nil }
func TestAutomaticPreparationRequiresNoApproval(t *testing.T) {
	ctx := context.Background()
	day := time.Now().UTC().AddDate(0, 0, 1)
	weekday := (int(day.Weekday()) + 6) % 7
	shows := scheduleStub{values: []schedule.Show{{ID: "show", StationID: "station", Name: "Autonomous", Day: weekday, Start: 540, End: 555, PlaylistID: "pool", DJ: djs.Profile{ID: "dj", TextReady: true}}}}
	stations := stationStub{values: []station.Station{{ID: "station", Timezone: "UTC"}}}
	store := &memoryStore{}
	text := &textStub{generate: func(_ ai.TextRequest, n int) (string, error) {
		if n == 1 {
			return `{"Action":"search"}`, nil
		}
		return `{"Action":"plan","Tracks":["one"],"Theme":"Autonomous"}`, nil
	}}
	service := &Service{Store: store, Pool: poolStub{recording("one", 885)}, Text: text}
	if err := service.sweep(ctx, stations, shows); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range store.items {
		if e.Status != "ready" {
			t.Fatal(e)
		}
		count++
	}
	if count != 1 {
		t.Fatal("expected one dated episode", count)
	}
	if err := service.sweep(ctx, stations, shows); err != nil {
		t.Fatal(err)
	}
	if text.calls != 2 {
		t.Fatal("ready episode replanned", text.calls)
	}
}
