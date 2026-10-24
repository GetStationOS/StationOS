package producer

import "testing"

func TestRecurringScheduleReadbackRetainsEveryEditionAndDistinctAssignment(t *testing.T) {
	shows := []namedShow{
		{ID: "monday", Name: "Morning", DayName: "Monday", DJID: "one", PlaylistID: "music", Start: 480, End: 660, StartTime: "08:00", EndTime: "11:00"},
		{ID: "tuesday", Name: "Morning", DayName: "Tuesday", DJID: "one", PlaylistID: "music", Start: 480, End: 660, StartTime: "08:00", EndTime: "11:00"},
		{ID: "saturday", Name: "Morning", DayName: "Saturday", DJID: "one", PlaylistID: "music", Start: 480, End: 600, StartTime: "08:00", EndTime: "10:00"},
		{ID: "sunday", Name: "Morning", DayName: "Sunday", DJID: "two", PlaylistID: "music", Start: 480, End: 660, StartTime: "08:00", EndTime: "11:00"},
		{ID: "friday", Name: "Morning", DayName: "Friday", DJID: "one", PlaylistID: "special", Start: 480, End: 660, StartTime: "08:00", EndTime: "11:00"},
	}
	groups := groupScheduleReview(shows)
	if len(groups) != 4 {
		t.Fatal("different times or assignments merged", groups)
	}
	expanded := map[string]namedShow{}
	for _, group := range groups {
		for _, edition := range group.Editions {
			if _, exists := expanded[edition.ID]; exists {
				t.Fatal("duplicate edition", edition.ID)
			}
			expanded[edition.ID] = namedShow{Name: group.Name, DayName: edition.DayName, DJID: group.DJID, PlaylistID: group.PlaylistID, StartTime: group.StartTime, EndTime: group.EndTime}
		}
	}
	if len(expanded) != len(shows) {
		t.Fatal("show lost from readback")
	}
	for _, show := range shows {
		got := expanded[show.ID]
		if got.Name != show.Name || got.DayName != show.DayName || got.DJID != show.DJID || got.PlaylistID != show.PlaylistID || got.StartTime != show.StartTime || got.EndTime != show.EndTime {
			t.Fatal("edition configuration changed", show, got)
		}
	}
}

func TestScheduleReviewRetainsDistinctPublicDescriptions(t *testing.T) {
	groups := groupScheduleReview([]namedShow{
		{ID: "one", Name: "Weekend", DJID: "presenter", PlaylistID: "music", Start: 360, End: 1200, Description: "Saturday's public introduction."},
		{ID: "two", Name: "Weekend", DJID: "presenter", PlaylistID: "music", Start: 360, End: 1200, Description: "Sunday's different introduction."},
	})
	if len(groups) != 2 || groups[0].Description == groups[1].Description {
		t.Fatal("readback merged distinct public copy", groups)
	}
}
