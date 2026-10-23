package web

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/schedule"
)

func (s *Server) episodeView(r *http.Request) (schedulePageView, schedule.Occurrence, error) {
	if id := r.PathValue("episode"); id != "" {
		showID, _, err := schedule.ParseEpisodeID(id)
		if err != nil {
			return schedulePageView{}, schedule.Occurrence{}, err
		}
		r.SetPathValue("show", showID)
	}
	v, err := s.scheduleView(r)
	if err != nil {
		return v, schedule.Occurrence{}, err
	}
	if s.d.Episodes == nil {
		return v, schedule.Occurrence{}, schedule.ErrNotFound
	}
	return s.loadEpisode(r, v, v.Show.ID)
}
func (s *Server) loadEpisode(r *http.Request, v schedulePageView, showID string) (schedulePageView, schedule.Occurrence, error) {
	var err error
	v.Show, err = s.d.Schedule.Get(r.Context(), v.Station.ID, showID)
	if err != nil {
		return v, schedule.Occurrence{}, err
	}
	loc, err := time.LoadLocation(v.Station.Timezone)
	if err != nil {
		return v, schedule.Occurrence{}, err
	}
	o, err := s.resolveEpisodeOccurrence(r, v.Show, loc)
	if err != nil {
		return v, o, err
	}
	{
		v.Episode, err = s.d.Episodes.Store.Get(r.Context(), o.Key)
		if errors.Is(err, episodes.ErrNotFound) {
			v.Episode = episodes.New(o)
			err = nil
		}
		if err == nil && !v.Episode.Cancelled && v.Episode.ShowKey != o.Key {
			v.Episode.Status = "attention"
			v.Episode.Problem = "Show settings changed; the DJ will update this episode automatically."
		}
		if len(v.Episode.Aired) == 0 && v.Episode.End.After(time.Now()) {
			v.Episode.Start, v.Episode.End = o.Start, o.End
		}
		// Older saved failures use full-show planning terminology.
		v.Episode.Problem = strings.ReplaceAll(v.Episode.Problem, "The DJ returned an invalid planning response. No new running order was saved. Automatic preparation will retry.", "The DJ returned an unreadable track choice. No new DJ selection was accepted.")
		v.Episode.Problem = strings.ReplaceAll(v.Episode.Problem, "The running order needs revision:", "The DJ choice could not be used:")
		v.Episode.Problem = strings.TrimSpace(strings.TrimSuffix(v.Episode.Problem, "Automatic preparation will retry."))
		v.Episode.Start = v.Episode.Start.In(loc)
		v.Episode.End = v.Episode.End.In(loc)
		v.Episode.PlanStartsAt = v.Episode.PlanStartsAt.In(loc)
		v.Episode.RetryAt = v.Episode.RetryAt.In(loc)
		for i := range v.Episode.Aired {
			v.Episode.Aired[i].AiredAt = v.Episode.Aired[i].AiredAt.In(loc)
		}
		page, size, pageErr := pageRequest(r.URL.Query())
		if pageErr != nil {
			return v, o, schedule.ErrInvalid
		}
		v.EpisodePlayed = append([]episodes.Track(nil), v.Episode.Aired...)
		// Reverse receipts first so equal or unknown timestamps retain newest receipt first.
		for i, j := 0, len(v.EpisodePlayed)-1; i < j; i, j = i+1, j-1 {
			v.EpisodePlayed[i], v.EpisodePlayed[j] = v.EpisodePlayed[j], v.EpisodePlayed[i]
		}
		sort.SliceStable(v.EpisodePlayed, func(i, j int) bool { return v.EpisodePlayed[i].AiredAt.After(v.EpisodePlayed[j].AiredAt) })
		v.EpisodePagination = paging(episodePath(v.Station.ID, o.Key), r.URL.Query(), page, size, len(v.EpisodePlayed))
		v.EpisodePagination.CountLabel = "track"
		v.EpisodePlayed = v.EpisodePlayed[(v.EpisodePagination.Number-1)*size : min(v.EpisodePagination.Number*size, len(v.EpisodePlayed))]
		v.EpisodeQueue = v.Episode.Upcoming()
		if s.d.Broadcast != nil {
			v.EpisodeQueue, v.EpisodeQueueLive = episodeQueue(v.Episode, s.d.Broadcast.State(v.Station.ID))
		}
		scheduled := false
		for _, candidate := range schedule.Occurrences([]schedule.Show{v.Show}, o.Start, loc) {
			if schedule.EpisodeKey(candidate.Key) == schedule.EpisodeKey(o.Key) {
				scheduled = true
			}
		}
		v.EpisodeReadOnly = !scheduled || v.Show.Archived || !o.End.After(time.Now())
		v.EpisodeCanCancel = !v.EpisodeReadOnly && time.Now().Before(o.Start) && len(v.Episode.Aired) == 0
		v.Title = o.Title + " · " + o.Start.In(loc).Format("2 Jan 2006")
		v.EpisodeURL = episodePath(v.Station.ID, o.Key)
		v.EpisodeStart = o.Start.Format(time.RFC3339)
		v.Week = schedule.Monday(o.Start, loc).Format("2006-01-02")
		return v, o, err
	}
}
func (s *Server) episodePage(w http.ResponseWriter, r *http.Request) {
	v, _, err := s.episodeView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	if r.URL.Query().Get("notice") == "cancelled" {
		v.Notice = "Episode cancelled. The station will use its default playlist during this slot."
	}
	if r.URL.Query().Get("notice") == "restored" {
		v.Notice = "Episode restored. It will run automatically at its scheduled time."
	}
	if r.URL.Query().Get("notice") == "saved" {
		v.Notice = "Episode direction saved."
	}
	s.renderPage(w, r, 200, "episode-page", v)
}
func (s *Server) saveEpisode(w http.ResponseWriter, r *http.Request) {
	v, o, err := s.episodeView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	version, err := strconv.Atoi(r.PostForm.Get("version"))
	if err == nil {
		err = s.d.Episodes.Edit(r.Context(), o, version, r.PostForm.Get("notes"), nil, nil)
	}
	if err != nil {
		v.EpisodeEditing = true
		v.Episode.Notes = r.PostForm.Get("notes")

		v.EpisodeError = err.Error()
		s.renderPage(w, r, 422, "episode-page", v)
		return
	}
	s.redirect(w, r, episodePath(v.Station.ID, o.Key)+"?notice=saved")
}
func (s *Server) scheduleLive(w http.ResponseWriter, r *http.Request) {
	v, err := s.scheduleView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	s.renderHTML(w, 200, "schedule-list", v)
}

