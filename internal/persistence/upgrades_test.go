package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"stationos.local/stationos/internal/mixer"
	"testing"

	"stationos.local/stationos/internal/catalogue"
	"stationos.local/stationos/internal/content"
)

func TestUpgradePreservesExistingStationsSourcesAndCatalogue(t *testing.T) {
	for _, version := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "legacy.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`PRAGMA foreign_keys=ON;CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			first, err := os.ReadFile("testdata/v1.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(string(first)); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(first)
			if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(1,?)`, hex.EncodeToString(hash[:])); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO stations VALUES('station','Keep station','Identity','UTC','en');INSERT INTO sources VALUES('source','station','Music','upnp','http://server/description.xml',7)`); err != nil {
				t.Fatal(err)
			}
			a := content.AudioItem{ID: content.ID("source", "one"), SourceID: "source", SourceItemID: "one", Title: "Keep track", Album: "Album", Genre: "Jazz", Kind: content.Track, Origin: content.Original}
			raw, _ := json.Marshal(a)
			if _, err = db.Exec(`INSERT INTO catalogue_items VALUES(?,?,?,?,?,?,?)`, a.ID, a.SourceID, a.SourceItemID, a.Title, "", a.Album, string(raw)); err != nil {
				t.Fatal(err)
			}
			if version >= 2 {
				second, err := os.ReadFile("testdata/v2.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(string(second)); err != nil {
					t.Fatal(err)
				}
				hash = sha256.Sum256(second)
				if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(2,?)`, hex.EncodeToString(hash[:])); err != nil {
					t.Fatal(err)
				}
			}

			if version >= 3 {
				third, err := os.ReadFile("testdata/v3.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(string(third)); err != nil {
					t.Fatal(err)
				}
				hash = sha256.Sum256(third)
				if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(3,?)`, hex.EncodeToString(hash[:])); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`INSERT INTO playlists VALUES('playlist','station','Pool','Keep description');INSERT INTO playlist_rules VALUES('rule','playlist','genre','Jazz','')`); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`INSERT INTO playlist_selections VALUES('playlist',?,1,?);INSERT INTO playlist_order VALUES('playlist',?,1)`, a.ID, string(raw), a.ID); err != nil {
					t.Fatal(err)
				}
			}
			if version >= 4 {
				fourth, err := os.ReadFile("testdata/v4.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(string(fourth)); err != nil {
					t.Fatal(err)
				}
				hash = sha256.Sum256(fourth)
				if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(4,?)`, hex.EncodeToString(hash[:])); err != nil {
					t.Fatal(err)
				}
			}
			if version >= 5 {
				fifth, err := os.ReadFile("testdata/v5.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(string(fifth)); err != nil {
					t.Fatal(err)
				}
				hash = sha256.Sum256(fifth)
				if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(5,?)`, hex.EncodeToString(hash[:])); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`UPDATE stations SET stream_slug='keep-station'; INSERT INTO station_broadcast VALUES('station','playlist')`); err != nil {
					t.Fatal(err)
				}
			}
			if version >= 6 {
				sixth, err := os.ReadFile("testdata/v6.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(string(sixth)); err != nil {
					t.Fatal(err)
				}
				hash = sha256.Sum256(sixth)
				if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(6,?)`, hex.EncodeToString(hash[:])); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`INSERT INTO djs VALUES('dj','Preserved DJ','gpt-6.1-sol','Keep prompt','Keep vibe',X'1234')`); err != nil {
					t.Fatal(err)
				}
			}
			if version >= 7 {
				frozen, err := os.ReadFile("testdata/v7.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(string(frozen)); err != nil {
					t.Fatal(err)
				}
				hash = sha256.Sum256(frozen)
				if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(7,?)`, hex.EncodeToString(hash[:])); err != nil {
					t.Fatal(err)
				}
			}
			for n := 8; n <= version; n++ {
				frozen, err := os.ReadFile(fmt.Sprintf("testdata/v%d.sql", n))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(string(frozen)); err != nil {
					t.Fatal(err)
				}
				hash = sha256.Sum256(frozen)
				if _, err = db.Exec(`INSERT INTO schema_migrations VALUES(?,?)`, n, hex.EncodeToString(hash[:])); err != nil {
					t.Fatal(err)
				}
			}
			if version >= 11 {
				if _, err = db.Exec(`UPDATE djs SET conversation_speed=1.35 WHERE id='dj'`); err != nil {
					t.Fatal(err)
				}
			}
			if version >= 12 {
				if _, err = db.Exec(`UPDATE djs SET background='Keep background',portrait_url='/static/djs/alex-bright.jpg' WHERE id='dj'`); err != nil {
					t.Fatal(err)
				}
			}
			if version >= 17 {
				if _, err = db.Exec(`INSERT INTO station_shows(id,station_id,name,weekday,start_minute,end_minute,dj_id,playlist_id,accept_requests,accept_shoutouts) VALUES('legacy-show','station','Preserved show',0,540,600,'dj','playlist',1,1)`); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			db, err = Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if version >= 17 {
				show, err := (Schedule{DB: db}).Get(ctx, "station", "legacy-show")
				if err != nil || show.Description != "" || !show.AcceptRequests || !show.AcceptShoutouts || show.Name != "Preserved show" {
					t.Fatal("show description migration changed existing policy", show, err)
				}
			}
			levels, err := (Mixer{DB: db}).Get(ctx, "station")
			if err != nil || levels != mixer.Default() {
				t.Fatal("upgrade changed station levels", levels, err)
			}
			station, err := (Stations{DB: db}).Get(ctx, "station")
			if err != nil || station.Description != "Identity" || station.StreamSlug != "keep-station" || station.RepeatMinutes != 180 || station.Publication() != "unlisted" {
				t.Fatal(station, err)
			}
			source, err := (Sources{DB: db}).Get(ctx, "source")
			if err != nil || source.Revision != 7 {
				t.Fatal(source, err)
			}
			result, err := (Catalogue{DB: db}).Search(ctx, catalogue.Query{Limit: 25, Genre: "Jazz"})
			if err != nil || result.Total != 1 || result.Items[0].ID != a.ID {
				t.Fatal(result, err)
			}

			var legacyOrderTables int
			if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='playlist_order'`).Scan(&legacyOrderTables); err != nil || legacyOrderTables != 0 {
				t.Fatal("legacy ordering remains", legacyOrderTables, err)
			}
			if version >= 12 {
				var background, portrait string
				if err = db.QueryRow(`SELECT background,portrait_url FROM djs WHERE id='dj'`).Scan(&background, &portrait); err != nil || background != "Keep background" || portrait != "/static/djs/alex-bright.jpg" {
					t.Fatal("upgrade changed DJ profile", err)
				}
			}

			if version >= 3 {
				pool, err := (Playlists{DB: db}).Get(ctx, "playlist")
				if err != nil || pool.Description != "Keep description" {
					t.Fatal(pool, err)
				}
				rules, err := (Playlists{DB: db}).Rules(ctx, "playlist")
				if err != nil || len(rules) != 1 {
					t.Fatal(rules, err)
				}
				resolved, err := (Playlists{DB: db}).Resolve(ctx, "playlist", 0, 25)
				if err != nil || resolved.Total != 1 || resolved.Entries[0].Item.ID != a.ID {
					t.Fatal(resolved, err)
				}
				var selections int
				if err = db.QueryRow(`SELECT count(*) FROM playlist_selections WHERE playlist_id='playlist' AND included=1`).Scan(&selections); err != nil || selections != 1 {
					t.Fatal("selection lost", selections, err)
				}
			}
			var djCount int
			expectedDJs := 3
			if version >= 6 {
				expectedDJs = 4
			}
			if err = db.QueryRow(`SELECT count(*) FROM djs`).Scan(&djCount); err != nil || djCount != expectedDJs {
				t.Fatal("DJ schema not upgraded", err)
			}
			if version >= 6 {
				var name, voice, prompt, vibe string
				var speed float64
				var encrypted []byte
				expectedSpeed := 1.0
				if version >= 11 {
					expectedSpeed = 1.35
				}
				if err = db.QueryRow(`SELECT name,voice,prompt,vibe,conversation_speed,api_key_cipher FROM djs WHERE id='dj'`).Scan(&name, &voice, &prompt, &vibe, &speed, &encrypted); err != nil || name != "Preserved DJ" || voice != "cedar" || speed != expectedSpeed || prompt != "Keep prompt" || vibe != "Keep vibe" || hex.EncodeToString(encrypted) != "1234" {
					t.Fatal("DJ voice upgrade lost metadata or encrypted credential", err)
				}
			}
			if version >= 5 {
				var assignment string
				if err = db.QueryRow(`SELECT playlist_id FROM station_broadcast WHERE station_id='station'`).Scan(&assignment); err != nil || assignment != "playlist" {
					t.Fatal("broadcast assignment lost", err)
				}
			}
			if err = Integrity(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err = migrate(ctx, db); err != nil {
				t.Fatal("idempotent migration", err)
			}
		})
	}
}
