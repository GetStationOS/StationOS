package broadcast

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"stationos.local/stationos/internal/schedule"
)

type editedEndSchedule struct {
	end      atomic.Int64
	revision atomic.Int64
}

func (s *editedEndSchedule) Resolve(_ context.Context, id string, at time.Time) (schedule.Timeline, error) {
	end := int(s.end.Load())
	return schedule.Resolve([]schedule.Show{{ID: "80s", StationID: id, Name: "80s", Day: 0, Start: 360, End: end, PlaylistID: "80s-pool", Revision: int(s.revision.Load())}, {ID: "jazz", StationID: id, Name: "Jazz", Day: 0, Start: end, End: 1380, PlaylistID: "jazz-pool"}}, id, "fallback", "Default", at, time.UTC), nil
}
func TestEditedEndAppliesOnlyAfterCurrentTrack(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		oldEnd, newEnd, before, after int
		want                          string
	}{{"shortened into past", 1200, 600, 720, 721, "jazz-pool"}, {"extended beyond original end", 600, 720, 599, 601, "80s-pool"}} {
		t.Run(tc.name, func(t *testing.T) {
			midnight := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
			var minute atomic.Int64
			minute.Store(int64(tc.before))
			source := &editedEndSchedule{}
			source.end.Store(int64(tc.oldEnd))
			source.revision.Store(1)
			encoder := &fakeEncoder{calls: make(chan opening, 16)}
			manager := New(showPools{}, encoder, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Schedule: source, Now: func() time.Time { return midnight.Add(time.Duration(minute.Load()) * time.Minute) }})
			defer manager.Close()
			sub, err := manager.Subscribe(context.Background(), "radio")
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			first := opened(t, encoder)
			if first.item.ID != "80s-pool" {
				t.Fatal(first.item)
			}
			first.write.Write([]byte("FIRST"))
			if string(receive(t, sub)) != "FIRST" {
				t.Fatal("first music missing")
			}
			source.end.Store(int64(tc.newEnd))
			source.revision.Store(2)
			minute.Store(int64(tc.after))
			first.write.Write([]byte("FINISH"))
			if string(receive(t, sub)) != "FINISH" {
				t.Fatal("edit interrupted music")
			}
			if manager.State("radio").Programme.ShowID != "80s" {
				t.Fatal("handover occurred mid-track")
			}
			first.write.Close()
			next := opened(t, encoder)
			if next.item.ID != tc.want {
				t.Fatal("wrong post-edit programme", next.item)
			}
			next.write.Write([]byte("NEXT"))
			if string(receive(t, sub)) != "NEXT" {
				t.Fatal("next music missing")
			}
			if manager.State("radio").Listeners != 1 {
				t.Fatal("listener disconnected")
			}
		})
	}
}
