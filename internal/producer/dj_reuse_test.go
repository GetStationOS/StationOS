package producer_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/station"
)

func TestAssistantRejectsDuplicateDJIdentities(t *testing.T) {
	_, registry, stations, _, _, repo := fixture(t)
	ctx := context.Background()
	if err := stations.Save(ctx, station.Station{ID: "listening-room", Name: "The Listening Room", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	profiles, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	existingID := ""
	for _, profile := range profiles {
		if profile.Name == "Clara Vale" {
			existingID = profile.ID
			break
		}
	}
	original, err := (djs.Service{Repo: repo}).Save(ctx, djs.Profile{ID: existingID, Name: "Clara Vale", Model: "model", Background: "Classical pianist and late-night presenter", Vibe: "Warm and reflective", Prompt: "Present thoughtful classical programmes"}, djs.KeyChange{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := repo.List(ctx)
	for _, name := range []string{"Clara Vale", "  CLARA   VALE ", "Clara Vale - The Listening Room", "Clara Vale — The Listening Room", "Clara Vale – The Listening Room", "Clara Vale (The Listening Room)"} {
		raw, _ := json.Marshal(map[string]string{"name": name, "prompt": "Unrequested replacement"})
		result := registry.Call(ctx, "save_dj", raw)
		if result.Error == "" || !strings.Contains(result.Error, original.ID) {
			t.Fatalf("accepted or failed to identify duplicate %q: %+v", name, result)
		}
	}
	after, _ := repo.List(ctx)
	stored, _ := repo.Get(ctx, original.ID)
	if len(after) != len(before) || stored.Prompt != original.Prompt || stored.Model != original.Model {
		t.Fatal("duplicate attempts changed shared profiles", after, stored)
	}
	summary := call(t, registry, "list_djs", map[string]any{}).Data.([]djs.Profile)
	found := false
	for _, profile := range summary {
		if profile.ID == original.ID {
			found = profile.Background == original.Background && profile.Vibe == original.Vibe
		}
	}
	if !found {
		t.Fatal("suitability information missing", summary)
	}
	newDJ := call(t, registry, "save_dj", map[string]string{"name": "Elena Brooks", "background": "Electronic music journalist"}).Data.(djs.Profile)
	if newDJ.ID == original.ID {
		t.Fatal("original presenter replaced existing DJ")
	}
	raw, _ := json.Marshal(map[string]string{"id": newDJ.ID, "name": "Clara Vale"})
	if registry.Call(ctx, "save_dj", raw).Error == "" {
		t.Fatal("rename created duplicate")
	}
	// Editing other fields preserves legacy profiles without requiring a rename.
	call(t, registry, "save_dj", map[string]string{"id": original.ID, "vibe": "Quiet and conversational"})
}

func TestAssistantDJsRemainSharedAcrossStations(t *testing.T) {
	_, registry, _, _, shows, repo := fixture(t)
	ctx := context.Background()
	presenter := call(t, registry, "save_dj", map[string]string{"name": "Sofia Grant"}).Data.(djs.Profile)
	before, _ := repo.List(ctx)
	for _, name := range []string{"Jazz Room", "Night Radio"} {
		st := call(t, registry, "save_station", map[string]string{"name": name}).Data.(station.Station)
		playlist := call(t, registry, "save_playlist", map[string]string{"station_id": st.ID, "name": "Evening pool"})
		b, _ := json.Marshal(playlist.Data)
		var pool struct{ ID string }
		_ = json.Unmarshal(b, &pool)
		call(t, registry, "save_show", map[string]any{"station_id": st.ID, "name": "Evening", "dj_id": presenter.ID, "playlist_id": pool.ID, "day": 0, "start": 1200, "end": 1320})
		scheduled, err := shows.List(ctx, st.ID)
		if err != nil || len(scheduled) != 1 || scheduled[0].DJID != presenter.ID {
			t.Fatal(scheduled, err)
		}
		raw, _ := json.Marshal(map[string]string{"name": "New Presenter - " + name})
		if registry.Call(ctx, "save_dj", raw).Error == "" {
			t.Fatal("accepted station-specific DJ name")
		}
	}
	after, _ := repo.List(ctx)
	if len(after) != len(before) {
		t.Fatal("station assignment cloned DJ", after)
	}
}

func TestConcurrentAssistantDJCreationsCannotDuplicate(t *testing.T) {
	_, registry, _, _, _, repo := fixture(t)
	ctx := context.Background()
	before, _ := repo.List(ctx)
	var wg sync.WaitGroup
	results := make(chan producer.Result, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- registry.Call(ctx, "save_dj", []byte(`{"name":"Arden West"}`))
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result.Error == "" {
			successes++
		}
	}
	after, _ := repo.List(ctx)
	if successes != 1 || len(after) != len(before)+1 {
		t.Fatal(successes, after)
	}
}
