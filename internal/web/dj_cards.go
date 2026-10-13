package web

import (
	"context"
	"net/url"
	"sort"
	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/djs"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/station"
	"strings"
	"time"
)

type djAssignment struct{ Name, Station, URL string }
type djCard struct {
	Profile                                                                     djs.Profile
	Status, StatusClass, SetupNote, Summary, Voice, Next, NextShow, LiveStation string
	Assignments                                                                 []djAssignment
}
type djStation struct {
	Station station.Station
	Shows   []schedule.Show
	State   broadcast.State
}

func (s *Server) djCards(ctx context.Context, profiles []djs.Profile, now time.Time) ([]djCard, error) {
	stations, err := s.d.Stations.List(ctx)
	if err != nil {
		return nil, err
	}
	data := make([]djStation, 0, len(stations))
	for _, st := range stations {
		item := djStation{Station: st}
		if s.d.Schedule != nil {
			item.Shows, err = s.d.Schedule.List(ctx, st.ID)
			if err != nil {
				return nil, err
			}
		}
		if s.d.Broadcast != nil {
			item.State = s.d.Broadcast.State(st.ID)
		}
		data = append(data, item)
	}
	cards := make([]djCard, 0, len(profiles))
	for _, p := range profiles {
		cards = append(cards, presenterCard(p, data, now))
	}
	return cards, nil
}

// Cards project saved profile text and measured activity; readiness does not test credentials.
func presenterCard(p djs.Profile, stations []djStation, now time.Time) djCard {
	card := djCard{Profile: p, Status: "Ready", StatusClass: "ready", Summary: strings.Join(strings.Fields(p.Background), " "), Voice: p.Voice}
	summary := []rune(card.Summary)
	if len(summary) > 180 {
		card.Summary = string(summary[:180]) + "…"
	}
	for _, voice := range djs.Voices() {
		if voice.ID == p.Voice {
			card.Voice = voice.Name
			break
		}
	}
	if !p.HasKey {
		card.SetupNote = "Add an API key to enable this DJ."
	}
	if p.Model == "" || p.Voice == "" {
		card.SetupNote = "Complete the model and voice settings."
	}
	seen := map[string]bool{}
	var next time.Time
	for _, item := range stations {
		st := item.Station
		loc, err := time.LoadLocation(st.Timezone)
		if err != nil {
			loc = time.UTC
		}
		monday := schedule.Monday(now, loc)
		for _, show := range item.Shows {
			if show.DJID != p.ID {
				continue
			}
			key := st.ID + "/" + show.Name
			if !seen[key] {
				seen[key] = true
				card.Assignments = append(card.Assignments, djAssignment{show.Name, st.Name, "/producer/stations/" + url.PathEscape(st.ID) + "/schedule"})
			}
			// Resolve recurrences in local civil time rather than adding 168 hours across DST.
			for week := 0; week < 2; week++ {
				date := monday.AddDate(0, 0, show.Day+week*7)
				start := schedule.WallTime(date, show.Start, false)
				end := schedule.WallTime(date.AddDate(0, 0, (show.Start+show.Duration())/1440), show.End, true)
				if !end.After(start) || !start.After(now) {
					continue
				}
				if next.IsZero() || start.Before(next) {
					next = start
					card.Next = start.Format("Mon 2 Jan, 15:04") + "–" + end.Format("15:04 MST")
					if start.Day() != end.Day() {
						card.Next = start.Format("Mon 2 Jan, 15:04") + "–" + end.Format("Mon 2 Jan, 15:04 MST")
					}
					card.NextShow = show.Name + " · " + st.Name
				}
			}
		}
		if item.State.Status == "Broadcasting" && item.State.Current.ID != "" && item.State.Programme.DJ.ID == p.ID {
			if card.LiveStation != "" {
				card.LiveStation += " · "
			}
			card.LiveStation += st.Name
		}
	}
	sort.SliceStable(card.Assignments, func(i, j int) bool {
		a, b := card.Assignments[i], card.Assignments[j]
		if a.Station == b.Station {
			return a.Name < b.Name
		}
		return a.Station < b.Station
	})
	if len(card.Assignments) > 0 {
		card.Status = "Scheduled"
		card.StatusClass = "scheduled"
	}
	if card.SetupNote != "" {
		card.Status = "Draft"
		card.StatusClass = "draft"
	}
	if card.LiveStation != "" {
		card.Status = "On air"
		card.StatusClass = "live"
	}
	return card
}
