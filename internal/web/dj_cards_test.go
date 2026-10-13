package web

import (
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
	"time"
)

func TestPresenterCardStatusAndAssignments(t *testing.T) {
	now := time.Date(2026, 10, 13, 15, 0, 0, 0, time.UTC)
	p := djs.Profile{ID: "miles", Name: "Miles", Model: "configured-model", Voice: "cedar", HasKey: true, Background: "A jazz presenter.", ConversationSpeed: 1}
	card := presenterCard(p, nil, now)
	if card.Status != "Ready" || card.Summary != p.Background || card.Voice != "Cedar" {
		t.Fatalf("ready card: %+v", card)
	}
	stations := []djStation{{Station: station.Station{ID: "one", Name: "First", Timezone: "Europe/London"}, Shows: []schedule.Show{
		{ID: "a", Name: "Jazz Afternoon", DJID: "miles", Day: 2, Start: 720, End: 960},
		{ID: "b", Name: "Jazz Afternoon", DJID: "miles", Day: 3, Start: 720, End: 960},
		{ID: "c", Name: "Other DJ", DJID: "alex", Day: 1, Start: 1020, End: 1080},
	}}, {Station: station.Station{ID: "two", Name: "Second", Timezone: "UTC"}, Shows: []schedule.Show{{ID: "d", Name: "Jazz Afternoon", DJID: "miles", Day: 1, Start: 1020, End: 1080}}}}
	card = presenterCard(p, stations, now)
	if card.Status != "Scheduled" || len(card.Assignments) != 2 || card.Next != "Tue 13 Oct, 17:00–18:00 UTC" || card.NextShow != "Jazz Afternoon · Second" {
		t.Fatalf("assignments: %+v", card)
	}
	stations[0].State.Programme.DJ = p
	stations[0].State.Status = "Dormant"
	card = presenterCard(p, stations, now)
	if card.Status == "On air" {
		t.Fatal("dormant scheduled profile called on air")
	}
	stations[0].State.Status = "Broadcasting"
	card = presenterCard(p, stations, now)
	if card.Status == "On air" {
		t.Fatal("no playing track called on air")
	}
	stations[0].State.Current.ID = "playing"
	card = presenterCard(p, stations, now)
	if card.Status != "On air" || card.LiveStation != "First" {
		t.Fatalf("live: %+v", card)
	}
	p.HasKey = false
	card = presenterCard(p, nil, now)
	if card.Status != "Draft" || !strings.Contains(card.SetupNote, "API key") {
		t.Fatalf("draft: %+v", card)
	}
	card = presenterCard(p, stations, now)
	if card.Status != "On air" || card.SetupNote == "" {
		t.Fatal("active broadcast or setup warning hidden")
	}
}

func TestPresenterCardNextAiringUsesCivilTime(t *testing.T) {
	p := djs.Profile{ID: "dj", HasKey: true, Model: "model", Voice: "cedar"}
	st := djStation{Station: station.Station{ID: "s", Name: "Station", Timezone: "Europe/London"}, Shows: []schedule.Show{{DJID: "dj", Name: "Morning", Day: 0, Start: 540, End: 600}}}
	// After this week's start, next Monday crosses the autumn clock change.
	card := presenterCard(p, []djStation{st}, time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC))
	if card.Next != "Mon 26 Oct, 09:00–10:00 GMT" {
		t.Fatal(card.Next)
	}
	st.Shows[0] = schedule.Show{DJID: "dj", Name: "Overnight", Day: 1, Start: 1380, End: 120}
	card = presenterCard(p, []djStation{st}, time.Date(2026, 10, 13, 15, 0, 0, 0, time.UTC))
	if card.Next != "Tue 13 Oct, 23:00–Wed 14 Oct, 02:00 BST" {
		t.Fatal(card.Next)
	}
	// UTF-8 profile excerpts retain whole characters and omit editorial prompts.
	p.Background = strings.Repeat("♫", 181)
	p.Prompt = "private editorial instructions"
	card = presenterCard(p, nil, time.Now())
	if len([]rune(card.Summary)) != 181 || strings.Contains(card.Summary, p.Prompt) {
		t.Fatal(card.Summary)
	}
}
