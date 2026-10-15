// Package openai implements the Responses tool loop's provider boundary.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"stationos.local/stationos/internal/producer"
)

type Model struct{ Client *http.Client }

func New() Model {
	return Model{Client: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("OpenAI redirects are not allowed") }}}
}
func (m Model) Respond(ctx context.Context, model, key, instructions string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	return m.RespondStructured(ctx, model, key, instructions, input, tools, nil)
}
func (m Model) RespondStructured(ctx context.Context, model, key, instructions string, input []producer.Item, tools []producer.Tool, schema json.RawMessage) (producer.Response, error) {
	functions := []any{}
	for _, t := range tools {
		functions = append(functions, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.Parameters, "strict": false})
	}
	payload := map[string]any{"model": model, "instructions": instructions, "input": input, "tools": functions, "parallel_tool_calls": false, "max_output_tokens": 6000, "store": false}
	if len(schema) > 0 {
		payload["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "stationos", "strict": true, "schema": schema}}
	}
	body, e := json.Marshal(payload)
	if e != nil {
		return producer.Response{}, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/responses", bytes.NewReader(body))
	if e != nil {
		return producer.Response{}, e
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	// Only the service may retry an explicit temporary 429 rejection. Never retry ambiguous transport failures.
	resp, e := m.Client.Do(req)
	if e != nil {
		return producer.Response{}, errors.New("Assistant model request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		switch resp.StatusCode {
		case 401, 403:
			return producer.Response{}, errors.New("OpenAI rejected the assistant credential. Check Assistant settings and the key's permissions.")
		case 429:
			return producer.Response{}, limitError(resp)
		case 400, 404:
			return producer.Response{}, errors.New("OpenAI rejected the model or tool configuration. Check the model in Assistant settings.")
		default:
			return producer.Response{}, errors.New("The assistant model service is unavailable. Try again shortly.")
		}
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return producer.Response{}, errors.New("Assistant response exceeds the limit")
	}
	var v struct {
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
		Usage  producer.Usage    `json:"usage"`
	}
	if e = json.Unmarshal(raw, &v); e != nil {
		return producer.Response{}, errors.New("Invalid assistant response")
	}
	if v.Status != "completed" {
		return producer.Response{Usage: v.Usage}, errors.New("Assistant returned an incomplete response")
	}
	out := producer.Response{Items: v.Output, Usage: v.Usage}
	var text strings.Builder
	for _, item := range v.Output {
		var part struct {
			Type      string                        `json:"type"`
			ID        string                        `json:"call_id"`
			Name      string                        `json:"name"`
			Arguments string                        `json:"arguments"`
			Summary   []struct{ Type, Text string } `json:"summary"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if e = json.Unmarshal(item, &part); e != nil {
			return producer.Response{}, errors.New("Invalid assistant response")
		}
		if part.Type == "reasoning" {
			for _, summary := range part.Summary {
				if summary.Type == "summary_text" {
					out.Reasoning += summary.Text + "\n"
				}
			}
		}
		if part.Type == "function_call" {
			out.Calls = append(out.Calls, producer.Call{ID: part.ID, Name: part.Name, Arguments: []byte(part.Arguments)})
		}
		if part.Type == "message" {
			for _, c := range part.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
		}
	}
	out.Text = text.String()
	if len(out.Text) > 32000 || len(out.Reasoning) > 32000 {
		return producer.Response{}, errors.New("Assistant answer exceeds the limit")
	}
	return out, nil
}
