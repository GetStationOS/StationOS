package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"stationos.local/stationos/internal/broadcast"
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
		card.NextTrack = stationTrack(st.ID, "Up next", "Not queued", card.Broadcast.Next)
		card.NeedsAttention = card.Broadcast.Error != "" || card.Broadcast.Status == "Retrying" || card.Broadcast.Status == "Unavailable" || card.Broadcast.PresenterStatus == "Unavailable" || card.Broadcast.CurationStatus == "Unavailable" || card.Operations.Active > 0 || (card.Operations.Available && !card.Operations.Healthy)
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
