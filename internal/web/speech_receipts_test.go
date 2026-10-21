package web

import (
	"context"
	"encoding/csv"
	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
	"time"
)

func TestTimelineShowsImmutableSpeechReceiptsAndUnknownHistory(t *testing.T) {
	ctx := context.Background()
	_, stations, _, _ := setupWeb(t, "")
	st, err := (station.Service{Repo: stations}).Save(ctx, station.Station{Name: "Receipt Radio", Timezone: "Europe/London", Locale: "en-GB"})
	if err != nil {
		t.Fatal(err)
	}
	store := persistence.Operations{DB: stations.DB}
	repo, _ := persistence.NewDJs(stations.DB, make([]byte, 32))
	providers := persistence.NewProducer(repo)
	if err = providers.SaveConnection(ctx, ai.Connection{ID: "08487a14774a0ef09e4b3cca55a28388", Name: "Chatterbox", Type: "chatterbox", Address: "http://server:8000", ContextTokens: 8192, OutputTokens: 2048}, ai.KeyChange{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Minute)
	for _, event := range []operations.Event{
		{ID: "new", StationID: st.ID, Kind: "speech", Outcome: "completed", Started: now, Ended: now.Add(time.Second), Title: "DJ link", DJ: "Elena", Speech: "Good evening", SpeechProvider: "chatterbox", SpeechConnection: "08487a14774a0ef09e4b3cca55a28388", SpeechModel: "chatterbox-nano", SpeechVoice: "Elena <British>"},
		{ID: "old", StationID: st.ID, Kind: "speech", Outcome: "completed", Started: now.Add(-time.Minute), Title: "Legacy speech"},
	} {
		if err = store.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(Dependencies{Stations: stations, Operations: store, Assistant: &producer.Service{Store: providers}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/producer/stations/" + st.ID + "/timeline"
	for _, headers := range []map[string]string{nil, {"HX-Request": "true"}} {
		w := request(h, "GET", path, nil, headers)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "Model: chatterbox-nano") || !strings.Contains(body, "Voice: Elena &lt;British&gt;") || !strings.Contains(body, "Speech model and voice: Not recorded") || strings.Contains(body, "Elena <British>") || !strings.Contains(body, "Provider: Chatterbox ·") || strings.Contains(body, "08487a14774a0ef09e4b3cca55a28388") {
			t.Fatal(w.Code, body)
		}
	}
	w := request(h, "GET", "/producer/stations/"+st.ID+"/history.csv", nil, nil)
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil || len(rows) != 3 || rows[0][16] != "Speech model" || rows[1][16] != "chatterbox-nano" || rows[1][17] != "Elena <British>" {
		t.Fatal(rows, err)
	}
}
