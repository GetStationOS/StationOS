package producer

import "testing"

func TestShowWindow(t *testing.T) {
	for _, tc := range []struct {
		start                      string
		duration, startMinute, end int
	}{{"20:00", 240, 1200, 1440}, {"22:00", 240, 1320, 120}, {"00:00", 1440, 0, 1440}, {"08:00", 120, 480, 600}} {
		start, end, err := showWindow(tc.start, tc.duration)
		if err != nil || start != tc.startMinute || end != tc.end {
			t.Fatal(tc, start, end, err)
		}
	}
	for _, tc := range []struct {
		start    string
		duration int
	}{{"24:00", 60}, {"8pm", 240}, {"+1:00", 60}, {"20:01", 60}, {"20:00", 0}, {"20:00", 1441}, {"20:00", 61}} {
		if _, _, err := showWindow(tc.start, tc.duration); err == nil {
			t.Fatal("accepted", tc)
		}
	}
}
