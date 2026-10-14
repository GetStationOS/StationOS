// Package inference implements native Ollama and OpenAI-compatible protocols.
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/producer"
)

type safeFailure struct{ error }
type Client struct {
	HTTP   *http.Client
	Gate   *Gate
	capsMu sync.Mutex
	caps   map[string]thinkingEntry
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Provider redirects are not allowed") }}, Gate: &Gate{}}
}
func (c *Client) request(ctx context.Context, conn ai.Connection, key, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(conn.Address, "/")+path, reader)
	if err != nil {
		return nil, errors.New("Invalid provider address")
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		switch conn.Type {
		case "anthropic":
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		case "gemini":
			req.Header.Set("x-goog-api-key", key)
		default:
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	client := *c.HTTP
	client.Timeout = conn.RequestTimeout()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("Provider redirects are not allowed") }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, &safeFailure{errors.New("Provider connection unavailable; check the server address from the StationOS container")}
		}
		return nil, errors.New("Provider response failed; request outcome is uncertain and was not retried")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == 404 {
			return nil, &safeFailure{errors.New("Provider model or endpoint is unavailable")}
		}
		return nil, fmt.Errorf("Provider returned HTTP %d; check its model, capability and credential settings", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("Provider response exceeds limits or is incomplete")
	}
	return raw, nil
}

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []wireCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`
}
type wireCall struct {
	Signature string `json:"-"`
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Function  struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

func history(input []producer.Item, ollama bool) ([]message, error) {
	out := []message{}
	for _, item := range input {
		var v struct {
			Role, Type, Name string
			Content          json.RawMessage
			CallID           string `json:"call_id"`
			Arguments        string
			Output           string
			GeminiSignature  string `json:"gemini_signature"`
		}
		if json.Unmarshal(item, &v) != nil {
			return nil, errors.New("Invalid saved conversation")
		}
		switch v.Type {
		case "reasoning":
			continue
		case "function_call":
			call := wireCall{ID: v.CallID, Type: "function", Signature: v.GeminiSignature}
			call.Function.Name = v.Name
			if !json.Valid([]byte(v.Arguments)) {
				return nil, errors.New("Invalid saved tool arguments")
			}
			if ollama {
				call.Function.Arguments = []byte(v.Arguments)
			} else {
				call.Function.Arguments, _ = json.Marshal(v.Arguments)
			}
			if len(out) > 0 && out[len(out)-1].Role == "assistant" {
				out[len(out)-1].ToolCalls = append(out[len(out)-1].ToolCalls, call)
			} else {
				out = append(out, message{Role: "assistant", ToolCalls: []wireCall{call}})
			}
		case "function_call_output":
			out = append(out, message{Role: "tool", Content: v.Output, ToolCallID: v.CallID})
		default:
			if v.Role == "" {
				continue
			}
			role := v.Role
			if role == "developer" {
				role = "system"
			}
			var text string
			if json.Unmarshal(v.Content, &text) != nil {
				var parts []struct{ Text string }
				if json.Unmarshal(v.Content, &parts) != nil {
					return nil, errors.New("Invalid saved message")
				}
				for _, p := range parts {
					text += p.Text
				}
			}
			out = append(out, message{Role: role, Content: text})
		}
	}
	if ollama {
		names := map[string]string{}
		for _, m := range out {
			for _, t := range m.ToolCalls {
				names[t.ID] = t.Function.Name
			}
		}
		for i := range out {
			if out[i].Role == "tool" {
				out[i].ToolName = names[out[i].ToolCallID]
				out[i].ToolCallID = ""
			}
		}
	}
	return out, nil
}
func (c *Client) chat(ctx context.Context, conn ai.Connection, key, model, prompt string, input []producer.Item, tools []producer.Tool, schema json.RawMessage, live bool) (producer.Response, error) {
	return c.chatWithOptions(ctx, conn, key, model, prompt, input, tools, schema, live, ai.GenerationOptions{})
}
func (c *Client) chatWithOptions(ctx context.Context, conn ai.Connection, key, model, prompt string, input []producer.Item, tools []producer.Tool, schema json.RawMessage, live bool, options ai.GenerationOptions) (producer.Response, error) {
	if options.MaxOutputTokens > 0 {
		conn.OutputTokens = min(conn.OutputTokens, options.MaxOutputTokens)
	}
	msgs, err := history(input, conn.Type == "ollama")
	if err != nil {
		return producer.Response{}, err
	}
	msgs = append([]message{{Role: "system", Content: prompt}}, msgs...)
	functions := []any{}
	for _, t := range tools {
		functions = append(functions, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Parameters}})
	}
	rawMessages, _ := json.Marshal(msgs)
	rawTools, _ := json.Marshal(functions)
	// Estimate input tokens conservatively at three UTF-8 bytes per token. Keep complete human
	// requirements; pause explicitly instead of silently truncating the task.
	if (len(rawMessages)+len(rawTools)+2)/3 > conn.ContextTokens-conn.OutputTokens {
		return producer.Response{}, errors.New("Configured context budget is too small for the complete request and tools. Original requirements remain saved; increase the budget or use a smaller task")
	}
	queueWait := conn.QueueTimeout(live)
	queueCtx, queueCancel := context.WithTimeout(ctx, queueWait)
	p := backgroundPriority
	if live {
		p = livePriority
		if options.MaxOutputTokens > 0 && options.MaxOutputTokens <= 512 {
			p = linkPriority
		}
	}
	release, err := c.Gate.acquirePriority(queueCtx, p)
	queueCancel()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return producer.Response{}, fmt.Errorf("Inference queue deadline reached before generation started (wait limit %s): %w", queueWait, err)
		}
		return producer.Response{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, conn.RequestTimeout())
	defer cancel()
	if conn.Type == "anthropic" {
		return c.anthropic(ctx, conn, key, model, msgs, tools, schema)
	}
	if conn.Type == "gemini" {
		return c.gemini(ctx, conn, key, model, msgs, tools, schema)
	}
	body := map[string]any{"model": model, "messages": msgs, "stream": false}
	path := "/chat/completions"
	if len(functions) > 0 {
		body["tools"] = functions
	}
	if conn.Type == "ollama" {
		path = "/api/chat"
		if options.DisableThinking && c.canDisableThinking(ctx, conn, key, model) {
			body["think"] = false
		}
		body["options"] = map[string]any{"num_ctx": conn.ContextTokens, "num_predict": conn.OutputTokens}
		if len(schema) > 0 {
			body["format"] = schema
		}
	} else {
		body["max_tokens"] = conn.OutputTokens
		if len(schema) > 0 {
			body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "stationos", "strict": true, "schema": schema}}
		}
	}
	raw, err := c.request(ctx, conn, key, "POST", path, body)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return producer.Response{}, fmt.Errorf("Provider response for model %q exceeded its deadline; generation was not automatically replayed: %w", model, err)
		}
		return producer.Response{}, err
	}
	var v struct {
		Message     message
		Done        bool
		DoneReason  string `json:"done_reason"`
		PromptCount int    `json:"prompt_eval_count"`
		Count       int    `json:"eval_count"`
		Choices     []struct {
			Message message
			Finish  string `json:"finish_reason"`
		}
		Usage struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		}
	}
	if json.Unmarshal(raw, &v) != nil {
		return producer.Response{}, errors.New("Invalid provider response")
	}
	m := v.Message
	usage := producer.Usage{Input: v.PromptCount, Output: v.Count}
	if conn.Type == "ollama" {
		if v.PromptCount >= conn.ContextTokens-conn.OutputTokens {
			return producer.Response{Usage: usage}, errors.New("Local input reached its context budget; response was rejected to preserve complete requirements")
		}
		if !v.Done || v.DoneReason == "length" {
			return producer.Response{Usage: usage}, errors.New("Local response incomplete: output limit reached")
		}
	} else {
		if len(v.Choices) != 1 || (v.Choices[0].Finish != "stop" && v.Choices[0].Finish != "tool_calls") {
			return producer.Response{}, errors.New("Compatible response incomplete or refused")
		}
		m = v.Choices[0].Message
		usage = producer.Usage{Input: v.Usage.Input, Output: v.Usage.Output}
	}
	if len(m.ToolCalls) > 16 || len(m.Content) > 32000 {
		return producer.Response{}, errors.New("Provider answer exceeds limits")
	}
	out := producer.Response{Text: m.Content, Usage: usage}
	if m.Content != "" {
		b, _ := json.Marshal(map[string]any{"role": "assistant", "content": m.Content})
		out.Items = append(out.Items, b)
	}
	ids := map[string]bool{}
	for _, t := range m.ToolCalls {
		args := t.Function.Arguments
		if conn.Type != "ollama" {
			var s string
			if json.Unmarshal(args, &s) != nil {
				return producer.Response{}, errors.New("Invalid tool arguments")
			}
			args = []byte(s)
		}
		var obj map[string]any
		if json.Unmarshal(args, &obj) != nil || obj == nil || t.Function.Name == "" {
			return producer.Response{}, errors.New("Invalid provider tool call")
		}
		id := t.ID
		if id == "" {
			id = producer.ID()
		}
		if ids[id] {
			return producer.Response{}, errors.New("Duplicate tool call identifier")
		}
		ids[id] = true
		call := producer.Call{ID: id, Name: t.Function.Name, Arguments: args}
		out.Calls = append(out.Calls, call)
		b, _ := json.Marshal(map[string]any{"type": "function_call", "call_id": id, "name": call.Name, "arguments": string(args)})
		out.Items = append(out.Items, b)
	}
	if out.Text == "" && len(out.Calls) == 0 {
		return producer.Response{}, errors.New("Provider returned no answer or tools")
	}
	return out, nil
}
