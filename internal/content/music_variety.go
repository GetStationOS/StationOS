package content

import "strings"

func musicIdentity(v string) string { return strings.ToLower(strings.Join(strings.Fields(v), " ")) }

// MusicConflict compares actual performing credits, not composers or generic
// creators. Semicolons separate names; commas remain part of an artist's name.
func MusicConflict(a, b AudioItem, format string) bool {
	if format == "album" {
		return false
	}
	if format != "artist" {
		for _, x := range strings.Split(a.Artist, ";") {
			x = musicIdentity(x)
			if x == "" {
				continue
			}
			for _, y := range strings.Split(b.Artist, ";") {
				if x == musicIdentity(y) {
					return true
				}
			}
		}
	}
	album := musicIdentity(a.Album)
	return album != "" && album == musicIdentity(b.Album)
}
