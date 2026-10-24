package broadcast

import (
	"context"
	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/announcements"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
	"sync"
	"time"
)

type announcementTarget struct {
	Show schedule.Occurrence
	At   time.Time
}
type preparedAnnouncement struct {
	Message         announcements.Message
	DJ              djs.Profile
	Audio           []byte
	Speech          *preparedSpeech
	Text            string
	Provenance      production.SpeechProvenance
	Writing         ai.TextProvenance
	SettingsVersion int
}
type announcementPreparation struct {
	ready *preparedAnnouncement
	wake  time.Time
}
type announcementWorker struct {
	requests chan announcementTarget
	mu       sync.Mutex
	ready    *preparedAnnouncement
	done     chan struct{}
}

func (w *announcementWorker) target(show schedule.Occurrence, at time.Time) {
	if w == nil {
		return
	}
	select {
	case <-w.requests:
	default:
	}
	select {
	case w.requests <- announcementTarget{show, at}:
	default:
	}
}
func (w *announcementWorker) take() *preparedAnnouncement {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	r := w.ready
	w.ready = nil
	return r
}
func (m *Manager) announcementChoice(ctx context.Context, id string, target announcementTarget) (announcements.Message, djs.Profile, int, error) {
	settings, err := m.options.Announcements.Settings(ctx, id)
	if err != nil {
		return announcements.Message{}, djs.Profile{}, 0, err
	}
	loc := time.UTC
	if m.options.Stations != nil {
		st, e := m.options.Stations.Get(ctx, id)
		if e != nil {
			return announcements.Message{}, djs.Profile{}, 0, e
		}
		loc, e = time.LoadLocation(st.Timezone)
		if e != nil {
			return announcements.Message{}, djs.Profile{}, 0, e
		}
	}
	items, err := m.options.Announcements.List(ctx, id)
	if err != nil {
		return announcements.Message{}, djs.Profile{}, 0, err
	}
	last, err := m.options.Announcements.LastAired(ctx, id)
	if err != nil {
		return announcements.Message{}, djs.Profile{}, 0, err
	}
	var chosen announcements.Message
	for _, a := range items {
		if !a.Eligible(target.At, loc, target.Show.ShowID, schedule.EpisodeKey(target.Show.Key), last, settings.GapMinutes) {
			continue
		}
		if chosen.ID == "" || a.Next && !chosen.Next || a.Next == chosen.Next && a.Due(target.At, loc).Before(chosen.Due(target.At, loc)) {
			chosen = a
		}
	}
	dj := target.Show.DJ
	djID := chosen.DJID
	if djID == "" && dj.ID == "" {
		djID = settings.DJID
	}
	if chosen.ID != "" && djID != "" {
		if m.options.DJs == nil {
			return chosen, djs.Profile{}, settings.Version, production.ErrUnavailable
		}
		dj, err = m.options.DJs.Get(ctx, djID)
	}
	return chosen, dj, settings.Version, err
}

