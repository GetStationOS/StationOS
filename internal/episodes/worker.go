package episodes

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/changefeed"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

// Start prepares only the next show per station, plus recovery for an active show. A persisted
// retry deadline doubles as a crash-recoverable lease. No audio work is started.
func (s *Service) Start(parent context.Context, stations station.Repository, shows schedule.Repository, bus *changefeed.Bus, logger *slog.Logger) func() {
	if logger == nil {
		logger = slog.Default()
	}
	s.Logger = logger
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		changes, stop := bus.Watch("episodes")
		defer stop()
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		for {
			if err := s.sweep(ctx, stations, shows); err != nil && ctx.Err() == nil {
				logger.Warn("Episode preparation deferred", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			case <-changes:
			}
		}
	}()
	return func() { cancel(); <-done }
}
func (s *Service) sweep(ctx context.Context, stations station.Repository, shows schedule.Repository) error {
	return s.sweepAt(ctx, stations, shows, time.Now())
}

// preparationWindow selects by airtime, never by readiness: a ready, failed or
// disabled next show must not open the gate for planning the rest of the week.
func preparationWindow(shows []schedule.Show, now time.Time, loc *time.Location) []schedule.Occurrence {
	out := []schedule.Occurrence{}
	for _, o := range schedule.Occurrences(shows, now, loc) {
		if !o.End.After(now) {
			continue
		}
		out = append(out, o)
		if o.Start.After(now) {
			break
		}
	}
	return out
}

func (s *Service) sweepAt(ctx context.Context, stations station.Repository, shows schedule.Repository, now time.Time) error {
	all, err := stations.List(ctx)
	if err != nil {
		return err
	}
	pending := []schedule.Occurrence{}
	for _, st := range all {
		loc, e := time.LoadLocation(st.Timezone)
		if e != nil {
			return e
		}
		ss, e := shows.List(ctx, st.ID)
		if e != nil {
			return e
		}
		for _, o := range preparationWindow(ss, now, loc) {
			episode, e := s.Store.Get(ctx, o.Key)
			if e == nil && episode.Cancelled {
				continue
			}
			// Rolling curation begins with the live episode session, never a week of preplanning.
			if errors.Is(e, ErrNotFound) || (e == nil && !episodeSessionReady(episode)) {
				continue
			}
			if e == nil && o.Start.After(now) {
				continue
			}
			if e != nil {
				return e
			}
			if episode.ShowKey != o.Key {
				if e = s.Store.Update(ctx, o, func(*Episode) error { return nil }); e != nil {
					return e
				}
				episode.Status = "attention"
				episode.RetryAt = time.Time{}
			}
			if episode.Status == "ready" {
				available, checkErr := s.Available(ctx, o, episode)
				if checkErr != nil {
					return checkErr
				}
				if available {
					continue
				}
				if e = s.Store.Update(ctx, o, func(ep *Episode) error {
					ep.Status = "attention"
					ep.Problem = "Playlist availability changed; updating the running order."
					ep.RetryAt = time.Time{}
					return nil
				}); e != nil {
					return e
				}
			}
			if now.Before(episode.RetryAt) || !o.DJ.TextReady {
				continue
			}
			pending = append(pending, o)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Start.Before(pending[j].Start) })
	// One batch yields to configuration changes and cancellation between jobs.
	for _, o := range pending[:min(4, len(pending))] {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		previous, readErr := s.Store.Get(ctx, o.Key)
		if readErr != nil {
			return readErr
		}
		err = s.Store.Update(ctx, o, func(e *Episode) error {
			e.Status = "planning"
			e.Note("planning attempt", "Preparing this episode running order.")
			e.Problem = ""
			e.RetryAt = time.Now().Add(6 * time.Minute)
			return nil
		})
		if err != nil {
			continue
		}
		task, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err = s.Plan(task, o)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// A producer edit or actual play invalidates the generation snapshot; never
			// overwrite newer choices, and let the next sweep adapt the same session.
			if errors.Is(err, ErrStale) {
				_ = s.Store.Update(ctx, o, func(e *Episode) error {
					if e.Status == "planning" {
						e.Status = "attention"
						e.RetryAt = time.Now().Add(time.Minute)
					}
					return nil
				})
				continue
			}
			logger := s.Logger
			if logger == nil {
				logger = slog.Default()
			}
			logger.Warn("Episode planning failed", "station_id", o.StationID, "show_id", o.ShowID, "episode_start", o.Start, "model", o.DJ.Model, "error", err)
			failure := err
			saveErr := s.Store.Update(ctx, o, func(e *Episode) error {
				e.PlanningFailed = true
				e.Status = "attention"
				if len(e.Plan) == 0 {
					e.Status = "unplanned"
				}
				e.Problem = "Episode planning failed. Music continues automatically; preparation will retry. Check the curation connection and server logs."
				if errors.Is(failure, ai.ErrInvalidStructuredResponse) {
					e.Problem = "The DJ returned an invalid planning response. No new running order was saved. Automatic preparation will retry."
				}
				if errors.Is(failure, context.DeadlineExceeded) {
					e.Problem = "Episode planning timed out. Music continues automatically; preparation will retry."
				}
				for i := len(e.Turns) - 1; i >= 0; i-- {
					if e.Turns[i].Kind == "planning attempt" {
						break
					}
					if e.Turns[i].Kind == "plan validation" {
						e.Problem = "The running order needs revision: " + e.Turns[i].Text + " Automatic preparation will retry."
						break
					}
				}
				e.RetryAt = time.Now().Add(5 * time.Minute)
				return nil
			})
			if saveErr != nil {
				return saveErr
			}
			if s.ReportPlanning != nil {
				s.ReportPlanning(o, failure)
			}
		} else if previous.PlanningFailed && s.ReportPlanning != nil {
			s.ReportPlanning(o, nil)
		}
	}
	return nil
}

func episodeSessionReady(e Episode) bool {
	if len(e.Aired) > 0 {
		return true
	}
	for _, t := range e.Turns {
		if t.Kind == "prepared presentation (not yet aired)" || t.Kind == "aired speech" {
			return true
		}
	}
	return false
}
