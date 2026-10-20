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

var ErrConversationUnavailable = errors.New("provider conversation is no longer available")

var ErrInvalidStructuredResponse = errors.New("Provider returned invalid structured JSON")

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
type Conversation struct {
	KnownAired         map[string]bool
	ProviderIdentity   string
	PreviousResponseID string
	History            []json.RawMessage
}
type ConversationStore interface {
	LoadConversation(context.Context, string) (Conversation, error)
	SaveConversation(context.Context, string, Conversation) error
}
type sessionKey struct{}

func WithSession(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, sessionKey{}, key)
}
func Session(ctx context.Context) string { key, _ := ctx.Value(sessionKey{}).(string); return key }

type GenerationOptions struct {
	StoreConversation  bool
	PreviousResponseID string
	MaxOutputTokens    int
	DisableThinking    bool
	CompactContext     bool
}
type TextRequest struct {
	Session                          string
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
	if c.Type != "openai" && c.Type != "ollama" && c.Type != "compatible" && c.Type != "anthropic" && c.Type != "gemini" && c.Type != "chatterbox" {
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

// Provider categories are safe to persist; raw provider messages are not.
var ErrEmptyPlaylist = errors.New("the episode playlist has no available recordings")
var ErrNoEligibleTracks = errors.New("no recordings satisfy current station rotation and playlist rules")

var ErrQuota = errors.New("API quota or spending limit reached")
var ErrRateLimit = errors.New("provider rate limit reached")
var ErrOutputLimit = errors.New("response output token limit reached")
var ErrContextLimit = errors.New("request context limit reached")
var ErrTokenRateLimit = errors.New("request exceeds token rate limit")
var ErrRefused = errors.New("provider declined the response")

func FailureCode(err error) string {
	for _, v := range []struct {
		err  error
		code string
	}{{ErrEmptyPlaylist, "empty_playlist"}, {ErrNoEligibleTracks, "no_eligible_tracks"}, {ErrQuota, "quota"}, {ErrTokenRateLimit, "token_rate"}, {ErrRateLimit, "rate_limit"}, {ErrOutputLimit, "output_limit"}, {ErrContextLimit, "context_limit"}, {ErrRefused, "refused"}, {ErrInvalidStructuredResponse, "invalid_response"}, {context.DeadlineExceeded, "timeout"}} {
		if errors.Is(err, v.err) {
			return v.code
		}
	}
	return "unknown"
}
func FailureMessage(code string) string {
	switch code {
	case "empty_playlist":
		return "This show’s playlist has no available recordings. Check its playlist and music sources. Available fallback music continues."
	case "no_eligible_tracks":
		return "No recording currently satisfies the playlist and station rotation rules. Automatic selection keeps music playing while preparation retries."
	case "quota":
		return "The provider reported an API quota, credits or spending limit. Check API billing and usage limits."
	case "token_rate":
		return "The request exceeds the model's token rate allowance; waiting alone will not make it fit."
	case "rate_limit":
		return "The provider temporarily rate-limited the request."
	case "output_limit":
		return "The response reached its output token limit before completing the track choice. This is a per-response limit, not an account balance."
	case "context_limit":
		return "The request exceeded the model's context limit."
	case "refused":
		return "The provider declined to return a track choice."
	case "invalid_response":
		return "The DJ returned an unreadable track choice. No new DJ selection was accepted."
	case "validation":
		return "The DJ choice failed playlist or timing validation; a replacement is needed."
	case "timeout":
		return "The DJ request timed out before a usable track choice was ready."
	default:
		return "The DJ selection attempt failed; the provider did not supply a recognised cause. Check the curation connection and server logs."
	}
}
