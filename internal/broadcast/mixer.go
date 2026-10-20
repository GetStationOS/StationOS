package broadcast

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"stationos.local/stationos/internal/mixer"
	"stationos.local/stationos/internal/production"
)

type musicLevelKey struct{}

func withMusicLevel(ctx context.Context, level int) context.Context {
	return context.WithValue(ctx, musicLevelKey{}, level)
}
func musicLevel(ctx context.Context) int {
	if v, ok := ctx.Value(musicLevelKey{}).(int); ok {
		return v
	}
	return 100
}

type musicNormalisationKey struct{}

func withMusicNormalisation(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, musicNormalisationKey{}, enabled)
}
func musicNormalisation(ctx context.Context) bool {
	enabled, _ := ctx.Value(musicNormalisationKey{}).(bool)
	return enabled
}

// PresenterEncoder applies station gain to already prepared speech at airtime.
// Speech caches remain independent of station levels and are never mutated.
type PresenterEncoder interface {
	OpenPresenter(context.Context, []byte, int) (io.ReadCloser, error)
}

func (m *Manager) levels(ctx context.Context, id string) (mixer.Levels, error) {
	if m.options.Mixer == nil {
		return mixer.Default(), nil
	}
	return m.options.Mixer.Get(ctx, id)
}

// Single-pass loudnorm works on streamed source bytes without downloading or
// scanning the complete recording first. Output remains 44.1 kHz stereo MP3;
// the encoder's explicit sample rate downsamples loudnorm's true-peak analysis.
// A wide LRA avoids imposing the speech chain's tight dynamics on music.
func musicLoudnessFilter(percent int) string {
	filter := "aformat=channel_layouts=stereo,loudnorm=I=-16:TP=-2:LRA=20:linear=false"
	if percent != 100 {
		filter += "," + gainFilter(percent)
	}
	return filter
}

// gainFilter scales decoded samples, then clamps peaks without undoing gain.
func gainFilter(percent int) string {
	return fmt.Sprintf("volume=%.4f,aeval=clip(val(0)\\,-0.95\\,0.95)|clip(val(1)\\,-0.95\\,0.95)", float64(percent)/100)
}

// Prepared gain stays outside timed playout. Keep original bytes so a later
// mixer change can be applied once, rather than scaling an already scaled link.
type preparedSpeech struct {
	raw, audio []byte
	level      int
}
type speechReader struct {
	io.ReadCloser
	prepared *preparedSpeech
}

func speechAudio(raw []byte, prepared *preparedSpeech) io.ReadCloser {
	if prepared == nil {
		return io.NopCloser(bytes.NewReader(raw))
	}
	return &speechReader{ReadCloser: io.NopCloser(bytes.NewReader(prepared.audio)), prepared: prepared}
}
func (m *Manager) prepareSpeech(ctx context.Context, id string, raw []byte) (*preparedSpeech, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	levels, err := m.levels(ctx, id)
	if err != nil {
		return nil, err
	}
	p := &preparedSpeech{raw: raw, audio: raw, level: 100}
	if e, ok := m.encoder.(PresenterEncoder); ok && levels.DJ != 100 {
		reader, err := e.OpenPresenter(ctx, raw, levels.DJ)
		if err != nil {
			return nil, err
		}
		p.audio, err = production.ReadBounded(reader, 1<<20)
		closeErr := reader.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		p.level = levels.DJ
	}
	return p, nil
}
