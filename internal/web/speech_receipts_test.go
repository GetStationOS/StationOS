package web

import (
	"context"
	"encoding/csv"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/persistence"
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
	now := time.Now().Add(-time.Minute)
	for _, event := range []operations.Event{
		{ID: "new", StationID: st.ID, Kind: "speech", Outcome: "completed", Started: now, Ended: now.Add(time.Second), Title: "DJ link", DJ: "Elena", Speech: "Good evening", SpeechProvider: "chatterbox", SpeechConnection: "nano", SpeechModel: "chatterbox-nano", SpeechVoice: "Elena <British>"},
		{ID: "old", StationID: st.ID, Kind: "speech", Outcome: "completed", Started: now.Add(-time.Minute), Title: "Legacy speech"},
	} {
		if err = store.Save(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(Dependencies{Stations: stations, Operations: store})
	if err != nil {
		t.Fatal(err)
	}
	path := "/producer/stations/" + st.ID + "/timeline"
	for _, headers := range []map[string]string{nil, {"HX-Request": "true"}} {
		w := request(h, "GET", path, nil, headers)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "Model: chatterbox-nano") || !strings.Contains(body, "Voice: Elena &lt;British&gt;") || !strings.Contains(body, "Speech model and voice: Not recorded") || strings.Contains(body, "Elena <British>") {
			t.Fatal(w.Code, body)
		}
	}
	w := request(h, "GET", "/producer/stations/"+st.ID+"/history.csv", nil, nil)
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil || len(rows) != 3 || rows[0][16] != "Speech model" || rows[1][16] != "chatterbox-nano" || rows[1][17] != "Elena <British>" {
		t.Fatal(rows, err)
	}
}
