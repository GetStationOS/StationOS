package producer_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

func TestCreateShowsValidationAndMidnight(t *testing.T) {
	_, r, _, _, repo, _ := fixture(t)
	ctx := context.Background()
	s := call(t, r, "save_station", map[string]any{"name": "Night Radio"}).Data.(station.Station)
	pool := call(t, r, "save_playlist", map[string]any{"station_id": s.ID, "name": "Night Music"})
	raw, _ := json.Marshal(pool.Data)
	var p struct{ ID string }
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	dj := call(t, r, "save_dj", map[string]string{"name": "Night Presenter"}).Data.(djs.Profile)
	base := map[string]any{"station_id": s.ID, "name": "Late Show", "dj_id": dj.ID, "playlist_id": p.ID, "start": 1320, "end": 1440}
	for _, tc := range []struct {
		name, field string
		value       any
		days        []int
		want        string
	}{
		{"weekday", "", nil, []int{0, 7}, "Monday=0"},
		{"duplicate", "", nil, []int{0, 0}, "only once"},
		{"empty", "", nil, []int{}, "one to seven"},
		{"start", "start", 1440, []int{0, 1}, "start must be minutes"},
		{"end", "end", 2400, []int{0, 1}, "end must be minutes"},
		{"quarter hour", "start", 1321, []int{0, 1}, "multiple of 15"},
		{"presenter", "dj_id", "missing", []int{0, 1}, "presenter does not exist"},
		{"playlist", "playlist_id", "missing", []int{0, 1}, "playlist does not exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			show := map[string]any{}
			for k, v := range base {
				show[k] = v
			}
			if tc.field != "" {
				show[tc.field] = tc.value
			}
			args, _ := json.Marshal(map[string]any{"show": show, "days": tc.days})
			result := r.Call(ctx, "create_shows", args)
			if !strings.Contains(result.Error, tc.want) {
				t.Fatalf("expected %q, got %+v", tc.want, result)
			}
			shows, err := repo.List(ctx, s.ID)
			if err != nil || len(shows) != 0 {
				t.Fatalf("invalid batch wrote shows: %+v, %v", shows, err)
			}
		})
	}
	result := call(t, r, "create_shows", map[string]any{"show": base, "days": []int{0, 5, 6}})
	created, ok := result.Data.([]schedule.Show)
	if !ok || len(created) != 3 {
		t.Fatalf("missing created references: %+v", result)
	}
	for _, show := range created {
		if show.ID == "" || show.End != 0 || show.Duration() != 120 {
			t.Fatalf("incorrect midnight normalization: %+v", show)
		}
	}
	// The conflict identifies the committed edition, and a multi-day retry
	// must roll back a free earlier day when a later day conflicts.
	args, _ := json.Marshal(map[string]any{"show": base, "days": []int{1, 5}})
	conflict := r.Call(ctx, "create_shows", args)
	for _, want := range []string{"Late Show", "Saturday 22:00", "Sunday 00:00", "show_id="} {
		if !strings.Contains(conflict.Error, want) {
			t.Fatalf("missing conflict detail %q: %+v", want, conflict)
		}
	}
	shows, err := repo.List(ctx, s.ID)
	if err != nil || len(shows) != 3 {
		t.Fatalf("overlap committed partial days: %+v, %v", shows, err)
	}
	// Editing a single show accepts the same end-of-day convention.
	updated := call(t, r, "save_show", map[string]any{"station_id": s.ID, "id": created[0].ID, "end": 1440}).Data.(schedule.Show)
	if updated.End != 0 || updated.Start != 1320 {
		t.Fatalf("midnight edit lost fields: %+v", updated)
	}
}

func TestCreateShowsDecoderReportsActualProblem(t *testing.T) {
	_, r, _, _, repo, _ := fixture(t)
	for _, tc := range []struct{ args, want string }{
		{`{"show":[],"days":[0]}`, "Never put an array in show"},
		{`{"show":{"start":"08:00"},"days":[0]}`, "show.start must be an integer number of minutes"},
		{`{"show":{"end":"24:00"},"days":[0]}`, "show.end must be an integer number of minutes"},
		{`{"show":{},"days":"Monday"}`, "days must be an array of integers"},
		{`{"show":{},"days":["Monday"]}`, "days must be an array of integers"},
		{`{"show":{},"days":[0,"Monday"]}`, "days must be an array of integers"},
		{`{"show":{},"days":[{}]}`, "days must be an array of integers"},
		{`{"show":{"title":"Morning"},"days":[0]}`, `unknown field "title"`},
		{`{"show":{"name":false},"days":[0]}`, "show.name must have JSON type string"},
		{`{"show":,}`, "Malformed JSON at byte"},
	} {
		t.Run(tc.args, func(t *testing.T) {
			result := r.Call(context.Background(), "create_shows", []byte(tc.args))
			for _, want := range []string{tc.want, "No shows were created", "earlier successful calls remain saved"} {
				if !strings.Contains(result.Error, want) {
					t.Fatalf("expected %q, got %+v", want, result)
				}
			}
			if !strings.Contains(tc.want, "array in show") && strings.Contains(result.Error, "Never put an array in show") {
				t.Fatal("misdiagnosed unrelated error as show array", result)
			}
		})
	}
	shows, err := repo.List(context.Background(), "")
	if err != nil || len(shows) != 0 {
		t.Fatalf("malformed calls created shows: %+v %v", shows, err)
	}
}
