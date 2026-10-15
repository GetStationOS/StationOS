package producer_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/station"
)

type scheduleCorrectionModel struct {
	t                           *testing.T
	n                           int
	station, playlist, dj, show string
}

func (m *scheduleCorrectionModel) Respond(_ context.Context, _, _, _ string, input []producer.Item, _ []producer.Tool) (producer.Response, error) {
	n := m.n
	m.n++
	switch n {
	case 0:
		return responseCall(n, "save_show", map[string]any{"station_id": m.station, "name": "Saturday Soirée", "dj_id": m.dj, "playlist_id": m.playlist, "day": 6, "start": 1080, "end": 1320}), nil
	case 1:
		m.show = resultID(input)
		return producer.Response{Text: "Saturday Soirée is ready on Saturday."}, nil
	case 2, 4:
		var message struct{ Role, Content string }
		if err := json.Unmarshal(input[len(input)-1], &message); err != nil {
			m.t.Fatal(err)
		}
		want := "Sunday"
		if n == 4 {
			want = "Saturday"
		}
		if message.Role != "developer" || !strings.Contains(message.Content, "Mandatory schedule readback") || !strings.Contains(message.Content, `"day_name":"`+want+`"`) || !strings.Contains(message.Content, m.show) {
			m.t.Errorf("missing measured %s schedule: %s", want, message.Content)
		}
		if n == 2 {
			return responseCall(n, "save_show", map[string]any{"station_id": m.station, "id": m.show, "day": 5}), nil
		}
	}
	return producer.Response{Text: "Verified Saturday Soirée on Saturday, 18:00–22:00."}, nil
}

func TestScheduleCompletionReadsBackAndAllowsCorrectionOfWrongWeekendDay(t *testing.T) {
	ctx := context.Background()
	store, registry, _, _, shows, _ := fixture(t)
	owner := call(t, registry, "save_station", map[string]string{"name": "Velvet Notes"}).Data.(station.Station)
	pool := call(t, registry, "save_playlist", map[string]string{"station_id": owner.ID, "name": "Saturday Soirée"}).Data.(playlists.Playlist)
	dj := call(t, registry, "save_dj", map[string]string{"name": "Julian Vance"}).Data.(djs.Profile)
	if err := store.SaveSettings(ctx, "test-model", producer.KeyChange{Value: "test-key"}); err != nil {
		t.Fatal(err)
	}
	model := &scheduleCorrectionModel{t: t, station: owner.ID, playlist: pool.ID, dj: dj.ID}
	svc, err := producer.New(ctx, store, registry, model)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	id, err := svc.Create(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, id, producer.ID(), "Schedule Saturday Soirée on Saturday 18:00–22:00."); err != nil {
		t.Fatal(err)
	}
	conversation := waitDone(t, store, id)
	saved, err := shows.List(ctx, owner.ID)
	if err != nil || len(saved) != 1 || saved[0].Day != 5 || model.n != 5 || conversation.Status != "idle" {
		t.Fatal("schedule was not corrected and reverified", saved, err, model.n, conversation.Status)
	}
	for _, message := range conversation.Messages {
		if message.Role == "assistant" && strings.Contains(message.Text, "ready on Saturday") {
			t.Fatal("premature completion was published")
		}
	}
}