// A single bounded worker per active station reacts to track reservations and
// committed edits. Cancellation is joined before another synthesis can start.
func (m *Manager) startAnnouncements(ctx context.Context, id string) *announcementWorker {
	if m.options.Announcements == nil || m.options.AnnouncementProducer == nil {
		return nil
	}
	w := &announcementWorker{requests: make(chan announcementTarget, 1), done: make(chan struct{})}
	changes, release := m.options.Changes.Watch("announcements")
	go func() {
		defer close(w.done)
		defer release()
		var target announcementTarget
		var cancel context.CancelFunc
		var result chan announcementPreparation
		var timer *time.Timer
		var deadline <-chan time.Time
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		var restart bool
		start := func() {
			if target.At.IsZero() {
				return
			}
			work, c := context.WithCancel(ctx)
			cancel = c
			result = make(chan announcementPreparation, 1)
			out := result
			snapshot := target
			if snapshot.At.Before(m.now()) {
				snapshot.At = m.now()
			}
			go func() {
				var ready *preparedAnnouncement
				defer func() {
					wake := time.Time{}
					if ready == nil && work.Err() == nil {
						wake = m.nextAnnouncementWake(work, id, snapshot)
					}
					out <- announcementPreparation{ready, wake}
				}()
				a, dj, version, err := m.announcementChoice(work, id, snapshot)
				if err != nil || a.ID == "" || dj.ID == "" {
					return
				}
				var text string
				var prov production.SpeechProvenance
				var writing ai.TextProvenance
				c := production.WithSpeechTranscript(production.WithSpeechProvenance(ai.WithTextProvenance(work, func(p ai.TextProvenance) { writing = p }), func(p production.SpeechProvenance) { prov = p }), func(s string) { text = s })
				audio, err := m.options.AnnouncementProducer.Announcement(c, a, dj)
				if err != nil {
					if work.Err() == nil {
						m.observe(id, "ai", "Announcement preparation failed; music continues", State{Programme: snapshot.Show}, "announcement", false)
					}
					return
				}
				speech, err := m.prepareSpeech(work, id, audio)
				if err != nil || work.Err() != nil {
					return
				}
				ready = &preparedAnnouncement{Message: a, DJ: dj, Audio: audio, Speech: speech, Text: text, Provenance: prov, Writing: writing, SettingsVersion: version}
			}()
		}
		invalidate := func() {
			if timer != nil {
				timer.Stop()
				timer = nil
			}
			deadline = nil
			w.mu.Lock()
			w.ready = nil
			w.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			if result != nil {
				restart = true
			} else {
				start()
			}
		}
		for {
			select {
			case <-ctx.Done():
				if cancel != nil {
					cancel()
				}
				if result != nil {
					<-result
				}
				return
			case t := <-w.requests:
				target = t
				invalidate()
			case <-changes:
				invalidate()
			case <-deadline:
				deadline = nil
				start()
			case prepared := <-result:
				result = nil
				if cancel != nil {
					cancel()
					cancel = nil
				}
				if restart {
					restart = false
					start()
					continue
				}
				w.mu.Lock()
				w.ready = prepared.ready
				w.mu.Unlock()
				if a := prepared.ready; a != nil {
					programme := target.Show
					programme.DJ = a.DJ
					m.observe(id, "recovery", "Announcement prepared: "+a.Message.Title+". This is not proof of airtime.", State{Programme: programme, SpeechText: a.Text, WritingProvenance: a.Writing, Phase: "announcement-preparation"}, "announcement", true)
				}
				if prepared.wake.After(m.now()) {
					if timer != nil {
						timer.Stop()
					}
					timer = time.NewTimer(prepared.wake.Sub(m.now()))
					deadline = timer.C
				}
			}
		}
	}()
	return w
}
func (m *Manager) announcementValid(ctx context.Context, id string, a *preparedAnnouncement, show schedule.Occurrence, at time.Time) bool {
	target := announcementTarget{show, at}
	choice, dj, version, err := m.announcementChoice(ctx, id, target)
	return err == nil && choice.ID == a.Message.ID && choice.Version == a.Message.Version && choice.Next == a.Message.Next && dj.ID == a.DJ.ID && production.CacheKey(schedule.Occurrence{DJ: dj}, schedule.Occurrence{}) == production.CacheKey(schedule.Occurrence{DJ: a.DJ}, schedule.Occurrence{}) && version == a.SettingsVersion
}

func (m *Manager) nextAnnouncementWake(ctx context.Context, id string, target announcementTarget) time.Time {
	now := m.now()
	loc := time.UTC
	if m.options.Stations != nil {
		st, err := m.options.Stations.Get(ctx, id)
		if err != nil {
			return time.Time{}
		}
		loc, err = time.LoadLocation(st.Timezone)
		if err != nil {
			return time.Time{}
		}
	}
	items, err := m.options.Announcements.List(ctx, id)
	if err != nil {
		return time.Time{}
	}
	settings, err := m.options.Announcements.Settings(ctx, id)
	if err != nil {
		return time.Time{}
	}
	last, err := m.options.Announcements.LastAired(ctx, id)
	if err != nil {
		return time.Time{}
	}
	var wake time.Time
	for _, a := range items {
		if !a.Matches(target.Show.ShowID, schedule.EpisodeKey(target.Show.Key)) {
			continue
		}
		at := a.NextOpportunity(now, loc)
		if at.IsZero() {
			continue
		}
		gap := last.Add(time.Duration(settings.GapMinutes) * time.Minute)
		if !a.Next && !last.IsZero() && at.Before(gap) {
			at = gap
		}
		if at.After(now) && at.Before(a.Expires) && (wake.IsZero() || at.Before(wake)) {
			wake = at
		}
	}
	return wake
}

// Presenter normalization produces 128 kbps MP3. Reserve its measured audio
// duration alongside the committed recording; never displace that recording.
func announcementFits(a *preparedAnnouncement, show schedule.Occurrence, next content.AudioItem, at time.Time) bool {
	if show.End.IsZero() {
		return true
	}
	duration := time.Duration(len(a.Audio))*time.Second/16000 + time.Second
	if next.Duration != nil && *next.Duration > 0 {
		duration += *next.Duration
	}
	return !at.Add(duration).After(show.End)
}
