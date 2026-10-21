package persistence

import (
	"context"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/station"
	"testing"
	"time"
)

func TestCachedSpeechReceiptsSurviveStorageAndLegacyRows(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := (station.Service{Repo: Stations{DB: db}}).Save(ctx, station.Station{Name: "Speech", Timezone: "Europe/London", Locale: "en-GB"})
	if err != nil {
		t.Fatal(err)
	}
	store := PresenterAssets{DB: db}
	meta := production.SpeechProvenance{Provider: "elevenlabs", Connection: "elena", Model: "eleven_multilingual_v2", Voice: "original-voice"}
	want := production.Prepared{Plan: production.Plan{IntroText: "Hello", OutroText: "Goodbye"}, Intro: []byte("intro"), Outro: []byte("outro"), IntroSpeech: meta, OutroSpeech: meta}
	if err = store.Put(ctx, "key", st.ID, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "key")
	if err != nil || got.IntroSpeech != meta || got.OutroSpeech != meta || got.Plan != want.Plan || string(got.Intro) != "intro" {
		t.Fatal(got, err)
	}
	history := Operations{DB: db}
	event := operations.Event{ID: "speech", StationID: st.ID, Kind: "speech", Started: time.Now(), SpeechProvider: got.IntroSpeech.Provider, SpeechConnection: got.IntroSpeech.Connection, SpeechModel: got.IntroSpeech.Model, SpeechVoice: got.IntroSpeech.Voice}
	if err = history.Save(ctx, event); err != nil {
		t.Fatal(err)
	}
	events, err := history.Events(ctx, operations.Filter{StationID: st.ID, Limit: 10})
	if err != nil || len(events) != 1 || events[0].SpeechVoice != meta.Voice || events[0].SpeechModel != meta.Model {
		t.Fatal(events, err)
	}
	if _, err = db.Exec(`UPDATE presenter_assets SET plan='{"intro":"Legacy intro","outro":"Legacy outro"}' WHERE cache_key='key'`); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, "key")
	if err != nil || got.IntroSpeech != (production.SpeechProvenance{}) || got.OutroSpeech != (production.SpeechProvenance{}) || got.Plan.IntroText != "Legacy intro" {
		t.Fatal(got, err)
	}
}
