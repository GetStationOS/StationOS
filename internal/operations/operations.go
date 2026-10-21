// Package operations owns durable transmission observations, not playlist ordering.
package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"stationos.local/stationos/internal/changefeed"
	"sync"
	"sync/atomic"
	"time"
)

type Event struct {
	SpeechProviderName                                              string
	SpeechProvider, SpeechConnection, SpeechModel, SpeechVoice      string
	ID, StationID, Kind, Outcome                                    string
	Started, Ended                                                  time.Time
	AudioBytes                                                      int64
	Title, Artist, Composer, Album, TrackID, SourceID, SourceItemID string
	ShowID, Show, DJID, DJ, PlaylistID, Phase, Reason, Speech       string
	Playlist                                                        string
	IncidentCode, Severity, Effect, Action                          string
	Recover                                                         bool
}

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

type Incident struct {
	ID, StationID, Code, Severity, Message, Effect, Action, EventID string
	First, Latest, Recovered, Acknowledged                          time.Time
	Count                                                           int
}
type Filter struct {
	StationID, Query, Kind, Outcome, ShowID, ShowName, DJID, Before, Anchor string
	From, Until                                                             time.Time
	Limit                                                                   int
}
type HistoryPage struct {
	Anchor              string
	Events              []Event
	Total, Number, Size int
}
type Store interface {
	History(context.Context, Filter, int, int) (HistoryPage, error)
	Save(context.Context, Event) error
	Recover(context.Context, time.Time) error
	Events(context.Context, Filter) ([]Event, error)
	Incidents(context.Context, string, bool) ([]Incident, error)
	Acknowledge(context.Context, string, string, time.Time) error
}

// Recorder never blocks the paced audio loop. Loss or write failures latch a
// visible health warning until restart: a later successful write cannot repair it.
type Recorder struct {
	changes changefeed.Bus
	store   Store
	log     *slog.Logger
	queue   chan Event
	done    chan struct{}
	mu      sync.RWMutex
	closed  bool
	failed  atomic.Bool
	cancel  context.CancelFunc
}

func New(ctx context.Context, store Store, logger *slog.Logger) (*Recorder, error) {
	if err := store.Recover(ctx, time.Now()); err != nil {
		return nil, err
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	if logger == nil {
		logger = slog.Default()
	}
	r := &Recorder{cancel: cancel, store: store, log: logger, queue: make(chan Event, 512), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		for e := range r.queue {
			ctx, cancel := context.WithTimeout(workerCtx, 2*time.Second)
			err := store.Save(ctx, e)
			cancel()
			if err != nil {
				if r.failed.CompareAndSwap(false, true) {
					r.changes.Publish("health")
				}
				r.log.Error("Transmission history write failed", "station_id", e.StationID, "event_id", e.ID, "error", err)
			}
		}
	}()
	return r, nil
}
func (r *Recorder) Record(e Event) {
	if e.ID == "" {
		e.ID = ID()
	}
	if e.Started.IsZero() {
		e.Started = time.Now()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return
	}
	select {
	case r.queue <- e:
	default:
		if r.failed.CompareAndSwap(false, true) {
			r.changes.Publish("health")
		}
		r.log.Error("Transmission history queue full", "station_id", e.StationID)
	}
}
func (r *Recorder) Healthy() bool { return !r.failed.Load() }
func (r *Recorder) Close() {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.queue)
	}
	r.mu.Unlock()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		r.cancel()
		<-r.done
	}
	r.cancel()
}

// WatchChanges announces the first recording failure, including queue overflow.
func (r *Recorder) WatchChanges() (<-chan struct{}, func()) { return r.changes.Watch("health") }
