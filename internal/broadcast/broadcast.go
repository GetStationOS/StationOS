// Package broadcast owns one listener-driven, shared programme per station.
package broadcast

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/mixer"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

var ErrUnavailable = errors.New("station has no playable music; assign a playlist with available tracks")
var ErrBusy = errors.New("broadcast capacity reached; try again shortly")
var ErrClosed = errors.New("broadcast stopped")
var errHistory = errors.New("track play history could not be saved")

type Repository interface {
	Assignment(context.Context, string) (string, error)
	Assign(context.Context, string, string) error
	Pool(context.Context, string) ([]content.AudioItem, error)
}

// Encoder must produce compatible 128 kbps MP3 bytes and close promptly on cancellation.
type Encoder interface {
	Open(context.Context, content.AudioItem) (io.ReadCloser, error)
}
type State struct {
	SpeechText             string
	AINotice               string
	CurationStatus         string
	RepeatRelaxed          bool
	Programme, Following   schedule.Occurrence
	Phase, PresenterStatus string
	Status                 string
	Listeners              int
	Current, Next          content.AudioItem
	Started                time.Time
	Error                  string
}
type Options struct {
	Session                                 production.EditorialSession
	Operations                              *operations.Recorder
	Audience                                audience.Repository
	History                                 History
	Mixer                                   mixer.Repository
	MaxStations, MaxListeners, BufferChunks int
	StreamBuffer                            time.Duration
	Now                                     func() time.Time
	Schedule                                schedule.Source
	Producer                                production.Producer
	Links                                   production.LinkProducer
	Selector                                production.TrackSelector
}
type Manager struct {
	repo     Repository
	encoder  Encoder
	logger   *slog.Logger
	options  Options
	mu       sync.Mutex
	runs     map[string]*run
	last     map[string]State
	closed   bool
	watchers map[chan struct{}]struct{}
	wg       sync.WaitGroup
}
type run struct {
	stopReason string
	playlist   string
	ctx        context.Context
	cancel     context.CancelFunc
	listeners  map[*Subscription]struct{}
	state      State
	recent     recentAudio
	done       chan struct{}
}

// StreamFrame carries metadata for these exact audio bytes, including late-join
// reserves. Consumers must not substitute the producer's newer runtime state.
type StreamFrame struct {
	Audio                   []byte
	Title, Artist, DJ, Show string
}

func streamFrame(data []byte, state State) StreamFrame {
	frame := StreamFrame{Audio: data, Title: state.Current.Title, Artist: state.Current.Artist}
	if state.PresenterStatus == "Speaking" {
		frame.Title, frame.Artist = "", ""
		frame.DJ, frame.Show = state.Programme.DJ.Name, state.Programme.Title
	}
	return frame
}

type Subscription struct {
	Frames   <-chan []StreamFrame
	frames   chan []StreamFrame
	Audio    <-chan []byte
	Done     <-chan struct{}
	done     chan struct{}
	queue    chan []byte
	m        *Manager
	id       string
	clientID string
	run      *run
}

func New(repo Repository, encoder Encoder, logger *slog.Logger, options Options) *Manager {
	options.StreamBuffer = max(time.Duration(0), min(5*time.Second, options.StreamBuffer))
	if options.MaxStations <= 0 {
		options.MaxStations = 4
	}
	if options.MaxListeners <= 0 {
		options.MaxListeners = 1024
	}
	if options.BufferChunks <= 0 {
		options.BufferChunks = 32
	}
	burstChunks := int((options.StreamBuffer*audioBytesPerSecond/time.Second + audioChunkBytes - 1) / audioChunkBytes)
	options.BufferChunks = max(options.BufferChunks, burstChunks+2)
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{repo: repo, encoder: encoder, logger: logger, options: options, runs: map[string]*run{}, last: map[string]State{}}
}
func (m *Manager) State(id string) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.runs[id]; r != nil {
		s := r.state
		s.Listeners = len(r.listeners)
		return s
	}
	if state, ok := m.last[id]; ok {
		return state
	}
	return State{Status: "Dormant"}
}

func (m *Manager) Subscribe(ctx context.Context, id string) (*Subscription, error) {
	return m.SubscribeClient(ctx, id, "")
}

// SubscribeStream provides audio with its immutable segment metadata.
func (m *Manager) SubscribeStream(ctx context.Context, id, clientID string) (*Subscription, error) {
	return m.subscribeClient(ctx, id, clientID, true)
}

// SubscribeClient admits one connection per station and opaque player session.
// A browser may leave its initial media probe open when it requests playback.
// Replace that connection atomically, without restarting the shared programme.
// Empty client IDs represent independent external stream connections.
func (m *Manager) SubscribeClient(ctx context.Context, id, clientID string) (*Subscription, error) {
	return m.subscribeClient(ctx, id, clientID, false)
}

