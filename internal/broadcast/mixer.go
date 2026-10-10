package broadcast

import (
	"context"
	"fmt"
	"io"
	"stationos.local/stationos/internal/mixer"
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

// gainFilter scales decoded samples, then clamps peaks without undoing gain.
func gainFilter(percent int) string {
	return fmt.Sprintf("volume=%.4f,aeval=clip(val(0)\\,-0.95\\,0.95)|clip(val(1)\\,-0.95\\,0.95)", float64(percent)/100)
}
