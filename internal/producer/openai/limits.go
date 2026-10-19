package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type rateLimit struct{ delay time.Duration }

func (e rateLimit) Error() string {
	return "OpenAI temporarily limited the request rate. Wait before continuing. Check the selected model’s API rate limits if this persists."
}
func (e rateLimit) RetryAfter() time.Duration { return e.delay }

// Inspect bounded structured fields; never expose the provider's raw message,
// which can contain credentials, organization identifiers or submitted content.
func limitError(resp *http.Response) error {
	var body struct {
		Error struct{ Code, Type, Message string }
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &body) != nil {
		return errors.New("OpenAI rejected the request with HTTP 429 but did not identify the limit. Check API billing and model rate limits before continuing.")
	}
	code := body.Error.Code
	switch code {
	case "credit_balance_exhausted":
		return errors.New("OpenAI API credits are exhausted. Check the API billing balance for the organization that owns the saved key. Add credits before continuing; retrying cannot restore the balance.")
	case "organization_spend_limit_exceeded":
		return errors.New("OpenAI’s organization spending limit was reached. Review that organization’s API spending limit before continuing.")
	case "project_spend_limit_exceeded":
		return errors.New("OpenAI’s project spending limit was reached. Review the saved key’s API project spending limit before continuing.")
	case "organization_usage_limit_exceeded":
		return errors.New("OpenAI’s approved organization usage limit was reached. Review its API usage limits before continuing.")
	case "insufficient_quota":
		return errors.New("OpenAI API quota is unavailable. Check credits, billing and usage limits for the organization and project that own the saved key. Resolve the quota before continuing; immediate retries will not help.")
	}
	if body.Error.Type == "insufficient_quota" {
		return errors.New("OpenAI API quota is unavailable. Check the saved key’s API billing and usage limits before continuing; immediate retries will not help.")
	}
	if code == "rate_limit_exceeded" && strings.Contains(strings.ToLower(body.Error.Message), "request too large") {
		return errors.New("This assistant request exceeds OpenAI’s token rate limit for the selected model. Waiting alone will not make it fit. Review the model’s API token limit or choose a model with a larger allowance in Assistant settings.")
	}
	if code == "rate_limit_exceeded" || code == "slow_down" || body.Error.Type == "rate_limit_error" {
		return rateLimit{delay: retryDelay(resp.Header.Get("Retry-After"), time.Now())}
	}
	return errors.New("OpenAI rejected the request with HTTP 429 but did not identify a recoverable rate limit. Check API billing and model rate limits before continuing.")
}
func retryDelay(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds > 0 && !math.IsInf(seconds, 0) {
		return time.Duration(min(seconds, 86400) * float64(time.Second))
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}

// Classify only documented codes and known request fields. Provider messages and
// unknown values can contain submitted content and must never enter our logs.
func configurationError(resp *http.Response) error {
	var body struct{ Error struct{ Code, Param string } }
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	detail := "Check the model and request configuration in Assistant settings."
	if err == nil && len(raw) <= 64<<10 && json.Unmarshal(raw, &body) == nil {
		switch body.Error.Code {
		case "model_not_found":
			detail = "The selected model does not exist or is not accessible to the saved API key."
		case "context_length_exceeded":
			detail = "The request exceeds the selected model's context limit."
		case "invalid_json_schema":
			detail = "The provider rejected the structured output schema."
		case "unsupported_parameter", "unsupported_value", "invalid_value":
			detail = "The provider rejected a request parameter or value."
		}
		switch body.Error.Param {
		case "model":
			detail += " Rejected field: model."
		case "text.format", "text.format.schema", "response_format":
			detail += " Rejected field: structured output format."
		case "tools":
			detail += " Rejected field: tools."
		case "parallel_tool_calls":
			detail += " Rejected field: parallel tool calls."
		case "max_output_tokens":
			detail += " Rejected field: output token limit."
		case "input":
			detail += " Rejected field: input."
		case "reasoning", "reasoning.effort":
			detail += " Rejected field: reasoning effort."
		}
	}
	return fmt.Errorf("OpenAI rejected the request (HTTP %d). %s", resp.StatusCode, detail)
}
