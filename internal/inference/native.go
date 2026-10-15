package inference

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/producer"
)

func result(text string, calls []producer.Call, usage producer.Usage) (producer.Response, error) {
	if len(calls) > producer.MaxToolCalls {
		return producer.Response{Usage: usage}, producer.ErrToolBatchLimit
	}
	if len(text) > 32000 {
		return producer.Response{}, errors.New("Provider response exceeds limits")
	}
	out := producer.Response{Text: text, Calls: calls, Usage: usage}
	if text != "" {
		b, _ := json.Marshal(map[string]string{"role": "assistant", "content": text})
		out.Items = append(out.Items, b)
	}
	for _, call := range calls {
		var obj map[string]any
		if call.Name == "" || json.Unmarshal(call.Arguments, &obj) != nil || obj == nil {
			return producer.Response{}, errors.New("Invalid native tool call")
		}
		b, _ := json.Marshal(map[string]string{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
		out.Items = append(out.Items, b)
	}
	if text == "" && len(calls) == 0 {
		return out, errors.New("Provider returned no answer")
	}
	return out, nil
}
func (c *Client) anthropic(ctx context.Context, conn ai.Connection, key, model string, messages []message, tools []producer.Tool, schema json.RawMessage) (producer.Response, error) {
	system := []string{}
	chat := []any{}
	for _, m := range messages {
		if m.Role == "system" {
			system = append(system, m.Content)
			continue
		}
		role := m.Role
		blocks := []any{}
		if role == "tool" {
			role = "user"
			blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content})
		} else {
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, call := range m.ToolCalls {
				var args string
				if json.Unmarshal(call.Function.Arguments, &args) != nil {
					return producer.Response{}, errors.New("Invalid native history")
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": json.RawMessage(args)})
			}
		}
		if len(blocks) > 0 {
			chat = append(chat, map[string]any{"role": role, "content": blocks})
		}
	}
	functions := []any{}
	for _, t := range tools {
		functions = append(functions, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Parameters})
	}
	body := map[string]any{"model": model, "system": strings.Join(system, "\n"), "messages": chat, "max_tokens": conn.OutputTokens}
	if len(schema) > 0 {
		functions = []any{map[string]any{"name": "structured_result", "description": "Return the requested structured result", "input_schema": schema}}
		body["tool_choice"] = map[string]any{"type": "tool", "name": "structured_result"}
	}
	if len(functions) > 0 {
		body["tools"] = functions
	}
	raw, err := c.request(ctx, conn, key, "POST", "/messages", body)
	if err != nil {
		return producer.Response{}, err
	}
	var v struct {
		Stop    string `json:"stop_reason"`
		Content []struct {
			Type, ID, Name, Text string
			Input                json.RawMessage
		}
		Usage producer.Usage
	}
	if json.Unmarshal(raw, &v) == nil && v.Stop == "max_tokens" {
		return producer.Response{Usage: v.Usage}, producer.ErrOutputLimit
	}
	if json.Unmarshal(raw, &v) != nil || (v.Stop != "end_turn" && v.Stop != "tool_use") {
		return producer.Response{}, errors.New("Anthropic response incomplete or refused")
	}
	text := ""
	calls := []producer.Call{}
	for _, b := range v.Content {
		switch b.Type {
		case "text":
			text += b.Text
		case "tool_use":
			if len(schema) > 0 {
				if b.Name != "structured_result" || len(calls) > 0 {
					return producer.Response{}, errors.New("Invalid Anthropic structured result")
				}
				text = string(b.Input)
			} else {
				calls = append(calls, producer.Call{ID: b.ID, Name: b.Name, Arguments: b.Input})
			}
		}
	}
	return result(text, calls, v.Usage)
}
func (c *Client) gemini(ctx context.Context, conn ai.Connection, key, model string, messages []message, tools []producer.Tool, schema json.RawMessage) (producer.Response, error) {
	system := []string{}
	chat := []any{}
	names := map[string]string{}
	for _, m := range messages {
		if m.Role == "system" {
			system = append(system, m.Content)
			continue
		}
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		parts := []any{}
		if role == "tool" {
			role = "user"
			var obj any
			if json.Unmarshal([]byte(m.Content), &obj) != nil {
				obj = map[string]string{"result": m.Content}
			}
			parts = append(parts, map[string]any{"functionResponse": map[string]any{"name": names[m.ToolCallID], "response": obj}})
		} else {
			if m.Content != "" {
				parts = append(parts, map[string]any{"text": m.Content})
			}
			for _, call := range m.ToolCalls {
				names[call.ID] = call.Function.Name
				var args string
				if json.Unmarshal(call.Function.Arguments, &args) != nil {
					return producer.Response{}, errors.New("Invalid native history")
				}
				part := map[string]any{"functionCall": map[string]any{"name": call.Function.Name, "args": json.RawMessage(args)}}
				if call.Signature != "" {
					part["thoughtSignature"] = call.Signature
				}
				parts = append(parts, part)
			}
		}
		if len(parts) > 0 {
			chat = append(chat, map[string]any{"role": role, "parts": parts})
		}
	}
	declarations := []any{}
	for _, t := range tools {
		declarations = append(declarations, map[string]any{"name": t.Name, "description": t.Description, "parametersJsonSchema": t.Parameters})
	}
	config := map[string]any{"maxOutputTokens": conn.OutputTokens}
	if len(schema) > 0 {
		config["responseMimeType"] = "application/json"
		config["responseJsonSchema"] = schema
	}
	body := map[string]any{"contents": chat, "systemInstruction": map[string]any{"parts": []any{map[string]string{"text": strings.Join(system, "\n")}}}, "generationConfig": config}
	if len(declarations) > 0 {
		body["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
	}
	raw, err := c.request(ctx, conn, key, "POST", "/models/"+url.PathEscape(strings.TrimPrefix(model, "models/"))+":generateContent", body)
	if err != nil {
		return producer.Response{}, err
	}
	var v struct {
		Candidates []struct {
			Finish  string `json:"finishReason"`
			Content struct {
				Parts []struct {
					Text         string
					Thought      bool
					Signature    string `json:"thoughtSignature"`
					FunctionCall *struct {
						Name string
						Args json.RawMessage
					} `json:"functionCall"`
				}
			}
		}
		Usage struct {
			Input  int `json:"promptTokenCount"`
			Output int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(raw, &v) == nil && len(v.Candidates) == 1 && v.Candidates[0].Finish == "MAX_TOKENS" {
		return producer.Response{Usage: producer.Usage{Input: v.Usage.Input, Output: v.Usage.Output}}, producer.ErrOutputLimit
	}
	if json.Unmarshal(raw, &v) != nil || len(v.Candidates) != 1 || v.Candidates[0].Finish != "STOP" {
		return producer.Response{}, errors.New("Gemini response incomplete or refused")
	}
	text := ""
	signatures := map[string]string{}
	calls := []producer.Call{}
	for _, part := range v.Candidates[0].Content.Parts {
		if part.Thought {
			continue
		}
		text += part.Text
		if part.FunctionCall != nil {
			id := producer.ID()
			signatures[id] = part.Signature
			calls = append(calls, producer.Call{ID: id, Name: part.FunctionCall.Name, Arguments: part.FunctionCall.Args})
		}
	}
	out, err := result(text, calls, producer.Usage{Input: v.Usage.Input, Output: v.Usage.Output})
	for i, raw := range out.Items {
		var item map[string]any
		_ = json.Unmarshal(raw, &item)
		id, _ := item["call_id"].(string)
		if signature := signatures[id]; signature != "" {
			item["gemini_signature"] = signature
			out.Items[i], _ = json.Marshal(item)
		}
	}
	return out, err
}