func (s *Server) episodeStatus(w http.ResponseWriter, r *http.Request) {
	v, _, err := s.episodeView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	version, err := strconv.Atoi(r.URL.Query().Get("version"))
	if err != nil || version < 0 {
		s.scheduleError(w, r, schedule.ErrInvalid)
		return
	}
	v.EpisodePlanChanged = v.Episode.Status == "ready" && version != v.Episode.Version
	s.renderHTML(w, 200, "episode-status", v)
}

func (s *Server) cancelEpisode(w http.ResponseWriter, r *http.Request) {
	v, o, err := s.episodeView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	if !v.EpisodeCanCancel {
		s.scheduleError(w, r, schedule.ErrInvalid)
		return
	}
	if r.Method == http.MethodGet {
		if r.Header.Get("HX-Target") == "dialog-body" {
			s.renderHTML(w, 200, "cancel-episode-confirm", v)
			return
		}
		v.Title = "Cancel episode · " + v.Show.Name
		s.renderPage(w, r, 200, "cancel-episode-page", v)
		return
	}
	version, err := strconv.Atoi(r.PostForm.Get("version"))
	if err == nil && r.PostForm.Get("confirm") != "yes" {
		err = schedule.ErrInvalid
	}
	restore := r.PostForm.Get("restore") == "yes"
	if err == nil {
		err = s.d.Episodes.Cancel(r.Context(), o, version, !restore)
	}
	if err != nil {
		v.EpisodeError = err.Error()
		s.renderPage(w, r, 422, "episode-page", v)
		return
	}
	if s.d.Broadcast != nil {
		s.d.Broadcast.Changed()
	}
	notice := "cancelled"
	if restore {
		notice = "restored"
	}
	s.redirect(w, r, episodePath(v.Station.ID, o.Key)+"?notice="+notice)
}

func (s *Server) episodeLive(w http.ResponseWriter, r *http.Request) {
	v, _, err := s.episodeView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	v.EpisodeEditing = !v.EpisodeReadOnly && r.URL.Query().Get("edit") == "1"
	s.renderHTML(w, 200, "episode-live", v)
}

