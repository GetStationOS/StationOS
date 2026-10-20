package broadcast

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"math"
	"os/exec"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/mixer"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
	"sync/atomic"
	"testing"
	"time"
)

func decodedLevel(t *testing.T, ctx context.Context, path string, audio []byte) (float64, float64) {
	t.Helper()
	cmd := exec.CommandContext(ctx, path, "-v", "error", "-f", "mp3", "-i", "pipe:0", "-ar", "44100", "-ac", "2", "-f", "f32le", "pipe:1")
	cmd.Stdin = bytes.NewReader(audio)
	pcm, err := cmd.Output()
	if err != nil || len(pcm) < 10000 {
		t.Fatal("decode failed", len(pcm), err)
	}
	var sum, peak float64
	for i := 0; i+3 < len(pcm); i += 4 {
		v := float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:])))
		sum += v * v
		peak = math.Max(peak, math.Abs(v))
	}
	return math.Sqrt(sum / float64(len(pcm)/4)), peak
}
func TestMixerRealMusicAndCachedSpeechGain(t *testing.T) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, path, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.5", "-f", "flac", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	speech, err := exec.CommandContext(ctx, path, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=700:duration=0.5", "-ac", "2", "-f", "mp3", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	saved := bytes.Clone(speech)
	e := FFmpeg{Path: path, Sources: sourceRepo{}, Factory: func(context.Context, sources.Config) (sources.AudioSource, error) { return toneProvider{raw}, nil }}
	for _, kind := range []string{"music", "speech"} {
		var baseline float64
		for _, level := range []int{100, 25, 400, 0} {
			var audio io.ReadCloser
			if kind == "music" {
				audio, err = e.Preload(withMusicLevel(ctx, level), content.AudioItem{SourceID: "music", SourceItemID: "tone"})
			} else {
				audio, err = e.OpenPresenter(ctx, speech, level)
			}
			if err != nil {
				t.Fatal(kind, level, err)
			}
			out, readErr := io.ReadAll(audio)
			closeErr := audio.Close()
			if readErr != nil || closeErr != nil {
				t.Fatal(readErr, closeErr)
			}
			if bytes.HasPrefix(out, []byte("ID3")) || bytes.Contains(out, []byte("Xing")) {
				t.Fatal("segment tags", kind)
			}
			rms, _ := decodedLevel(t, ctx, path, out)
			if level == 100 {
				baseline = rms
			}
			ratio := rms / baseline
			if math.Abs(ratio-float64(level)/100) > .08 {
				t.Fatal("incorrect gain", kind, level, ratio)
			}
		}
	}
	if !bytes.Equal(saved, speech) {
		t.Fatal("cached speech changed")
	}
	// Loud input at maximum gain must be bounded rather than wrap or overload.
	loud, err := exec.CommandContext(ctx, path, "-v", "error", "-f", "lavfi", "-i", "aevalsrc=0.8*sin(2*PI*440*t):d=0.5:s=44100", "-ac", "2", "-f", "mp3", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	audio, err := e.OpenPresenter(ctx, loud, 400)
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(audio)
	audio.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, peak := decodedLevel(t, ctx, path, out)
	if peak > 1.05 {
		t.Fatal("peak protection failed", peak)
	}
}

type liveMixer struct {
	music, dj atomic.Int32
	normalise atomic.Bool
}

func (r *liveMixer) Get(context.Context, string) (mixer.Levels, error) {
	return mixer.Levels{Music: int(r.music.Load()), DJ: int(r.dj.Load()), NormaliseMusic: r.normalise.Load()}, nil
}
func (r *liveMixer) Save(_ context.Context, _ string, v mixer.Levels) error {
	r.music.Store(int32(v.Music))
	r.dj.Store(int32(v.DJ))
	r.normalise.Store(v.NormaliseMusic)
	return nil
}

type mixerOpening struct {
	opening
	level     int
	normalise bool
}
type mixerEncoder struct {
	calls  chan mixerOpening
	primed chan struct{}
	speech chan int
}