func (m *Manager) subscribeClient(ctx context.Context, id, clientID string, metadata bool) (*Subscription, error) {
	// Lookup before taking the runtime lock, then serialize admission/worker creation.
	timeline, err := m.timeline(ctx, id)
	playlist := timeline.Current.PlaylistID
	if err != nil {
		m.observe(id, "failure", "Station could not resolve its programme; check schedule and database", State{}, "output", false)
		return nil, err
	}
	if playlist == "" {
		m.observe(id, "failure", "Station has no assigned playlist; configure its default or scheduled playlist", State{}, "output", false)
		return nil, ErrUnavailable
	}
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, ErrClosed
		}
		r := m.runs[id]
		if r != nil && r.ctx.Err() != nil {
			r.cancel()
			done := r.done
			m.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if r == nil {
			if len(m.runs) >= m.options.MaxStations {
				m.mu.Unlock()
				return nil, ErrBusy
			}
			worker, cancel := context.WithCancel(context.Background())
			r = &run{playlist: playlist, ctx: worker, cancel: cancel, done: make(chan struct{}), listeners: map[*Subscription]struct{}{}, state: State{Status: "Starting", Programme: timeline.Current, Following: timeline.Next}, recent: recentAudio{limit: int(m.options.StreamBuffer * audioBytesPerSecond / time.Second)}}
			m.runs[id] = r
			m.wg.Add(1)
			go m.play(id, r)
		}
		if clientID != "" {
			for previous := range r.listeners {
				if previous.clientID == clientID {
					delete(r.listeners, previous)
					previous.closeQueue()
					close(previous.done)
				}
			}
		}
		if len(r.listeners) >= m.options.MaxListeners {
			m.mu.Unlock()
			return nil, ErrBusy
		}
		done := make(chan struct{})
		sub := &Subscription{Done: done, done: done, m: m, id: id, clientID: clientID, run: r}
		if metadata {
			frames := make(chan []StreamFrame, m.options.BufferChunks)
			sub.Frames, sub.frames = frames, frames
			if r.recent.bytes > 0 {
				frames <- r.recent.frameSnapshot()
			}
		} else {
			queue := make(chan []byte, m.options.BufferChunks)
			sub.Audio, sub.queue = queue, queue
			if r.recent.bytes > 0 {
				queue <- r.recent.snapshot()
			}
		}
		r.listeners[sub] = struct{}{}
		m.changedLocked()
		m.mu.Unlock()
		return sub, nil
	}
}
func (s *Subscription) closeQueue() {
	if s.frames != nil {
		close(s.frames)
	} else {
		close(s.queue)
	}
}

func (s *Subscription) enqueue(frame StreamFrame) bool {
	if s.frames != nil {
		select {
		case s.frames <- []StreamFrame{frame}:
			return true
		default:
			return false
		}
	}
	select {
	case s.queue <- frame.Audio:
		return true
	default:
		return false
	}
}

