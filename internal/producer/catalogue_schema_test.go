package producer_test

import (
	"context"
	"encoding/json"
	"testing"
)

// Gemma confused singular playlist rule kinds with plural discovery facets.
// Ensure every advertised facet is accepted by the dispatcher as well.
func TestMusicDiscoverySchemaAdvertisesSupportedFacets(t *testing.T) {
	_, registry, _, _, _, _ := fixture(t)
	tool, ok := registry.Find("explore_music")
	if !ok {
		t.Fatal("missing discovery tool")
	}
	raw, err := json.Marshal(tool.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required   []string
		Properties map[string]struct{ Enum []string }
	}
	if err = json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "kind" {
		t.Fatal("discovery must require kind", schema.Required)
	}
	facets := schema.Properties["kind"].Enum
	if len(facets) != 6 {
		t.Fatal("missing supported facets", facets)
	}
	for _, kind := range facets {
		t.Run(kind, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"kind": kind, "limit": 5})
			if result := registry.Call(context.Background(), tool.Name, args); result.Error != "" {
				t.Fatal("advertised facet rejected", result.Error)
			}
		})
	}
	for _, kind := range []string{"", "genre", "artist", "unknown"} {
		args, _ := json.Marshal(map[string]string{"kind": kind})
		if result := registry.Call(context.Background(), tool.Name, args); result.Error == "" {
			t.Fatal("invalid facet accepted", kind)
		}
	}
}
