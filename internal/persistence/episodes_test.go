package persistence

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"stationos.local/stationos/internal/audience"
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

func TestCancellationSkipsOnlyOneDatedEpisodeAndCanBeRestored(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stations := Stations{DB: db}
	pools := Playlists{DB: db}
	shows := Schedule{DB: db}
	if err = stations.Save(ctx, station.Station{ID: "s", Name: "Station", Timezone: "Europe/London", Locale: "en", Visibility: "listed", StreamSlug: "radio"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"show-pool", "fallback"} {
		if err = pools.Save(ctx, playlists.Playlist{ID: id, StationID: "s", Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err = (Broadcast{DB: db}).Assign(ctx, "s", "fallback"); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Europe/London")
	day := time.Now().In(loc).AddDate(0, 0, 1)
	show := schedule.Show{ID: "show", StationID: "s", Name: "Weekly", DJID: "default-pop", PlaylistID: "show-pool", Day: (int(day.Weekday()) + 6) % 7, Start: 540, End: 600, AcceptShoutouts: true}
	if err = shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	show, _ = shows.Get(ctx, "s", "show")
	start := schedule.WallTime(day, 540, false)
	var occurrence schedule.Occurrence
	for _, o := range schedule.Occurrences([]schedule.Show{show}, start, loc) {
		if o.Start.Equal(start) {
			occurrence = o
		}
	}
	store := Episodes{DB: db}
	service := episodes.Service{Store: store}
	if err = service.Cancel(ctx, occurrence, 0, true); err != nil {
		t.Fatal(err)
	}
	message := audience.Message{ID: "listener-message", StationID: "s", ShowID: "show", OccurrenceKey: occurrence.Key, Kind: audience.Shoutout, Status: audience.Pending, Name: "Listener", Text: "Hello", SenderKey: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", Created: time.Now(), ShowStart: occurrence.Start, Expires: occurrence.End}
	if err = (Audience{DB: db}).Submit(ctx, message); !errors.Is(err, audience.ErrClosed) {
		t.Fatal("cancelled show accepted a contribution", err)
	}
	e, _ := store.Get(ctx, occurrence.Key)
	if !e.Cancelled || e.Label() != "Cancelled" {
		t.Fatal(e)
	}
	for _, at := range []time.Time{start.Add(time.Minute), start.Add(30 * time.Minute)} {
		timeline, err := shows.Resolve(ctx, "s", at)
		if err != nil || timeline.Current.ShowID != "" || timeline.Current.PlaylistID != "fallback" {
			t.Fatal(timeline, err)
		}
	}
	// A template revision cannot silently restore the same cancelled date.
	show.Name = "Revised weekly"
	if err = shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	timeline, err := shows.Resolve(ctx, "s", start.Add(time.Minute))
	if err != nil || timeline.Current.ShowID != "" {
		t.Fatal(timeline, err)
	}
	next, err := shows.Resolve(ctx, "s", start.AddDate(0, 0, 7).Add(time.Minute))
	if err != nil || next.Current.ShowID != "show" {
		t.Fatal("cancellation leaked to next week", next, err)
	}
	show, _ = shows.Get(ctx, "s", "show")
	for _, o := range schedule.Occurrences([]schedule.Show{show}, start, loc) {
		if o.Start.Equal(start) {
			occurrence = o
		}
	}
	if err = service.Cancel(ctx, occurrence, 0, false); !errors.Is(err, episodes.ErrStale) {
		t.Fatal("stale restore accepted", err)
	}
	if err = service.Cancel(ctx, occurrence, e.Version, false); err != nil {
		t.Fatal(err)
	}
	message.OccurrenceKey = occurrence.Key
	if err = (Audience{DB: db}).Submit(ctx, message); err != nil {
		t.Fatal("restored show remained closed", err)
	}
	timeline, err = shows.Resolve(ctx, "s", start.Add(time.Minute))
	if err != nil || timeline.Current.ShowID != "show" {
		t.Fatal(timeline, err)
	}
}