func (s *Subscription) Close() {
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := s.run.listeners[s]; ok {
		delete(s.run.listeners, s)
		s.closeQueue()
		close(s.done)
		if len(s.run.listeners) == 0 {
			s.run.stopReason = "Last listener disconnected"
			s.run.cancel()
		}
		m.changedLocked()
	}
}
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.changedLocked()
	delete(m.last, id)
	if r := m.runs[id]; r != nil {
		m.observe(id, "intervention", "Station stopped following an operator or configuration action", r.state, "", false)
		r.stopReason = "Operator or configuration action"
		r.cancel()
		for s := range r.listeners {
			delete(r.listeners, s)
			s.closeQueue()
			close(s.done)
		}
	}
}
func (m *Manager) StopPlaylist(id string) {
	m.mu.Lock()
	m.changedLocked()
	defer m.mu.Unlock()
	for stationID, r := range m.runs {
		if r.playlist == id {
			m.observe(stationID, "intervention", "Station stopped because its playlist changed", r.state, "", false)
			r.stopReason = "Playlist changed or removed"
			r.cancel()
			for s := range r.listeners {
				delete(r.listeners, s)
				s.closeQueue()
				close(s.done)
			}
		}
	}
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	for ch := range m.watchers {
		delete(m.watchers, ch)
		close(ch)
	}
	for _, r := range m.runs {
		r.stopReason = "Application shutdown"
		r.cancel()
		for s := range r.listeners {
			delete(r.listeners, s)
			s.closeQueue()
			close(s.done)
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// Normal playback fallback chooses independently of playlist browsing order.
func choose(pool []content.AudioItem, previous string) content.AudioItem {
	choices := make([]content.AudioItem, 0, len(pool))
	for _, item := range pool {
		if item.ID != previous || len(pool) == 1 {
			choices = append(choices, item)
		}
	}
	if len(choices) == 0 {
		return content.AudioItem{}
	}
	return choices[rand.IntN(len(choices))]
}
func (m *Manager) play(id string, r *run) {
	defer m.wg.Done()
	m.observe(id, "started", "Station production started", r.state, "", false)
	defer func() {
		if r.ctx.Err() == nil {
			m.observe(id, "unavailable", "Station production stopped without playable audio", r.state, "output", false)
			r.stopReason = "No playable audio or production dependency unavailable"
		}
		m.observe(id, "stopped", "Station production stopped: "+r.stopReason, r.state, "", false)
	}()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		for s := range r.listeners {
			delete(r.listeners, s)
			s.closeQueue()
			close(s.done)
		}
		delete(m.runs, id)
		if r.ctx.Err() == nil {
			if len(m.last) >= 128 {
				for key := range m.last {
					delete(m.last, key)
					break
				}
			}
			m.last[id] = State{Status: "Unavailable", Error: "Music could not be played. Check the assigned playlist, source and database."}
		}
		m.changedLocked()
		close(r.done)
	}()
	m.playProgramme(id, r)
}

func (m *Manager) now() time.Time {
	if m.options.Now != nil {
		return m.options.Now()
	}
	return time.Now()
}
func (m *Manager) timeline(ctx context.Context, id string) (schedule.Timeline, error) {
	return m.timelineAt(ctx, id, m.now())
}
func (m *Manager) timelineAt(ctx context.Context, id string, at time.Time) (schedule.Timeline, error) {
	if m.options.Schedule != nil {
		return m.options.Schedule.Resolve(ctx, id, at)
	}
	playlist, err := m.repo.Assignment(ctx, id)
	return schedule.Timeline{Current: schedule.Occurrence{Key: "default:" + playlist, StationID: id, PlaylistID: playlist, Title: "Continuous music"}}, err
}

type prepareRequest struct {
	show, next schedule.Occurrence
}
type preparedResult struct {
	show         schedule.Occurrence
	key          string
	prepared     production.Prepared
	intro, outro *preparedSpeech
	err          error
}
type linkStatus struct{ show, status string }

type selectionResult struct {
	show, current string
	item          content.AudioItem
	err           error
}

type linkResult struct {
	text             string
	audienceIDs      []string
	audienceValidIDs []string
	request          production.LinkRequest
	audio            []byte
	speech           *preparedSpeech
	err              error
}

func (m *Manager) playProgramme(id string, r *run) {
	prepCtx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	requests := make(chan prepareRequest, 2)
	results := make(chan preparedResult, 2)
	linkUpdates := make(chan linkStatus, 2)
	linkStates := map[string]string{}
	type inferenceNotice struct{ show, message string }
	inferenceNotices := make(chan inferenceNotice, 8)
	noticeStates := map[string]string{}
	prepDone := make(chan struct{})
	go func() {
		defer close(prepDone)
		for {
			select {
			case <-prepCtx.Done():
				return
			case req := <-requests:
				workCtx := production.WithInferenceNotice(prepCtx, func(message string) {
					select {
					case inferenceNotices <- inferenceNotice{req.show.Key, message}:
					default:
					}
				})
				prepared, err := m.options.Producer.Prepare(workCtx, req.show, req.next)
				var intro, outro *preparedSpeech
				if err == nil {
					intro, err = m.prepareSpeech(prepCtx, id, prepared.Intro)
				}
				if err == nil {
					outro, err = m.prepareSpeech(prepCtx, id, prepared.Outro)
				}
				select {
				case results <- preparedResult{show: req.show, key: production.CacheKey(req.show, req.next), prepared: prepared, intro: intro, outro: outro, err: err}:
				case <-prepCtx.Done():
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-prepDone }()
	prepared := map[string]production.Prepared{}
	preparedVoices := map[string]preparedResult{}
	attempted := map[string]bool{}
	failed := map[string]bool{}
	var recent []string
	var queued content.AudioItem
	var queuedShow, queuedAfter string
	selectionResults := make(chan selectionResult, 2)
	selectionStates := map[string]string{}
	drain := func() {
		for {
			select {
			case result := <-results:
				if result.err == nil {
					if attempted[result.key] {
						prepared[result.key] = result.prepared
						preparedVoices[result.key] = result
						delete(failed, result.key)
						m.observe(id, "recovery", "DJ show preparation recovered", State{Programme: result.show}, "preparation", true)
					}
				} else {
					if attempted[result.key] {
						failed[result.key] = true
					}
					m.logger.Warn("DJ preparation unavailable", "station_id", id, "error", result.err)
					m.observe(id, "ai", "DJ show preparation failed; music continues", State{Programme: result.show}, "preparation", false)
				}
			case update := <-inferenceNotices:
				if len(noticeStates) >= 8 && noticeStates[update.show] == "" {
					clear(noticeStates)
				}
				noticeStates[update.show] = update.message
			case result := <-selectionResults:
				if result.show == queuedShow && result.current == queuedAfter {
					clear(selectionStates)
					selectionStates[result.show] = "Ready"
					if result.err != nil {
						selectionStates[result.show] = "Unavailable"
					} else {
						queued = result.item
					}
				}
			case update := <-linkUpdates:
				// Keep state bounded and ignore older shows when updating playout.
				clear(linkStates)
				linkStates[update.show] = update.status
			default:
				return
			}
		}
	}
	var rotation Rotation
	request := func(show, next schedule.Occurrence) {
		if m.options.Producer == nil || show.DJ.ID == "" || !(show.DJ.HasKey || show.DJ.TextReady) {
			return
		}
		if !show.End.IsZero() && !m.now().Before(show.End) {
			return
		}
		key := production.CacheKey(show, next)
		// This admission point is called at song boundaries. A failed plan can
		// be tried again; successful and still-pending work must not be repeated.
		if attempted[key] && !failed[key] {
			return
		}
		select {
		case requests <- prepareRequest{show, next}:
			delete(failed, key)
			if attempted[key] {
				return
			}
			attempted[key] = true
			recent = append(recent, key)
			if len(recent) > 8 {
				old := recent[0]
				recent = recent[1:]
				delete(attempted, old)
				delete(prepared, old)
				delete(preparedVoices, old)
				delete(failed, old)
			}
		default:
		}
	}
	var audioCleanup sync.WaitGroup
	defer audioCleanup.Wait()
	clock := deliveryClock{lead: m.options.StreamBuffer}
	playoutNow := func() time.Time { return clock.programmeTime(m.now(), time.Now()) }
	emit := func(data []byte, state *State) bool {
		// Reflect completed preparation while the current track is still playing.
		// Draining ready results never waits for external generation.
		drain()
		state.AINotice = noticeStates[state.Programme.Key]
		state.CurationStatus = selectionStates[state.Programme.Key]
		if state.Phase == "music" && state.Current.ID == queuedAfter {
			state.Next = queued
		}

		if state.Phase == "music" && (state.Programme.DJ.HasKey || state.Programme.DJ.TextReady) {
			key := production.CacheKey(state.Programme, state.Following)
			if status := linkStates[state.Programme.Key]; status != "" {
				state.PresenterStatus = status
			} else if _, ok := prepared[key]; ok {
				state.PresenterStatus = "Ready"
			} else if failed[key] {
				state.PresenterStatus = "Unavailable"
			} else if attempted[key] {
				state.PresenterStatus = "Preparing"
			}
		}
		if delay := clock.delay(time.Now()); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-r.ctx.Done():
				timer.Stop()
				return false
			case <-timer.C:
			}
		}
		if r.ctx.Err() != nil {
			return false
		}
		if state.Started.IsZero() {
			state.Started = playoutNow()
		}
		clock.advance(len(data))
		m.mu.Lock()
		defer m.mu.Unlock()
		changed := !sameRuntimeState(r.state, *state)
		beforeListeners := len(r.listeners)
		r.state = *state
		frame := streamFrame(data, *state)
		r.recent.append(frame)
		for sub := range r.listeners {
			if !sub.enqueue(frame) {
				m.logger.Warn("Slow stream listener disconnected", "station_id", id)
				m.observe(id, "listener", "A slow listener was disconnected; other listeners continue", *state, "", false)
				delete(r.listeners, sub)
				sub.closeQueue()
				close(sub.done)
			}
		}
		if changed || beforeListeners != len(r.listeners) {
			m.changedLocked()
		}
		if len(r.listeners) == 0 {
			r.stopReason = "No listeners remain"
			r.cancel()
			return false
		}
		return true
	}
	stream := func(audio io.ReadCloser, state State, audienceIDs ...string) (startedResult bool, streamErr error) {
		event := transmissionEvent(id, state)
		event.Started = m.now()
		var bytes int64
		var checkpoint int64
		defer func() {
			if event.Outcome == "" && r.ctx.Err() != nil {
				return
			}
			event.AudioBytes = bytes
			event.Ended = m.now()
			event.Outcome = "completed"
			if streamErr != nil || r.ctx.Err() != nil {
				event.Outcome = "interrupted"
				event.Reason = "Audio interrupted before completion"
			}
			if bytes == 0 {
				event.Outcome = "failed"
				event.Reason = "No audio emitted"
			}
			m.record(event)
			if streamErr != nil && r.ctx.Err() == nil {
				m.observe(id, "failure", "Audio could not be transmitted; check the music source and decoder", state, "audio", false)
			}
		}()
		if len(audienceIDs) > 0 && m.options.Audience != nil {
			ok, err := m.options.Audience.Usable(r.ctx, id, state.Programme.Key, audienceIDs, playoutNow())
			if err != nil || !ok {
				if state.Phase == "music" {
					audienceIDs = nil
				} else {
					audio.Close()
					return false, err
				}
			}
		}
		if state.Current.Kind == content.PresenterLink {
			levels, err := m.levels(r.ctx, id)
			if err != nil {
				audio.Close()
				return false, err
			}
			gain := 100
			var original []byte
			if ready, ok := audio.(*speechReader); ok {
				gain = ready.prepared.level
				original = ready.prepared.raw
			}
			if encoder, ok := m.encoder.(PresenterEncoder); ok && levels.DJ != gain {
				data := original
				if data == nil {
					data, err = production.ReadBounded(audio, 1<<20)
				}
				audio.Close()
				if err != nil {
					return false, err
				}
				audio, err = encoder.OpenPresenter(r.ctx, data, levels.DJ)
				if err != nil {
					return false, err
				}
			}
		}
		// Source/process cleanup cannot delay the following ready audio segment.
		defer func() {
			audioCleanup.Add(1)
			go func() {
				defer audioCleanup.Done()
				if err := audio.Close(); err != nil && r.ctx.Err() == nil {
					m.logger.Warn("Audio cleanup failed", "station_id", id, "error", err)
				}
			}()
		}()
		started := false
		for r.ctx.Err() == nil {
			buf := make([]byte, 4096)
			readStart := time.Now()
			n, err := audio.Read(buf)
			if elapsed := time.Since(readStart); elapsed > time.Second && r.ctx.Err() == nil {
				m.logger.Warn("Audio supply stalled", "station_id", id, "track_id", state.Current.ID, "elapsed", elapsed)
				m.observe(id, "stall", "Audio supply took more than one second; check the source and host load", state, "supply", false)
			}
			if n > 0 {
				if time.Since(readStart) > time.Second {
					m.observe(id, "recovery", "Audio supply resumed", state, "supply", true)
				}
				if !emit(buf[:n], &state) {
					return started, r.ctx.Err()
				}
				bytes += int64(n)
				if event.Outcome == "" {
					event.Started = m.now()
					event.Outcome = "transmitting"
					event.AudioBytes = bytes
					m.record(event)
					m.observe(id, "recovery", "Audio delivery resumed", state, "audio", true)
					m.observe(id, "recovery", "Audio supply resumed", state, "supply", true)
					m.observe(id, "recovery", "Station output available", state, "output", true)
				}
				if bytes-checkpoint >= 240000 {
					event.AudioBytes = bytes
					m.record(event)
					checkpoint = bytes
				}
				if !started {
					started = true
					if len(audienceIDs) > 0 && m.options.Audience != nil {
						ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 2*time.Second)
						err := m.options.Audience.MarkUsed(ctx, id, audienceIDs, state.Started)
						cancel()
						if err != nil {
							m.logger.Warn("Audience broadcast receipt unavailable", "station_id", id, "error", err)
						}
					}
					if m.options.Session != nil && state.Programme.ShowID != "" {
						ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 2*time.Second)
						err := m.options.Session.Aired(ctx, state.Programme, state.Current, state.SpeechText)
						cancel()
						if err != nil {
							m.logger.Warn("Episode airtime context unavailable", "error", err)
						}
					}
					if state.Phase == "music" && m.options.History != nil {
						// Persist after the first delivered music bytes, even if the last
						// listener disconnects immediately afterwards. Preparation is not a play.
						ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 2*time.Second)
						err := m.options.History.RecordPlay(ctx, id, state.Current.ID, state.Started)
						cancel()
						if err != nil {
							m.logger.Error("Track history could not be saved", "station_id", id, "track_id", state.Current.ID, "error", err)
							return started, errors.Join(errHistory, err)
						}
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return started, nil
				}
				return started, err
			}
		}
		return started, r.ctx.Err()
	}
	var previous string
	var previousMusic content.AudioItem
	var playing schedule.Occurrence
	var oldPrepared production.Prepared
	var oldVoice preparedResult
	var pendingLink <-chan linkResult
	var linkCancel context.CancelFunc
	linkSlots := make(chan struct{}, 1)
	var linkWorkers sync.WaitGroup
	var audioWorkers sync.WaitGroup
	var ahead *audioPreparation
	stopLink := func() {
		if linkCancel != nil {
			linkCancel()
			linkCancel = nil
		}
	}
	defer func() {
		stopLink()
		if ahead != nil {
			ahead.cancel()
		}
		linkWorkers.Wait()
		audioWorkers.Wait()
	}()
	introPlayed := false
	failures := 0
	for r.ctx.Err() == nil {
		timeline, err := m.timelineAt(r.ctx, id, playoutNow())
		if err != nil || timeline.Current.PlaylistID == "" {
			return
		}
		pool, err := m.repo.Pool(r.ctx, timeline.Current.PlaylistID)
		if err != nil || len(pool) == 0 {
			return
		}
		drain()
		key := production.CacheKey(timeline.Current, timeline.Next)
		plan, ready := prepared[key]
		changing := playing.Key != "" && playing.Key != timeline.Current.Key
		var link *linkResult
		if pendingLink != nil {
			select {
			case result := <-pendingLink:
				if result.err == nil {
					link = &result
				}
			default:
				if m.options.Links != nil {
					m.observe(id, "ai", "DJ speech was not ready at the track boundary; music continues", State{Programme: playing}, "speech", false)
				}
			}
		}
		stopLink()
		pendingLink = nil
		if changing {
			// Never disconnect subscribers at a show boundary. Prepared speech is sent
			// through the same paced fan-out as music, in the outgoing DJ's voice.
			outro := []byte(nil)
			var outroVoice *preparedSpeech
			outroText := oldPrepared.Plan.OutroText
			if link != nil && link.request.Handover && link.request.Following.Key == timeline.Current.Key {
				outro = link.audio
				outroText = link.text
				outroVoice = link.speech
			} else if oldPrepared.Plan.NextKey == timeline.Current.Key {
				outro = oldPrepared.Outro
				outroVoice = oldVoice.outro
			}
			if len(outro) > 0 {
				cue := content.AudioItem{ID: "handover:" + playing.Key, Title: "Handover", Artist: playing.DJ.Name, Kind: content.PresenterLink, Origin: content.Generated}
				stream(speechAudio(outro, outroVoice), State{Status: "Broadcasting", Programme: playing, Following: timeline.Current, Current: cue, SpeechText: outroText, Phase: "handover", PresenterStatus: "Speaking"})
			}
			// A long outgoing track may span more than one short show. Resolve again
			// after speech instead of starting a show which has already ended.
			timeline, err = m.timelineAt(r.ctx, id, playoutNow())
			if err != nil || timeline.Current.PlaylistID == "" {
				return
			}
			pool, err = m.repo.Pool(r.ctx, timeline.Current.PlaylistID)
			if err != nil || len(pool) == 0 {
				return
			}
			key = production.CacheKey(timeline.Current, timeline.Next)
			plan, ready = prepared[key]
			introPlayed = false
			// Keep bounded prefetched assets across the boundary; no duplicate API
			// preparation just because the next show has started.
		}
		if changing && r.ctx.Err() == nil {
			m.observe(id, "handover", "Programme handover to "+timeline.Current.Title, State{Programme: timeline.Current}, "", false)
		}
		rotation, err = m.rotation(r.ctx, id, playoutNow())
		if err != nil {
			m.logger.Error("Track history unavailable", "station_id", id, "error", err)
			return
		}
		if previous == "" {
			previous = rotation.LastTrackID
		}
		pool, err = m.editorialPool(r.ctx, timeline.Current, pool)
		if err != nil {
			return
		}
		candidates, repeatRelaxed := rotationPool(pool, rotation, playoutNow(), previous)
		candidates = varietyPoolFormat(candidates, pool, rotation, previous, previousMusic, timeline.Current.RotationFormat)
		if len(candidates) == 0 {
			return
		}
		playing = timeline.Current
		m.mu.Lock()
		r.playlist = playing.PlaylistID
		m.mu.Unlock()
		request(playing, timeline.Next)
		current := content.AudioItem{}
		if queuedShow == playing.Key {
			for _, a := range candidates {
				if a.ID == queued.ID {
					current = a
					break
				}
			}
		}
		remaining := time.Duration(0)
		if !playing.End.IsZero() {
			remaining = playing.End.Sub(playoutNow())
			if link != nil && !link.request.Handover {
				remaining -= time.Duration(len(link.audio)) * time.Second / 16000
			} else if ready && !introPlayed {
				remaining -= time.Duration(len(plan.Intro)) * time.Second / 16000
			}
		}
		if current.ID == "" {
			// Audience requests enter the prepared next-track link. A cold start
			// continues normal music until a request introduction is ready.
			if selector, ok := m.options.Selector.(interface{ FullPlaylist() bool }); ok && selector.FullPlaylist() && playing.ShowID != "" {
				id, err := m.options.Selector.SelectTrack(r.ctx, production.SelectionRequest{Show: playing, Current: previousMusic, Candidates: candidates, RemainingSeconds: int64(remaining.Seconds())})
				if err == nil {
					for _, a := range candidates {
						if a.ID == id {
							current = a
							break
						}
					}
				}
			}
			if current.ID == "" {
				current = selectTrack(candidates, previous, remaining)
			}
		}
		levels, levelErr := m.levels(r.ctx, id)
		if levelErr != nil {
			return
		}
		musicCtx := withMusicLevel(r.ctx, levels.Music)
		var audio io.ReadCloser
		if ahead != nil {
			if ahead.show == playing.Key && ahead.music == levels.Music && ahead.item.ID == current.ID {
				select {
				case audio = <-ahead.result:
				default:
				}
			}
			if audio == nil {
				ahead.cancel()
			}
			ahead = nil
		}
		var musicErr error
		if audio == nil {
			audio, musicErr = m.encoder.Open(musicCtx, current)
		}
		var announcedRequestIDs []string
		linkUsable := true
		if link != nil && len(link.audienceValidIDs) > 0 && m.options.Audience != nil {
			linkUsable, _ = m.options.Audience.Usable(r.ctx, id, playing.Key, link.audienceValidIDs, playoutNow())
		}
		if musicErr == nil && linkUsable && !changing && link != nil && !link.request.Handover && link.request.Show.Key == playing.Key && link.request.Next.ID == current.ID && len(link.audio) > 0 {
			cue := content.AudioItem{ID: "link:" + playing.Key + ":" + current.ID, Title: "DJ link", Artist: playing.DJ.Name, Kind: content.PresenterLink, Origin: content.Generated}
			spoke, speechErr := stream(speechAudio(link.audio, link.speech), State{Status: "Broadcasting", Programme: playing, Following: timeline.Next, Current: cue, Next: current, SpeechText: link.text, Phase: "chatter", PresenterStatus: "Speaking"}, link.audienceIDs...)
			if spoke && speechErr == nil {
				for _, requestID := range requestIDs(m.approvedAudience(r.ctx, playing, playoutNow()), current.ID) {
					for _, announcedID := range link.audienceValidIDs {
						if requestID == announcedID {
							announcedRequestIDs = append(announcedRequestIDs, requestID)
						}
					}
				}
			}
			introPlayed = true
		} else if musicErr == nil && ready && !introPlayed && len(plan.Intro) > 0 {
			cue := content.AudioItem{ID: "intro:" + playing.Key, Title: "Show introduction", Artist: playing.DJ.Name, Kind: content.PresenterLink, Origin: content.Generated}
			stream(speechAudio(plan.Intro, preparedVoices[key].intro), State{Status: "Broadcasting", Programme: playing, Following: timeline.Next, Current: cue, Next: current, SpeechText: plan.Plan.IntroText, Phase: "chatter", PresenterStatus: "Speaking"})
			introPlayed = true
		}
		oldPrepared = plan
		oldVoice = preparedVoices[key]
		predictedEnd := playoutNow()
		if current.Duration != nil {
			predictedEnd = predictedEnd.Add(*current.Duration)
		}
		nextTimeline := timeline
		if m.options.Schedule != nil && !playing.End.IsZero() && current.Duration != nil && !predictedEnd.Before(playing.End) {
			if resolved, err := m.options.Schedule.Resolve(r.ctx, id, predictedEnd); err == nil {
				nextTimeline = resolved
			}
		}
		nextPool := pool
		if nextTimeline.Current.Key != playing.Key {
			if p, err := m.repo.Pool(r.ctx, nextTimeline.Current.PlaylistID); err == nil {
				nextPool = p
			}
		}
		nextRemaining := time.Duration(0)
		if !nextTimeline.Current.End.IsZero() {
			nextRemaining = nextTimeline.Current.End.Sub(predictedEnd)
		}
		// Reserve the current choice only for predictions; persistence records
		// it when music actually starts. Revalidate the queue at the next boundary.
		if rotation.LastPlayed == nil {
			rotation.LastPlayed = map[string]time.Time{}
		}
		rotation.LastPlayed[current.ID] = playoutNow()
		rotation.LastTrackID = current.ID
		nextPool, err = m.editorialPool(r.ctx, nextTimeline.Current, nextPool)
		if err != nil {
			return
		}
		nextCatalogue := nextPool
		nextPool, _ = rotationPool(nextPool, rotation, predictedEnd, current.ID)
		nextPool = varietyPoolFormat(nextPool, nextCatalogue, rotation, current.ID, current, nextTimeline.Current.RotationFormat)
		queued = requestTrack(m.approvedAudience(r.ctx, nextTimeline.Current, playoutNow()), nextPool, nextRemaining)
		requestedTrack := queued.ID != ""
		if queued.ID == "" {
			queued = selectTrack(nextPool, current.ID, nextRemaining)
		}
		queuedShow = nextTimeline.Current.Key
		queuedAfter = current.ID
		clear(selectionStates)
		// Prepare the next DJ in advance while the outgoing programme is playing.
		if timeline.Next.DJ.ID != "" && !playing.End.IsZero() && playing.End.Sub(playoutNow()) <= 10*time.Minute {
			after, err := m.options.Schedule.Resolve(r.ctx, id, timeline.Next.Start.Add(time.Second))
			if err == nil {
				if p, err := m.repo.Pool(r.ctx, after.Current.PlaylistID); err == nil && len(p) > 0 {
					request(after.Current, after.Next)
				}
			}
		}
		presenter := "Music only"
		if playing.DJ.ID != "" && (playing.DJ.HasKey || playing.DJ.TextReady) {
			presenter = "Preparing"
			if ready {
				presenter = "Ready"
			}
		}
		if (m.options.Links != nil || m.options.Selector != nil) && (playing.DJ.HasKey || playing.DJ.TextReady) {
			select {
			case linkSlots <- struct{}{}:
				if m.options.Selector != nil {
					selectionStates[queuedShow] = "Preparing"
				}
				clear(linkStates)
				linkStates[playing.Key] = "Preparing"
				linkCtx, c := context.WithCancel(r.ctx)
				linkCancel = c
				out := make(chan linkResult, 1)
				pendingLink = out
				linkWorkers.Add(1)
				req := production.LinkRequest{Show: playing, Following: nextTimeline.Current, Current: current, Next: queued, Handover: nextTimeline.Current.Key != playing.Key, RemainingSeconds: int64(remaining.Seconds())}
				selection := production.SelectionRequest{Show: nextTimeline.Current, Current: current, Candidates: m.selectionCandidates(nextPool, current.ID, rotation, nextRemaining), RemainingSeconds: int64(nextRemaining.Seconds())}
				go func() {
					defer linkWorkers.Done()
					defer func() { <-linkSlots }()
					if m.options.Selector != nil {
						chosen := req.Next
						var selectionErr error
						chosen, selectionErr = m.selectNextTrack(linkCtx, selection, chosen, requestedTrack)
						if selectionErr != nil && !errors.Is(selectionErr, context.Canceled) {
							m.logger.Warn("DJ music selection unavailable", "station_id", id, "error", selectionErr)
							m.observe(id, "ai", "DJ curation failed; local music selection continues", State{Programme: req.Show}, "curation", false)
						}
						select {
						case selectionResults <- selectionResult{selection.Show.Key, selection.Current.ID, chosen, selectionErr}:
						case <-linkCtx.Done():
							return
						}
						if selectionErr == nil {
							m.observe(id, "recovery", "DJ curation recovered", State{Programme: req.Show}, "curation", true)
						}
						req.Next = chosen
					}
					if m.options.Links == nil {
						return
					}
					var transcript string
					transcriptCtx := production.WithSpeechTranscript(linkCtx, func(text string) { transcript = text })
					audio, audienceIDs, audienceValidIDs, err := m.audienceLink(production.WithInferenceNotice(transcriptCtx, func(message string) {
						select {
						case inferenceNotices <- inferenceNotice{req.Show.Key, message}:
						default:
						}
					}), req)
					var speech *preparedSpeech
					if err == nil {
						speech, err = m.prepareSpeech(linkCtx, id, audio)
					}
					if err != nil && !errors.Is(err, context.Canceled) {
						m.logger.Warn("DJ link unavailable", "station_id", id, "error", err)
						m.observe(id, "ai", "DJ speech preparation failed; music continues", State{Programme: req.Show}, "speech", false)
					}
					if err == nil {
						m.observe(id, "recovery", "DJ speech preparation recovered", State{Programme: req.Show}, "speech", true)
					}
					status := "Ready"
					if err != nil {
						status = "Unavailable"
					}
					if linkCtx.Err() == nil {
						select {
						case linkUpdates <- linkStatus{req.Show.Key, status}:
						case <-linkCtx.Done():
						}
					}
					select {
					case out <- linkResult{text: transcript, request: req, audio: audio, speech: speech, err: err, audienceIDs: audienceIDs, audienceValidIDs: audienceValidIDs}:
					case <-linkCtx.Done():
					}
				}()
			default:
				// A cancelled worker still cleaning up cannot hold up music or
				// create an unbounded backlog of speech requests.
			}
		}
		// Keep only the next track primed, shared by every station listener.
		// Existing next-item eligibility and show revalidation still apply.
		if preloader, ok := m.encoder.(PreloadingEncoder); ok && queued.ID != "" {
			ahead = startAudioPreparation(musicCtx, preloader, queued, queuedShow, &audioWorkers)
		}
		err = musicErr
		if musicErr != nil {
			e := transmissionEvent(id, State{Programme: playing, Current: current, Phase: "music"})
			e.Started = m.now()
			e.Ended = e.Started
			e.Outcome = "failed"
			e.Reason = "Source or decoder could not open this track"
			m.record(e)
			m.observe(id, "failure", "A track could not be opened; check its source and format", State{Programme: playing, Current: current}, "audio", false)
		}
		started := false
		if err == nil {
			started, err = stream(audio, State{Status: "Broadcasting", Programme: playing, Following: timeline.Next, Current: current, Next: queued, Phase: "music", PresenterStatus: presenter, RepeatRelaxed: repeatRelaxed}, announcedRequestIDs...)
		}
		if errors.Is(err, errHistory) {
			return
		}
		if started {
			failures = 0
			previous = current.ID
			previousMusic = current
		}
		if err != nil || !started {
			failures++
			if failures >= 3 {
				return
			}
			m.mu.Lock()
			r.state.Status = "Retrying"
			r.state.Error = "A track could not be played. Retrying…"
			m.changedLocked()
			m.mu.Unlock()
			select {
			case <-r.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}
