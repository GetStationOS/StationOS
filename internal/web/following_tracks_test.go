package web

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"reflect"
	"strings"
	"testing"

	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/episodes"
	"stationos.local/stationos/internal/schedule"
)

type followingTrackStore struct {
	episode episodes.Episode
	err     error
	key     string
	writes  int
}

func (s *followingTrackStore) Get(_ context.Context, key string) (episodes.Episode, error) {
	s.key = key
	return s.episode, s.err
}
func (s *followingTrackStore) Update(context.Context, schedule.Occurrence, func(*episodes.Episode) error) error {
	s.writes++
	return errors.New("dashboard must not write")
}

func TestFollowingTracksReadsOnlyTwoAcceptedUnairedChoices(t *testing.T) {
	next := schedule.Occurrence{ShowID: "next-show", Key: "next-show:dated:revision"}
	plan := []episodes.Track{{ID: "old", Approved: false}, {ID: "aired", Approved: true}, {ID: "excluded", Approved: true}, {ID: "one", Title: "First <track>", Artist: "First artist", Approved: true}, {ID: "one", Approved: true}, {ID: "two", Title: "Second track", Artist: "Second artist", Approved: true}, {ID: "three", Approved: true}}
	for _, test := range []struct {
		name    string
		episode episodes.Episode
		err     error
		want    []string
		wantErr bool
	}{
		{name: "accepted order", episode: episodes.Episode{Plan: plan, Aired: []episodes.Track{{ID: "aired"}}, Excluded: []string{"excluded"}}, want: []string{"one", "two"}},
		{name: "partial", episode: episodes.Episode{Plan: plan[3:4]}, want: []string{"one"}},
		{name: "unaccepted", episode: episodes.Episode{Plan: plan[:1]}},
		{name: "cancelled", episode: episodes.Episode{Plan: plan, Cancelled: true}},
		{name: "not prepared", err: episodes.ErrNotFound},
		{name: "read failure", err: errors.New("database unavailable"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &followingTrackStore{episode: test.episode, err: test.err}
			s := &Server{d: Dependencies{Episodes: &episodes.Service{Store: store}}}
			tracks, err := s.followingTracks(context.Background(), next)
			if (err != nil) != test.wantErr {
				t.Fatal("unexpected error", err)
			}
			var ids []string
			for _, track := range tracks {
				ids = append(ids, track.ID)
			}
			if !reflect.DeepEqual(ids, test.want) {
				t.Fatalf("got %v, want %v", ids, test.want)
			}
			if store.key != next.Key || store.writes != 0 {
				t.Fatal("preview did not make a read-only dated episode lookup", store)
			}
		})
	}
	if _, err := (&Server{}).followingTracks(context.Background(), next); err == nil {
		t.Fatal("missing service must be unavailable")
	}
}

func TestDashboardOpeningTracksAndHonestEmptyStates(t *testing.T) {
	tm, err := template.New("test").Funcs(templateFunctions()).ParseFS(assets, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		tracks      []episodes.Track
		unavailable bool
		want        string
	}{
		{name: "ready", tracks: []episodes.Track{{Title: "First <track>", Artist: "Artist & One"}, {Title: "Second track", Artist: "Artist Two"}}, want: "Artist Two"},
		{name: "partial", tracks: []episodes.Track{{Title: "First <track>"}}, want: "Second track not chosen yet"},
		{name: "not prepared", want: "Tracks not chosen yet"},
		{name: "unavailable", unavailable: true, want: "Track choices unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var html bytes.Buffer
			card := stationCard{Broadcast: broadcast.State{Following: schedule.Occurrence{ShowID: "next", Title: "Next show"}}, FollowingTracks: test.tracks, FollowingTracksUnavailable: test.unavailable}
			if err := tm.ExecuteTemplate(&html, "station-cards", stationDirectoryView{StationCards: []stationCard{card}}); err != nil {
				t.Fatal(err)
			}
			result := html.String()
			if !strings.Contains(result, "Opening tracks") || !strings.Contains(result, test.want) {
				t.Fatal("missing opening tracks state", result)
			}
			if strings.Contains(result, "First <track>") {
				t.Fatal("unescaped title")
			}
			if test.name == "ready" && (strings.Index(result, "First &lt;track&gt;") > strings.Index(result, "Second track") || !strings.Contains(result, "Artist &amp; One")) {
				t.Fatal("lost ordered metadata", result)
			}
		})
	}
}
