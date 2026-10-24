package producer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"stationos.local/stationos/internal/station"
)

type workStep struct {
	Area        string `json:"area"`
	StationID   string `json:"station_id"`
	StationName string `json:"station_name"`
}

func inspectionTool() Tool {
	return Tool{Name: "inspect_station", Description: "Inspect a station's saved settings, playlists and weekly schedule. Supply station_id or exact station_name; omit both to use the current station. Returns measured configuration for comparison with the brief. Intentional gaps and unrelated empty pools do not require changes. This read does not change configuration or tool access.", Parameters: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"station_id": map[string]any{"type": "string"}, "station_name": map[string]any{"type": "string"}}}}
}

func (s *Service) resolveWorkStep(ctx context.Context, step workStep, c Conversation) (station.Station, error) {
	switch step.Area {
	case "station", "playlists", "schedule", "presenters", "sources", "audience", "descriptions", "review":
	default:
		return station.Station{}, errors.New("Choose one offered work area")
	}
	id := strings.TrimSpace(step.StationID)
	name := strings.TrimSpace(step.StationName)
	if id == "" && name == "" {
		id = c.StationID
	}
	var result Result
	if id != "" {
		raw, _ := json.Marshal(entityID{ID: id})
		result = s.Tools.call(ctx, "get_station", raw, false)
		if result.Error != "" {
			return station.Station{}, errors.New(result.Error)
		}
		value, ok := result.Data.(station.Station)
		if !ok {
			return station.Station{}, errors.New("Station lookup returned invalid data")
		}
		if name != "" && !strings.EqualFold(name, value.Name) {
			return station.Station{}, errors.New("station_id and station_name refer to different stations; use the exact requested name")
		}
		return value, nil
	}
	if name != "" {
		result = s.Tools.call(ctx, "list_stations", []byte(`{}`), false)
		if result.Error != "" {
			return station.Station{}, errors.New(result.Error)
		}
		values, ok := result.Data.([]station.Station)
		if !ok {
			return station.Station{}, errors.New("Station lookup returned invalid data")
		}
		matches := []station.Station{}
		for _, value := range values {
			if strings.EqualFold(name, value.Name) {
				matches = append(matches, value)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return station.Station{}, errors.New("More than one station has that name; supply an existing station_id")
		}
		if step.Area == "station" {
			return station.Station{Name: name}, nil
		}
		return station.Station{}, errors.New("No station has that exact name; use the name in the human request or an existing station_id. No station was created")
	}
	if step.Area == "playlists" || step.Area == "schedule" || step.Area == "audience" || step.Area == "descriptions" {
		return station.Station{}, errors.New("Supply station_name from the human request or an existing station_id for this work area")
	}
	return station.Station{}, nil
}
