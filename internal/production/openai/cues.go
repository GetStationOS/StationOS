package openai

import (
	"context"
	"errors"
	"regexp"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"strings"
	"unicode"
)

func (p Producer) speechCapabilities(ctx context.Context, model string) djs.SpeechCapabilities {
	if source, ok := p.Speech.(production.SpeechCapabilitySource); ok {
		capabilities, err := source.SpeechCapabilities(ctx, model)
		if err == nil {
			return capabilities
		}
	}
	return djs.SpeechCapabilities{}
}

var bracketedCue = regexp.MustCompile(`\[[^\[\]\r\n]{0,80}\]`)

// Retain supported generator-written cues and remove unsupported tags.
// Never insert a cue: missing cues must remain visible during the prompt test.
func cleanSpeechCues(text string, capabilities djs.SpeechCapabilities) (string, error) {
	if strings.TrimSpace(text) == "" {
		return text, nil
	}
	if strings.ContainsAny(bracketedCue.ReplaceAllString(text, ""), "[]") {
		return "", errors.New("DJ speech contains malformed inline cues")
	}
	allowed := map[string]bool{}
	for _, cue := range capabilities.InlineCues {
		allowed[cue.Tag] = true
	}
	clean := bracketedCue.ReplaceAllStringFunc(text, func(tag string) string {
		if !allowed[tag] {
			return " "
		}
		return tag
	})
	if !strings.ContainsFunc(bracketedCue.ReplaceAllString(clean, ""), func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
		return "", errors.New("DJ speech contains no spoken words")
	}
	clean = strings.TrimSpace(clean)
	return clean, nil
}
