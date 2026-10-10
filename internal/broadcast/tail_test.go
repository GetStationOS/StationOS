package broadcast

import (
	"bytes"
	"io"
	"testing"
)

func TestMusicTailPreservesInternalPauseAndBoundsTrailingTrim(t *testing.T) {
	audible := bytes.Repeat([]byte{0, 4, 0, 4}, 44100/2)
	pause := make([]byte, 44100*4)
	tail := make([]byte, 3*44100*4)
	wanted := append(append(append([]byte{}, audible...), pause...), audible...)
	raw := append(append([]byte{}, wanted...), tail...)
	got, err := io.ReadAll(&musicTail{ReadCloser: io.NopCloser(bytes.NewReader(raw))})
	if err != nil || !bytes.Equal(got, wanted) {
		t.Fatal("music or internal pause altered", len(got), len(wanted), err)
	}
	// More than five seconds stays bounded and trims only the final five.
	raw = append(append([]byte{}, audible...), make([]byte, 7*44100*4)...)
	got, err = io.ReadAll(&musicTail{ReadCloser: io.NopCloser(bytes.NewReader(raw))})
	if err != nil || len(got) != len(raw)-tailLimit {
		t.Fatal("tail limit not respected", len(got), err)
	}
}
