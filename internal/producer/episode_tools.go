package producer

import (
	"context"
	"errors"
	"net/url"
	"time"

	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/schedule"
)

type episodeID struct {
	StationID string `json:"station_id"`
	ShowID    string `json:"show_id"`
	Start     string `json:"start"`
}
type episodeRead struct {
	StationID string `json:"station_id"`
	ShowID    string `json:"show_id"`
	Start     string `json:"start"`
	Offset    int    `json:"offset"`
	Limit     int    `json:"limit"`
}
type episodePatch struct {
	StationID string    `json:"station_id"`
	ShowID    string    `json:"show_id"`
	Start     string    `json:"start"`
	Version   int       `json:"version"`
	Notes     *string   `json:"notes"`
	Locked    *[]string `json:"locked_track_ids"`
	Excluded  []string  `json:"exclude_track_ids"`
}
type episodeSummary struct {
	ShowID  string    `json:"show_id"`
	Name    string    `json:"name"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Status  string    `json:"status"`
	Theme   string    `json:"theme"`
	Version int       `json:"version"`
}

func episodeURL(o schedule.Occurrence) string {
	return "/producer/stations/" + url.PathEscape(o.StationID) + "/episodes/" + schedule.EpisodeID(o.Key)
}

func resolveEpisode(ctx context.Context, d Dependencies, id episodeID) (schedule.Occurrence, error) {
	st, err := d.Stations.Get(ctx, id.StationID)
	if err != nil {
		return schedule.Occurrence{}, err
	}
	show, err := d.Schedule.Get(ctx, st.ID, id.ShowID)
	if err != nil {
		return schedule.Occurrence{}, err
	}
	start, err := time.Parse(time.RFC3339, id.Start)
	if err != nil {
		return schedule.Occurrence{}, errors.New("use the exact RFC3339 start returned by list_episodes, including its timezone offset")
	}
	loc, err := time.LoadLocation(st.Timezone)
	if err != nil {
		return schedule.Occurrence{}, err
	}
	for _, o := range schedule.Occurrences([]schedule.Show{show}, start, loc) {
		if o.Start.Equal(start) {
			return o, nil
		}
	}
	return schedule.Occurrence{}, schedule.ErrNotFound
}
func readEpisode(ctx context.Context, d Dependencies, o schedule.Occurrence) (episodes.Episode, error) {
	e, err := d.Episodes.Store.Get(ctx, o.Key)
	if errors.Is(err, episodes.ErrNotFound) {
		return episodes.New(o), nil
	}
	if err == nil && e.ShowKey != o.Key {
		e.Status = "attention"
		e.Problem = "Show settings changed; automatic replanning is pending."
	}
	return e, err
}
func registerEpisodeTools(r *Registry, d Dependencies) {
	register(r, "get_show", "Read one complete recurring show, including its full music_direction DJ agent prompt, public description, presenter assignment and audience settings. Read before editing. DJ credentials/profile are omitted; use get_dj separately.", false, func(ctx context.Context, a struct {
		StationID string `json:"station_id"`
		ID        string `json:"id"`
	}) (Result, error) {
		show, err := d.Schedule.Get(ctx, a.StationID, a.ID)
		show.DJ = djs.Profile{}
		return read(show, err)
	})
	if d.Episodes == nil {
		return
	}
	register(r, "list_episodes", "Read dated episode readiness for one station-local week (week=YYYY-MM-DD; defaults to this week). Paginated, optional show_id filter. Returns exact starts for get_episode/save_episode. Read-only: never starts planning, AI calls or playback. Ready means automatic validation, not human approval.", false, func(ctx context.Context, a struct {
		StationID string `json:"station_id"`
		Week      string `json:"week"`
		ShowID    string `json:"show_id"`
		Offset    int    `json:"offset"`
		Limit     int    `json:"limit"`
	}) (Result, error) {
		offset, limit, err := bounds(a.Offset, a.Limit)
		if err != nil {
			return Result{}, err
		}
		limit = min(limit, 25)
		st, err := d.Stations.Get(ctx, a.StationID)
		if err != nil {
			return Result{}, err
		}
		loc, err := time.LoadLocation(st.Timezone)
		if err != nil {
			return Result{}, err
		}
		at := time.Now()
		if a.Week != "" {
			at, err = time.ParseInLocation("2006-01-02", a.Week, loc)
			if err != nil {
				return Result{}, errors.New("week must be YYYY-MM-DD in the station timezone")
			}
		}
		week := schedule.Monday(at, loc)
		shows, err := d.Schedule.List(ctx, st.ID)
		if err != nil {
			return Result{}, err
		}
		occurrences := []schedule.Occurrence{}
		for _, o := range schedule.Occurrences(shows, week, loc) {
			if !o.Start.Before(week) && o.Start.Before(week.AddDate(0, 0, 7)) && (a.ShowID == "" || a.ShowID == o.ShowID) {
				occurrences = append(occurrences, o)
			}
		}
		out := []episodeSummary{}
		for _, o := range occurrences[min(offset, len(occurrences)):min(offset+limit, len(occurrences))] {
			e, err := readEpisode(ctx, d, o)
			if err != nil {
				return Result{}, err
			}
			out = append(out, episodeSummary{o.ShowID, o.Title, o.Start, o.End, e.Label(), e.Theme, e.Version})
		}
		return read(map[string]any{"items": out, "total": len(occurrences), "offset": offset, "limit": limit, "week": week.Format("2006-01-02"), "timezone": st.Timezone}, nil)
	})
	register(r, "get_episode", "Inspect one dated episode's plan, theme, producer notes, validation problem and optimistic version. Use the exact start from list_episodes. Plan is paginated; actual aired receipts are separate. This does not generate a plan or start broadcasting.", false, func(ctx context.Context, a episodeRead) (Result, error) {
		offset, limit, err := bounds(a.Offset, a.Limit)
		if err != nil {
			return Result{}, err
		}
		limit = min(limit, 25)
		o, err := resolveEpisode(ctx, d, episodeID{a.StationID, a.ShowID, a.Start})
		if err != nil {
			return Result{}, err
		}
		e, err := readEpisode(ctx, d, o)
		if err != nil {
			return Result{}, err
		}
		return Result{Data: map[string]any{"show_id": o.ShowID, "name": o.Title, "start": o.Start, "end": o.End, "status": e.Label(), "theme": e.Theme, "notes": e.Notes, "repetition_reason": e.RepetitionReason, "problem": e.Problem, "version": e.Version, "tracks": e.Plan[min(offset, len(e.Plan)):min(offset+limit, len(e.Plan))], "plan_total": len(e.Plan), "offset": offset, "limit": limit, "aired_count": len(e.Aired), "recently_aired": e.Aired[max(0, len(e.Aired)-5):]}, URL: episodeURL(o)}, nil
	})
	register(r, "save_episode", "Save optional producer intervention for ONE dated episode. Read get_episode first and pass its version. notes is an episode DJ music prompt; omit to preserve, empty clears. locked_track_ids replaces the kept track set (omit preserves; [] clears), using IDs from the existing plan. exclude_track_ids adds episode-only exclusions. Automatic replanning follows; this neither approves a show nor starts broadcasting. Use save_show.music_direction for recurring direction.", true, func(ctx context.Context, a episodePatch) (Result, error) {
		o, err := resolveEpisode(ctx, d, episodeID{a.StationID, a.ShowID, a.Start})
		if err != nil {
			return Result{}, err
		}
		e, err := readEpisode(ctx, d, o)
		if err != nil {
			return Result{}, err
		}
		notes := e.Notes
		if a.Notes != nil {
			notes = *a.Notes
		}
		locked := []string{}
		if a.Locked != nil {
			locked = *a.Locked
		} else {
			for _, t := range e.Plan {
				if t.Locked {
					locked = append(locked, t.ID)
				}
			}
		}
		if err = d.Episodes.Edit(ctx, o, a.Version, notes, locked, a.Excluded); err != nil {
			return Result{}, err
		}
		return saved(nil, "Episode producer changes saved; automatic replanning is pending.", episodeURL(o))
	})
}
