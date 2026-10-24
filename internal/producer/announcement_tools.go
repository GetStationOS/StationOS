package producer

import (
	"context"
	"errors"
	"stationos.local/stationos/internal/announcements"
	"time"
)

type announcementID struct {
	StationID string `json:"station_id"`
	ID        string `json:"id"`
}
type announcementInput struct {
	StationID       string   `json:"station_id"`
	ID              string   `json:"id"`
	Version         int      `json:"version"`
	Title           string   `json:"title"`
	Category        string   `json:"category"`
	Text            string   `json:"text"`
	Pronunciation   string   `json:"pronunciation"`
	Mode            string   `json:"mode"`
	Status          string   `json:"status"`
	Repeat          string   `json:"repeat"`
	DJID            string   `json:"dj_id"`
	ShowIDs         []string `json:"show_ids"`
	EpisodeKey      string   `json:"episode_key"`
	Starts          string   `json:"starts"`
	Expires         string   `json:"expires"`
	IntervalMinutes int      `json:"interval_minutes"`
	Times           []string `json:"times"`
}

func registerAnnouncementTools(r *Registry, d Dependencies) {
	if d.Announcements == nil {
		return
	}
	register(r, "list_announcements", "List station announcement summaries without generating speech or starting playback. Use get_announcement for the full script and actual-air history.", false, func(ctx context.Context, a struct {
		StationID string `json:"station_id"`
		Offset    int    `json:"offset"`
		Limit     int    `json:"limit"`
	}) (Result, error) {
		o, l, err := bounds(a.Offset, a.Limit)
		if err != nil {
			return Result{}, err
		}
		items, err := d.Announcements.List(ctx, a.StationID)
		if err != nil {
			return Result{}, err
		}
		out := []any{}
		for _, m := range items[min(o, len(items)):min(o+l, len(items))] {
			out = append(out, map[string]any{"id": m.ID, "title": m.Title, "status": m.State(time.Now()), "version": m.Version, "plays": m.Plays, "last_aired": m.LastAired})
		}
		return read(map[string]any{"items": out, "total": len(items)}, nil)
	})
	register(r, "get_announcement", "Read the complete announcement, saved version and latest actual transmission receipts. Preparation and previews are not airtime.", false, func(ctx context.Context, a announcementID) (Result, error) {
		m, err := d.Announcements.Get(ctx, a.StationID, a.ID)
		if err != nil {
			return Result{}, err
		}
		history, err := d.Announcements.History(ctx, a.StationID, a.ID)
		if len(history) > 5 {
			history = history[:5]
		}
		return read(map[string]any{"announcement": m, "recent_transmissions": history}, err)
	})
	register(r, "save_announcement", "Create or replace a text announcement. For edits read get_announcement first and supply its version and ALL configuration fields. New: omit id, version=0. Required: station_id,title,text,starts,expires (RFC3339 with offset). Defaults: category=message,mode=exact,status=draft,repeat=once. mode=exact speaks verbatim; adapt uses only supplied facts. repeat=interval needs interval_minutes; times needs daily HH:MM times. Optional show_ids or one stable episode_key with its show_id. Saving drafts never generates audio or starts broadcasting.", true, func(ctx context.Context, a announcementInput) (Result, error) {
		if a.ID == "" {
			if a.Version != 0 {
				return Result{}, announcements.ErrConflict
			}
			a.ID = announcements.ID()
		} else if a.Version <= 0 {
			return Result{}, announcements.ErrConflict
		}
		if a.Category == "" {
			a.Category = "message"
		}
		if a.Mode == "" {
			a.Mode = "exact"
		}
		if a.Status == "" {
			a.Status = "draft"
		}
		if a.Repeat == "" {
			a.Repeat = "once"
		}
		if a.DJID != "" {
			if _, err := d.DJs.Get(ctx, a.DJID); err != nil {
				return Result{}, err
			}
		}
		starts, err := time.Parse(time.RFC3339, a.Starts)
		if err != nil {
			return Result{}, errors.New("starts must be RFC3339 with timezone offset")
		}
		expires, err := time.Parse(time.RFC3339, a.Expires)
		if err != nil {
			return Result{}, errors.New("expires must be RFC3339 with timezone offset")
		}
		m := announcements.Message{ID: a.ID, StationID: a.StationID, Version: a.Version, Title: a.Title, Category: a.Category, Text: a.Text, Pronunciation: a.Pronunciation, Mode: a.Mode, Status: a.Status, Repeat: a.Repeat, DJID: a.DJID, ShowIDs: a.ShowIDs, EpisodeKey: a.EpisodeKey, Starts: starts, Expires: expires, IntervalMinutes: a.IntervalMinutes, Times: a.Times}
		if err := d.Announcements.Save(ctx, m); err != nil {
			return Result{}, err
		}
		m.Version++
		return saved(m, "Announcement saved", "/producer/stations/"+a.StationID+"/announcements/"+a.ID)
	})
	register(r, "set_announcement_delivery", "Activate, pause or prioritise a saved announcement. Supply latest version and action=active,paused,next. next activates and bypasses the station gap but still respects validity and show/episode targeting. No track is interrupted; dormant stations stay dormant.", true, func(ctx context.Context, a struct {
		StationID string `json:"station_id"`
		ID        string `json:"id"`
		Version   int    `json:"version"`
		Action    string `json:"action"`
	}) (Result, error) {
		m, err := d.Announcements.Get(ctx, a.StationID, a.ID)
		if err != nil {
			return Result{}, err
		}
		if m.Version != a.Version {
			return Result{}, announcements.ErrConflict
		}
		switch a.Action {
		case "active", "paused":
			m.Status = a.Action
			m.Next = false
		case "next":
			if !time.Now().Before(m.Expires) {
				return Result{}, errors.New("extend the announcement expiry first")
			}
			m.Status = "active"
			m.Next = true
		default:
			return Result{}, errors.New("choose active, paused or next")
		}
		if err = d.Announcements.Save(ctx, m); err != nil {
			return Result{}, err
		}
		return saved(nil, "Announcement delivery updated", "/producer/stations/"+a.StationID+"/announcements/"+a.ID)
	})
	register(r, "get_announcement_settings", "Read the station's default announcement presenter and minimum gap.", false, func(ctx context.Context, a stationID) (Result, error) {
		v, e := d.Announcements.Settings(ctx, a.StationID)
		return read(v, e)
	})
	register(r, "save_announcement_settings", "Save announcement default presenter (dj_id empty clears) and minimum gap 1–1440 minutes. Read settings first and supply their version. No audio is generated.", true, func(ctx context.Context, a struct {
		StationID string `json:"station_id"`
		DJID      string `json:"dj_id"`
		Gap       int    `json:"gap_minutes"`
		Version   int    `json:"version"`
	}) (Result, error) {
		if a.DJID != "" {
			if _, e := d.DJs.Get(ctx, a.DJID); e != nil {
				return Result{}, e
			}
		}
		s := announcements.Settings{StationID: a.StationID, DJID: a.DJID, GapMinutes: a.Gap, Version: a.Version}
		e := d.Announcements.SaveSettings(ctx, s)
		if e != nil {
			return Result{}, e
		}
		return saved(nil, "Announcement settings saved", "/producer/stations/"+a.StationID+"/announcements")
	})
	announcementToolSchemas(r)
}

