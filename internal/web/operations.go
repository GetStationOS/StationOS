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
	"strconv"
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
	StartLabel, EndLabel, Duration string
}
type operationalIncident struct {
	operations.Incident
	FirstLabel, LatestLabel, RecoveredLabel string
}
type operationChoice struct{ ID, Name string }
type operationsView struct {
	ShowChoices, DJChoices []operationChoice
	pageView
	Broadcast                                       broadcast.State
	Summary                                         operationSummary
	Events                                          []operationalEvent
	Incidents                                       []operationalIncident
	Query, Kind, Outcome, From, Until, ShowID, DJID string
	NextURL, ExportURL, Version, RefreshURL         string
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
	f := operations.Filter{StationID: r.PathValue("id"), Query: q.Get("q"), Kind: q.Get("kind"), Outcome: q.Get("outcome"), ShowID: q.Get("show"), DJID: q.Get("dj"), Before: q.Get("before"), Limit: 51}
	if len(f.Query) > 200 || len(f.Before) > 64 || len(f.ShowID) > 128 || len(f.DJID) > 128 {
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
	v.Title = map[string]string{"timeline": "Transmission timeline", "incidents": "Incidents"}[v.Section]
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
	v.DJID = f.DJID
	v.RefreshURL = r.URL.RequestURI()
	if strings.HasSuffix(r.URL.Path, "/history.csv") {
		s.exportOperations(w, r, f, loc)
		return
	}
	events, err := s.d.Operations.Events(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(events) > 50 {
		events = events[:50]
		q := r.URL.Query()
		q.Set("before", events[49].ID)
		v.NextURL = "?" + q.Encode()
	}
	for _, e := range events {
		d := "—"
		if e.Kind == "music" || e.Kind == "speech" {
			d = fmt.Sprintf("%.2f s", float64(e.AudioBytes)/16000)
		}
		v.Events = append(v.Events, operationalEvent{e, operationalTime(e.Started, loc), operationalTime(e.Ended, loc), d})
	}
	q := r.URL.Query()
	q.Del("before")
	v.ExportURL = "/producer/stations/" + url.PathEscape(st.ID) + "/history.csv?" + q.Encode()
	incidents, err := s.d.Operations.Incidents(r.Context(), st.ID, false)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, i := range incidents {
		v.Incidents = append(v.Incidents, operationalIncident{i, operationalTime(i.First, loc), operationalTime(i.Latest, loc), operationalTime(i.Recovered, loc)})
	}
	shows, djs := map[string]string{}, map[string]string{}
	for _, e := range events {
		if e.ShowID != "" {
			shows[e.ShowID] = e.Show
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
			shows[show.ID] = show.Name
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
	if f.ShowID != "" && shows[f.ShowID] == "" {
		shows[f.ShowID] = "Historical show"
	}
	if f.DJID != "" && djs[f.DJID] == "" {
		djs[f.DJID] = "Historical DJ"
	}
	for id, name := range shows {
		v.ShowChoices = append(v.ShowChoices, operationChoice{id, name})
	}
	for id, name := range djs {
		v.DJChoices = append(v.DJChoices, operationChoice{id, name})
	}
	sort.Slice(v.ShowChoices, func(i, j int) bool { return v.ShowChoices[i].Name < v.ShowChoices[j].Name })
	sort.Slice(v.DJChoices, func(i, j int) bool { return v.DJChoices[i].Name < v.DJChoices[j].Name })
	v.Version, err = s.operationsFingerprint(r, st.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.renderPage(w, r, 200, "operations-page", v)
}
func (s *Server) operationsFingerprint(r *http.Request, id string) (string, error) {
	e, err := s.d.Operations.Events(r.Context(), operations.Filter{StationID: id, Limit: 50})
	if err != nil {
		return "", err
	}
	i, err := s.d.Operations.Incidents(r.Context(), id, true)
	if err != nil {
		return "", err
	}
	var runtime broadcast.State
	if s.d.Broadcast != nil {
		runtime = s.d.Broadcast.State(id)
	}
	raw, _ := json.Marshal(struct {
		Current, Next, Presenter, Curation, Programme, Following string
		Events                                                   []operations.Event
		Incidents                                                []operations.Incident
		Healthy                                                  bool
	}{runtime.Current.ID, runtime.Next.ID, runtime.PresenterStatus, runtime.CurationStatus, runtime.Programme.Key, runtime.Following.Key, e, i, s.d.Recording == nil || s.d.Recording.Healthy()})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func (s *Server) operationsVersion(w http.ResponseWriter, r *http.Request) {
	if s.d.Operations == nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.d.Stations.Get(r.Context(), r.PathValue("id")); err != nil {
		http.NotFound(w, r)
		return
	}
	v, err := s.operationsFingerprint(r, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.json(w, 200, map[string]string{"version": v})
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
	_ = writer.Write([]string{"Event ID", "Started", "Ended", "Type", "Outcome", "Title", "Artist", "Composer", "Album", "Show", "DJ", "Audio bytes", "Reason", "Speech"})
	for _, e := range events {
		row := []string{e.ID, operationalTime(e.Started, loc), operationalTime(e.Ended, loc), e.Kind, e.Outcome, e.Title, e.Artist, e.Composer, e.Album, e.Show, e.DJ, strconv.FormatInt(e.AudioBytes, 10), e.Reason, e.Speech}
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
