package producer_test

import (
	"context"
	"encoding/json"
	"testing"

	"stationos.local/stationos/internal/station"
)

func TestStationToolAdvertisesVisibilityAndRejectsInvalidValues(t *testing.T) {
	_, r, repo, _, _, _ := fixture(t)
	tool, ok := r.Find("save_station")
	if !ok {
		t.Fatal("missing station tool")
	}
	raw, err := json.Marshal(tool.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	var definition struct {
		Properties map[string]struct{ Enum []string }
	}
	if err := json.Unmarshal(raw, &definition); err != nil {
		t.Fatal(err)
	}
	choices := definition.Properties["visibility"].Enum
	if len(choices) != 3 || choices[0] != "listed" || choices[1] != "unlisted" || choices[2] != "private" {
		t.Fatalf("visibility choices: %v", choices)
	}
	ctx := context.Background()
	bad := r.Call(ctx, "save_station", json.RawMessage(`{"name":"Jazz","visibility":"public"}`))
	if bad.Error == "" {
		t.Fatal("invalid visibility accepted")
	}
	stations, err := repo.List(ctx)
	if err != nil || len(stations) != 0 {
		t.Fatalf("failed attempt saved station: %v, %v", stations, err)
	}
	created := call(t, r, "save_station", map[string]string{"name": "Jazz"}).Data.(station.Station)
	if created.Publication() != station.Unlisted {
		t.Fatal("new station must default to unlisted")
	}
	for _, visibility := range choices {
		updated := call(t, r, "save_station", map[string]string{"id": created.ID, "visibility": visibility}).Data.(station.Station)
		if string(updated.Publication()) != visibility {
			t.Fatalf("visibility not saved: %v", updated)
		}
	}
	stations, err = repo.List(ctx)
	if err != nil || len(stations) != 1 {
		t.Fatalf("recovery duplicated station: %v, %v", stations, err)
	}
}