// Announcement parameters remain explicit even for backends that depend on the
// JSON schema rather than the prose description.
func announcementToolSchemas(r *Registry) {
	for name, required := range map[string][]string{
		"list_announcements": {"station_id"}, "get_announcement": {"station_id", "id"},
		"save_announcement":         {"station_id", "title", "text", "starts", "expires"},
		"set_announcement_delivery": {"station_id", "id", "version", "action"},
		"get_announcement_settings": {"station_id"}, "save_announcement_settings": {"station_id", "dj_id", "gap_minutes", "version"},
	} {
		tool, ok := r.tools[name]
		if !ok {
			continue
		}
		definition := tool.Parameters.(map[string]any)
		definition["required"] = required
		props := definition["properties"].(map[string]any)
		for _, field := range []string{"starts", "expires"} {
			if _, ok := props[field]; ok {
				props[field] = map[string]any{"type": "string", "format": "date-time", "description": "RFC3339 timestamp including timezone offset."}
			}
		}
		for field, values := range map[string][]string{"mode": {"exact", "adapt"}, "status": {"draft", "active", "paused"}, "repeat": {"once", "interval", "times"}, "category": {"message", "news", "promotion"}, "action": {"active", "paused", "next"}} {
			if _, ok := props[field]; ok {
				props[field] = map[string]any{"type": "string", "enum": values}
			}
		}
	}
}
