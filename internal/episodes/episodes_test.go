package episodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	store.Update(ctx, o, func(e *Episode) error {
		e.Notes = "Explore the evening"
		e.Theme = "Night drives"
		return nil
	})
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
		if n == 2 {
			return `{"Action":"plan","Tracks":["31"],"Theme":"Night drives"}`, nil
		}
		return `{"Action":"plan","Tracks":["32"],"Theme":"Night drives"}`, nil
	}}
	service := &Service{Store: store, Pool: pool, Text: text}
	if err := service.Plan(ctx, o); err != nil {
		t.Fatal(err)
	}
	e, _ := store.Get(ctx, o.Key)
	if e.Status != "ready" || len(e.Plan) != 1 || len(e.Turns) != 3 {
		t.Fatal(e)
	}
	if err := service.Aired(ctx, o, pool[31], ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Plan(ctx, o); err != nil {
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
	now := time.Now().UTC()
	weekday := (int(now.Weekday()) + 6) % 7
	shows := scheduleStub{values: []schedule.Show{{ID: "show", StationID: "station", Name: "Autonomous", Day: weekday, Start: 0, End: 0, PlaylistID: "pool", DJ: djs.Profile{ID: "dj", TextReady: true}}}}
	stations := stationStub{values: []station.Station{{ID: "station", Timezone: "UTC"}}}
	store := &memoryStore{}
	text := &textStub{generate: func(ai.TextRequest, int) (string, error) {
		return `{"Action":"plan","Tracks":["one"],"Theme":"Autonomous"}`, nil
	}}
	service := &Service{Store: store, Pool: poolStub{recording("one", 885)}, Text: text}
	if err := service.sweep(ctx, stations, shows); err != nil {
		t.Fatal(err)
	}
	if text.calls != 0 || len(store.items) != 0 {
		t.Fatal("unopened session used tokens")
	}
	o := preparationWindow(shows.values, now, time.UTC)[0]
	service.Begin(ctx, o)
	if err := service.sweep(ctx, stations, shows); err != nil {
		t.Fatal(err)
	}
	if text.calls != 0 {
		t.Fatal("selection ran before introduction")
	}
	service.Remember(ctx, o, "prepared presentation (not yet aired)", "Welcome")
	for i := 0; i < 3; i++ {
		if err := service.sweep(ctx, stations, shows); err != nil {
			t.Fatal(err)
		}
	}
	if text.calls != 1 || len(store.items) != 1 {
		t.Fatal("not exactly one autonomous choice", text.calls, len(store.items))
	}
}

func TestMusicPromptCanDeliberatelyRepeatPerformerAndAlbum(t *testing.T) {
	ctx := context.Background()
	o := futureEpisode()
	o.MusicDirection = "An album special: stay with this artist and album."
	store := &memoryStore{}
	store.Update(ctx, o, func(e *Episode) error { e.Note("prepared presentation (not yet aired)", "Welcome"); return nil })
	a, b := recording("a", 165), recording("b", 165)
	b.Artist = a.Artist
	b.Album = a.Album
	text := &textStub{generate: func(q ai.TextRequest, n int) (string, error) {
		if !strings.Contains(q.Input, o.MusicDirection) {
			t.Fatal("DJ music prompt missing")
		}
		if n == 1 {
			return `{"Action":"search"}`, nil
		}
		return `{"Action":"plan","Tracks":["a"],"Theme":"Album special","RepetitionReason":"The DJ music prompt requests an album special."}`, nil
	}}
	service := &Service{Store: store, Pool: poolStub{a, b, recording("alternative", 165)}, Text: text}
	if err := service.Plan(ctx, o); err != nil {
		t.Fatal(err)
	}
	if !service.RepetitionAllowed(ctx, o) {
		t.Fatal("prompt-driven special blocked by runtime variety rules")
	}
}

func TestPendingSelectionPreservesPlanAndRetryVersions(t *testing.T) {
	ctx := context.Background()
	o := futureEpisode()
	store := &memoryStore{}
	service := &Service{Store: store}
	if _, err := service.SelectTrack(ctx, production.SelectionRequest{Show: o}); !errors.Is(err, production.ErrPlanPending) {
		t.Fatal(err)
	}
	for _, status := range []string{"planning", "unplanned", "attention"} {
		retry := time.Now().Add(5 * time.Minute)
		store.Update(ctx, o, func(e *Episode) error { e.Status = status; e.RetryAt = retry; return nil })
		before, _ := store.Get(ctx, o.Key)
		for i := 0; i < 5; i++ {
			if _, err := service.SelectTrack(ctx, production.SelectionRequest{Show: o}); !errors.Is(err, production.ErrPlanPending) {
				t.Fatal(status, err)
			}
		}
		after, _ := store.Get(ctx, o.Key)
		if after.Version != before.Version || !after.RetryAt.Equal(retry) {
			t.Fatal("pending selection invalidated planning or defeated retry backoff", before, after)
		}
	}
}
func TestPlanningSurvivesNormalPlaybackAndSpeechContextUpdates(t *testing.T) {
	ctx := context.Background()
	o := futureEpisode()
	store := &memoryStore{}
	store.Update(ctx, o, func(e *Episode) error { e.Status = "planning"; return nil })
	service := &Service{Store: store, Pool: poolStub{recording("a", 165), recording("b", 165)}}
	service.Text = &textStub{generate: func(_ ai.TextRequest, n int) (string, error) {
		if err := service.Remember(ctx, o, "prepared speech (not yet aired)", "A normal presenter link"); err != nil {
			t.Fatal(err)
		}
		if _, err := service.SelectTrack(ctx, production.SelectionRequest{Show: o}); !errors.Is(err, production.ErrPlanPending) {
			t.Fatal(err)
		}
		if n == 1 {
			if err := service.Aired(ctx, o, recording("current", 30), ""); err != nil {
				t.Fatal(err)
			}
			return `{"Action":"search"}`, nil
		}
		return `{"Action":"plan","Tracks":["a"],"Theme":"A varied hour"}`, nil
	}}
	if err := service.Plan(ctx, o); err != nil {
		t.Fatal("ordinary context updates repeatedly cancelled planning", err)
	}
	e, _ := store.Get(ctx, o.Key)
	if e.Status != "ready" || len(e.Aired) != 1 || e.AiredRevision != 1 {
		t.Fatal(e)
	}
}
func TestPlanningStillRejectsConcurrentProducerEdits(t *testing.T) {
	ctx := context.Background()
	o := futureEpisode()
	store := &memoryStore{}
	store.Update(ctx, o, func(e *Episode) error { e.Note("prepared presentation (not yet aired)", "Welcome"); return nil })
	service := &Service{Store: store, Pool: poolStub{recording("a", 165)}}
	service.Text = &textStub{generate: func(_ ai.TextRequest, _ int) (string, error) {
		e, _ := store.Get(ctx, o.Key)
		if err := service.Edit(ctx, o, e.Version, "New producer direction", nil, nil); err != nil {
			t.Fatal(err)
		}
		return `{"Action":"search"}`, nil
	}}
	if err := service.Plan(ctx, o); !errors.Is(err, ErrStale) {
		t.Fatal("old plan overwrote new direction", err)
	}
	e, _ := store.Get(ctx, o.Key)
	if e.Notes != "New producer direction" || e.Status == "ready" {
		t.Fatal(e)
	}
}
func TestGenuinePlanningFailureIsReportedOnceAndRetryBackoffSurvivesPlayback(t *testing.T) {
	ctx := context.Background()
	day := time.Now().UTC()
	weekday := (int(day.Weekday()) + 6) % 7
	shows := scheduleStub{values: []schedule.Show{{ID: "show", StationID: "station", Name: "Automatic", Day: weekday, Start: 0, End: 0, PlaylistID: "pool", DJ: djs.Profile{ID: "dj", TextReady: true}}}}
	stations := stationStub{values: []station.Station{{ID: "station", Timezone: "UTC"}}}
	store := &memoryStore{}
	var logs bytes.Buffer
	reported, recovered := 0, 0
	text := &textStub{generate: func(_ ai.TextRequest, _ int) (string, error) {
		return "", fmt.Errorf("%w: provider test failure", ai.ErrOutputLimit)
	}}
	service := &Service{Store: store, Pool: poolStub{recording("one", 885)}, Text: text, Logger: slog.New(slog.NewTextHandler(&logs, nil)), ReportPlanning: func(_ schedule.Occurrence, err error) {
		if err == nil {
			recovered++
		} else {
			reported++
		}
	}}
	for _, o := range preparationWindow(shows.values, time.Now(), time.UTC) {
		store.Update(ctx, o, func(e *Episode) error { e.Note("prepared presentation (not yet aired)", "Welcome"); return nil })
	}
	if err := service.sweep(ctx, stations, shows); err != nil {
		t.Fatal(err)
	}
	var o schedule.Occurrence
	for _, candidate := range schedule.Occurrences(shows.values, day, time.UTC) {
		if candidate.Start.YearDay() == day.YearDay() && candidate.Start.Year() == day.Year() {
			o = candidate
		}
	}
	before, _ := store.Get(ctx, o.Key)
	if !before.PlanningFailed || before.FailureCode != "output_limit" || reported != 1 || !strings.Contains(logs.String(), "provider test failure") {
		t.Fatal("real failure was hidden", before, reported, logs.String())
	}
	for i := 0; i < 3; i++ {
		service.SelectTrack(ctx, production.SelectionRequest{Show: o})
	}
	if err := service.sweep(ctx, stations, shows); err != nil {
		t.Fatal(err)
	}
	after, _ := store.Get(ctx, o.Key)
	if text.calls != 1 || reported != 1 || !after.RetryAt.Equal(before.RetryAt) {
		t.Fatal("playback caused an error retry storm", text.calls, reported, after)
	}
	store.Update(ctx, o, func(e *Episode) error { e.RetryAt = time.Time{}; return nil })
	text.calls = 0
	text.generate = func(_ ai.TextRequest, n int) (string, error) {
		if n == 1 {
			return `{"Action":"search"}`, nil
		}
		return `{"Action":"plan","Tracks":["one"],"Theme":"Recovered"}`, nil
	}
	if err := service.sweep(ctx, stations, shows); err != nil {
		t.Fatal(err)
	}
	after, _ = store.Get(ctx, o.Key)
	if after.Status != "ready" || after.PlanningFailed || recovered != 1 {
		t.Fatal("real recovery missing", after, recovered)
	}
}

func TestPreparationWindowFollowsAirtimeAcrossWeekBoundary(t *testing.T) {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 23, 30, 0, 0, loc)
	shows := []schedule.Show{
		{ID: "overnight", Day: 6, Start: 23 * 60, End: 60},
		{ID: "next", Day: 0, Start: 60, End: 120},
		{ID: "later", Day: 0, Start: 120, End: 180},
	}
	window := preparationWindow(shows, now, loc)
	if len(window) != 2 || window[0].ShowID != "overnight" || window[1].ShowID != "next" {
		t.Fatal(window)
	}
	// At the exact handover the old show is no longer eligible; advance once.
	window = preparationWindow(shows, time.Date(2026, 10, 26, 1, 0, 0, 0, loc), loc)
	if len(window) != 2 || window[0].ShowID != "next" || window[1].ShowID != "later" {
		t.Fatal(window)
	}
	// In a gap only the nearest future show is eligible.
	window = preparationWindow(shows[1:], now, loc)
	if len(window) != 1 || window[0].ShowID != "next" {
		t.Fatal(window)
	}
}