func (s *Server) episodeDirection(w http.ResponseWriter, r *http.Request) {
	v, _, err := s.episodeView(r)
	if err != nil {
		s.scheduleError(w, r, err)
		return
	}
	if v.EpisodeReadOnly {
		s.redirect(w, r, v.EpisodeURL)
		return
	}
	v.EpisodeEditing = true
	s.renderPage(w, r, 200, "episode-page", v)
}

// The saved editorial choice is provisional; only runtime owns the broadcast queue.
func episodeQueue(e episodes.Episode, state broadcast.State) ([]episodes.RunningTrack, bool) {
	active := state.Status == "Broadcasting" || state.Status == "Starting" || state.Status == "Retrying"
	matches := func(key string) bool { return key != "" && episodes.SessionKey(key) == episodes.SessionKey(e.Key) }
	if !active || (!matches(state.Programme.Key) && !matches(state.NextProgrammeKey)) {
		return e.Upcoming(), false
	}
	if state.Next.ID == "" || !matches(state.NextProgrammeKey) {
		return nil, true
	}
	// Link duration and buffering are not a dependable wall-clock start estimate.
	next := episodes.RunningTrack{Track: episodes.Snapshot(state.Next), Committed: true}
	next.Approved = true
	for _, track := range e.Plan {
		if track.ID == next.ID {
			next.SelectionSource = track.SelectionSource
			break
		}
	}
	queue := []episodes.RunningTrack{next}
	for _, track := range e.Upcoming() {
		if track.Approved && track.ID != next.ID && track.ID != state.Current.ID {
			queue = append(queue, track)
		}
	}
	return queue, true
}

func episodePath(stationID, key string) string {
	return "/producer/stations/" + stationID + "/episodes/" + schedule.EpisodeID(key)
}

func (s *Server) resolveEpisodeOccurrence(r *http.Request, show schedule.Show, loc *time.Location) (schedule.Occurrence, error) {
	var key string
	var start time.Time
	var err error
	if id := r.PathValue("episode"); id != "" {
		_, key, err = schedule.ParseEpisodeID(id)
		if err != nil {
			return schedule.Occurrence{}, err
		}
		parts := strings.Split(key, ":")
		if len(parts) != 3 {
			return schedule.Occurrence{}, schedule.ErrNotFound
		}
		start, err = time.ParseInLocation("2006-01-02", parts[2], loc)
	} else {
		start, err = time.Parse(time.RFC3339, r.URL.Query().Get("start"))
	}
	if err != nil {
		return schedule.Occurrence{}, schedule.ErrInvalid
	}
	var saved episodes.Episode
	found := false
	if key != "" {
		saved, err = s.d.Episodes.Store.Get(r.Context(), key)
		found = err == nil
		if err != nil && !errors.Is(err, episodes.ErrNotFound) {
			return schedule.Occurrence{}, err
		}
	}
	for _, o := range schedule.Occurrences([]schedule.Show{show}, start, loc) {
		matches := key != "" && schedule.EpisodeKey(o.Key) == key || key == "" && o.Start.Equal(start)
		if !matches {
			continue
		}
		if !found {
			saved, err = s.d.Episodes.Store.Get(r.Context(), o.Key)
			found = err == nil
			if err != nil && !errors.Is(err, episodes.ErrNotFound) {
				return schedule.Occurrence{}, err
			}
		}
		// An aired/ended occurrence retains its actual timing when the weekly slot changes.
		if found && (len(saved.Aired) > 0 || saved.End.Before(time.Now())) {
			o.Start, o.End = saved.Start.In(loc), saved.End.In(loc)
		}
		return o, nil
	}
	if found {
		return savedOccurrence(show, saved), nil
	}
	return schedule.Occurrence{}, schedule.ErrNotFound
}
func savedOccurrence(show schedule.Show, e episodes.Episode) schedule.Occurrence {
	return schedule.Occurrence{Key: e.ShowKey, StationID: show.StationID, ShowID: show.ID, Title: show.Name, PlaylistID: show.PlaylistID, PlaylistName: show.PlaylistName, DJ: show.DJ, MusicDirection: show.MusicDirection, Start: e.Start, End: e.End, AcceptRequests: show.AcceptRequests, AcceptShoutouts: show.AcceptShoutouts}
}
