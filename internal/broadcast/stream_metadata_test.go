package broadcast

import (
	"bytes"
	"testing"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/schedule"
)

func TestLateJoinRetainsMetadataAcrossMusicAndSpeech(t *testing.T) {
	music := State{Current: content.AudioItem{Title: "Track", Artist: "Performer"}, Phase: "music"}
	speech := State{Current: content.AudioItem{Title: "Link"}, PresenterStatus: "Speaking", Programme: schedule.Occurrence{Title: "Show", DJ: djs.Profile{Name: "Presenter"}}}
	reserve := recentAudio{limit: 12}
	reserve.append(streamFrame([]byte("old"), music))
	reserve.append(streamFrame([]byte("music"), music))
	reserve.append(streamFrame([]byte("speech"), speech))
	frames := reserve.frameSnapshot()
	if len(frames) != 2 || string(reserve.snapshot()) != "musicspeech" {
		t.Fatal("reserve bounds", frames)
	}
	if frames[0].Title != "Track" || frames[0].Artist != "Performer" || frames[0].DJ != "" {
		t.Fatal("music metadata", frames[0])
	}
	if frames[1].DJ != "Presenter" || frames[1].Show != "Show" || frames[1].Title != "" {
		t.Fatal("speech metadata", frames[1])
	}
	reserve.append(streamFrame([]byte("next"), music))
	if !bytes.Equal(frames[0].Audio, []byte("music")) || frames[1].DJ != "Presenter" {
		t.Fatal("snapshot mutated")
	}
}
