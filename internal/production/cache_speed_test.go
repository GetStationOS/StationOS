package production

import (
	"stationos.local/stationos/internal/schedule"
	"testing"
)

func TestConversationSpeedInvalidatesPreparedSpeech(t *testing.T) {
	show := schedule.Occurrence{Key: "show"}
	show.DJ.ConversationSpeed = 1
	original := CacheKey(show, schedule.Occurrence{})
	show.DJ.ConversationSpeed = 1.15
	if original == CacheKey(show, schedule.Occurrence{}) {
		t.Fatal("speed change reused cached speech")
	}
}
