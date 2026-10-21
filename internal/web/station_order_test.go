package web

import (
	"reflect"
	"stationos.local/stationos/internal/broadcast"
	"stationos.local/stationos/internal/station"
	"testing"
)

func TestStationOrderPrioritisesStatusThenListeners(t *testing.T) {
	cards := []stationCard{}
	for _, v := range []struct {
		id, name, status string
		listeners        int
	}{
		{"dormant", "Dormant", "Dormant", 0},
		{"quiet", "Zulu", "Broadcasting", 1},
		{"beta", "Beta", "Broadcasting", 10},
		{"retry", "Retry", "Retrying", 40},
		{"alpha2", "alpha", "Broadcasting", 10},
		{"starting", "Starting", "Starting", 20},
		{"unavailable", "Unavailable", "Unavailable", 0},
		{"alpha1", "Alpha", "Broadcasting", 10},
		{"unknown", "Unknown", "", 100},
	} {
		cards = append(cards, stationCard{Station: station.Station{ID: v.id, Name: v.name}, Broadcast: broadcast.State{Status: v.status, Listeners: v.listeners}})
	}
	sortStationCards(cards)
	ids := func() []string {
		var out []string
		for _, card := range cards {
			out = append(out, card.Station.ID)
		}
		return out
	}
	want := []string{"alpha1", "alpha2", "beta", "quiet", "starting", "retry", "unavailable", "dormant", "unknown"}
	if !reflect.DeepEqual(ids(), want) {
		t.Fatal(ids(), want)
	}
	// An audience change must reorder the next event snapshot as well.
	cards[3].Broadcast.Listeners = 11
	sortStationCards(cards)
	if cards[0].Station.ID != "quiet" {
		t.Fatal("listener update did not reorder stations", ids())
	}
	cards[0].Broadcast.Status = "Dormant"
	cards[0].Broadcast.Listeners = 0
	sortStationCards(cards)
	if cards[0].Station.ID != "alpha1" {
		t.Fatal("status update did not reorder stations", ids())
	}
}
