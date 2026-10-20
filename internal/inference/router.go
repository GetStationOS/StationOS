package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/production"
)

type Router struct {
	Conversations ai.ConversationStore
	sessionMu     sync.Mutex
	sessionLocks  map[string]*conversationLock
	Store         ai.Store
	OpenAI        producer.Model
	Local         *Client
}

func New(store ai.Store, cloud producer.Model) *Router {
	return &Router{Store: store, OpenAI: cloud, Local: NewClient()}
}
func (r *Router) selection(ctx context.Context, role string) (ai.Selection, ai.Connection, string, error) {
	routes, err := r.routes(ctx)
	if err != nil {
		return ai.Selection{}, ai.Connection{}, "", err
	}
	s := routes.For(role)
	c, err := r.Store.Connection(ctx, s.Connection)
	if err != nil {
		return s, c, "", err
	}
	key, err := r.Store.Credential(ctx, c.ID)
	if err == nil && c.Type == "openai" && key == "" {
		err = errors.New("The selected OpenAI connection has no saved key")
	}
	return s, c, key, err
}
func (r *Router) Respond(ctx context.Context, model, _, prompt string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	return r.respond(ctx, "assistant", model, prompt, input, tools, nil, ai.GenerationOptions{})
}
func (r *Router) RespondWithOptions(ctx context.Context, model, _, prompt string, input []producer.Item, tools []producer.Tool, options ai.GenerationOptions) (producer.Response, error) {
	return r.respond(ctx, "assistant", model, prompt, input, tools, nil, options)
}

func (r *Router) respond(ctx context.Context, role, model, prompt string, input []producer.Item, tools []producer.Tool, schema json.RawMessage, options ai.GenerationOptions) (producer.Response, error) {
	_, c, key, err := r.selection(ctx, role)
	if err != nil {
		return producer.Response{}, err
	}
	invoke := func(c ai.Connection, key, model string) (producer.Response, error) {
		if c.Type == "openai" {
			cloudInput := cleanAnnotations(input)
			if len(schema) > 0 {
				prompt += "\nReturn only JSON matching this schema: " + string(schema)
			}
			if structured, ok := r.OpenAI.(interface {
				RespondStructuredWithOptions(context.Context, string, string, string, []producer.Item, []producer.Tool, json.RawMessage, ai.GenerationOptions) (producer.Response, error)
			}); ok {
				return structured.RespondStructuredWithOptions(ctx, model, key, prompt, cloudInput, tools, schema, options)
			}
			if structured, ok := r.OpenAI.(interface {
				RespondStructured(context.Context, string, string, string, []producer.Item, []producer.Tool, json.RawMessage) (producer.Response, error)
			}); ok && len(schema) > 0 {
				return structured.RespondStructured(ctx, model, key, prompt, cloudInput, tools, schema)
			}
			return r.OpenAI.Respond(ctx, model, key, prompt, cloudInput, tools)
		}
		return r.Local.chatWithOptions(ctx, c, key, model, prompt, input, tools, schema, role != "assistant", options)
	}
	return invoke(c, key, model)
}
func (r *Router) Generate(ctx context.Context, q ai.TextRequest) (string, error) {
	input, _ := json.Marshal(map[string]string{"role": "user", "content": q.Input})
	out, err := r.converse(ctx, q, input)
	if out.Notice != "" {
		production.ReportInference(ctx, out.Notice)
	}
	if err != nil {
		if errors.Is(err, producer.ErrOutputLimit) {
			return "", ai.ErrOutputLimit
		}
		if errors.Is(err, producer.ErrInputContextLimit) || errors.Is(err, producer.ErrContextBudget) {
			return "", ai.ErrContextLimit
		}
		return "", err
	}
	if len(out.Calls) > 0 {
		return "", errors.New("Text preparation returned unexpected tool calls")
	}
	if len(q.Schema) > 0 && !json.Valid([]byte(out.Text)) {
		return "", ai.ErrInvalidStructuredResponse
	}
	return out.Text, err
}
func (r *Router) Identity(ctx context.Context, role string) (string, error) {
	s, c, _, err := r.selection(ctx, role)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(struct {
		S ai.Selection
		C ai.Connection
	}{s, c})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (r *Router) Kind(ctx context.Context, role string) (string, error) {
	routes, err := r.routes(ctx)
	if err != nil {
		return "", err
	}
	c, err := r.Store.Connection(ctx, routes.For(role).Connection)
	return c.Type, err
}

func (r *Router) Check(ctx context.Context, c ai.Connection, key, model string, capability bool) (Check, error) {
	return r.Local.Check(ctx, c, key, model, capability)
}

func (r *Router) routes(ctx context.Context) (ai.Routes, error) {
	if routes, ok := ai.PinnedRoutes(ctx); ok {
		return routes, nil
	}
	return r.Store.Routes(ctx)
}

// Private wire annotations are never sent to a different provider. The standard
// call/result items remain usable; durable checkpoints already normalize them.
func cleanAnnotations(input []producer.Item) []producer.Item {
	out := make([]producer.Item, 0, len(input))
	for _, raw := range input {
		var item map[string]any
		if json.Unmarshal(raw, &item) != nil {
			out = append(out, raw)
			continue
		}
		delete(item, "gemini_signature")
		b, _ := json.Marshal(item)
		out = append(out, b)
	}
	return out
}

func (r *Router) Budget(ctx context.Context, role string) (time.Duration, error) {
	routes, err := r.routes(ctx)
	if err != nil {
		return 0, err
	}
	c, err := r.Store.Connection(ctx, routes.For(role).Connection)
	if err != nil {
		return 0, err
	}
	if c.Type == "openai" || role == "assistant" {
		return 0, nil
	}
	return c.QueueTimeout(role != "assistant") + c.RequestTimeout(), nil
}
