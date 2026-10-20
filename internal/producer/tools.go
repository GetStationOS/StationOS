package producer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/mixer"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"stationos.local/stationos/internal/station"
)

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
	Mutates     bool   `json:"-"`
}
type Result struct {
	Usage        Usage  `json:"-"`
	ArtworkError string `json:"artwork_error,omitempty"`
	Data         any    `json:"data,omitempty"`
	Summary      string `json:"summary,omitempty"`
	URL          string `json:"url,omitempty"`
	Error        string `json:"error,omitempty"`
}
type registered struct {
	Tool
	invoke func(context.Context, json.RawMessage) (Result, error)
}
type Registry struct {
	availability func(context.Context, string) (playlists.Availability, error)
	artwork      func(context.Context, string, json.RawMessage, Result) Result
	tools        map[string]registered
	changed      func()
	djMu         sync.Mutex
	playlistMu   sync.Mutex
}

func (r *Registry) List() []Tool {
	out := []Tool{}
	for _, t := range r.tools {
		out = append(out, t.Tool)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (r *Registry) Find(name string) (Tool, bool) { t, ok := r.tools[name]; return t.Tool, ok }
func (r *Registry) Call(ctx context.Context, name string, raw json.RawMessage) Result {
	return r.call(ctx, name, raw, true)
}
func (r *Registry) call(ctx context.Context, name string, raw json.RawMessage, automatic bool) Result {
	t, ok := r.tools[name]
	if !ok {
		return Result{Error: "Unknown tool"}
	}
	if len(raw) > 64<<10 {
		return Result{Error: "Tool arguments exceed the limit"}
	}
	result, err := t.invoke(ctx, raw)
	if err != nil {
		return Result{Error: err.Error()}
	}
	if automatic && result.Error == "" && r.artwork != nil {
		result = r.artwork(ctx, name, raw, result)
	}
	if t.Mutates && r.changed != nil {
		r.changed()
	}
	if b, e := json.Marshal(result); e != nil || len(b) > 128<<10 {
		if t.Mutates {
			return Result{Summary: result.Summary, URL: result.URL}
		}
		return Result{Error: "Result exceeds the context limit. Use a narrower or paginated query."}
	}
	return result
}

// Definitions and typed dispatch are shared by the internal runner and a future MCP adapter.
func register[T any](r *Registry, name, description string, write bool, fn func(context.Context, T) (Result, error)) {
	r.tools[name] = registered{Tool: Tool{Name: name, Description: description, Parameters: schema(reflect.TypeOf((*T)(nil)).Elem()), Mutates: write}, invoke: func(ctx context.Context, raw json.RawMessage) (Result, error) {
		var args T
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return Result{}, errors.New("Arguments must be an object")
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&args); err != nil {
			if name == "create_shows" {
				return Result{}, createShowsArgumentError(err)
			}
			return Result{}, errors.New("Invalid tool arguments: " + err.Error())
		}
		if err := d.Decode(new(any)); err != io.EOF {
			return Result{}, errors.New("Expected one arguments object")
		}
		return fn(ctx, args)
	}}
}

// Keep the actual decoder failure: an array-shaped show is only one of
// several possible errors. Never echo complete arguments into the transcript.
func createShowsArgumentError(err error) error {
	correction := err.Error() + ". Use only fields in the create_shows schema."
	var typed *json.UnmarshalTypeError
	var syntax *json.SyntaxError
	switch {
	case errors.As(err, &typed):
		switch {
		case typed.Field == "show":
			correction = "show must be one object. Never put an array in show. Call separately for each distinct programme."
		// Decoder field paths may include an array index, such as days.0.
		case typed.Field == "days" || strings.HasPrefix(typed.Field, "days."):
			correction = "days must be an array of integers, Monday=0 through Sunday=6, for example [0,1,2,3,4]."
		case typed.Field == "show.start" || typed.Field == "show.end":
			correction = typed.Field + " must be an integer number of minutes after midnight, in multiples of 15; 08:00 is 480, 20:00 is 1200, and a midnight end is 0 or 1440. Do not send a time string or HHMM value."
		default:
			field := typed.Field
			if field == "" {
				field = "arguments"
			}
			correction = fmt.Sprintf("%s must have JSON type %s; received %s.", field, typed.Type, typed.Value)
		}
	case errors.As(err, &syntax):
		correction = fmt.Sprintf("Malformed JSON at byte %d. Supply one complete arguments object with a show object and a days array.", syntax.Offset)
	}
	return errors.New("Invalid create_shows arguments: " + correction + " No shows were created by this call. Correct the arguments and retry only this failed call; earlier successful calls remain saved.")
}

func schema(t reflect.Type) any {
	if t.Kind() == reflect.Pointer {
		return schema(t.Elem())
	}
	if t == reflect.TypeOf(station.Visibility("")) {
		return map[string]any{"type": "string", "enum": []string{string(station.Listed), string(station.Unlisted), string(station.Private)}, "description": "Listed appears in the public directory; unlisted allows direct links; private allows producer preview only. Omit to keep the current value or use unlisted for a new station."}
	}
	switch t.Kind() {
	case reflect.Struct:
		props := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" {
				name = f.Name
			}
			if name == "-" {
				continue
			}
			props[name] = schema(f.Type)
		}
		definition := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if t == reflect.TypeOf(rule{}) {
			props["kind"] = map[string]any{"type": "string", "enum": []string{"genre", "album", "artist", "composer", "year", "decade"}}
			props["value"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 4096, "description": "Exact catalogue genre, album, artist or composer; year as digits; decade as its first year (1980)."}
			props["source_id"] = map[string]any{"type": "string", "maxLength": 128, "description": "Omit except for album rules, which require the album source_id."}
			definition["required"] = []string{"kind", "value"}
		}
		if t == reflect.TypeOf(playlistRuleArgs{}) {
			props["playlist_id"] = map[string]any{"type": "string", "minLength": 1, "description": "Existing playlist ID returned by list_playlists or save_playlist."}
			definition["required"] = []string{"playlist_id", "rule"}
		}
		if t == reflect.TypeOf(showPatch{}) {
			props["day"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 6, "description": "Monday=0, Tuesday=1, Wednesday=2, Thursday=3, Friday=4, Saturday=5, Sunday=6."}
			props["start"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 1439, "multipleOf": 15, "description": "Station-local minutes after midnight; 08:00 is 480 and 20:00 is 1200. Never use HHMM values such as 2000."}
			props["end"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 1440, "multipleOf": 15, "description": "Station-local minutes after midnight. Midnight may be 0 or 1440 (24:00), never 2400. End at or before start crosses midnight."}
			props["name"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
			props["description"] = map[string]any{"type": "string", "maxLength": 2000, "description": "Public programme description for listeners; not DJ agent instructions."}
			props["music_direction"] = map[string]any{"type": "string", "maxLength": 4000, "description": "Optional DJ music prompt for this recurring show: themes, track selection, energy arc, performer/album spacing and deliberate specialist focus. Sent to each dated episode agent. Omit to preserve; empty string clears to a varied mix. No rotation_format field exists."}
			definition["required"] = []string{"station_id"}
		}
		if t == reflect.TypeOf(episodeRead{}) || t == reflect.TypeOf(episodeID{}) || t == reflect.TypeOf(episodePatch{}) {
			definition["required"] = []string{"station_id", "show_id", "start"}
			props["start"] = map[string]any{"type": "string", "maxLength": 64, "description": "Exact RFC3339 occurrence start from list_episodes, including timezone offset."}
			if t == reflect.TypeOf(episodePatch{}) {
				definition["required"] = []string{"station_id", "show_id", "start", "version"}
				props["version"] = map[string]any{"type": "integer", "minimum": 0, "description": "Latest optimistic version returned by get_episode. Re-read after a stale-version error."}
				props["notes"] = map[string]any{"type": "string", "maxLength": 4000, "description": "Optional episode-only DJ music prompt. Omit preserves existing notes; empty string clears."}
			}
		}
		if t == reflect.TypeOf(createShowsArgs{}) {
			props["days"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 7, "uniqueItems": true, "items": map[string]any{"type": "integer", "minimum": 0, "maximum": 6}, "description": "Selected weekdays, Monday=0 through Sunday=6, each appearing once."}
			props["show"].(map[string]any)["required"] = []string{"station_id", "name", "dj_id", "playlist_id", "start", "end"}
			definition["required"] = []string{"show", "days"}
		}
		return definition
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schema(t.Elem()), "maxItems": 100}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		return map[string]any{"type": "string"}
	}
}

