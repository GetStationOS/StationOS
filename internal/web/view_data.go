package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

// Catalogue choices belong only to track browsing and playlist rule editing.
func (s *Server) loadCatalogueChoices(ctx context.Context, v *catalogueChoices, stationIDs ...string) error {
	var err error
	v.Facets, err = s.d.Catalogue.Facets(ctx, stationIDs...)
	if err != nil {
		return err
	}
	for _, album := range v.Facets.Albums {
		token, _ := json.Marshal(albumChoice{SourceID: album.SourceID, Name: album.Name})
		v.Albums = append(v.Albums, albumOption{Value: string(token), Name: album.Name, Source: album.SourceName, Tracks: album.Tracks})
	}
	return nil
}

// A station workspace needs only its own pools. Global lists also need station names.
func (s *Server) loadPlaylistOptions(ctx context.Context, v *playlistChoices, current station.Station) error {
	stationID := current.ID
	if stationID == "" {
		var err error
		v.Stations, err = s.d.Stations.List(ctx)
		if err != nil {
			return err
		}
	}
	if s.d.Playlists == nil {
		return nil
	}
	items, err := s.d.Playlists.List(ctx, stationID)
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, st := range v.Stations {
		names[st.ID] = st.Name
	}
	if current.ID != "" {
		names[current.ID] = current.Name
	}
	for _, p := range items {
		name := names[p.StationID]
		if name == "" {
			name = "Unknown station"
		}
		v.PlaylistOptions = append(v.PlaylistOptions, playlistOption{Playlist: p, Station: name})
	}
	v.StationPlaylistCount = len(items)
	return nil
}

func (s *Server) markDefaultPlaylists(ctx context.Context, v *playlistChoices, current station.Station) error {
	if s.d.BroadcastConfig == nil {
		return nil
	}
	stations := v.Stations
	if current.ID != "" {
		stations = []station.Station{current}
	}
	for _, st := range stations {
		id, err := s.d.BroadcastConfig.Assignment(ctx, st.ID)
		if err != nil {
			return err
		}
		for i := range v.PlaylistOptions {
			if v.PlaylistOptions[i].Playlist.ID == id {
				v.PlaylistOptions[i].Default = true
			}
		}
	}
	return nil
}

// The overview and its live updates share the same measured card projection.
func (s *Server) loadStationCards(r *http.Request, v *stationDirectoryView) error {
	if err := s.loadPlaylistOptions(r.Context(), &v.playlistChoices, station.Station{}); err != nil {
		return err
	}
	var err error
	for _, st := range v.Stations {
		card := stationCard{Station: st, Operations: s.operationalSummary(r, st.ID)}
		card.Broadcast = broadcast.State{Status: "Dormant"}
		if s.d.Broadcast != nil {
			card.Broadcast = s.d.Broadcast.State(st.ID)
		}
		for _, p := range v.PlaylistOptions {
			if p.Playlist.StationID == st.ID {
				card.Playlists++
			}
		}
		if s.d.BroadcastConfig != nil {
			card.PlaylistID, err = s.d.BroadcastConfig.Assignment(r.Context(), st.ID)
			if err != nil {
				return err
			}
		}
		if card.Broadcast.Current.ID != "" && card.Broadcast.Programme.PlaylistID != "" {
			card.PlaylistID = card.Broadcast.Programme.PlaylistID
		}
		for _, p := range v.PlaylistOptions {
			if p.Playlist.ID == card.PlaylistID {
				card.PlaylistName = p.Playlist.Name
				break
			}
		}
		if card.PlaylistName == "" && card.Broadcast.Current.ID != "" {
			card.PlaylistName = card.Broadcast.Programme.PlaylistName
		}
		card.ProgrammeTime, _ = programmeDetails(card.Broadcast, st.Timezone, time.Now())
		card.DJIndicators = djIndicators(card.Broadcast)
		if !card.Broadcast.Following.Start.IsZero() {
			loc, e := time.LoadLocation(st.Timezone)
			if e != nil {
				loc = time.UTC
			}
			card.FollowingTime = card.Broadcast.Following.Start.In(loc).Format("Mon 15:04")
		}
		if card.Broadcast.Following.ShowID != "" {
			card.FollowingTracks, err = s.followingTracks(r.Context(), card.Broadcast.Following)
			card.FollowingTracksUnavailable = err != nil
			card.FollowingArtwork = s.followingArtwork(r.Context(), st.ID, card.FollowingTracks)
		}
		card.NextTrack = stationTrack(st.ID, "Up next", "Not queued", card.Broadcast.Next)
		card.HealthTone, card.HealthLabel = stationHealth(card.Broadcast, card.Operations)
		card.NeedsAttention = card.HealthTone == "warning"
		v.Dashboard.Listeners += card.Broadcast.Listeners
		if card.Broadcast.Status == "Broadcasting" {
			v.Dashboard.Broadcasting++
		}
		if card.NeedsAttention {
			v.Dashboard.Attention++
		}
		v.StationCards = append(v.StationCards, card)
	}
	return nil
}

