package web

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/station"
)

type operationSummary struct {
	Available, Healthy bool
	Active             int
}

func (s *Server) operationalSummary(r *http.Request, id string) operationSummary {
	v := operationSummary{Available: s.d.Operations != nil, Healthy: true}
	if !v.Available {
		return v
	}
	if s.d.Recording != nil {
		v.Healthy = s.d.Recording.Healthy()
	}
	incidents, err := s.d.Operations.Incidents(r.Context(), id, true)
	if err != nil {
		v.Healthy = false
		return v
	}
	v.Active = len(incidents)
	return v
}

type operationalEvent struct {
	operations.Event
	StartLabel, EndLabel, Duration, TypeLabel, OutcomeLabel, Notes string
	StartDate, StartTime, EndTime                                  string
	OutcomeStyle, OutcomeIcon                                      string
}
type operationalIncident struct {
	operations.Incident
	FirstLabel, LatestLabel, RecoveredLabel         string
	LatestDate, LatestTime, StatusLabel, Icon, Tone string
}
type operationChoice struct{ ID, Name string }
type operationsView struct {
	Pagination             pagination
	FilterCount            int
	ShowChoices, DJChoices []operationChoice
	pageView
	Broadcast                                       broadcast.State
	Summary                                         operationSummary
	Events                                          []operationalEvent
	Incidents                                       []operationalIncident
	Query, Kind, Outcome, From, Until, ShowID, DJID string
	ExportURL, Version, RefreshURL, VersionURL      string
}

