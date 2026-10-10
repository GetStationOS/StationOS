package web

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestDeletionConfirmationAndStationScope(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	token := csrf(t, h)
	request(h, "POST", "/station", url.Values{"csrf": {token}, "name": {"Test station"}, "timezone": {"UTC"}}, nil)
	stations, _ := st.List(context.Background())
	stationPath := "/stations/" + stations[0].ID
	w := request(h, "POST", "/playlists", url.Values{"csrf": {token}, "name": {"Test playlist"}, "station": {stations[0].ID}}, nil)
	location, _ := url.Parse(w.Header().Get("Location"))
	path := location.Path
	for _, target := range []string{path, stationPath} {
		w = request(h, "GET", target+"/delete", nil, map[string]string{"HX-Request": "true", "HX-Target": "dialog-body"})
		if w.Code != 200 || !strings.Contains(w.Body.String(), `id="dialog-heading"`) || !strings.Contains(w.Body.String(), `data-close-dialog`) || strings.Contains(w.Body.String(), `<title>`) {
			t.Fatal("invalid confirmation fragment", w.Code, w.Body.String())
		}
		w = request(h, "GET", target+"/delete", nil, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "<!doctype") {
			t.Fatal("missing ordinary fallback")
		}
		w = request(h, "POST", target+"/delete", url.Values{"csrf": {token}}, nil)
		if w.Code != 400 {
			t.Fatal("unconfirmed delete", w.Code)
		}
		w = request(h, "POST", target+"/delete", url.Values{"confirm": {"yes"}}, nil)
		if w.Code != 403 {
			t.Fatal("delete bypasses csrf", w.Code)
		}
		if w = request(h, "GET", target, nil, nil); w.Code != 200 {
			t.Fatal("unconfirmed deletion changed data")
		}
	}
	w = request(h, "POST", path+"/delete", url.Values{"csrf": {token}, "confirm": {"yes"}}, map[string]string{"HX-Request": "true"})
	var dest struct{ Path string }
	json.Unmarshal([]byte(w.Header().Get("HX-Location")), &dest)
	if w.Code != 204 || dest.Path != "/playlists?station="+stations[0].ID+"&notice=playlist-deleted" {
		t.Fatal(w.Code, w.Header())
	}
	if w = request(h, "GET", path, nil, nil); w.Code != 404 {
		t.Fatal("playlist remained")
	}
	w = request(h, "POST", stationPath+"/delete", url.Values{"csrf": {token}, "confirm": {"yes"}}, nil)
	if w.Code != 303 || w.Header().Get("Location") != "/?notice=station-deleted" {
		t.Fatal(w.Code, w.Header())
	}
	if w = request(h, "GET", stationPath, nil, nil); w.Code != 404 {
		t.Fatal("station remained")
	}
}