func stationSources(all []sources.Config, st station.Station) []sources.Config {
	if st.ID == "" {
		return all
	}
	selected := map[string]bool{}
	for _, id := range st.SourceIDs {
		selected[id] = true
	}
	out := []sources.Config{}
	for _, source := range all {
		if selected[source.ID] {
			out = append(out, source)
		}
	}
	return out
}

// Read the dated episode checkpoint only; viewing the dashboard never plans music.
func (s *Server) followingTracks(ctx context.Context, next schedule.Occurrence) ([]episodes.Track, error) {
	if s.d.Episodes == nil || s.d.Episodes.Store == nil {
		return nil, errors.New("episode choices unavailable")
	}
	e, err := s.d.Episodes.Store.Get(ctx, next.Key)
	if errors.Is(err, episodes.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if e.Cancelled {
		return nil, nil
	}
	var tracks []episodes.Track
	seen := map[string]bool{}
	for _, track := range e.Upcoming() {
		if !track.Approved || track.ID == "" || seen[track.ID] {
			continue
		}
		tracks = append(tracks, track.Track)
		seen[track.ID] = true
		if len(tracks) == 2 {
			break
		}
	}
	return tracks, nil
}

// Live capability flags only describe an active producer, never an old show.
func stationHealth(state broadcast.State, summary operationSummary) (string, string) {
	var reasons []string
	if state.Status == "Retrying" || state.Status == "Unavailable" {
		reasons = append(reasons, "Output "+strings.ToLower(state.Status))
	}
	if state.Error != "" && state.Status != "Dormant" {
		reasons = append(reasons, state.Error)
	}
	if state.Current.ID != "" && state.Status != "Dormant" {
		if state.PresenterStatus == "Unavailable" {
			reasons = append(reasons, "DJ speech unavailable; music continues")
		}
		if state.CurationStatus == "Unavailable" {
			reasons = append(reasons, "DJ music selection unavailable")
		}
	}
	if summary.Available && !summary.Healthy {
		reasons = append(reasons, "Transmission history unavailable")
	}
	if len(reasons) > 0 {
		return "warning", strings.Join(reasons, " · ")
	}
	if !summary.Available {
		return "unknown", "Health monitoring unavailable"
	}
	if state.Status == "Dormant" {
		return "neutral", "Dormant · no current runtime faults"
	}
	if state.Status == "Starting" {
		return "neutral", "Starting · no current runtime faults"
	}
	if state.Status != "Broadcasting" {
		return "unknown", "Output health unknown"
	}
	return "healthy", "Broadcasting · no current runtime faults"
}

func (s *Server) followingArtwork(ctx context.Context, stationID string, tracks []episodes.Track) []cardTrack {
	var out []cardTrack
	for _, track := range tracks {
		item := content.AudioItem{ID: track.ID, Title: track.Title, Artist: track.Artist, Album: track.Album}
		if s.d.Catalogue != nil {
			result, lookupErr := s.d.Catalogue.Search(ctx, catalogue.Query{ItemID: track.ID, Limit: 1})
			if lookupErr == nil && len(result.Items) > 0 {
				item = result.Items[0]
			}
		}
		view := stationTrack(stationID, "", "", item)
		if item.Artwork != "" && item.SourceID != "" {
			view.Artwork = sourceCoverURL(item.SourceID, item)
		}
		out = append(out, view)
	}
	return out
}
