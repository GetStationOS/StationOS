package upnp

import (
	"encoding/xml"
	"fmt"
	"stationos.local/stationos/internal/content"
	"testing"
)

func TestDIDLCreditsPreservePerformerAndComposerRoles(t *testing.T) {
	cases := []struct{ name, xml, artist, composer, albumArtist, conductor, creator string }{
		{"composer last", `<artist>Maria João Pires</artist><artist role="Composer">Frédéric Chopin</artist>`, "Maria João Pires", "Frédéric Chopin", "", "", ""},
		{"composer first", `<artist role="Composer">Frédéric Chopin</artist><artist role="Performer">Maria João Pires</artist>`, "Maria João Pires", "Frédéric Chopin", "", "", ""},
		{"multiple credits", `<artist role="Composer">Ludwig van Beethoven</artist><artist role="Orchestra">Berlin Philharmonic</artist><artist role="Soloist">Anne-Sophie Mutter</artist><artist role="Soloist">Anne-Sophie Mutter</artist><artist role="Conductor">Herbert von Karajan</artist><artist role="AlbumArtist">Classical Collection</artist>`, "Berlin Philharmonic; Anne-Sophie Mutter", "Ludwig van Beethoven", "Classical Collection", "Herbert von Karajan", ""},
		{"author and explicit composer", `<artist>Performer</artist><author role="Composer">Writer</author><composer>Writer</composer><composer>Co-writer</composer>`, "Performer", "Writer; Co-writer", "", "", ""},
		{"composer only", `<artist role="Composer">Writer</artist><creator>Writer</creator>`, "", "Writer", "", "", "Writer"},
		{"ambiguous creator", `<creator>Unknown role</creator>`, "", "", "", "", "Unknown role"},
		{"pop compilation", `<artist>Fiction Factory</artist><artist role="AlbumArtist">Various Artists</artist>`, "Fiction Factory", "", "Various Artists", "", ""},
		{"pop recording and songwriters", `<artist>Recording Artist</artist><artist role="AlbumArtist">Various Artists</artist><artist role="Composer">First Songwriter</artist><artist role="Composer">Second Songwriter</artist>`, "Recording Artist", "First Songwriter; Second Songwriter", "Various Artists", "", ""},
		{"ordinary pop artists", `<artist>First Artist</artist><artist>Second Artist</artist>`, "First Artist; Second Artist", "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := fmt.Sprintf(`<item id="song"><title>Song</title><class>object.item.audioItem.musicTrack</class>%s</item>`, c.xml)
			var v didlEntry
			if err := xml.Unmarshal([]byte(raw), &v); err != nil {
				t.Fatal(err)
			}
			a := content.AudioItem{}
			v.applyCredits(&a)
			if a.Artist != c.artist || a.Composer != c.composer || a.AlbumArtist != c.albumArtist || a.Conductor != c.conductor || a.Creator != c.creator {
				t.Fatal("credits mixed or lost", a)
			}
		})
	}
}
