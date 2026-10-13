package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/playlists"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
)

func TestAudienceLifecycleIsolationLimitsAndScheduleRevision(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := Stations{db}
	shows := Schedule{db}
	repo := Audience{db}
	if err = st.Save(ctx, station.Station{ID: "s", Name: "Radio", Timezone: "UTC", Visibility: station.Listed}); err != nil {
		t.Fatal(err)
	}
	if err = (Playlists{db}).Save(ctx, playlists.Playlist{ID: "p", StationID: "s", Name: "Music"}); err != nil {
		t.Fatal(err)
	}
	show := schedule.Show{ID: "show", StationID: "s", Name: "Breakfast", DJID: "default-pop", PlaylistID: "p", Day: 0, Start: 540, End: 600, AcceptRequests: true, AcceptShoutouts: true}
	if err = shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	show, err = shows.Get(ctx, "s", "show")
	if err != nil || !show.AcceptRequests || !show.AcceptShoutouts {
		t.Fatal(show, err)
	}
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	occ := schedule.Resolve([]schedule.Show{show}, "s", "p", "Music", now, time.UTC).Current
	m := audience.Message{ID: "a", StationID: "s", ShowID: "show", ShowName: show.Name, OccurrenceKey: occ.Key, SenderKey: strings.Repeat("a", 64), Kind: audience.Shoutout, Status: audience.Pending, Name: "Sam", Text: "Hello family", Created: now, ShowStart: now, Expires: occ.End}
	if err = repo.Submit(ctx, m); err != nil {
		t.Fatal(err)
	}
	if ok, _ := repo.Usable(ctx, "s", occ.Key, []string{"a"}, now); ok {
		t.Fatal("pending usable")
	}
	dupe := m
	dupe.ID = "duplicate"
	if err = repo.Submit(ctx, dupe); !errors.Is(err, audience.ErrDuplicate) {
		t.Fatal("duplicate", err)
	}
	if err = repo.Decide(ctx, "other", "a", audience.Approved, "", now); !errors.Is(err, audience.ErrConflict) {
		t.Fatal("cross station decision", err)
	}
	if err = repo.Decide(ctx, "s", "a", audience.Approved, "AI review", now); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.Usable(ctx, "s", occ.Key, []string{"a"}, now); !ok || err != nil {
		t.Fatal(ok, err)
	}
	for _, id := range []string{"b", "c"} {
		n := m
		n.ID = id
		n.Text = id
		if err = repo.Submit(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	n := m
	n.ID = "d"
	n.Text = "fourth"
	if err = repo.Submit(ctx, n); !errors.Is(err, audience.ErrLimited) {
		t.Fatal("rate limit", err)
	}
	if err = repo.MarkUsed(ctx, "other", []string{"a"}, now); err != nil {
		t.Fatal(err)
	}
	if ok, _ := repo.Usable(ctx, "s", occ.Key, []string{"a"}, now); !ok {
		t.Fatal("foreign receipt consumed")
	}
	if err = repo.MarkUsed(ctx, "s", []string{"a"}, now); err != nil {
		t.Fatal(err)
	}
	used, total, err := repo.List(ctx, "s", "used", 0, 50, now)
	if err != nil || total != 1 || used[0].UsedAt.IsZero() {
		t.Fatal(used, total, err)
	}
	if err = shows.Save(ctx, show); err != nil {
		t.Fatal(err)
	}
	expired, total, err := repo.List(ctx, "s", "expired", 0, 50, now)
	if err != nil || total != 2 {
		t.Fatal(expired, total, err)
	}
	combined, total, err := repo.List(ctx, "s", "used,expired", 1, 1, now)
	if err != nil || total != 3 || len(combined) != 1 {
		t.Fatal("multi-status filtering or pagination failed", combined, total, err)
	}
	if items, total, err := repo.List(ctx, "s", "none", 0, 50, now); err != nil || total != 0 || len(items) != 0 {
		t.Fatal("empty selection returned contributions", items, total, err)
	}
	if err = repo.Decide(ctx, "s", "b", audience.Approved, "", now); !errors.Is(err, audience.ErrConflict) {
		t.Fatal("revived expired", err)
	}
}

func TestAudienceReviewDoesNotOverrideProducerDecision(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := Stations{db}
	shows := Schedule{db}
	repo := Audience{db}
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	if err = st.Save(ctx, station.Station{ID: "s", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if err = (Playlists{db}).Save(ctx, playlists.Playlist{ID: "p", StationID: "s", Name: "Music"}); err != nil {
		t.Fatal(err)
	}
	if err = shows.Save(ctx, schedule.Show{ID: "show", StationID: "s", Name: "Show", DJID: "default-pop", PlaylistID: "p", Day: 0, Start: 540, End: 600, AcceptShoutouts: true}); err != nil {
		t.Fatal(err)
	}
	show, _ := shows.Get(ctx, "s", "show")
	occ := schedule.Resolve([]schedule.Show{show}, "s", "p", "Music", now, time.UTC).Current
	for i, id := range []string{"approved", "dismissed", "pending"} {
		m := audience.Message{ID: id, StationID: "s", ShowID: "show", ShowName: "Show", OccurrenceKey: occ.Key, SenderKey: strings.Repeat(string(rune('a'+i)), 64), Kind: audience.Shoutout, Status: audience.Pending, Text: id, Created: now, ShowStart: now, Expires: occ.End}
		if err = repo.Submit(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.Decide(ctx, "s", "approved", audience.Approved, "Producer", now); err != nil {
		t.Fatal(err)
	}
	if err = repo.Decide(ctx, "s", "dismissed", audience.Rejected, "Producer", now); err != nil {
		t.Fatal(err)
	}
	if err = repo.Review(ctx, "s", []audience.Decision{{ID: "approved", Approve: false}, {ID: "dismissed", Approve: true}, {ID: "pending", Approve: true}}, now); err != nil {
		t.Fatal(err)
	}
	messages, _, err := repo.List(ctx, "s", "", 0, 50, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range messages {
		if m.ID == "dismissed" && m.Status != audience.Rejected || m.ID != "dismissed" && m.Status != audience.Approved {
			t.Fatal(m)
		}
	}
	if ok, _ := repo.Usable(ctx, "s", occ.Key, []string{"approved"}, occ.End); ok {
		t.Fatal("expired speech usable")
	}
}

func TestAudienceUncertainReviewLeavesAutomaticBatch(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := Stations{db}
	shows := Schedule{db}
	repo := Audience{db}
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	if err = st.Save(ctx, station.Station{ID: "s", Name: "Radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if err = (Playlists{db}).Save(ctx, playlists.Playlist{ID: "p", StationID: "s", Name: "Music"}); err != nil {
		t.Fatal(err)
	}
	if err = shows.Save(ctx, schedule.Show{ID: "show", StationID: "s", Name: "Show", DJID: "default-pop", PlaylistID: "p", Day: 0, Start: 540, End: 600, AcceptShoutouts: true}); err != nil {
		t.Fatal(err)
	}
	show, _ := shows.Get(ctx, "s", "show")
	occ := schedule.Resolve([]schedule.Show{show}, "s", "p", "Music", now, time.UTC).Current
	m := audience.Message{ID: "uncertain", StationID: "s", ShowID: "show", ShowName: "Show", OccurrenceKey: occ.Key, SenderKey: strings.Repeat("a", 64), Kind: audience.Shoutout, Status: audience.Pending, Text: "Message", Created: now, ShowStart: now, Expires: occ.End}
	if err = repo.Submit(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err = repo.Review(ctx, "s", []audience.Decision{{ID: m.ID, Uncertain: true}}, now); err != nil {
		t.Fatal(err)
	}
	pending, err := repo.ForShow(ctx, "s", occ.Key, audience.Pending, now, 4)
	if err != nil || len(pending) != 0 {
		t.Fatal("uncertain repeated", pending, err)
	}
	active, total, err := repo.List(ctx, "s", "active", 0, 50, now)
	if err != nil || total != 1 || active[0].Status != audience.ReviewNeeded {
		t.Fatal(active, total, err)
	}
	if ok, _ := repo.Usable(ctx, "s", occ.Key, []string{m.ID}, now); ok {
		t.Fatal("uncertain usable")
	}
	if err = repo.Decide(ctx, "s", m.ID, audience.Approved, "Producer", now); err != nil {
		t.Fatal(err)
	}
}
