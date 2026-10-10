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

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/mixer"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

var ErrUnavailable = errors.New("station has no playable music; assign a playlist with available tracks")
var ErrBusy = errors.New("broadcast capacity reached; try again shortly")
var ErrClosed = errors.New("broadcast stopped")

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
	Programme, Following   schedule.Occurrence
	Phase, PresenterStatus string
	Status                 string
	Listeners              int
	Current, Next          content.AudioItem
	Started                time.Time
	Error                  string
}
type Options struct {
	Mixer                                   mixer.Repository
	MaxStations, MaxListeners, BufferChunks int
	StreamBuffer                            time.Duration
	Now                                     func() time.Time
	Schedule                                schedule.Source
	Producer                                production.Producer
	Links                                   production.LinkProducer
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
	playlist  string
	ctx       context.Context
	cancel    context.CancelFunc
	listeners map[*Subscription]struct{}
	state     State
	recent    recentAudio
	done      chan struct{}
}
type Subscription struct {
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

// SubscribeClient admits one connection per station and opaque player session.
// A browser may leave its initial media probe open when it requests playback.
// Replace that connection atomically, without restarting the shared programme.
// Empty client IDs represent independent external stream connections.
func (m *Manager) SubscribeClient(ctx context.Context, id, clientID string) (*Subscription, error) {
	// Lookup before taking the runtime lock, then serialize admission/worker creation.
	timeline, err := m.timeline(ctx, id)
	playlist := timeline.Current.PlaylistID
	if err != nil {
		return nil, err
	}
	if playlist == "" {
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
			r = &run{playlist: playlist, ctx: worker, cancel: cancel, done: make(chan struct{}), listeners: map[*Subscription]struct{}{}, state: State{Status: "Starting"}, recent: recentAudio{limit: int(m.options.StreamBuffer * audioBytesPerSecond / time.Second)}}
			m.runs[id] = r
			m.wg.Add(1)
			go m.play(id, r)
		}
		if clientID != "" {
			for previous := range r.listeners {
				if previous.clientID == clientID {
					delete(r.listeners, previous)
					close(previous.queue)
					close(previous.done)
				}
			}
		}
		if len(r.listeners) >= m.options.MaxListeners {
			m.mu.Unlock()
			return nil, ErrBusy
		}
		queue := make(chan []byte, m.options.BufferChunks)
		done := make(chan struct{})
		if r.recent.bytes > 0 {
			queue <- r.recent.snapshot()
		}
		sub := &Subscription{Audio: queue, queue: queue, Done: done, done: done, m: m, id: id, clientID: clientID, run: r}
		r.listeners[sub] = struct{}{}
		m.changedLocked()
		m.mu.Unlock()
		return sub, nil
	}
}
func (s *Subscription) Close() {
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := s.run.listeners[s]; ok {
		delete(s.run.listeners, s)
		close(s.queue)
		close(s.done)
		if len(s.run.listeners) == 0 {
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
		r.cancel()
		for s := range r.listeners {
			delete(r.listeners, s)
			close(s.queue)
			close(s.done)
		}
	}
}
func (m *Manager) StopPlaylist(id string) {
	m.mu.Lock()
	m.changedLocked()
	defer m.mu.Unlock()
	for _, r := range m.runs {
		if r.playlist == id {
			r.cancel()
			for s := range r.listeners {
				delete(r.listeners, s)
				close(s.queue)
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
		r.cancel()
		for s := range r.listeners {
			delete(r.listeners, s)
			close(s.queue)
			close(s.done)
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// Temporary selector; playlist order never determines the programme. An AI
// curator can replace this boundary without changing membership or delivery.
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
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		for s := range r.listeners {
			delete(r.listeners, s)
			close(s.queue)
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
			m.last[id] = State{Status: "Unavailable", Error: "Music could not be played. Check the assigned playlist and source."}
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
	pool       []content.AudioItem
}
type preparedResult struct {
	key          string
	prepared     production.Prepared
	intro, outro *preparedSpeech
	err          error
}
type linkResult struct {
	request production.LinkRequest
	audio   []byte
	speech  *preparedSpeech
	err     error
}

func (m *Manager) playProgramme(id string, r *run) {
	prepCtx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	requests := make(chan prepareRequest, 2)
	results := make(chan preparedResult, 2)
	prepDone := make(chan struct{})
	go func() {
		defer close(prepDone)
		for {
			select {
			case <-prepCtx.Done():
				return
			case req := <-requests:
				prepared, err := m.options.Producer.Prepare(prepCtx, req.show, req.next, req.pool)
				var intro, outro *preparedSpeech
				if err == nil {
					intro, err = m.prepareSpeech(prepCtx, id, prepared.Intro)
				}
				if err == nil {
					outro, err = m.prepareSpeech(prepCtx, id, prepared.Outro)
				}
				select {
				case results <- preparedResult{key: production.CacheKey(req.show, req.next), prepared: prepared, intro: intro, outro: outro, err: err}:
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
	drain := func() {
		for {
			select {
			case result := <-results:
				if result.err == nil {
					if attempted[result.key] {
						prepared[result.key] = result.prepared
						preparedVoices[result.key] = result
					}
				} else {
					if attempted[result.key] {
						failed[result.key] = true
					}
					m.logger.Warn("DJ preparation unavailable", "station_id", id, "error", result.err)
				}
			default:
				return
			}
		}
	}
	request := func(show, next schedule.Occurrence, pool []content.AudioItem) {
		if m.options.Producer == nil || show.DJ.ID == "" || !show.DJ.HasKey {
			return
		}
		key := production.CacheKey(show, next)
		if attempted[key] {
			return
		}
		select {
		case requests <- prepareRequest{show, next, pool}:
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
	emit := func(data []byte, state State) bool {
		// Reflect completed preparation while the current track is still playing.
		// Draining ready results never waits for external generation.
		drain()
		if state.Phase == "music" && state.Programme.DJ.HasKey {
			key := production.CacheKey(state.Programme, state.Following)
			if _, ok := prepared[key]; ok {
				state.PresenterStatus = "Ready"
			} else if failed[key] {
				state.PresenterStatus = "Unavailable"
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
		clock.advance(len(data))
		m.mu.Lock()
		defer m.mu.Unlock()
		changed := !sameRuntimeState(r.state, state)
		beforeListeners := len(r.listeners)
		r.state = state
		r.recent.append(data)
		for sub := range r.listeners {
			select {
			case sub.queue <- data:
			default:
				m.logger.Warn("Slow stream listener disconnected", "station_id", id)
				delete(r.listeners, sub)
				close(sub.queue)
				close(sub.done)
			}
		}
		if changed || beforeListeners != len(r.listeners) {
			m.changedLocked()
		}
		if len(r.listeners) == 0 {
			r.cancel()
			return false
		}
		return true
	}
	stream := func(audio io.ReadCloser, state State) (bool, error) {
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
			}
			if n > 0 {
				if !started {
					state.Started = m.now()
					started = true
				}
				if !emit(buf[:n], state) {
					return started, r.ctx.Err()
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
	var queued content.AudioItem
	var queuedShow string
	var selectedIDs []string
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
			}
		}
		stopLink()
		pendingLink = nil
		if changing {
			// Never disconnect subscribers at a show boundary. Prepared speech is sent
			// through the same paced fan-out as music, in the outgoing DJ's voice.
			outro := []byte(nil)
			var outroVoice *preparedSpeech
			if link != nil && link.request.Handover && link.request.Following.Key == timeline.Current.Key {
				outro = link.audio
				outroVoice = link.speech
			} else if oldPrepared.Plan.NextKey == timeline.Current.Key {
				outro = oldPrepared.Outro
				outroVoice = oldVoice.outro
			}
			if len(outro) > 0 {
				cue := content.AudioItem{ID: "handover:" + playing.Key, Title: "Handover", Artist: playing.DJ.Name, Kind: content.PresenterLink, Origin: content.Generated}
				stream(speechAudio(outro, outroVoice), State{Status: "Broadcasting", Programme: playing, Following: timeline.Current, Current: cue, Phase: "handover", PresenterStatus: "Speaking"})
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
			selectedIDs = nil
			previous = ""
			// Keep bounded prefetched assets across the boundary; no duplicate API
			// preparation just because the next show has started.
		}
		playing = timeline.Current
		m.mu.Lock()
		r.playlist = playing.PlaylistID
		m.mu.Unlock()
		request(playing, timeline.Next, pool)
		if ready && len(selectedIDs) == 0 {
			selectedIDs = append([]string(nil), plan.Plan.TrackIDs...)
		}
		current := content.AudioItem{}
		if queuedShow == playing.Key {
			for _, a := range pool {
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
			current = selectTrack(pool, previous, selectedIDs, remaining)
		}
		for i, id := range selectedIDs {
			if id == current.ID {
				selectedIDs = append(selectedIDs[:i], selectedIDs[i+1:]...)
				break
			}
		}
		if !changing && link != nil && !link.request.Handover && link.request.Show.Key == playing.Key && link.request.Next.ID == current.ID && len(link.audio) > 0 {
			cue := content.AudioItem{ID: "link:" + playing.Key + ":" + current.ID, Title: "DJ link", Artist: playing.DJ.Name, Kind: content.PresenterLink, Origin: content.Generated}
			stream(speechAudio(link.audio, link.speech), State{Status: "Broadcasting", Programme: playing, Following: timeline.Next, Current: cue, Next: current, Phase: "chatter", PresenterStatus: "Speaking"})
			introPlayed = true
		} else if ready && !introPlayed && len(plan.Intro) > 0 {
			cue := content.AudioItem{ID: "intro:" + playing.Key, Title: "Show introduction", Artist: playing.DJ.Name, Kind: content.PresenterLink, Origin: content.Generated}
			stream(speechAudio(plan.Intro, preparedVoices[key].intro), State{Status: "Broadcasting", Programme: playing, Following: timeline.Next, Current: cue, Next: current, Phase: "chatter", PresenterStatus: "Speaking"})
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
		queued = selectTrack(nextPool, current.ID, selectedIDs, nextRemaining)
		queuedShow = nextTimeline.Current.Key
		// Prepare the next DJ in advance while the outgoing programme is playing.
		if timeline.Next.DJ.ID != "" && !playing.End.IsZero() && playing.End.Sub(playoutNow()) <= 10*time.Minute {
			after, err := m.options.Schedule.Resolve(r.ctx, id, timeline.Next.Start.Add(time.Second))
			if err == nil {
				if p, err := m.repo.Pool(r.ctx, after.Current.PlaylistID); err == nil {
					request(after.Current, after.Next, p)
				}
			}
		}
		presenter := "Music only"
		if playing.DJ.ID != "" && playing.DJ.HasKey {
			presenter = "Preparing"
			if ready {
				presenter = "Ready"
			}
		}
		if m.options.Links != nil && playing.DJ.HasKey {
			select {
			case linkSlots <- struct{}{}:
				linkCtx, c := context.WithCancel(r.ctx)
				linkCancel = c
				out := make(chan linkResult, 1)
				pendingLink = out
				linkWorkers.Add(1)
				req := production.LinkRequest{Show: playing, Following: nextTimeline.Current, Current: current, Next: queued, Handover: nextTimeline.Current.Key != playing.Key, RemainingSeconds: int64(remaining.Seconds())}
				go func() {
					defer linkWorkers.Done()
					defer func() { <-linkSlots }()
					audio, err := m.options.Links.Link(linkCtx, req)
					var speech *preparedSpeech
					if err == nil {
						speech, err = m.prepareSpeech(linkCtx, id, audio)
					}
					if err != nil && !errors.Is(err, context.Canceled) {
						m.logger.Warn("DJ link unavailable", "station_id", id, "error", err)
					}
					select {
					case out <- linkResult{request: req, audio: audio, speech: speech, err: err}:
					case <-linkCtx.Done():
					}
				}()
			default:
				// A cancelled worker still cleaning up cannot hold up music or
				// create an unbounded backlog of speech requests.
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
		// Keep only the next track primed, shared by every station listener.
		// Existing next-item eligibility and show revalidation still apply.
		if preloader, ok := m.encoder.(PreloadingEncoder); ok && queued.ID != "" {
			ahead = startAudioPreparation(musicCtx, preloader, queued, queuedShow, &audioWorkers)
		}
		err = nil
		if audio == nil {
			audio, err = m.encoder.Open(musicCtx, current)
		}
		started := false
		if err == nil {
			started, err = stream(audio, State{Status: "Broadcasting", Programme: playing, Following: timeline.Next, Current: current, Next: queued, Phase: "music", PresenterStatus: presenter})
		}
		if started {
			failures = 0
			previous = current.ID
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
