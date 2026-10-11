package web

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"stationos.local/stationos/internal/broadcast"
)

func TestICYAudioAndMetadataAcrossSegments(t *testing.T) {
	var wire bytes.Buffer
	w := icyWriter{output: &wire, remaining: icyInterval}
	frames := []broadcast.StreamFrame{
		{Audio: bytes.Repeat([]byte{1}, icyInterval+137), Artist: "Björk", Title: "It's Oh So Quiet"},
		{Audio: bytes.Repeat([]byte{2}, icyInterval*2), DJ: "Alex Bright", Show: "Afternoon"},
		{Audio: bytes.Repeat([]byte{3}, icyInterval*2-137), Artist: "Artist", Title: "Next track"},
	}
	var expected bytes.Buffer
	for _, frame := range frames {
		expected.Write(frame.Audio)
		w.title = icyTitle(frame, "Radio")
		// HTTP and producer chunks need not align with metadata intervals.
		for offset := 0; offset < len(frame.Audio); {
			end := min(offset+317, len(frame.Audio))
			n, err := w.Write(frame.Audio[offset:end])
			if err != nil || n != end-offset {
				t.Fatal(n, err)
			}
			offset = end
		}
	}
	var audio bytes.Buffer
	var titles []string
	for wire.Len() > 0 {
		chunk := make([]byte, icyInterval)
		if _, err := io.ReadFull(&wire, chunk); err != nil {
			t.Fatal(err)
		}
		audio.Write(chunk)
		size, err := wire.ReadByte()
		if err != nil {
			t.Fatal(err)
		}
		block := make([]byte, int(size)*16)
		if _, err := io.ReadFull(&wire, block); err != nil {
			t.Fatal(err)
		}
		titles = append(titles, strings.TrimRight(string(block), "\x00"))
	}
	if !bytes.Equal(audio.Bytes(), expected.Bytes()) {
		t.Fatal("metadata changed audio bytes")
	}
	want := []string{"StreamTitle='Björk - It’s Oh So Quiet';", "StreamTitle='Alex Bright speaking - Afternoon';", "", "StreamTitle='Artist - Next track';", ""}
	if len(titles) != len(want) {
		t.Fatal(titles)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("block %d: %q", i, titles[i])
		}
	}
}

func TestICYBoundedSafeMetadata(t *testing.T) {
	title := "DJ';StreamTitle='fake\r\n\\" + strings.Repeat("音", 2000)
	block := icyBlock(title)
	if len(block) != 1+int(block[0])*16 || len(block) > 4081 {
		t.Fatal("invalid length", len(block))
	}
	text := strings.TrimRight(string(block[1:]), "\x00")
	if !utf8.ValidString(text) || strings.ContainsAny(text, "\r\n\\") || strings.Count(text, "'") != 2 {
		t.Fatal("unsafe metadata", text)
	}
	if got := icyTitle(broadcast.StreamFrame{}, "Radio"); got != "Radio" {
		t.Fatal(got)
	}
}

type shortICYWriter struct{}

func (shortICYWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
func TestICYShortWrite(t *testing.T) {
	w := icyWriter{output: shortICYWriter{}, remaining: icyInterval}
	if _, err := w.Write([]byte("audio")); err != io.ErrShortWrite {
		t.Fatal(err)
	}
}
