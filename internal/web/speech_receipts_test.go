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
		{ID: "eleven", StationID: st.ID, Kind: "speech", Outcome: "completed", Started: now.Add(-time.Second), SpeechProvider: "elevenlabs", SpeechVoice: "jKCAjEfHkR2aEl8KQM96", SpeechVoiceName: "Elena <Vance>"},
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
		if !strings.Contains(body, "Voice: Elena &lt;Vance&gt;") || strings.Contains(body, "jKCAjEfHkR2aEl8KQM96") || w.Code != 200 || !strings.Contains(body, "Model: chatterbox-nano") || !strings.Contains(body, "Voice: Elena &lt;British&gt;") || !strings.Contains(body, "Speech model and voice: Not recorded") || strings.Contains(body, "Elena <British>") || !strings.Contains(body, "Provider: Chatterbox ·") || strings.Contains(body, "08487a14774a0ef09e4b3cca55a28388") {
			t.Fatal(w.Code, body)
		}
	}
	w := request(h, "GET", "/producer/stations/"+st.ID+"/history.csv", nil, nil)
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil || len(rows) != 4 || rows[0][16] != "Speech model" || rows[1][16] != "chatterbox-nano" || rows[1][17] != "Elena <British>" {
		t.Fatal(rows, err)
	}
}

func TestTimelineTranscriptBelongsToPreparedEntry(t *testing.T) {
	ctx := context.Background()
	_, stations, _, _ := setupWeb(t, "")
	st, err := (station.Service{Repo: stations}).Save(ctx, station.Station{Name: "Prepared Radio", Timezone: "Europe/London", Locale: "en-GB"})
	if err != nil {
		t.Fatal(err)
	}
	store := persistence.Operations{DB: stations.DB}
	now := time.Now().Add(-time.Minute)
	speech := "Here is River. <script>untrusted</script>"
	prepared := operations.Event{ID: "prepared", Phase: "speech-preparation", StationID: st.ID, Kind: "recovery", Recover: true, IncidentCode: "speech", Outcome: "observed", Started: now, Ended: now, Speech: speech, SpeechProvider: "chatterbox", SpeechProviderName: "Studio <Mac>", SpeechConnection: "studio-mac", SpeechModel: "nano", SpeechVoiceName: "Alex", Reason: "DJ speech preparation recovered"}
	aired := prepared
	aired.ID = "aired"
	aired.Kind = "speech"
	aired.Phase = "chatter"
	aired.Recover = false
	aired.IncidentCode = ""
	aired.Outcome = "completed"
	aired.Started = now.Add(time.Second)
	aired.Ended = now.Add(2 * time.Second)
	legacy := operations.Event{ID: "legacy-prepared", Phase: "speech-preparation", StationID: st.ID, Kind: "recovery", Recover: true, IncidentCode: "speech", Outcome: "observed", Started: now.Add(-time.Second)}
	for _, e := range []operations.Event{prepared, aired, legacy} {
		if err = store.Save(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.Events(ctx, operations.Filter{StationID: st.ID, Limit: 10})
	if err != nil || len(events) != 3 {
		t.Fatal("preparation without a prior failure was lost", events, err)
	}
	h, err := New(Dependencies{Stations: stations, Operations: store})
	if err != nil {
		t.Fatal(err)
	}
	for _, headers := range []map[string]string{nil, {"HX-Request": "true"}} {
		w := request(h, "GET", "/producer/stations/"+st.ID+"/timeline", nil, headers)
		body := w.Body.String()
		row := func(id string) string {
			parts := strings.SplitN(body, `id="event-`+id+`"`, 2)
			if len(parts) != 2 {
				t.Fatal("missing row", id, body)
			}
			return strings.SplitN(parts[1], "</tr>", 2)[0]
		}
		prep, air, old := row("prepared"), row("aired"), row("legacy-prepared")
		for _, want := range []string{"Here is River. &lt;script&gt;", "Provider: Chatterbox", `href="/producer/assistant/endpoints?connection=studio-mac"`, "Studio &lt;Mac&gt;", "Model: nano", "Voice: Alex"} {
			if !strings.Contains(prep, want) {
				t.Fatal("missing prepared detail", want, prep)
			}
		}
		if strings.Contains(air, "Here is River") || !strings.Contains(air, "Model: nano") || strings.Count(body, "Here is River") != 1 || strings.Contains(body, "<script>untrusted") {
			t.Fatal("transcript duplicated or not escaped", body)
		}
		if !strings.Contains(old, "Transcript: Not recorded") || !strings.Contains(old, "Endpoint: Not recorded") {
			t.Fatal("legacy details fabricated", old)
		}
	}
	// Moving display content must not discard durable aired transcripts or exports.
	w := request(h, "GET", "/producer/stations/"+st.ID+"/history.csv", nil, nil)
	if !strings.Contains(w.Body.String(), speech) {
		t.Fatal("historical transcript removed from export")
	}
}
