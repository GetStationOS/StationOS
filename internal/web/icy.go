package web

import (
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"stationos.local/stationos/internal/broadcast"
)

// ICY clients opt in to length-prefixed, 16-byte-padded metadata blocks.
// Count only audio bytes, across producer chunks and segment boundaries.
const icyInterval = 8192

type icyWriter struct {
	output          io.Writer
	remaining       int
	title, previous string
	logo            string
	sent            bool
}

func (w *icyWriter) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		n := min(w.remaining, len(data))
		count, err := w.output.Write(data[:n])
		written += count
		w.remaining -= count
		if err != nil {
			return written, err
		}
		if count != n {
			return written, io.ErrShortWrite
		}
		data = data[n:]
		if w.remaining == 0 {
			block := []byte{0}
			if !w.sent || w.title != w.previous {
				block = icyArtworkBlock(w.title, w.logo)
			}
			count, err = w.output.Write(block)
			if err != nil {
				return written, err
			}
			if count != len(block) {
				return written, io.ErrShortWrite
			}
			w.previous, w.sent, w.remaining = w.title, true, icyInterval
		}
	}
	return written, nil
}

func icyTitle(frame broadcast.StreamFrame, station string) string {
	if frame.DJ != "" {
		title := frame.DJ + " speaking"
		if frame.Show != "" {
			title += " - " + frame.Show
		}
		return cleanICY(title)
	}
	if frame.Title != "" && frame.Artist != "" {
		return cleanICY(frame.Artist + " - " + frame.Title)
	}
	if frame.Title != "" {
		return cleanICY(frame.Title)
	}
	if frame.Artist != "" {
		return cleanICY(frame.Artist)
	}
	return cleanICY(station)
}

// Avoid control characters and ICY delimiter injection. Keep UTF-8 intact when
// bounding the one-byte block length (at most 255 blocks of 16 bytes).
func cleanICY(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		switch r {
		case '\'':
			return '’'
		case '\\':
			return '/'
		case utf8.RuneError:
			return ' '
		}
		return r
	}, text))
}

func icyBlock(title string) []byte {
	return icyArtworkBlock(title, "")
}
func icyArtworkBlock(title, logo string) []byte {
	title = cleanICY(title)
	suffix := ""
	if logo != "" && len(logo) <= 2048 {
		suffix = "StreamUrl='" + cleanICY(logo) + "';"
	}
	maxTitle := 255*16 - len("StreamTitle='';") - len(suffix)
	if len(title) > maxTitle {
		title = title[:maxTitle]
		for !utf8.ValidString(title) {
			title = title[:len(title)-1]
		}
	}
	text := "StreamTitle='" + title + "';" + suffix
	blocks := (len(text) + 15) / 16
	data := make([]byte, 1+blocks*16)
	data[0] = byte(blocks)
	copy(data[1:], text)
	return data
}