func TestRollingEpisodeChoosesOneTrackAndRetainsIntroAndPlaybackContext(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	o := futureEpisode()
	o.Start = time.Now().Add(-time.Minute)
	o.End = time.Now().Add(time.Hour)
	pool := poolStub{recording("a", 165), recording("b", 165), recording("c", 165)}
	service := &Service{Store: store, Pool: pool}
	if err := service.Begin(ctx, o); err != nil {
		t.Fatal(err)
	}
	e, _ := store.Get(ctx, o.Key)
	if episodeSessionReady(e) {
		t.Fatal("intro not prepared yet")
	}
	service.Remember(ctx, o, "prepared presentation (not yet aired)", "Welcome to our musical journey")
	text := &textStub{generate: func(q ai.TextRequest, n int) (string, error) {
		if q.Options.MaxOutputTokens != 512 || !q.Options.DisableThinking || !strings.Contains(q.Input, "Welcome to our musical journey") || !strings.Contains(q.Instructions, "exactly ONE") {
			t.Fatal(q)
		}
		if n == 1 {
			return `{"Action":"plan","Tracks":["a"],"Theme":"Musical journey"}`, nil
		}
		if !strings.Contains(q.Input, "Musical journey") || !strings.Contains(q.Input, `\"a\"`) && !strings.Contains(q.Input, `"AiredIDs":["a"]`) {
			t.Fatal("missing played history", q.Input)
		}
		return `{"Action":"plan","Tracks":["b"],"Theme":"Musical journey"}`, nil
	}}
	service.Text = text
	if err := service.Plan(ctx, o); err != nil {
		t.Fatal(err)
	}
	e, _ = store.Get(ctx, o.Key)
	if len(e.Plan) != 1 || e.Plan[0].ID != "a" || text.calls != 1 {
		t.Fatal(e, text.calls)
	}
	ready, err := service.Available(ctx, o, e)
	if err != nil || !ready {
		t.Fatal(ready, err)
	}
	service.Aired(ctx, o, pool[0], "")
	e, _ = store.Get(ctx, o.Key)
	ready, err = service.Available(ctx, o, e)
	if err != nil || ready {
		t.Fatal("consumed queue was not replenished", ready, err)
	}
	if err := service.Plan(ctx, o); err != nil {
		t.Fatal(err)
	}
	e, _ = store.Get(ctx, o.Key)
	if len(e.Plan) != 1 || e.Plan[0].ID != "b" || len(e.Aired) != 1 || text.calls != 2 {
		t.Fatal(e, text.calls)
	}
}

