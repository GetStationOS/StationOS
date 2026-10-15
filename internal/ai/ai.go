// Package ai owns provider configuration and text generation ports, without wire protocols.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Connection struct {
	ID, Name, Type, Address     string
	HasKey                      bool
	ContextTokens, OutputTokens int
	RequestTimeoutSeconds       int
}
type Selection struct {
	Connection string
}
type Routes struct{ Assistant, Curation, Audience, Speech, Image Selection }
type KeyChange struct {
	Value string
	Clear bool
}
type Store interface {
	Connections(context.Context) ([]Connection, error)
	Connection(context.Context, string) (Connection, error)
	Credential(context.Context, string) (string, error)
	SaveConnection(context.Context, Connection, KeyChange) error
	DeleteConnection(context.Context, string) error
	Routes(context.Context) (Routes, error)
}
type GenerationOptions struct {
	MaxOutputTokens int
	DisableThinking bool
	CompactContext  bool
}
type TextRequest struct {
	Options                          GenerationOptions
	Role, Model, Instructions, Input string
	Schema                           json.RawMessage
}
type Text interface {
	Generate(context.Context, TextRequest) (string, error)
	Identity(context.Context, string) (string, error)
	Kind(context.Context, string) (string, error)
}

func (r Routes) For(role string) Selection {
	switch role {
	case "curation":
		return r.Curation
	case "audience":
		return r.Audience
	case "speech":
		return r.Speech
	case "image":
		return r.Image
	default:
		return r.Assistant
	}
}
func (c Connection) RequestTimeout() time.Duration {
	seconds := c.RequestTimeoutSeconds
	if seconds == 0 {
		seconds = 180
		if c.Type == "openai" {
			seconds = 60
		}
	}
	return time.Duration(seconds) * time.Second
}

// QueueTimeout bounds admission for time-sensitive live preparation. Producer
// conversations remain queued until admitted or their caller cancels them; zero
// means no additional admission deadline, not an immediate expiry.
func (c Connection) QueueTimeout(live bool) time.Duration {
	if live {
		return c.RequestTimeout()
	}
	return 0
}

// TimedText gives production a bounded budget that includes queueing and generation.
type TimedText interface {
	Budget(context.Context, string) (time.Duration, error)
}

func (c Connection) Validate() error {
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(c.ID) || strings.TrimSpace(c.Name) == "" || len(c.Name) > 120 {
		return errors.New("Enter a connection name")
	}
	if c.Type != "openai" && c.Type != "ollama" && c.Type != "compatible" && c.Type != "anthropic" && c.Type != "gemini" {
		return errors.New("Choose a supported provider type")
	}
	u, err := url.Parse(c.Address)
	if err != nil || len(c.Address) > 2048 || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("Enter an HTTP or HTTPS server address without credentials, query or fragment")
	}
	if c.Type == "openai" && c.Address != "https://api.openai.com/v1" {
		return errors.New("Use the official OpenAI address; choose compatible for a private server")
	}
	if c.ContextTokens < 2048 || c.ContextTokens > 32768 || c.OutputTokens < 256 || c.OutputTokens > 8192 || c.OutputTokens >= c.ContextTokens {
		return errors.New("Use 2048–32768 context tokens and 256–8192 output tokens, below context size")
	}
	if c.RequestTimeoutSeconds != 0 && (c.RequestTimeoutSeconds < 15 || c.RequestTimeoutSeconds > 600) {
		return errors.New("Use an inference response deadline between 15 and 600 seconds")
	}
	return nil
}

var modelPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@+-]{0,127}$`)

func ValidModel(s string) bool { return modelPattern.MatchString(s) }

type routesKey struct{}

// PinRoutes keeps an admitted task on one provider configuration. A resumed
// request gets the newly saved routes and provider-independent checkpoints.
func PinRoutes(ctx context.Context, r Routes) context.Context {
	return context.WithValue(ctx, routesKey{}, r)
}
func PinnedRoutes(ctx context.Context) (Routes, bool) {
	r, ok := ctx.Value(routesKey{}).(Routes)
	return r, ok
}