type Dependencies struct {
	Voices    djs.VoiceSource
	Episodes  *episodes.Service
	Images    ImageGenerator
	Stations  station.Repository
	Sources   sources.Repository
	Catalogue catalogue.Repository
	Playlists playlists.Repository
	DJs       djs.Repository
	Schedule  schedule.Repository
	Broadcast broadcast.Repository
	Runtime   *broadcast.Manager
	Sync      catalogue.Sync
	Mixer     mixer.Repository
	Audience  audience.Repository
}
type empty struct{}
type entityID struct {
	ID string `json:"id"`
}
type stationID struct {
	StationID string `json:"station_id"`
}
type page struct {
	Text   string `json:"text"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func bounds(offset, limit int) (int, int, error) {
	if limit == 0 {
		limit = 25
	}
	if offset < 0 || offset > 1000000 || limit < 1 || limit > 100 {
		return 0, 0, errors.New("Use an offset >=0 and a limit from 1 to 100")
	}
	return offset, limit, nil
}
func saved(data any, summary, url string) (Result, error) {
	return Result{Data: data, Summary: summary, URL: url}, nil
}
func read(data any, err error) (Result, error) { return Result{Data: data}, err }
func set[T any](dst *T, patch *T) {
	if patch != nil {
		*dst = *patch
	}
}

type stationPatch struct {
	ID            string              `json:"id"`
	Name          *string             `json:"name"`
	Description   *string             `json:"description"`
	Timezone      *string             `json:"timezone"`
	Locale        *string             `json:"locale"`
	StreamSlug    *string             `json:"stream_slug"`
	Visibility    *station.Visibility `json:"visibility"`
	RepeatMinutes *int                `json:"repeat_minutes"`
}
type playlistPatch struct {
	ID          string  `json:"id"`
	StationID   string  `json:"station_id"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}
type voiceQuery struct {
	DJID       string `json:"dj_id"`
	Connection string `json:"connection"`
}
type djPatch struct {
	ID                string               `json:"id"`
	Name              *string              `json:"name"`
	Voice             *string              `json:"voice"`
	SpeechVoice       *djs.VoiceSelection  `json:"speech_voice"`
	Speech            *djs.SpeechSelection `json:"speech"`
	Prompt            *string              `json:"prompt"`
	Vibe              *string              `json:"vibe"`
	Background        *string              `json:"background"`
	ConversationSpeed *float64             `json:"conversation_speed"`
}
type showPatch struct {
	ID              string  `json:"id"`
	StationID       string  `json:"station_id"`
	Name            *string `json:"name"`
	Description     *string `json:"description"`
	MusicDirection  *string `json:"music_direction"`
	DJID            *string `json:"dj_id"`
	PlaylistID      *string `json:"playlist_id"`
	Day             *int    `json:"day"`
	Start           *int    `json:"start"`
	End             *int    `json:"end"`
	AcceptRequests  *bool   `json:"accept_requests"`
	AcceptShoutouts *bool   `json:"accept_shoutouts"`
}