func TestRollingChoiceRejectsMultipleTracksAndAvoidableOverrun(t *testing.T) {
	pool := map[string]content.AudioItem{"a": recording("a", 60), "b": recording("b", 600)}
	offered := map[string]bool{"a": true, "b": true}
	for _, cmd := range []command{{Tracks: []string{"a", "b"}}, {Tracks: []string{"b"}}} {
		if _, err := validateNextTrack(cmd, Episode{}, pool, offered, 120, "varied"); err == nil {
			t.Fatal("invalid rolling choice accepted", cmd)
		}
	}
	if _, err := validateNextTrack(command{Tracks: []string{"a"}}, Episode{}, pool, offered, 3600, "varied"); err != nil {
		t.Fatal("one track forced to fill the show", err)
	}
}

func TestCancellationProtectsAiredHistoryAndRejectsInFlightPlanning(t *testing.T) {
	ctx := context.Background()
	o := futureEpisode()
	store := &memoryStore{}
	service := &Service{Store: store, Pool: poolStub{recording("a", 165)}}
	service.Begin(ctx, o)
	e, _ := store.Get(ctx, o.Key)
	if err := service.Cancel(ctx, o, e.Version, true); err != nil {
		t.Fatal(err)
	}
	if err := service.Plan(ctx, o); !errors.Is(err, ErrCancelled) {
		t.Fatal(err)
	}
	e, _ = store.Get(ctx, o.Key)
	if err := service.Edit(ctx, o, e.Version, "", nil, nil); !errors.Is(err, ErrCancelled) {
		t.Fatal(err)
	}
	if err := service.Cancel(ctx, o, e.Version, false); err != nil {
		t.Fatal(err)
	}
	service.Text = &textStub{generate: func(ai.TextRequest, int) (string, error) {
		e, _ := store.Get(ctx, o.Key)
		if err := service.Cancel(ctx, o, e.Version, true); err != nil {
			t.Fatal(err)
		}
		return `{"Action":"plan","Tracks":["a"]}`, nil
	}}
	if err := service.Plan(ctx, o); !errors.Is(err, ErrStale) {
		t.Fatal("in-flight generation overwrote cancellation", err)
	}
	e, _ = store.Get(ctx, o.Key)
	if !e.Cancelled || len(e.Plan) != 0 {
		t.Fatal(e)
	}
	service.Cancel(ctx, o, e.Version, false)
	service.Aired(ctx, o, recording("a", 165), "")
	e, _ = store.Get(ctx, o.Key)
	if err := service.Cancel(ctx, o, e.Version, true); err == nil {
		t.Fatal("aired episode cancelled")
	}
	o.Start = time.Now().Add(-time.Minute)
	if err := service.Cancel(ctx, o, e.Version, true); err == nil {
		t.Fatal("started episode cancelled")
	}
	after, _ := store.Get(ctx, o.Key)
	if len(after.Aired) != 1 || after.Cancelled {
		t.Fatal("history changed", after)
	}
}

