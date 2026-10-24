package broadcast

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/operations"
	"stationos.local/stationos/internal/production"
	"stationos.local/stationos/internal/schedule"
)

func TestLinkPlaybackRejection(t *testing.T) {
	show := schedule.Occurrence{Key: "show:1"}
	next := content.AudioItem{ID: "next"}
	base := linkResult{request: production.LinkRequest{Show: show, Next: next}}
	for _, tc := range []struct {
		name     string
		change   func(*linkResult)
		musicErr error
		usable   bool
		want     string
	}{
		{name: "ready", usable: true},
		{name: "music failed", musicErr: errors.New("decoder failed"), usable: true, want: "could not be opened"},
		{name: "audience expired", want: "audience contribution"},
		{name: "handover predicted", usable: true, change: func(l *linkResult) { l.request.Handover = true }, want: "handover did not occur"},
		{name: "show revision changed", usable: true, change: func(l *linkResult) { l.request.Show.Key = "show:0" }, want: "programme changed"},
		{name: "next changed", usable: true, change: func(l *linkResult) { l.request.Next.ID = "other" }, want: "next recording changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link := base
			if tc.change != nil {
				tc.change(&link)
			}
			got := linkPlaybackRejection(link, show, next, tc.musicErr, tc.usable)
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("reason %q, want %q", got, tc.want)
			}
		})
	}
}

type speechRevisionSchedule struct{ *revisedEpisodeSchedule }

func (s speechRevisionSchedule) Resolve(ctx context.Context, id string, at time.Time) (schedule.Timeline, error) {
	timeline, err := s.revisedEpisodeSchedule.Resolve(ctx, id, at)
	timeline.Current.DJ = djs.Profile{ID: "alex", TextReady: true}
	return timeline, err
}

func TestPreparedSpeechRevisionRejectionIsReported(t *testing.T) {
	store := &recordingStore{events: map[string]operations.Event{}}
	recorder, err := operations.New(context.Background(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	now := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	source := speechRevisionSchedule{&revisedEpisodeSchedule{start: now}}
	source.revision.Store(1)
	var logs bytes.Buffer
	encoder := &fakeEncoder{calls: make(chan opening, 16)}
	links := readyLinks{called: make(chan production.LinkRequest, 8)}
	repo := &memoryRepo{playlist: "pool", pool: []content.AudioItem{length("a", 90), length("b", 90)}}
	manager := New(repo, encoder, slog.New(slog.NewTextHandler(&logs, nil)), Options{Schedule: source, Links: links, Operations: recorder, Now: func() time.Time { return now }})
	defer manager.Close()
	sub, err := manager.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, encoder)
	musicStatus(t, manager, sub, first, "Ready")
	source.revision.Store(2)
	first.write.Close()
	second := opened(t, encoder)
	if _, err := second.write.Write([]byte("SECOND")); err != nil {
		t.Fatal(err)
	}
	if string(receive(t, sub)) != "SECOND" {
		t.Fatal("stale speech aired or music interrupted")
	}
	sub.Close()
	manager.Close()
	recorder.Close()
	if !strings.Contains(logs.String(), "Prepared DJ speech skipped") || !strings.Contains(logs.String(), "programme changed") {
		t.Fatal(logs.String())
	}
	for _, event := range store.events {
		if event.IncidentCode == "speech" && strings.Contains(event.Reason, "programme changed") {
			return
		}
	}
	t.Fatal("skipped prepared speech missing from incident history")
}

type newlyCreatedShowSchedule struct {
	enabled atomic.Bool
	shows   timedSchedule
}

func (s *newlyCreatedShowSchedule) Resolve(ctx context.Context, id string, at time.Time) (schedule.Timeline, error) {
	if !s.enabled.Load() {
		return schedule.Timeline{Current: schedule.Occurrence{Key: "default:" + id + ":pool", StationID: id, PlaylistID: "pool", Title: "Continuous music"}}, nil
	}
	return s.shows.Resolve(ctx, id, at)
}

func TestShowCreatedDuringDefaultPlaybackAirsSpeechWithoutRestart(t *testing.T) {
	now := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	source := &newlyCreatedShowSchedule{shows: timedSchedule{shows: []schedule.Show{{ID: "pop", StationID: "radio", Name: "The 80s Collection", Day: 5, Start: 360, End: 1200, PlaylistID: "pool", DJ: djs.Profile{ID: "alex", Name: "Alex", TextReady: true}}}}}
	encoder := &fakeEncoder{calls: make(chan opening, 16)}
	links := readyLinks{called: make(chan production.LinkRequest, 8)}
	repo := &memoryRepo{playlist: "pool", pool: []content.AudioItem{length("a", 90), length("b", 90), length("c", 90)}}
	manager := New(repo, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: source, Links: links, Now: func() time.Time { return now }})
	defer manager.Close()
	sub, err := manager.Subscribe(context.Background(), "radio")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first := opened(t, encoder)
	first.write.Write([]byte("DEFAULT"))
	if string(receive(t, sub)) != "DEFAULT" || manager.State("radio").Programme.ShowID != "" {
		t.Fatal("did not start on default playlist")
	}
	source.enabled.Store(true)
	first.write.Close()
	second := opened(t, encoder)
	musicStatus(t, manager, sub, second, "Ready")
	if manager.State("radio").Programme.ShowID != "pop" {
		t.Fatal("new show not adopted at boundary")
	}
	second.write.Close()
	third := opened(t, encoder)
	if string(receive(t, sub)) != "HANDOVER" {
		t.Fatal("new show's prepared speech did not air")
	}
	if _, err := third.write.Write([]byte("SHOW")); err != nil {
		t.Fatal(err)
	}
	if string(receive(t, sub)) != "SHOW" {
		t.Fatal("show music did not follow speech")
	}
}