func (v operationsView) player() playerView {
	return playerView{Station: v.Station, Broadcast: v.Broadcast}
}
func operationalTime(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "Unknown"
	}
	return t.In(loc).Format("02 Jan 2006 15:04:05 MST -07:00")
}
func operationFilter(r *http.Request, loc *time.Location) (operations.Filter, error) {
	q := r.URL.Query()
	f := operations.Filter{StationID: r.PathValue("id"), Query: q.Get("q"), Kind: q.Get("kind"), Outcome: q.Get("outcome"), ShowID: q.Get("show"), DJID: q.Get("dj"), Before: q.Get("before"), Anchor: q.Get("anchor"), Limit: 51}
	if strings.HasPrefix(f.ShowID, "name:") {
		f.ShowName = strings.TrimPrefix(f.ShowID, "name:")
		f.ShowID = ""
	}
	if len(f.Query) > 200 || len(f.Before) > 64 || len(f.Anchor) > 64 || len(f.ShowID) > 128 || len(f.ShowName) > 200 || len(f.DJID) > 128 {
		return f, errors.New("Filter is too long")
	}
	var err error
	if q.Get("from") != "" {
		f.From, err = time.ParseInLocation("2006-01-02", q.Get("from"), loc)
		if err != nil {
			return f, errors.New("Choose a valid start date")
		}
	}
	if q.Get("until") != "" {
		f.Until, err = time.ParseInLocation("2006-01-02", q.Get("until"), loc)
		if err != nil {
			return f, errors.New("Choose a valid end date")
		}
		f.Until = f.Until.AddDate(0, 0, 1)
	}
	if !f.From.IsZero() && !f.Until.IsZero() && !f.From.Before(f.Until) {
		return f, errors.New("End date must not precede start date")
	}
	return f, nil
}
func (s *Server) operationsPage(w http.ResponseWriter, r *http.Request) {
	st, err := s.d.Stations.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if s.d.Operations == nil {
		http.Error(w, "Transmission history is not configured", 503)
		return
	}
	loc, err := time.LoadLocation(st.Timezone)
	if err != nil {
		loc = time.UTC
	}
	f, err := operationFilter(r, loc)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	v := operationsView{pageView: s.baseView(r), Summary: s.operationalSummary(r, st.ID)}
	v.Station = st
	v.StationID = st.ID
	v.Section = "timeline"
	if strings.HasSuffix(r.URL.Path, "/incidents") {
		v.Section = "incidents"
	}
	v.Title = map[string]string{"timeline": "Broadcast history", "incidents": "Incidents"}[v.Section]
	if s.d.Broadcast != nil {
		v.Broadcast = s.d.Broadcast.State(st.ID)
	} else {
		v.Broadcast.Status = "Dormant"
	}
	v.Query = f.Query
	v.Kind = f.Kind
	v.Outcome = f.Outcome
	v.From = r.URL.Query().Get("from")
	v.Until = r.URL.Query().Get("until")
	v.ShowID = f.ShowID
	if f.ShowName != "" {
		v.ShowID = "name:" + f.ShowName
	}
	v.DJID = f.DJID
	for _, value := range []string{v.From, v.Until, v.Kind, v.Outcome, v.ShowID, v.DJID} {
		if value != "" {
			v.FilterCount++
		}
	}
	v.RefreshURL = r.URL.RequestURI()
	if strings.HasSuffix(r.URL.Path, "/history.csv") {
		s.exportOperations(w, r, f, loc)
		return
	}
	page, size, err := pageRequest(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	history, err := s.d.Operations.History(r.Context(), f, page, size)
	if err != nil {
		s.fail(w, err)
		return
	}
	events := history.Events
	pageValues := r.URL.Query()
	if f.Anchor == "" && len(events) > 0 {
		pageValues.Set("anchor", history.Anchor)
	}
	v.Pagination = paging("/producer/stations/"+url.PathEscape(st.ID)+"/timeline", pageValues, history.Number, size, history.Total)
	v.Pagination.SizeLabel = "Events per page"
	v.Pagination.RefreshURL = v.RefreshURL
	v.Pagination.RefreshTarget = "#history-results"
	v.Pagination.RefreshLabel = "Broadcast history"
	for _, e := range s.historyDisplayEvents(r, st.ID, events) {
		v.Events = append(v.Events, producerEvent(e, loc))
	}

	q := r.URL.Query()
	q.Del("before")
	q.Del("anchor")
	q.Del("page")
	q.Del("per_page")
	v.ExportURL = "/producer/stations/" + url.PathEscape(st.ID) + "/history.csv?" + q.Encode()
	v.Pagination.ExportURL = v.ExportURL
	incidents, err := s.d.Operations.Incidents(r.Context(), st.ID, false)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, i := range incidents {
		row := operationalIncident{Incident: i, FirstLabel: operationalTime(i.First, loc), LatestLabel: operationalTime(i.Latest, loc), RecoveredLabel: operationalTime(i.Recovered, loc), LatestDate: i.Latest.In(loc).Format("02 Jan 2006"), LatestTime: i.Latest.In(loc).Format("15:04:05"), StatusLabel: "Active", Icon: "warning", Tone: "warning"}
		if i.Severity == "critical" || i.Severity == "error" {
			row.Icon, row.Tone = "error", "error"
		}
		if !i.Recovered.IsZero() {
			row.StatusLabel, row.Icon, row.Tone = "Recovered", "recovery", "recovered"
		}
		v.Incidents = append(v.Incidents, row)
	}
	shows, djs := map[string]bool{}, map[string]string{}
	for _, e := range events {
		if e.Show != "" {
			shows[e.Show] = true
		}
		if e.DJID != "" {
			djs[e.DJID] = e.DJ
		}
	}
	if s.d.Schedule != nil {
		rows, err := s.d.Schedule.List(r.Context(), st.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, show := range rows {
			shows[show.Name] = true
		}
	}

	if s.d.DJs != nil {
		rows, err := s.d.DJs.List(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, dj := range rows {
			djs[dj.ID] = dj.Name
		}
	}
	if f.ShowName != "" {
		shows[f.ShowName] = true
	}
	if f.ShowID != "" {
		v.ShowChoices = append(v.ShowChoices, operationChoice{f.ShowID, "Selected show edition"})
	}
	if f.DJID != "" && djs[f.DJID] == "" {
		djs[f.DJID] = "Historical DJ"
	}
	for name := range shows {
		v.ShowChoices = append(v.ShowChoices, operationChoice{"name:" + name, name})
	}
	for id, name := range djs {
		v.DJChoices = append(v.DJChoices, operationChoice{id, name})
	}
	sort.Slice(v.ShowChoices, func(i, j int) bool { return v.ShowChoices[i].Name < v.ShowChoices[j].Name })
	sort.Slice(v.DJChoices, func(i, j int) bool { return v.DJChoices[i].Name < v.DJChoices[j].Name })
	q = r.URL.Query()
	q.Del("section")
	for key, values := range q {
		if len(values) == 1 && values[0] == "" {
			q.Del(key)
		}
	}
	if v.Section == "incidents" {
		q.Set("section", "incidents")
		v.Version = incidentFingerprint(incidents)
		v.Pagination = paging(r.URL.Path, r.URL.Query(), page, size, len(v.Incidents))
		v.Pagination.SizeLabel = "Incidents per page"
		v.Pagination.RefreshURL, v.Pagination.RefreshTarget, v.Pagination.RefreshLabel = v.RefreshURL, "#incident-results", "Incidents"
		start := (v.Pagination.Number - 1) * size
		v.Incidents = v.Incidents[start:min(start+size, len(v.Incidents))]
	} else {
		v.Version = historyPageFingerprint(history)
	}
	v.VersionURL = "/producer/stations/" + url.PathEscape(st.ID) + "/operations/version?" + q.Encode()

	if v.Section == "incidents" && fragmentTarget(r, "incident-results") {
		s.renderHTML(w, http.StatusOK, "incident-results", v)
		return
	}
	if v.Section == "timeline" && fragmentTarget(r, "history-results") {
		s.renderHTML(w, http.StatusOK, "history-results", v)
		return
	}
	s.renderPage(w, r, 200, "operations-page", v)
}

// Byte checkpoints are technical progress, not producer-visible history changes.
// Use the same filtered, paginated records as the rendered table.
func historyPageFingerprint(page operations.HistoryPage) string {
	return fmt.Sprintf("%s:%d", historyFingerprint(page.Events), page.Total)
}
func historyFingerprint(events []operations.Event) string {
	snapshot := make([]operations.Event, len(events))
	copy(snapshot, events)
	for i := range snapshot {
		if snapshot[i].Outcome == "transmitting" {
			snapshot[i].AudioBytes = 0
		}
	}
	raw, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func incidentFingerprint(incidents []operations.Incident) string {
	raw, _ := json.Marshal(incidents)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *Server) operationsVersion(w http.ResponseWriter, r *http.Request) {
	if s.d.Operations == nil {
		http.NotFound(w, r)
		return
	}
	st, err := s.d.Stations.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, station.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	var version string
	if r.URL.Query().Get("section") == "incidents" {
		incidents, err := s.d.Operations.Incidents(r.Context(), st.ID, false)
		if err != nil {
			s.fail(w, err)
			return
		}
		version = incidentFingerprint(incidents)
	} else {
		loc, err := time.LoadLocation(st.Timezone)
		if err != nil {
			loc = time.UTC
		}
		f, err := operationFilter(r, loc)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		page, size, err := pageRequest(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		history, err := s.d.Operations.History(r.Context(), f, page, size)
		if err != nil {
			s.fail(w, err)
			return
		}
		version = historyPageFingerprint(history)
	}
	w.Header().Set("Cache-Control", "no-store")
	s.json(w, 200, map[string]string{"version": version})
}

// Prefer the immutable name captured at transmission. Older records can show
// an explicitly labelled current playlist name without rewriting their snapshot.
func (s *Server) historyDisplayEvents(r *http.Request, stationID string, events []operations.Event) []operations.Event {
	if s.d.Playlists == nil {
		return events
	}
	needsNames := false
	for _, e := range events {
		if e.Playlist == "" && e.PlaylistID != "" {
			needsNames = true
			break
		}
	}
	if !needsNames {
		return events
	}
	pools, err := s.d.Playlists.List(r.Context(), stationID)
	if err != nil {
		s.d.Logger.Warn("Historical playlist names unavailable", "station_id", stationID, "error", err)
		return events
	}
	names := map[string]string{}
	for _, p := range pools {
		names[p.ID] = p.Name
	}
	display := append([]operations.Event(nil), events...)
	for i := range display {
		if display[i].Playlist == "" && names[display[i].PlaylistID] != "" {
			display[i].Playlist = names[display[i].PlaylistID] + " (current name)"
		}
	}
	return display
}
func operationTypeLabel(kind string) string {
	label := map[string]string{"music": "Music", "speech": "DJ speech", "started": "Station started", "stopped": "Station stopped", "handover": "Show handover", "ai": "DJ preparation", "failure": "Playback problem", "recovery": "Recovered", "stall": "Audio delayed", "listener": "Listener disconnected", "intervention": "Producer action", "unavailable": "Station unavailable"}[kind]
	if label == "" {
		label = "Station activity"
	}
	return label
}
func operationOutcomeLabel(outcome string) string {
	label := map[string]string{"transmitting": "In progress", "completed": "Completed", "interrupted": "Interrupted", "failed": "Failed", "unknown": "Not confirmed", "observed": "Station activity"}[outcome]
	if label == "" {
		label = "Not recorded"
	}
	return label
}
func producerEvent(e operations.Event, loc *time.Location) operationalEvent {
	v := operationalEvent{Event: e, StartLabel: operationalTime(e.Started, loc), EndLabel: operationalTime(e.Ended, loc), Duration: "—"}
	v.StartDate = e.Started.In(loc).Format("02 Jan 2006")
	v.StartTime = e.Started.In(loc).Format("15:04:05")
	if !e.Ended.IsZero() {
		start, end := e.Started.In(loc), e.Ended.In(loc)
		v.EndTime = end.Format("15:04:05")
		if start.Format("2006-01-02") != end.Format("2006-01-02") {
			v.EndTime = end.Format("02 Jan 15:04:05")
		}
		_, startOffset := start.Zone()
		_, endOffset := end.Zone()
		if startOffset != endOffset {
			v.StartTime += start.Format(" MST")
			v.EndTime += end.Format(" MST")
		}
	}
	v.TypeLabel = operationTypeLabel(e.Kind)
	v.OutcomeLabel = operationOutcomeLabel(e.Outcome)
	v.OutcomeStyle, v.OutcomeIcon = e.Outcome, e.Outcome
	if e.Outcome == "observed" {
		switch {
		case e.Recover || e.Kind == "recovery":
			v.OutcomeLabel, v.OutcomeStyle, v.OutcomeIcon = "Recovered", "recovered", "recovery"
		case e.Kind == "failure" || e.Kind == "unavailable" || e.Severity == "critical" || e.Severity == "error":
			v.OutcomeLabel, v.OutcomeStyle, v.OutcomeIcon = "Error", "error", "error"
		case e.Kind == "ai" || e.Kind == "stall" || e.Kind == "listener" || e.Severity == "warning":
			v.OutcomeLabel, v.OutcomeStyle, v.OutcomeIcon = "Warning", "warning", "warning"
		}
	}
	if e.Outcome == "transmitting" {
		v.EndLabel = "In progress"
	} else if e.Ended.IsZero() {
		v.EndLabel = "Not recorded"
	}
	if e.Kind == "music" || e.Kind == "speech" {
		if e.Outcome == "transmitting" {
			v.Duration = "—"
		} else {
			seconds := e.AudioBytes / 16000
			v.Duration = fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
		}
		if e.Outcome == "failed" || e.Outcome == "interrupted" || e.Outcome == "unknown" {
			v.Notes = e.Reason
		}
	} else {
		v.Notes = e.Reason
	}
	return v
}
func (s *Server) acknowledgeIncident(w http.ResponseWriter, r *http.Request) {
	if s.d.Operations == nil {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	if _, err := s.d.Stations.Get(r.Context(), id); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.d.Operations.Acknowledge(r.Context(), id, r.PathValue("incident"), time.Now()); err != nil {
		http.Error(w, "Incident not found or already acknowledged", 409)
		return
	}
	if s.d.Broadcast != nil {
		s.d.Broadcast.Changed()
	}
	s.redirect(w, r, "/producer/stations/"+url.PathEscape(id)+"/incidents")
}

// Prefix cells which spreadsheet software could interpret as formulas.
func csvText(v string) string {
	if strings.ContainsAny(strings.TrimLeft(v, " \t\r\n")[:min(1, len(strings.TrimLeft(v, " \t\r\n")))], "=+-@") || strings.HasPrefix(v, "\t") || strings.HasPrefix(v, "\r") {
		return "'" + v
	}
	return v
}
func (s *Server) exportOperations(w http.ResponseWriter, r *http.Request, f operations.Filter, loc *time.Location) {
	f.Before = ""
	f.Anchor = ""
	f.Limit = 1001
	events, err := s.d.Operations.Events(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(events) > 1000 {
		http.Error(w, "More than 1,000 events match. Narrow the date range before exporting.", 400)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="station-transmissions.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"Started", "Ended", "Type", "Title", "Artist", "Album", "Composer", "Show", "DJ", "Playlist", "Duration", "Outcome", "Notes", "Spoken text"})
	for _, e := range s.historyDisplayEvents(r, f.StationID, events) {
		v := producerEvent(e, loc)
		row := []string{v.StartLabel, v.EndLabel, v.TypeLabel, e.Title, e.Artist, e.Album, e.Composer, e.Show, e.DJ, e.Playlist, v.Duration, v.OutcomeLabel, v.Notes, e.Speech}
		for j := range row {
			row[j] = csvText(row[j])
		}
		if err = writer.Write(row); err != nil {
			break
		}
	}
	writer.Flush()
	if err != nil || writer.Error() != nil {
		s.d.Logger.Error("Transmission export interrupted")
	}
}

func (s *Server) incidentIndicator(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.d.Stations.Get(r.Context(), id); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.renderHTML(w, http.StatusOK, "incident-menu-icon", s.operationalSummary(r, id).Active)
}