type createShowsArgs struct {
	Show showPatch `json:"show"`
	Days []int     `json:"days"`
}

func (a showPatch) merge(ctx context.Context, d Dependencies) (schedule.Show, error) {
	if a.ID == "" && (a.Start == nil || a.End == nil || a.Day == nil) {
		return schedule.Show{}, errors.New("Supply day, start and end for a new show")
	}
	v := schedule.Show{StationID: a.StationID}
	var err error
	if a.ID != "" {
		v, err = d.Schedule.Get(ctx, a.StationID, a.ID)
		if err != nil {
			return v, err
		}
	}
	set(&v.Name, a.Name)
	set(&v.Description, a.Description)
	set(&v.MusicDirection, a.MusicDirection)
	set(&v.DJID, a.DJID)
	set(&v.PlaylistID, a.PlaylistID)
	set(&v.Day, a.Day)
	set(&v.Start, a.Start)
	set(&v.End, a.End)
	set(&v.AcceptRequests, a.AcceptRequests)
	set(&v.AcceptShoutouts, a.AcceptShoutouts)
	return v.Normalized(), nil
}

type playlistRuleArgs struct {
	PlaylistID string `json:"playlist_id"`
	Rule       rule   `json:"rule"`
}

type rule struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	SourceID string `json:"source_id"`
}

func (r rule) domain() playlists.Rule {

	return playlists.Rule{Kind: r.Kind, Value: r.Value, SourceID: r.SourceID}
}

// Music results deliberately omit playable URLs and return known credits/duration only.
type recording struct {
	ID, SourceID, Title, Performers, Composer, AlbumArtist, Conductor, Creator, Album, Genre string
	ReleaseDate, OriginalDate                                                                string
	Year                                                                                     int // Original date year when available, otherwise release date year.
	Seconds                                                                                  *float64
}