func TestPendingSelectionPreservesSpecificFailureAndLiveProgress(t *testing.T) {
	ctx := context.Background()
	o := futureEpisode()
	store := &memoryStore{}
	service := &Service{Store: store}
	for _, code := range []string{"quota", "rate_limit", "output_limit", "context_limit", "invalid_response", "timeout", "unknown"} {
		retry := time.Now().Add(time.Minute)
		store.Update(ctx, o, func(e *Episode) error {
			e.Status = "unplanned"
			e.PlanningFailed = true
			e.FailureCode = code
			e.RetryAt = retry
			e.Plan = []Track{{ID: "aired"}}
			e.Aired = []Track{{ID: "aired"}}
			return nil
		})
		before, _ := store.Get(ctx, o.Key)
		_, err := service.SelectTrack(ctx, production.SelectionRequest{Show: o})
		var pending *production.PendingSelection
		if !errors.Is(err, production.ErrPlanPending) || !errors.As(err, &pending) || pending.Reason != ai.FailureMessage(code) || !pending.RetryAt.Equal(retry) {
			t.Fatal(code, err)
		}
		after, _ := store.Get(ctx, o.Key)
		if after.Version != before.Version {
			t.Fatal("diagnostic read changed retry state")
		}
	}
	store.Update(ctx, o, func(e *Episode) error { e.Status = "planning"; e.Plan = nil; return nil })
	_, err := service.SelectTrack(ctx, production.SelectionRequest{Show: o})
	var pending *production.PendingSelection
	if !errors.As(err, &pending) || !strings.Contains(pending.Reason, "still choosing") || !pending.RetryAt.IsZero() {
		t.Fatal("stale failure shown during active preparation", err)
	}
}
