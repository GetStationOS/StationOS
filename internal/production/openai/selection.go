package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"stationos.local/stationos/internal/prompts"
	"strings"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/production"
)

// SelectTrack chooses one supplied candidate without generating speech or a queue.
func (p Producer) SelectTrack(parent context.Context, request production.SelectionRequest) (string, error) {
	if err := parent.Err(); err != nil {
		return "", err
	}
	if len(request.Candidates) == 0 || len(request.Candidates) > 10 || request.Show.DJ.ID == "" {
		return "", production.ErrUnavailable
	}
	type candidate struct {
		ID string
		songMetadata
	}
	candidates := []candidate{}
	ids := make([]string, 0, len(request.Candidates))
	references := map[string]string{}
	seen := map[string]bool{}
	for i, item := range request.Candidates {
		if item.ID == "" || seen[item.ID] {
			return "", errors.New("invalid curation candidates")
		}
		seen[item.ID] = true
		ref := fmt.Sprintf("t%02d", i+1)
		ids = append(ids, ref)
		references[ref] = item.ID
		candidates = append(candidates, candidate{ref, songDetails(item)})
	}
	if len(candidates) == 1 {
		return request.Candidates[0].ID, nil
	}
	budget, err := p.textBudget(parent, "curation", 0)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(parent, budget+production.Timeout)
	defer cancel()
	model, err := p.model(ctx, "curation", request.Show.DJ.Model)
	if err != nil {
		return "", err
	}
	kind := "openai"
	if p.Text != nil {
		kind, err = p.Text.Kind(ctx, "curation")
		if err != nil {
			return "", err
		}
	}
	input, _ := json.Marshal(map[string]any{"episode_session": p.episodeContext(ctx, request.Show), "musical_direction": request.Show.MusicDirection, "show": request.Show.Title, "dj": request.Show.DJ.Name, "fictional_background": request.Show.DJ.Background, "current": songDetails(request.Current), "remaining_seconds": request.RemainingSeconds, "tracks": candidates})
	basePrompt, err := prompts.Load(ctx, p.Prompts, "selection")
	if err != nil {
		return "", err
	}
	background, err := prompts.Load(ctx, p.Prompts, "background")
	if err != nil {
		return "", err
	}
	instructions := basePrompt + "DJ prompt:\n" + request.Show.DJ.Prompt + background
	var ref string
	if kind == "openai" && model == "gpt-6-luna" {
		ref, err = p.decideTrack(ctx, request.Show.DJ.ID, model, string(input), instructions, ids)
	} else {
		schema, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"track_id"}, "properties": map[string]any{"track_id": map[string]any{"type": "string", "enum": ids}}})
		var raw string
		if p.Text != nil {
			raw, err = p.Text.Generate(ctx, ai.TextRequest{Role: "curation", Model: model, Instructions: instructions + " Return JSON containing track_id only.", Input: string(input), Schema: schema, Options: ai.GenerationOptions{DisableThinking: true, MaxOutputTokens: 128}})
		} else {
			key, e := p.Keys.APIKey(ctx, request.Show.DJ.ID)
			if e != nil || key == "" {
				return "", production.ErrUnavailable
			}
			body := map[string]any{"model": model, "instructions": instructions + " Return JSON containing track_id only.", "input": string(input), "max_output_tokens": 128, "store": false, "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "next_track", "strict": true, "schema": json.RawMessage(schema)}}}
			var data []byte
			data, err = p.textCall(ctx, key, body, 1<<20)
			if err == nil {
				raw, err = completedText(data)
			}
		}
		if err == nil {
			var answer struct {
				ID string `json:"track_id"`
			}
			decoder := json.NewDecoder(strings.NewReader(raw))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&answer) != nil || decoder.Decode(new(any)) != io.EOF {
				return "", errors.New("invalid next-track response")
			}
			ref = answer.ID
		}
	}
	if err != nil {
		return "", err
	}
	id, ok := references[ref]
	if !ok {
		return "", errors.New("DJ selected an unoffered track")
	}
	return id, nil
}

func (p Producer) decideTrack(ctx context.Context, djID, model, input, instructions string, ids []string) (string, error) {
	key, err := p.Keys.APIKey(ctx, djID)
	// Selection uses the curation connection, independently of speech credentials.
	if store, ok := p.Models.(ai.Store); ok {
		routes, e := store.Routes(ctx)
		if e != nil {
			return "", e
		}
		key, err = store.Credential(ctx, routes.Curation.Connection)
	}
	if err != nil || key == "" {
		return "", production.ErrUnavailable
	}
	choices := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		choices = append(choices, map[string]any{"value": id})
	}
	raw, err := p.call(ctx, "decisions", key, map[string]any{"model": model, "input": input, "questions": []any{map[string]any{"type": "choice", "name": "next_track", "instructions": instructions, "choices": choices}}}, 1<<20)
	if err != nil {
		return "", err
	}
	var response struct {
		Answers []struct{ Type, Name, Choice string }
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Answers) != 1 {
		return "", errors.New("invalid next-track decision")
	}
	answer := response.Answers[0]
	if answer.Type != "choice" || answer.Name != "next_track" {
		return "", errors.New("next-track decision unavailable")
	}
	return answer.Choice, nil
}

// Only recording metadata is shared with providers, never source URLs or artwork.
type songMetadata struct {
	Title, Performers, Composer, AlbumArtist, Conductor, Creator, Album, Genre string
	Seconds                                                                    int64
}

func songDetails(a content.AudioItem) songMetadata {
	v := songMetadata{Title: short(a.Title, 300), Performers: short(a.Artist, 400), Composer: short(a.Composer, 400), AlbumArtist: short(a.AlbumArtist, 400), Conductor: short(a.Conductor, 400), Creator: short(a.Creator, 400), Album: short(a.Album, 200), Genre: short(a.Genre, 100)}
	if a.Duration != nil {
		v.Seconds = int64(a.Duration.Seconds())
	}
	return v
}

func completedText(raw []byte) (string, error) {
	var response struct {
		Status string
		Output []struct{ Content []struct{ Type, Text string } }
	}
	if json.Unmarshal(raw, &response) != nil || response.Status != "completed" {
		return "", errors.New("next-track response incomplete")
	}
	var text strings.Builder
	for _, output := range response.Output {
		for _, c := range output.Content {
			if c.Type == "output_text" {
				text.WriteString(c.Text)
			}
		}
	}
	return text.String(), nil
}
