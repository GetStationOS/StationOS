package upnp

import (
	"stationos.local/stationos/internal/content"
	"strings"
)

type didlCredit struct {
	Name string `xml:",chardata"`
	Role string `xml:"role,attr"`
}

func creditNames(values []string) string {
	names := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			names = append(names, v)
			seen[v] = true
		}
	}
	return strings.Join(names, "; ")
}

func (v didlEntry) applyCredits(a *content.AudioItem) {
	var performers, albumArtists, conductors []string
	composers := append([]string(nil), v.Composers...)
	creators := append([]string(nil), v.Creators...)
	for _, c := range v.Artists {
		switch strings.ToLower(strings.TrimSpace(c.Role)) {
		case "composer":
			composers = append(composers, c.Name)
		case "conductor":
			conductors = append(conductors, c.Name)
		case "albumartist", "album artist":
			albumArtists = append(albumArtists, c.Name)
		case "", "artist", "performer", "soloist", "orchestra", "ensemble", "band", "vocalist":
			performers = append(performers, c.Name)
		}
	}
	for _, c := range v.Authors {
		switch strings.ToLower(strings.TrimSpace(c.Role)) {
		case "composer":
			composers = append(composers, c.Name)
		case "albumartist", "album artist":
			albumArtists = append(albumArtists, c.Name)
		case "conductor":
			conductors = append(conductors, c.Name)
		default:
			creators = append(creators, c.Name)
		}
	}
	a.Artist = creditNames(performers)
	a.Composer = creditNames(composers)
	a.AlbumArtist = creditNames(albumArtists)
	a.Conductor = creditNames(conductors)
	a.Creator = creditNames(creators)
	// Album-level credits are kept separate from track performers. A dc:creator
	// is ambiguous, so retain it for the DJ without relabelling it as an artist.
}
