package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

func TestActiveEpisodeTimingEdits(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end int
		wantEnd    int
	}{{"shorten past now", 360, 600, 600}, {"legacy frozen window", 360, 600, 600}, {"extend", 360, 1320, 1320}, {"start only", 480, 1200, 1200}, {"start and end", 480, 840, 840}, {"overnight extension", 360, 0, 1440}, {"DST overnight extension", 360, 180, 1620}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := Open(ctx, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			loc, _ := time.LoadLocation("Europe/London")
			now := time.Date(2026, 10, 24, 12, 0, 0, 0, loc)
			stations := Stations{DB: db}
			pools := Playlists{DB: db}
			shows := Schedule{DB: db, Now: func() time.Time { return now }}
			repo := Episodes{DB: db}
			if err = stations.Save(ctx, station.Station{ID: "s", Name: "Station", Timezone: "Europe/London", Locale: "en"}); err != nil {
				t.Fatal(err)
			}
			if err = pools.Save(ctx, playlists.Playlist{ID: "pool", StationID: "s", Name: "Pool"}); err != nil {
				t.Fatal(err)
			}
			show := schedule.Show{ID: "show", StationID: "s", Name: "80s", DJID: "default-pop", PlaylistID: "pool", Day: 5, Start: 360, End: 1200}
			if err = shows.Save(ctx, show); err != nil {
				t.Fatal(err)
			}
			show, err = shows.Get(ctx, "s", "show")
			if err != nil {
				t.Fatal(err)
			}
			var active, past, future schedule.Occurrence
			for _, o := range schedule.Occurrences([]schedule.Show{show}, now, loc) {
				if o.Start.Equal(time.Date(2026, 10, 24, 6, 0, 0, 0, loc)) {
					active = o
				}
				if o.Start.Equal(time.Date(2026, 10, 17, 6, 0, 0, 0, loc)) {
					past = o
				}
				if o.Start.Equal(time.Date(2026, 10, 31, 6, 0, 0, 0, loc)) {
					future = o
				}
			}
			if active.Key == "" || past.Key == "" || future.Key == "" {
				t.Fatal("missing occurrence")
			}
			for _, o := range []schedule.Occurrence{past, active, future} {
				err = repo.Update(ctx, o, func(e *episodes.Episode) error {
					e.Status = "ready"
					e.Notes = "Keep notes"
					e.Excluded = []string{"excluded"}
					e.Plan = []episodes.Track{{ID: "next", Seconds: 180, Approved: true}}
					e.AcceptedNextID = "next"
					if !o.Start.After(now) {
						e.Aired = []episodes.Track{{ID: "aired", AiredAt: o.Start, Seconds: 180}}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			originalPast, _ := repo.Get(ctx, past.Key)
			if tc.name == "legacy frozen window" {
				// Simulate an end edit saved by the older code, which left the session frozen.
				if _, err = db.ExecContext(ctx, `UPDATE station_shows SET end_minute=600,revision=revision+1 WHERE id='show'`); err != nil {
					t.Fatal(err)
				}
				show, err = shows.Get(ctx, "s", "show")
				if err != nil {
					t.Fatal(err)
				}
			}
			show.Start = tc.start
			show.End = tc.end
			if tc.name == "extend" {
				invalid := show
				invalid.ID = "invalid"
				invalid.DJID = "missing"
				if err = shows.SaveMany(ctx, []schedule.Show{show, invalid}); err == nil {
					t.Fatal("invalid transaction committed")
				}
				rolledBack, _ := repo.Get(ctx, active.Key)
				if !rolledBack.End.Equal(active.End) || rolledBack.Version != 1 {
					t.Fatal("partial episode edit committed", rolledBack)
				}
			}
			if err = shows.Save(ctx, show); err != nil {
				t.Fatal(err)
			}
			updated, err := repo.Get(ctx, active.Key)
			if err != nil {
				t.Fatal(err)
			}
			wantEnd := schedule.WallTime(active.Start, tc.wantEnd, true)
			if !updated.Start.Equal(active.Start) || !updated.End.Equal(wantEnd) || len(updated.Aired) != 1 || updated.Aired[0].ID != "aired" || updated.Notes != "Keep notes" || len(updated.Excluded) != 1 {
				t.Fatal("lost session or wrong timing", updated, wantEnd)
			}
			if tc.end != 1200 && (updated.TimingChangedAt.IsZero() || updated.Version != 2) {
				t.Fatal("timing change not recorded", updated)
			}
			if tc.wantEnd == 600 && (len(updated.Plan) != 0 || updated.AcceptedNextID != "" || updated.Label() != "Finished") {
				t.Fatal("obsolete queue retained", updated)
			}
			savedPast, _ := repo.Get(ctx, past.Key)
			if !savedPast.Start.Equal(originalPast.Start) || !savedPast.End.Equal(originalPast.End) || savedPast.Version != originalPast.Version {
				t.Fatal("completed history changed", savedPast)
			}
			revised, err := shows.Get(ctx, "s", "show")
			if err != nil {
				t.Fatal(err)
			}
			var current, nextWeek schedule.Occurrence
			for _, o := range schedule.Occurrences([]schedule.Show{revised}, now, loc) {
				if schedule.EpisodeKey(o.Key) == schedule.EpisodeKey(active.Key) {
					current = o
				}
				if schedule.EpisodeKey(o.Key) == schedule.EpisodeKey(future.Key) {
					nextWeek = o
				}
			}
			if !current.Start.Equal(active.Start) || !current.End.Equal(wantEnd) || nextWeek.Start.In(loc).Hour() != tc.start/60 {
				t.Fatal("wrong occurrence projection", current, nextWeek)
			}
			if err = repo.Update(ctx, active, func(*episodes.Episode) error { return nil }); !errors.Is(err, episodes.ErrStale) {
				t.Fatal("stale worker saved", err)
			}
			if tc.wantEnd > 720 {
				if err = repo.Update(ctx, current, func(*episodes.Episode) error { return nil }); err != nil {
					t.Fatal("revised worker could not continue", err)
				}
			}
			if tc.wantEnd == 600 {
				next := schedule.Show{ID: "jazz", StationID: "s", Name: "Jazz", DJID: "default-pop", PlaylistID: "pool", Day: 5, Start: 600, End: 960}
				if err = shows.Save(ctx, next); err != nil {
					t.Fatal(err)
				}
				// A later edit must not resurrect the episode that has now ended.
				latest, _ := shows.Get(ctx, "s", "show")
				latest.Start = 960
				latest.End = 1320
				if err = shows.Save(ctx, latest); err != nil {
					t.Fatal(err)
				}
				ended, _ := repo.Get(ctx, active.Key)
				if !ended.End.Equal(wantEnd) || ended.Version != updated.Version {
					t.Fatal("completed episode resurrected", ended)
				}
				timeline, err := shows.Resolve(ctx, "s", now)
				if err != nil || timeline.Current.ShowID != "jazz" {
					t.Fatal("shortened show still dominates", timeline, err)
				}
			}
		})
	}
}
