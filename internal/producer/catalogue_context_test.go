package producer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSummaryReleasesOnlyEarlierCataloguePages(t *testing.T) {
	page := func(title string) Item {
		return toolOutput("page", Result{Data: map[string]any{"catalogue_page": true, "items": []string{title}, "total": 100000}})
	}
	input := []Item{mustJSON(map[string]string{"role": "user", "content": "Create Velvet Notes"}), page("OLD_DETAIL"), toolOutput("mutation", Result{Summary: "Created station", Data: map[string]string{"ID": "station-kept"}}), toolOutput("summary", Result{Data: map[string]any{"catalogue_summary": true, "Tracks": 100000}}), page("NEW_TARGETED_RECORDING")}
	raw, _ := json.Marshal(releaseCatalogueDetails(input))
	if strings.Contains(string(raw), "OLD_DETAIL") || !strings.Contains(string(raw), "NEW_TARGETED_RECORDING") || !strings.Contains(string(raw), "station-kept") || !strings.Contains(string(raw), "Create Velvet Notes") {
		t.Fatal(string(raw))
	}
	// Failed summaries cannot invalidate details that are still needed.
	input[3] = toolOutput("summary", Result{Error: "unavailable"})
	raw, _ = json.Marshal(releaseCatalogueDetails(input))
	if !strings.Contains(string(raw), "OLD_DETAIL") {
		t.Fatal("failed summary dropped data")
	}
}
func TestCheckpointKeepsApprovedPlanAheadOfOversizedResults(t *testing.T) {
	plan := "Weekdays 08:00–22:00; Sunday Sanctuary 10:00–14:00"
	input := []Item{mustJSON(map[string]string{"role": "user", "content": "Create Velvet Notes"}), mustJSON(map[string]string{"role": "assistant", "content": plan}), mustJSON(map[string]string{"role": "user", "content": "Yes, please proceed"}), toolOutput("large", Result{Data: strings.Repeat("catalogue ", 30000)})}
	raw, _ := json.Marshal(compactHistory(input))
	if !strings.Contains(string(raw), "Sunday Sanctuary") || !strings.Contains(string(raw), "Yes, please proceed") {
		t.Fatal("checkpoint discarded approval context")
	}
}
