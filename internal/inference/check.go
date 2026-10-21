package inference

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/production/chatterbox"
)

type Check struct {
	Reachable, ModelAvailable, Tools, Structured bool
	Models                                       []string
	Message                                      string
}

// Discovery never pulls or substitutes models. Capability tests require an
// explicit producer action and may load the selected model or incur API usage.
func (c *Client) Check(parent context.Context, conn ai.Connection, key, model string, capability bool) (Check, error) {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	var out Check
	if conn.Type == "elevenlabs" {
		if key == "" {
			return out, errors.New("Enter an ElevenLabs API key with Models, Voices and Text to Speech access")
		}
		models, err := c.elevenModels(ctx, conn, key)
		out = Check{Reachable: err == nil, Models: models, Message: "ElevenLabs speech models retrieved; no speech was generated."}
		for _, id := range models {
			if model == "" || id == model {
				out.ModelAvailable = true
			}
		}
		return out, err
	}
	if conn.Type == "chatterbox" {
		catalog, err := (chatterbox.Provider{HTTP: c.HTTP}).Models(ctx, conn)
		if err != nil {
			return out, err
		}
		out = Check{Reachable: true, Message: "Chatterbox speech models retrieved; no speech was generated."}
		if model == "chatterbox-nano" {
			model = "nano"
		}
		for _, m := range catalog {
			out.Models = append(out.Models, m.ID)
			if model == "" || model == m.ID {
				out.ModelAvailable = true
			}
		}
		return out, nil
	}

	path := "/models"
	if conn.Type == "gemini" {
		path = "/models?pageSize=200"
	}
	if conn.Type == "ollama" {
		path = "/api/tags"
	}
	raw, err := c.request(ctx, conn, key, "GET", path, nil)
	if err != nil {
		return out, err
	}
	var v struct {
		Models []struct{ Name string }
		Data   []struct{ ID string }
	}
	out.Reachable = true
	if json.Unmarshal(raw, &v) != nil || ((conn.Type == "ollama" || conn.Type == "gemini") && v.Models == nil) || ((conn.Type != "ollama" && conn.Type != "gemini") && v.Data == nil) {
		return out, errors.New("Invalid provider model listing")
	}
	out.Reachable = true
	for _, m := range v.Models {
		if len(out.Models) < 200 {
			out.Models = append(out.Models, m.Name)
		}
		if m.Name == model || strings.TrimPrefix(m.Name, "models/") == model {
			out.ModelAvailable = true
		}
	}
	for _, m := range v.Data {
		if len(out.Models) < 200 {
			out.Models = append(out.Models, m.ID)
		}
		if m.ID == model {
			out.ModelAvailable = true
		}
	}
	out.Message = "Server reachable. Model listing retrieved; no model was changed or downloaded."
	if !capability {
		return out, nil
	}
	if !ai.ValidModel(model) {
		return out, errors.New("Enter the exact model ID for a capability test")
	}
	if conn.Type == "openai" {
		return out, errors.New("OpenAI capability tests use normal assistant execution; discovery does not prove tools or structured output")
	}
	tool := producer.Tool{Name: "connection_test", Description: "Return the supplied value to test tool calling", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)}
	input := json.RawMessage(`{"role":"user","content":"Call connection_test with value stationos. Do not answer with text."}`)
	response, err := c.chat(ctx, conn, key, model, "You are testing a provider connection. Call only the offered tool.", []producer.Item{input}, []producer.Tool{tool}, nil, false)
	if err != nil {
		return out, err
	}
	var args struct{ Value string }
	if len(response.Calls) != 1 || response.Calls[0].Name != "connection_test" || json.Unmarshal(response.Calls[0].Arguments, &args) != nil || args.Value != "stationos" {
		return out, errors.New("Tool capability test failed")
	}
	out.Tools = true
	schema := json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)
	input = json.RawMessage(`{"role":"user","content":"Return JSON with ok true."}`)
	response, err = c.chat(ctx, conn, key, model, "Return the requested JSON object.", []producer.Item{input}, nil, schema, false)
	if err != nil {
		return out, err
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal([]byte(response.Text), &result) != nil || !result.OK {
		return out, errors.New("Structured JSON capability test failed")
	}
	out.Structured = true
	out.ModelAvailable = true
	out.Message = "Server reachable. Tool calling and structured JSON capability tests passed for the exact model entered."
	return out, nil
}
