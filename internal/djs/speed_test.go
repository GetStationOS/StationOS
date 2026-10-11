package djs

import (
	"math"
	"testing"
)

func TestConversationSpeedBounds(t *testing.T) {
	p := Profile{Name: "DJ", Model: "gpt-4o-mini", Voice: DefaultVoice}
	for _, speed := range []float64{0.25, 1, 1.15, 4} {
		p.ConversationSpeed = speed
		if err := p.Validate(); err != nil {
			t.Fatal(speed, err)
		}
	}
	for _, speed := range []float64{0, -1, 0.24, 4.01, math.NaN(), math.Inf(1)} {
		p.ConversationSpeed = speed
		if err := p.Validate(); err == nil {
			t.Fatal("invalid speed accepted", speed)
		}
	}
}