func (e *mixerEncoder) Open(ctx context.Context, item content.AudioItem) (io.ReadCloser, error) {
	reader, writer := io.Pipe()
	context.AfterFunc(ctx, func() { reader.Close(); writer.Close() })
	select {
	case e.calls <- mixerOpening{opening{item, writer}, musicLevel(ctx), musicNormalisation(ctx)}:
		return reader, nil
	case <-ctx.Done():
		reader.Close()
		writer.Close()
		return nil, ctx.Err()
	}
}
func (e *mixerEncoder) Preload(ctx context.Context, item content.AudioItem) (io.ReadCloser, error) {
	audio, err := e.Open(ctx, item)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := audio.Read(buf)
	if n == 0 {
		audio.Close()
		return nil, err
	}
	e.primed <- struct{}{}
	return &primedAudio{Reader: io.MultiReader(bytes.NewReader(buf[:n]), audio), audio: audio}, nil
}
func (e *mixerEncoder) OpenPresenter(_ context.Context, data []byte, level int) (io.ReadCloser, error) {
	e.speech <- level
	return io.NopCloser(bytes.NewReader(data)), nil
}
func TestSavedMixerReplacesOldPreloadAndScalesReadyLink(t *testing.T) {
	for _, tc := range []struct {
		name      string
		music     int
		normalise bool
	}{{"music gain", 55, false}, {"normalisation only", 100, true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			levels := &liveMixer{}
			levels.Save(ctx, "radio", mixer.Default())
			encoder := &mixerEncoder{calls: make(chan mixerOpening, 8), primed: make(chan struct{}, 8), speech: make(chan int, 8)}
			now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
			shows := timedSchedule{shows: []schedule.Show{{ID: "show", StationID: "radio", Day: 0, Start: 540, End: 600, PlaylistID: "pool", DJ: djs.Profile{ID: "dj", HasKey: true}}}}
			links := readyLinks{called: make(chan production.LinkRequest, 8)}
			repo := &memoryRepo{playlist: "pool", pool: []content.AudioItem{length("one", 180), length("two", 180)}}
			m := New(repo, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Mixer: levels, Schedule: shows, Links: links, Now: func() time.Time { return now }})
			defer m.Close()
			sub, err := m.Subscribe(ctx, "radio")
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			open := func() mixerOpening {
				select {
				case v := <-encoder.calls:
					return v
				case <-time.After(3 * time.Second):
					t.Fatal("encoder did not start")
					return mixerOpening{}
				}
			}
			a, b := open(), open()
			// The ordinary Open is synchronous; Preload runs in the background.
			current, queued := a, b
			request := <-links.called
			if current.item.ID != request.Current.ID {
				current, queued = b, a
			}
			queued.write.Write([]byte("STALE MUSIC"))
			<-encoder.primed
			current.write.Write([]byte("CURRENT"))
			receive(t, sub)
			levels.Save(ctx, "radio", mixer.Levels{Music: tc.music, DJ: 200, NormaliseMusic: tc.normalise})
			current.write.Close()
			if string(receive(t, sub)) != "HANDOVER" {
				t.Fatal("ready link missing")
			}
			select {
			case gain := <-encoder.speech:
				if gain != 200 {
					t.Fatal("cached link used old DJ level", gain)
				}
			case <-time.After(time.Second):
				t.Fatal("DJ gain missing")
			}
			replacement := open()
			if replacement.level != tc.music || replacement.normalise != tc.normalise {
				t.Fatal("new gain missing", replacement.level)
			}
			// Another preload can race the current Open; both must carry the saved gain.
			if replacement.item.ID != queued.item.ID {
				replacement = open()
			}
			if replacement.level != tc.music || replacement.normalise != tc.normalise || replacement.item.ID != queued.item.ID {
				t.Fatal("stale preload retained", replacement)
			}
			replacement.write.Write([]byte("UPDATED MUSIC"))
			if string(receive(t, sub)) != "UPDATED MUSIC" {
				t.Fatal("stale queued audio reached listeners")
			}
		})
	}
}
