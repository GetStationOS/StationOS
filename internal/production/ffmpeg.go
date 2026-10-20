package production

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

type FFmpeg struct{ Path string }
type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errors.New("presenter audio exceeds limit")
	}
	return b.Buffer.Write(p)
}
func (f FFmpeg) MP3(parent context.Context, data []byte) ([]byte, error) {
	format := "mp3"
	limit := 1 << 20
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		format = "wav"
		limit = 16 << 20
	}
	if len(data) == 0 || len(data) > limit {
		return nil, errors.New("invalid presenter audio size")
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "pipe", "-f", format, "-i", "pipe:0", "-map", "0:a:0", "-vn", "-af", "acompressor=threshold=0.0316:ratio=3:attack=5:release=100:makeup=1,loudnorm=I=-14:TP=-2:LRA=7,silenceremove=start_periods=1:start_duration=0.01:start_threshold=-60dB,areverse,silenceremove=start_periods=1:start_duration=0.01:start_threshold=-60dB,areverse", "-threads", "1", "-filter_threads", "1", "-c:a", "libmp3lame", "-b:a", "128k", "-ar", "44100", "-ac", "2", "-reservoir", "0", "-f", "mp3", "-write_xing", "0", "-id3v2_version", "0", "pipe:1"}
	cmd := exec.CommandContext(ctx, f.Path, args...)
	cmd.Env = []string{}
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stderr = io.Discard
	var out boundedBuffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, errors.New("presenter audio conversion failed")
	}
	if out.Len() == 0 {
		return nil, errors.New("empty presenter audio")
	}
	return out.Bytes(), nil
}
