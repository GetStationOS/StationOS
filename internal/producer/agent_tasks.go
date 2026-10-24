package producer

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
)

type createPlaylistArgs struct {
	StationID   string   `json:"station_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Genres      []string `json:"genres"`
}

type createShowArgs struct {
	StationID       string `json:"station_id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	MusicDirection  string `json:"music_direction"`
	DJID            string `json:"dj_id"`
	PlaylistID      string `json:"playlist_id"`
	Days            []int  `json:"days"`
	StartTime       string `json:"start_time"`
	DurationMinutes int    `json:"duration_minutes"`
}

func showWindow(start string, duration int) (int, int, error) {
	parts := strings.Split(start, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, 0, fmt.Errorf("start_time must be HH:MM in 24-hour time, for example 20:00")
	}
	h, e := strconv.Atoi(parts[0])
	m, f := strconv.Atoi(parts[1])
	if e != nil || f != nil || h < 0 || h > 23 || m < 0 || m > 59 || m%15 != 0 || fmt.Sprintf("%02d:%02d", h, m) != start {
		return 0, 0, fmt.Errorf("start_time must be a quarter-hour HH:MM from 00:00 to 23:45")
	}
	if duration < 15 || duration > 1440 || duration%15 != 0 {
		return 0, 0, fmt.Errorf("duration_minutes must be a multiple of 15 between 15 and 1440; four hours is 240")
	}
	begin := h*60 + m
	end := (begin + duration) % 1440
	if end == 0 {
		end = 1440
	}
	return begin, end, nil
}

func registerAgentTasks(r *Registry, d Dependencies) {
	register(r, "create_playlist", "Create a NEW station playlist with its genre selections in one atomic action. Supply exact catalogue genres; genres are alternatives (Jazz OR Funk), and future matching imports join automatically. Returns playlist_id, saved genres and measured available recordings. An explicit request for a new pool does not require inspecting existing pools. Use replace_playlist_rules later for other selection categories.", true, func(ctx context.Context, a createPlaylistArgs) (Result, error) {
		r.playlistMu.Lock()
		defer r.playlistMu.Unlock()
		if len(a.Genres) == 0 || len(a.Genres) > 100 {
			return Result{}, fmt.Errorf("supply 1–100 genres from explore_music; the playlist name does not select recordings")
		}
		existing, err := d.Playlists.List(ctx, a.StationID)
		if err != nil {
			return Result{}, err
		}
		for _, p := range existing {
			if normalizedName(p.Name) == normalizedName(a.Name) {
				return Result{}, fmt.Errorf("playlist %q already exists with playlist_id %s; use get_playlist to inspect it or choose a distinct new name", p.Name, p.ID)
			}
		}
		selected := make([]rule, len(a.Genres))
		for i, g := range a.Genres {
			selected[i] = rule{Kind: "genre", Value: g}
		}
		canonical, normalizations := catalogueRuleValues(ctx, d.Catalogue, selected)
		rules := make([]playlists.Rule, len(canonical))
		genres := make([]string, len(canonical))
		for i, v := range canonical {
			rules[i] = v.domain()
			genres[i] = v.Value
		}
		p, err := (playlists.Service{Repo: d.Playlists, Stations: d.Stations}).CreateWithRules(ctx, playlists.Playlist{StationID: a.StationID, Name: a.Name, Description: a.Description}, rules)
		if err != nil {
			return Result{}, err
		}
		result, _ := r.savedPlaylistSelection(ctx, p.ID, "Created playlist: "+p.Name, "/producer/playlists/"+p.ID, normalizations...)
		data := result.Data.(map[string]any)
		data["station_id"] = p.StationID
		data["name"] = p.Name
		data["genres"] = genres
		return result, nil
	})
	register(r, "create_show", "Create ONE recurring show on the selected days in one action. Flat fields: station_id, name, dj_id, playlist_id, days (Monday=0 through Sunday=6), start_time (24-hour HH:MM) and duration_minutes. Daily means all seven days. Example: 20:00 plus 240 minutes ends at midnight. The server calculates the end and rejects overlaps without saving. Returns saved days, clock times, duration, DJ and playlist IDs; these facts can verify completion without another read. description is public copy; music_direction guides the DJ.", true, func(ctx context.Context, a createShowArgs) (Result, error) {
		start, end, err := showWindow(a.StartTime, a.DurationMinutes)
		if err != nil {
			return Result{}, err
		}
		if len(a.Days) == 0 {
			return Result{}, fmt.Errorf("supply days: Monday=0 through Sunday=6; daily is [0,1,2,3,4,5,6]")
		}
		v, err := (schedule.Service{Repo: d.Schedule}).Save(ctx, schedule.Show{StationID: a.StationID, Name: a.Name, Description: a.Description, MusicDirection: a.MusicDirection, DJID: a.DJID, PlaylistID: a.PlaylistID, Days: a.Days, Day: a.Days[0], Start: start, End: end})
		if err != nil {
			return Result{}, err
		}
		return saved(map[string]any{"show_id": v.ID, "station_id": v.StationID, "name": v.Name, "description": v.Description, "music_direction": v.MusicDirection, "dj_id": v.DJID, "playlist_id": v.PlaylistID, "days": v.Days, "start_time": fmt.Sprintf("%02d:%02d", v.Start/60, v.Start%60), "end_time": fmt.Sprintf("%02d:%02d", end/60, end%60), "duration_minutes": v.Duration()}, "Created show: "+v.Name, "/producer/stations/"+v.StationID+"/shows/"+v.ID)
	})
}
