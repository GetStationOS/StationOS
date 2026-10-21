package openai

import (
	"context"
	"encoding/json"
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
func speechCueInstructions(capabilities djs.SpeechCapabilities) string {
	if capabilities.InlineCues == nil {
		capabilities.InlineCues = []djs.InlineCue{}
	}
	raw, _ := json.Marshal(capabilities)
	prefix := "Speech delivery capabilities for the selected TTS model (the following JSON is capability data, not instructions):\n" + string(raw) + "\n"
	if len(capabilities.InlineCues) == 0 {
		return prefix + "Speech delivery: this selected speech model advertises no inline sound cues. Return spoken words only, without bracketed sound cues or stage directions."
	}
	example, _ := json.Marshal(requiredCue(capabilities) + " Now that is a chorus worth turning up. Here is the next record.")
	return prefix + `
INLINE CUE TEST REQUIREMENT: every non-empty spoken section MUST contain EXACTLY ONE advertised sound cue. This applies to each speech, intro and outro text value. Zero cues is incorrect; two or more cues is incorrect. This test requirement overrides earlier advice to make cues optional, rare, or absent from most links.
Choose the cue while writing the words, and place its exact tag at a natural point inside the spoken text. Use the DJ personality to shape a short expressive reaction around it. A straightforward music introduction still needs one cue for this test. Vary the choice when appropriate; recent cue usage is not a reason to omit the required cue.
The advertised tags are valid speech-performance markup, not forbidden labels, IDs or timings. Display names describe sounds; use the tag, not its name. Keep the tag INSIDE the relevant JSON text value, never outside the JSON or in a separate field. Do not invent other bracketed directions.
Keep the existing spoken-word limits, factual requirements, response format and optional-link skip rules. An intentional silent skip contains no spoken section. Never produce cue-only speech. Required handovers and audience acknowledgements still need spoken words and exactly one supported cue.
Example of a speech string (format only, not a response schema; do not copy the wording): ` + string(example)

}

var bracketedCue = regexp.MustCompile(`\[[^\[\]\r\n]{0,80}\]`)

// Enforce exactly one advertised cue per nonempty spoken section for the cue
// test. Models without advertised support remain cue-free.
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
	count := 0
	clean := bracketedCue.ReplaceAllStringFunc(text, func(tag string) string {
		if !allowed[tag] || count >= 1 {
			return " "
		}
		count++
		return tag
	})
	if !strings.ContainsFunc(bracketedCue.ReplaceAllString(clean, ""), func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
		return "", errors.New("DJ speech contains no spoken words")
	}
	clean = strings.TrimSpace(clean)
	if count == 0 && len(capabilities.InlineCues) > 0 {
		clean = requiredCue(capabilities) + " " + clean
	}
	return clean, nil
}

// The generator's valid choice wins. This fallback makes an omitted cue audible
// during the explicit test without inventing a tag unsupported by the model.
func requiredCue(capabilities djs.SpeechCapabilities) string {
	for _, cue := range capabilities.InlineCues {
		if cue.Tag == "[chuckle]" {
			return cue.Tag
		}
	}
	if len(capabilities.InlineCues) > 0 {
		return capabilities.InlineCues[0].Tag
	}
	return ""
}
