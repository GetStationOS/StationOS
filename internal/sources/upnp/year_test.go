package upnp

import "testing"

func TestSourceDateYear(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want int
	}{{"", 0}, {"1984", 1984}, {"1984-06", 1984}, {"1984-06-01", 1984}, {" 1984-06-01T12:00:00Z ", 1984}, {"1984-02-30", 0}, {"unknown", 0}, {"0000", 0}, {"1984junk", 0}, {"-1984", 0}, {"1984-13-01", 0}} {
		if got := sourceYear(c.raw); got != c.want {
			t.Errorf("%q: %d, want %d", c.raw, got, c.want)
		}
	}
}
