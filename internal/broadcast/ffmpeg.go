// SPDX-License-Identifier: GPL-3.0-only
package broadcast

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/sources"
)

type FFmpeg struct {
	Path    string
	Sources sources.Repository
	Factory sources.Factory
}
type encoded struct {
	io.ReadCloser
	cancel   context.CancelFunc
	source   io.ReadCloser
	cmd      *exec.Cmd
	once     sync.Once
	waitOnce sync.Once
	waitErr  error
	eof      atomic.Bool
}

func (e *encoded) wait() error { e.waitOnce.Do(func() { e.waitErr = e.cmd.Wait() }); return e.waitErr }
func (e *encoded) Read(buf []byte) (int, error) {
	// EOF marks the audio boundary. Reaping the process and closing its
	// source happens in Close, off the station's timed playout path.
	n, err := e.ReadCloser.Read(buf)
	if errors.Is(err, io.EOF) {
		e.eof.Store(true)
	}
	return n, err
}

func (e *encoded) Close() error {
	e.once.Do(func() {
		if !e.eof.Load() {
			e.cancel()
		}
		e.source.Close()
		e.ReadCloser.Close()
		e.wait()
		e.cancel()
	})
	if e.eof.Load() && e.waitErr != nil {
		return errors.New("audio encoder failed")
	}
	return nil
}
func (f FFmpeg) Open(parent context.Context, item content.AudioItem) (io.ReadCloser, error) {
	if f.Path == "" {
		return nil, errors.New("FFmpeg is unavailable")
	}
	ctx, cancel := context.WithCancel(parent)
	// Startup is bounded without imposing a duration limit on a long track.
	timer := time.AfterFunc(20*time.Second, cancel)
	cfg, err := f.Sources.Get(ctx, item.SourceID)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, err
	}
	provider, err := f.Factory(ctx, cfg)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, err
	}
	profile := provider.Profile()
	permission := profile.Restrictions.PublicBroadcast
	if !profile.Capabilities.Streaming || (permission != sources.Allowed && permission != sources.OperatorResponsible) || profile.Restrictions.PlaybackReportingRequired {
		timer.Stop()
		cancel()
		return nil, errors.New("source does not permit this broadcast path")
	}
	opener, ok := provider.(sources.StreamOpener)
	if !ok {
		timer.Stop()
		cancel()
		return nil, sources.ErrUnsupported
	}
	source, format, err := opener.OpenStream(ctx, item.SourceItemID)
	timer.Stop()
	if err != nil {
		cancel()
		return nil, err
	}
	// Decode checked source bytes before encoding so only the final near-silence
	// can be trimmed with bounded memory, without cutting pauses within a song.
	decodeArgs := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "pipe", "-f", format, "-i", "pipe:0", "-map", "0:a:0", "-vn", "-threads", "1", "-filter_threads", "1", "-ar", "44100", "-ac", "2", "-f", "s16le", "pipe:1"}
	decoderCtx, decoderCancel := context.WithCancel(ctx)
	decoder := exec.CommandContext(decoderCtx, f.Path, decodeArgs...)
	decoder.Env = []string{}
	decoder.WaitDelay = time.Second
	decoder.Stdin = source
	decoder.Stderr = io.Discard
	pcm, err := decoder.StdoutPipe()
	if err == nil {
		err = decoder.Start()
	}
	if err != nil {
		decoderCancel()
		if pcm != nil {
			pcm.Close()
		}
		cancel()
		source.Close()
		return nil, err
	}
	originalSource := source
	stopSource := context.AfterFunc(decoderCtx, func() { originalSource.Close() })
	source = &musicTail{ReadCloser: &startupReader{encoded: &encoded{ReadCloser: pcm, cancel: decoderCancel, source: originalSource, cmd: decoder}, stop: stopSource}}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "pipe", "-f", "s16le", "-ar", "44100", "-ac", "2", "-i", "pipe:0", "-map", "0:a:0", "-vn", "-threads", "1", "-filter_threads", "1", "-c:a", "libmp3lame", "-b:a", "128k", "-ar", "44100", "-ac", "2", "-reservoir", "0", "-f", "mp3", "-write_xing", "0", "-id3v2_version", "0", "-flush_packets", "1", "pipe:1"}
	if level := musicLevel(parent); level != 100 {
		args = append(args[:len(args)-1], "-af", "aformat=channel_layouts=stereo,"+gainFilter(level), "pipe:1")
	}
	cmd := exec.CommandContext(ctx, f.Path, args...)
	cmd.Env = []string{}
	cmd.WaitDelay = time.Second
	cmd.Stdin = source
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		if stdout != nil {
			stdout.Close()
		}
		cancel()
		source.Close()
		return nil, err
	}
	// Closing source bytes on cancellation also releases the stdin copier.
	stop := context.AfterFunc(ctx, func() { source.Close() })
	return readAheadAudio(ctx, &startupReader{encoded: &encoded{ReadCloser: stdout, cancel: cancel, source: source, cmd: cmd}, stop: stop}), nil
}

type startupReader struct {
	*encoded
	stop    func() bool
	started bool
}

func (r *startupReader) Read(buf []byte) (int, error) {
	if r.started {
		return r.encoded.Read(buf)
	}
	timer := time.AfterFunc(20*time.Second, r.cancel)
	n, err := r.encoded.Read(buf)
	timer.Stop()
	if n > 0 {
		r.started = true
	}
	return n, err
}
func (r *startupReader) Close() error { err := r.encoded.Close(); r.stop(); return err }

// OpenPresenter transforms cached audio locally; it never regenerates speech.
func (f FFmpeg) OpenPresenter(parent context.Context, data []byte, level int) (io.ReadCloser, error) {
	if f.Path == "" || len(data) == 0 || len(data) > 1<<20 || level < 0 || level > 400 {
		return nil, errors.New("invalid presenter audio or volume")
	}
	ctx, cancel := context.WithCancel(parent)
	source := io.NopCloser(bytes.NewReader(data))
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "pipe", "-f", "mp3", "-i", "pipe:0", "-map", "0:a:0", "-vn", "-af", "aformat=channel_layouts=stereo," + gainFilter(level), "-threads", "1", "-filter_threads", "1", "-c:a", "libmp3lame", "-b:a", "128k", "-ar", "44100", "-ac", "2", "-reservoir", "0", "-f", "mp3", "-write_xing", "0", "-id3v2_version", "0", "-flush_packets", "1", "pipe:1"}
	cmd := exec.CommandContext(ctx, f.Path, args...)
	cmd.Env = []string{}
	cmd.WaitDelay = time.Second
	cmd.Stdin = source
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		if stdout != nil {
			stdout.Close()
		}
		cancel()
		return nil, err
	}
	return readAheadAudio(ctx, &startupReader{encoded: &encoded{ReadCloser: stdout, cancel: cancel, source: source, cmd: cmd}, stop: func() bool { return false }}), nil
}