func music(a content.AudioItem) recording {
	v := recording{ID: a.ID, SourceID: a.SourceID, Title: a.Title, Performers: a.Artist, Composer: a.Composer, AlbumArtist: a.AlbumArtist, Conductor: a.Conductor, Creator: a.Creator, Album: a.Album, Genre: a.Genre, Year: a.Year, ReleaseDate: a.ReleaseDate, OriginalDate: a.OriginalDate}
	if a.Duration != nil {
		n := a.Duration.Seconds()
		v.Seconds = &n
	}
	return v
}
func NewRegistry(d Dependencies) *Registry {
	r := &Registry{tools: map[string]registered{}}
	r.availability = func(ctx context.Context, id string) (playlists.Availability, error) {
		if repo, ok := d.Playlists.(playlists.AvailabilityRepository); ok {
			return repo.Availability(ctx, id)
		}
		result, err := d.Playlists.Resolve(ctx, id, 0, 1)
		return playlists.Availability{Total: result.Total, Unavailable: result.Unavailable}, err
	}
	registerArtwork(r, d)
	if d.Runtime != nil {
		r.changed = d.Runtime.Changed
	}
	register(r, "list_stations", "List stations and their IDs, timezones and settings.", false, func(ctx context.Context, _ empty) (Result, error) { v, e := d.Stations.List(ctx); return read(v, e) })
	register(r, "get_station", "Read a station before editing it.", false, func(ctx context.Context, a entityID) (Result, error) {
		v, e := d.Stations.Get(ctx, a.ID)
		return read(v, e)
	})
	register(r, "save_station", "Create a station (omit id), or update supplied fields of an existing station. New stations default to Europe/London, en-GB and unlisted, and automatically receive a generated logo. artwork_error means the station was saved but its logo failed; retry the artwork using the returned ID.", true, func(ctx context.Context, a stationPatch) (Result, error) {
		v := station.Station{Timezone: "Europe/London", Locale: "en-GB"}
		var err error
		if a.ID != "" {
			v, err = d.Stations.Get(ctx, a.ID)
			if err != nil {
				return Result{}, err
			}
		}
		set(&v.Name, a.Name)
		set(&v.Description, a.Description)
		set(&v.Timezone, a.Timezone)
		set(&v.Locale, a.Locale)
		set(&v.StreamSlug, a.StreamSlug)
		set(&v.Visibility, a.Visibility)
		set(&v.RepeatMinutes, a.RepeatMinutes)
		v, err = (station.Service{Repo: d.Stations}).Save(ctx, v)
		if err != nil {
			return Result{}, err
		}
		if !v.Public() && d.Runtime != nil {
			d.Runtime.Stop(v.ID)
		}
		return saved(v, "Saved station: "+v.Name, "/producer/stations/"+v.ID)
	})
	register(r, "delete_station", "Delete a station and its playlists and shows. Shared sources and DJs remain.", true, func(ctx context.Context, a entityID) (Result, error) {
		v, e := d.Stations.Get(ctx, a.ID)
		if e != nil {
			return Result{}, e
		}
		if e = d.Stations.Delete(ctx, a.ID); e != nil {
			return Result{}, e
		}
		if d.Runtime != nil {
			d.Runtime.Stop(a.ID)
		}
		return saved(nil, "Deleted station: "+v.Name, "/producer")
	})
	register(r, "list_sources", "List configured shared music sources and imported track counts.", false, func(ctx context.Context, _ empty) (Result, error) {
		ss, e := d.Sources.List(ctx)
		if e != nil {
			return Result{}, e
		}
		out := []any{}
		for _, s := range ss {
			v, e := d.Catalogue.Search(ctx, catalogue.Query{SourceID: s.ID, Limit: 1})
			if e != nil {
				return Result{}, e
			}
			out = append(out, map[string]any{"source": s, "imported_tracks": v.Total})
		}
		return read(out, nil)
	})
	register(r, "save_source", "Create or edit a shared UPnP music source using its description endpoint. Saving does not import music.", true, func(ctx context.Context, a struct {
		ID       string  `json:"id"`
		Name     *string `json:"name"`
		Endpoint *string `json:"endpoint"`
	}) (Result, error) {
		v := sources.Config{Kind: "upnp"}
		var e error
		if a.ID != "" {
			v, e = d.Sources.Get(ctx, a.ID)
			if e != nil {
				return Result{}, e
			}
		}
		set(&v.Name, a.Name)
		set(&v.Endpoint, a.Endpoint)
		v, e = (sources.Service{Repo: d.Sources}).Save(ctx, v)
		if e != nil {
			return Result{}, e
		}
		return saved(v, "Saved source: "+v.Name, "/producer/sources/"+v.ID)
	})
	register(r, "refresh_source", "Import music metadata from a configured source. A failed import preserves its previous catalogue.", true, func(ctx context.Context, a entityID) (Result, error) {
		n, e := d.Sync.Run(ctx, a.ID)
		if e != nil {
			return Result{}, errors.New("Source import failed; previous catalogue preserved")
		}
		return saved(map[string]int{"tracks": n}, fmt.Sprintf("Imported %d tracks", n), "/producer/sources/"+a.ID)
	})
	register(r, "summarize_music", "Summarize the imported catalogue using measured counts, genre/artist coverage and known duration. Start library exploration here; optional text, exact genre or source filters. Limit controls the top genres/artists (1–20, default 12), never recording rows. Detailed earlier catalogue pages leave working context after this summary; use small targeted searches for exact recordings.", false, func(ctx context.Context, q catalogue.SummaryQuery) (Result, error) {
		repo, ok := d.Catalogue.(catalogue.SummaryRepository)
		if !ok {
			return Result{}, errors.New("Catalogue summary is unavailable")
		}
		summary, err := (catalogue.SummaryService{Repo: repo}).Summarize(ctx, q)
		return read(summary, err)
	})
	register(r, "search_music", "Search music by text, exact genre, album, performing artist, composer, source or inclusive year_from/year_to. Years prefer original dates, falling back to release dates that may describe reissues; unknown dates do not match. Results include IDs, credits, year and known durations. Start with summarize_music. Paginated, limit 1–20 (default 5).", false, func(ctx context.Context, a struct {
		Artist   string `json:"artist"`
		Composer string `json:"composer"`
		YearFrom int    `json:"year_from"`
		YearTo   int    `json:"year_to"`
		Text     string `json:"text"`
		Genre    string `json:"genre"`
		Album    string `json:"album"`
		SourceID string `json:"source_id"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}) (Result, error) {
		if a.Limit == 0 {
			a.Limit = 5
		}
		if a.Limit > 20 {
			return Result{}, errors.New("Use at most 20 recordings per search page")
		}
		o, l, e := bounds(a.Offset, a.Limit)
		if e != nil {
			return Result{}, e
		}
		v, e := d.Catalogue.Search(ctx, catalogue.Query{Artist: a.Artist, Composer: a.Composer, YearFrom: a.YearFrom, YearTo: a.YearTo, Text: a.Text, Genre: a.Genre, Album: a.Album, SourceID: a.SourceID, Offset: o, Limit: l})
		if e != nil {
			return Result{}, e
		}
		out := []recording{}
		for _, item := range v.Items {
			out = append(out, music(item))
		}
		return read(map[string]any{"catalogue_page": true, "query": a, "items": out, "total": v.Total, "offset": o, "limit": l}, nil)
	})
	register(r, "explore_music", "Explore dynamic rule values. kind: genres, albums, artists, composers, years or decades. Decades use their first year. Dates prefer source original dates, falling back to release dates; absence is unknown. Filter by text; paginate with offset and limit.", false, func(ctx context.Context, a struct {
		Kind   string `json:"kind"`
		Text   string `json:"text"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}) (Result, error) {
		o, l, e := bounds(a.Offset, a.Limit)
		if e != nil {
			return Result{}, e
		}
		v, e := d.Catalogue.Facets(ctx)
		if e != nil {
			return Result{}, e
		}
		out := []any{}
		match := strings.ToLower(a.Text)
		switch a.Kind {
		case "genres":
			for _, g := range v.Genres {
				if strings.Contains(strings.ToLower(g), match) {
					out = append(out, g)
				}
			}
		case "artists", "composers", "years", "decades":
			values := v.Artists
			switch a.Kind {
			case "composers":
				values = v.Composers
			case "years":
				values = v.Years
			case "decades":
				values = v.Decades
			}
			for _, value := range values {
				if strings.Contains(strings.ToLower(value), match) {
					out = append(out, value)
				}
			}
		case "albums":
			for _, album := range v.Albums {
				if strings.Contains(strings.ToLower(album.Name), match) {
					out = append(out, album)
				}
			}
		default:
			return Result{}, errors.New("Choose genres, albums, artists, composers, years or decades")
		}
		total := len(out)
		o = min(o, total)
		return read(map[string]any{"catalogue_page": true, "query": a, "items": out[o:min(o+l, total)], "total": total}, nil)
	})
	ps := playlists.Service{Repo: d.Playlists, Stations: d.Stations}
	register(r, "list_playlists", "List a station's existing playlists before creating another. Assess candidates using get_playlist and reuse suitable IDs in that station's shows/default assignment. All playlists are listed when station_id is omitted; playlists cannot be assigned across stations.", false, func(ctx context.Context, a stationID) (Result, error) {
		v, e := d.Playlists.List(ctx, a.StationID)
		return read(v, e)
	})
	register(r, "get_playlist", "Inspect playlist settings, dynamic rules and resolved tracks to assess suitability; check actual eligibility and availability, not just the name. Reuse suitable station-owned pools without changing unrelated rules. Tracks are an unordered pool. Paginated with offset and limit.", false, func(ctx context.Context, a struct {
		ID     string `json:"id"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}) (Result, error) {
		o, l, e := bounds(a.Offset, a.Limit)
		if e != nil {
			return Result{}, e
		}
		v, e := d.Playlists.Get(ctx, a.ID)
		if e != nil {
			return Result{}, e
		}
		rules, e := d.Playlists.Rules(ctx, a.ID)
		if e != nil {
			return Result{}, e
		}
		items, e := d.Playlists.Resolve(ctx, a.ID, o, l)
		if e != nil {
			return Result{}, e
		}
		out := []any{}
		for _, i := range items.Entries {
			out = append(out, map[string]any{"track": music(i.Item), "available": i.Available})
		}
		return read(map[string]any{"playlist": v, "rules": rules, "tracks": out, "total": items.Total, "unavailable": items.Unavailable, "seconds": items.Duration.Seconds(), "unknown_durations": items.UnknownDurations}, nil)
	})
	register(r, "save_playlist", "Create a station-owned eligibility pool (omit id), or update supplied fields. Inspect and reuse suitable existing pools first. New pools require station_id and a distinct programme or musical identity name; recurring editions share one pool. Preserve unrelated fields and rules.", true, func(ctx context.Context, a playlistPatch) (Result, error) {
		r.playlistMu.Lock()
		defer r.playlistMu.Unlock()
		v := playlists.Playlist{StationID: a.StationID}
		var e error
		if a.ID != "" {
			v, e = d.Playlists.Get(ctx, a.ID)
			if e != nil {
				return Result{}, e
			}
			if a.StationID != "" && a.StationID != v.StationID {
				return Result{}, playlists.ErrInvalid
			}
		}
		originalName := v.Name
		set(&v.Name, a.Name)
		set(&v.Description, a.Description)
		if err := v.Validate(); err != nil {
			return Result{}, err
		}
		if a.ID == "" || normalizedName(v.Name) != normalizedName(originalName) {
			owner, err := d.Stations.Get(ctx, v.StationID)
			if err != nil {
				return Result{}, err
			}
			existing, err := d.Playlists.List(ctx, v.StationID)
			if err != nil {
				return Result{}, err
			}
			identity := withoutStationSuffix(v.Name, []station.Station{owner})
			for _, pool := range existing {
				if pool.ID != v.ID && withoutStationSuffix(pool.Name, []station.Station{owner}) == identity {
					return Result{}, fmt.Errorf("Playlist %q already exists in this station (ID %s). Inspect its rules and tracks with get_playlist and reuse this ID if suitable. If a different eligibility pool is required, choose a distinct descriptive name.", pool.Name, pool.ID)
				}
			}
		}
		v, e = ps.Save(ctx, v)
		if e != nil {
			return Result{}, e
		}
		return saved(v, "Saved playlist: "+v.Name, "/producer/playlists/"+v.ID)
	})
	register(r, "delete_playlist", "Delete a playlist. Shows using it must be changed first.", true, func(ctx context.Context, a entityID) (Result, error) {
		v, e := d.Playlists.Get(ctx, a.ID)
		if e != nil {
			return Result{}, e
		}
		if e = d.Playlists.Delete(ctx, a.ID); e != nil {
			return Result{}, e
		}
		return saved(nil, "Deleted playlist: "+v.Name, "/producer/playlists?station="+v.StationID)
	})
	register(r, "add_playlist_rule", "Add ONE dynamic rule to an existing playlist. Kinds: genre, album, artist, composer, year, decade. Use exact explore_music values. Album requires source_id; others omit it. Values within a category are OR; selected categories combine with AND. Years and decades share the date category. Use ordinary rules; separate combined rules are unnecessary. Years prefer source original dates, falling back to release dates. New matching imports join automatically.", true, func(ctx context.Context, a playlistRuleArgs) (Result, error) {
		if a.Rule.Kind == "filter" {
			return Result{}, errors.New("Use genre, album, artist, composer, year or decade selections; categories combine automatically")
		}
		if e := ps.AddRule(ctx, a.PlaylistID, a.Rule.domain()); e != nil {
			return Result{}, e
		}
		return saved(nil, "Added playlist rule: "+a.Rule.Value, "/producer/playlists/"+a.PlaylistID)
	})
	register(r, "replace_playlist_rules", "Atomically replace ALL dynamic rules in a playlist, up to 100. Values within each category are alternatives; every selected category must match. Years and decades share a date category. Manual inclusions/exclusions remain. An empty rules array clears rules.", true, func(ctx context.Context, a struct {
		PlaylistID string `json:"playlist_id"`
		Rules      []rule `json:"rules"`
	}) (Result, error) {
		rules := []playlists.Rule{}
		for _, rule := range a.Rules {
			rules = append(rules, rule.domain())
		}
		if e := ps.ReplaceRules(ctx, a.PlaylistID, rules); e != nil {
			return Result{}, e
		}
		return saved(nil, "Replaced playlist rules", "/producer/playlists/"+a.PlaylistID)
	})
	register(r, "remove_playlist_rule", "Remove one playlist rule using a rule ID returned by get_playlist.", true, func(ctx context.Context, a struct {
		PlaylistID string `json:"playlist_id"`
		RuleID     string `json:"rule_id"`
	}) (Result, error) {
		if e := d.Playlists.RemoveRule(ctx, a.PlaylistID, a.RuleID); e != nil {
			return Result{}, e
		}
		return saved(nil, "Removed playlist rule", "/producer/playlists/"+a.PlaylistID)
	})
	register(r, "select_tracks", "Include or exclude 1–100 exact catalogue track IDs. Exclusions override dynamic rules. This does not order playback.", true, func(ctx context.Context, a struct {
		PlaylistID string   `json:"playlist_id"`
		TrackIDs   []string `json:"track_ids"`
		Included   *bool    `json:"included"`
	}) (Result, error) {
		if a.Included == nil {
			return Result{}, errors.New("Supply included: true to include, false to exclude")
		}
		if e := ps.Select(ctx, a.PlaylistID, a.TrackIDs, *a.Included); e != nil {
			return Result{}, e
		}
		return saved(nil, fmt.Sprintf("Updated %d track selections", len(a.TrackIDs)), "/producer/playlists/"+a.PlaylistID+"/tracks")
	})
	register(r, "assign_default_playlist", "Set a station's gap/default playlist. Empty playlist_id clears the assignment. Playlist must belong to this station.", true, func(ctx context.Context, a struct {
		StationID  string  `json:"station_id"`
		PlaylistID *string `json:"playlist_id"`
	}) (Result, error) {
		if a.PlaylistID == nil {
			return Result{}, errors.New("Supply playlist_id, or an empty string to clear the default")
		}
		if e := d.Broadcast.Assign(ctx, a.StationID, *a.PlaylistID); e != nil {
			return Result{}, e
		}
		return saved(nil, "Updated default playlist", "/producer/stations/"+a.StationID)
	})
	register(r, "list_djs", "List shared fictional DJ profiles with brief background and voice-direction summaries. Inspect suitable candidates with get_dj for each programme and reuse their IDs where appropriate. Plan a varied lineup across distinct programmes in a full weekly schedule, with the same presenter for recurring editions; honour explicit single-presenter requests. HasKey means configured, not tested. No credentials are returned.", false, func(ctx context.Context, _ empty) (Result, error) {
		v, e := d.DJs.List(ctx)
		for i := range v {
			v[i].Prompt = ""
			v[i].Vibe = djSummary(v[i].Vibe, 400)
			v[i].Background = djSummary(v[i].Background, 800)
		}
		return read(v, e)
	})
	register(r, "get_dj", "Read a DJ profile before editing. No credentials are returned.", false, func(ctx context.Context, a entityID) (Result, error) { v, e := d.DJs.Get(ctx, a.ID); return read(v, e) })
	register(r, "list_speech_endpoints", "List shared OpenAI and Chatterbox speech connections available for each DJ. Credentials and server addresses are not returned.", false, func(ctx context.Context, _ empty) (Result, error) {
		source, ok := d.Voices.(djs.SpeechSource)
		if !ok {
			return read([]djs.SpeechEndpoint{}, nil)
		}
		endpoints, err := source.SpeechConnections(ctx)
		return read(endpoints, err)
	})
	register(r, "list_voices", "List voices for a DJ's saved speech endpoint, or a specified connection before editing. Supply dj_id or connection. Omission lists built-in OpenAI voices. Use list_speech_endpoints before choosing a provider. No speech is generated.", false, func(ctx context.Context, a voiceQuery) (Result, error) {
		id := a.Connection
		if id == "" && a.DJID != "" {
			dj, err := d.DJs.Get(ctx, a.DJID)
			if err != nil {
				return Result{}, err
			}
			id = dj.SpeechConnection
		}
		if id == "" {
			id = "openai"
		}
		if d.Voices != nil {
			v, e := d.Voices.Voices(djs.WithSpeechConnection(ctx, id))
			return read(v, e)
		}
		return read(djs.Voices(), nil)
	})
	register(r, "save_dj", "Create or update supplied fields of a shared fictional DJ. First assess existing DJs using list_djs/get_dj and reuse a suitable presenter in shows. Create when existing profiles do not cover a programme’s needs or the distinct identities needed for a varied weekly lineup, or when an original presenter is explicitly requested. Use an original name, never an existing name with a station suffix. Duplicate names are rejected. Defaults: cedar OpenAI voice, speed 1; Chatterbox uses its server default until speech_voice is selected. Use list_speech_endpoints and list_voices, then set speech={connection,settings:{model,voice,speed,vibe}} for this DJ. Chatterbox uses its fixed Nano model, server-discovered voice, speed 1 and no vibe. Prior endpoint settings are retained when switching. speech_voice is a legacy Chatterbox-only edit; voice remains the legacy OpenAI choice. All DJs use the global curation/writing provider and model in Assistant settings; DJ profiles cannot override text model selection; each DJ owns its speech endpoint and model. New DJs automatically receive a generated fictional portrait. artwork_error means the DJ was saved but the photo failed; retry only the artwork using its returned ID. Credentials belong to shared endpoints in Assistant settings; DJ profiles contain no keys.", true, func(ctx context.Context, a djPatch) (Result, error) {
		r.djMu.Lock()
		defer r.djMu.Unlock()
		v := djs.Profile{}
		var e error
		if a.ID != "" {
			v, e = d.DJs.Get(ctx, a.ID)
			if e != nil {
				return Result{}, e
			}
		}
		originalName := v.Name
		set(&v.Name, a.Name)
		set(&v.Voice, a.Voice)
		v.SpeechVoiceChange = a.SpeechVoice
		v.SpeechChange = a.Speech
		set(&v.Prompt, a.Prompt)
		set(&v.Vibe, a.Vibe)
		set(&v.Background, a.Background)
		set(&v.ConversationSpeed, a.ConversationSpeed)
		if a.ID == "" || normalizedName(v.Name) != normalizedName(originalName) {
			profiles, err := d.DJs.List(ctx)
			if err != nil {
				return Result{}, err
			}
			stations, err := d.Stations.List(ctx)
			if err != nil {
				return Result{}, err
			}
			identity := withoutStationSuffix(v.Name, stations)
			for _, existing := range profiles {
				if existing.ID != v.ID && withoutStationSuffix(existing.Name, stations) == identity {
					return Result{}, fmt.Errorf("A shared DJ named %q already exists (ID %s). Inspect that profile with get_dj and reuse its ID in shows if suitable. Otherwise choose an original presenter name; do not append a station name.", existing.Name, existing.ID)
				}
			}
			if identity != normalizedName(v.Name) {
				return Result{}, errors.New("DJs are shared across stations. Use an original presenter name without a station suffix; inspect existing DJs before creating another.")
			}
		}
		v, e = (djs.Service{Repo: d.DJs, Voices: d.Voices}).Save(ctx, v, djs.KeyChange{})
		if e != nil {
			return Result{}, e
		}
		return saved(v, "Saved DJ: "+v.Name, "/producer/djs/"+v.ID)
	})
	register(r, "delete_dj", "Delete a shared DJ. Shows using it must be changed first.", true, func(ctx context.Context, a entityID) (Result, error) {
		v, e := d.DJs.Get(ctx, a.ID)
		if e != nil {
			return Result{}, e
		}
		if e = d.DJs.Delete(ctx, a.ID); e != nil {
			return Result{}, e
		}
		return saved(nil, "Deleted DJ: "+v.Name, "/producer/djs")
	})
	register(r, "list_shows", "Read the saved weekly shows to verify actual configuration. Monday=0, Tuesday=1, Wednesday=2, Thursday=3, Friday=4, Saturday=5, Sunday=6. Results include day_name, local start_time/end_time and a bounded music_direction preview; use get_show for the complete DJ music prompt. Compare every requested day and broadcast window with the original brief. End<=start means next day.", false, func(ctx context.Context, a stationID) (Result, error) {
		v, e := d.Schedule.List(ctx, a.StationID)
		for i := range v {
			v[i].DJ = djs.Profile{}
		}
		return read(namedSchedule(v), e)
	})
	register(r, "save_show", "Create or update supplied show fields. Provide station_id; days Monday=0..Sunday=6; start/end are minutes after midnight, multiples of 15. Midnight ends may be 0 or 1440 (24:00), never 2400. Use existing DJ and station-owned playlist IDs. music_direction is the DJ agent prompt for this programme, distinct from its public description and the shared DJ personality prompt. Overlaps are rejected.", true, func(ctx context.Context, a showPatch) (Result, error) {
		v, e := a.merge(ctx, d)
		if e != nil {
			return Result{}, e
		}
		v, e = (schedule.Service{Repo: d.Schedule}).Save(ctx, v)
		if e != nil {
			return Result{}, e
		}
		return saved(v, "Saved show: "+v.Name, "/producer/stations/"+v.StationID+"/schedule/"+v.ID)
	})
	register(r, "create_shows", "Create one programme on selected weekdays atomically. Supply ONE show object and days, never an array in show. Call separately for distinct programmes. Monday=0 through Sunday=6. Start/end use quarter-hour minutes after midnight; midnight end is 0 or 1440, never 2400. Use existing presenter and station-owned playlist IDs; omit show.id. Set show.music_direction to the programme-specific DJ music prompt, shared across its recurring days. Conflicts reject every selected day.", true, func(ctx context.Context, a createShowsArgs) (Result, error) {
		if a.Show.ID != "" {
			return Result{}, schedule.Invalid("omit show.id in create_shows; use save_show to edit an existing show")
		}
		if len(a.Days) == 0 {
			return Result{}, schedule.Invalid("supply days with one to seven weekdays, using Monday=0 through Sunday=6")
		}
		a.Show.Day = &a.Days[0]
		v, e := a.Show.merge(ctx, d)
		if e != nil {
			return Result{}, e
		}
		if e = (schedule.Service{Repo: d.Schedule}).CreateDays(ctx, v, a.Days); e != nil {
			return Result{}, e
		}
		shows, e := d.Schedule.List(ctx, v.StationID)
		if e != nil {
			return Result{}, e
		}
		created := []schedule.Show{}
		for _, show := range shows {
			for _, day := range a.Days {
				if show.Day == day && show.Start == v.Start && show.End == v.End && show.Name == v.Name {
					show.DJ = djs.Profile{}
					created = append(created, show)
				}
			}
		}
		return saved(created, "Added weekly shows: "+v.Name, "/producer/stations/"+v.StationID+"/schedule")
	})
	register(r, "delete_show", "Delete one weekly show, retaining its DJ and playlist.", true, func(ctx context.Context, a struct {
		StationID string `json:"station_id"`
		ID        string `json:"id"`
	}) (Result, error) {
		v, e := d.Schedule.Get(ctx, a.StationID, a.ID)
		if e != nil {
			return Result{}, e
		}
		if e = d.Schedule.Delete(ctx, a.StationID, a.ID); e != nil {
			return Result{}, e
		}
		return saved(nil, "Deleted show: "+v.Name, "/producer/stations/"+a.StationID+"/schedule")
	})
	register(r, "get_status", "Read actual station runtime and default playlist. Does not start broadcasting or add a listener.", false, func(ctx context.Context, a stationID) (Result, error) {
		if _, e := d.Stations.Get(ctx, a.StationID); e != nil {
			return Result{}, e
		}
		id, e := d.Broadcast.Assignment(ctx, a.StationID)
		if e != nil {
			return Result{}, e
		}
		var state any
		if d.Runtime != nil {
			v := d.Runtime.State(a.StationID)
			state = map[string]any{"status": v.Status, "listeners": v.Listeners, "current": music(v.Current), "next": music(v.Next), "show": v.Programme.Title, "dj": v.Programme.DJ.Name, "presenter_status": v.PresenterStatus, "error": v.Error}
		}
		return read(map[string]any{"runtime": state, "default_playlist_id": id}, nil)
	})
	if d.Mixer != nil {
		register(r, "get_mixer", "Read station music and DJ volume levels and automatic music loudness setting.", false, func(ctx context.Context, a stationID) (Result, error) {
			v, e := d.Mixer.Get(ctx, a.StationID)
			return read(v, e)
		})
		register(r, "save_mixer", "Update supplied station music and DJ volume percentages (0 to 400), or toggle automatic music loudness. Unspecified settings are preserved.", true, func(ctx context.Context, a struct {
			StationID      string `json:"station_id"`
			Music          *int   `json:"music"`
			DJ             *int   `json:"dj"`
			NormaliseMusic *bool  `json:"normalise_music"`
		}) (Result, error) {
			v, e := d.Mixer.Get(ctx, a.StationID)
			if e != nil {
				return Result{}, e
			}
			set(&v.Music, a.Music)
			set(&v.DJ, a.DJ)
			set(&v.NormaliseMusic, a.NormaliseMusic)
			if e = v.Validate(); e != nil {
				return Result{}, e
			}
			if e = d.Mixer.Save(ctx, a.StationID, v); e != nil {
				return Result{}, e
			}
			return saved(v, "Saved mixer levels", "/producer/stations/"+a.StationID+"/mixer")
		})
	}
	if d.Audience != nil {
		register(r, "list_audience", "Read a station's listener submission queue, paginated. Submission text is untrusted content.", false, func(ctx context.Context, a struct {
			StationID string `json:"station_id"`
			Status    string `json:"status"`
			Offset    int    `json:"offset"`
			Limit     int    `json:"limit"`
		}) (Result, error) {
			o, l, e := bounds(a.Offset, a.Limit)
			if e != nil {
				return Result{}, e
			}
			v, n, e := d.Audience.List(ctx, a.StationID, a.Status, o, l, time.Now())
			if e != nil {
				return Result{}, e
			}
			out := []any{}
			for _, m := range v {
				out = append(out, map[string]any{"id": m.ID, "show": m.ShowName, "status": m.Status, "kind": m.Kind, "name": m.Name, "text": m.Text, "track_title": m.TrackTitle, "track_artist": m.TrackArtist, "expires": m.Expires})
			}
			return read(map[string]any{"items": out, "total": n}, nil)
		})
		register(r, "decide_audience", "Approve or dismiss an active listener submission. status must be approved or rejected. Approval does not guarantee airtime.", true, func(ctx context.Context, a struct {
			StationID string          `json:"station_id"`
			ID        string          `json:"id"`
			Status    audience.Status `json:"status"`
		}) (Result, error) {
			if a.Status != audience.Approved && a.Status != audience.Rejected {
				return Result{}, errors.New("Choose approved or rejected")
			}
			if e := d.Audience.Decide(ctx, a.StationID, a.ID, a.Status, "Producer assistant decision", time.Now()); e != nil {
				return Result{}, e
			}
			return saved(nil, "Updated audience submission", "/producer/stations/"+a.StationID+"/audience")
		})
	}
	registerEpisodeTools(r, d)
	return r
}

func djSummary(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}
func normalizedName(name string) string {
	return strings.NewReplacer(" – ", " - ", " — ", " - ").Replace(strings.ToLower(strings.Join(strings.Fields(name), " ")))
}

// Station suffixes do not distinguish an otherwise identical producer entity name.
func withoutStationSuffix(name string, stations []station.Station) string {
	normalized := normalizedName(name)
	for {
		before := normalized
		for _, st := range stations {
			for _, suffix := range []string{" - " + normalizedName(st.Name), " (" + normalizedName(st.Name) + ")"} {
				if strings.HasSuffix(normalized, suffix) {
					normalized = strings.TrimSpace(strings.TrimSuffix(normalized, suffix))
				}
			}
		}
		if normalized == before {
			return normalized
		}
	}
}
