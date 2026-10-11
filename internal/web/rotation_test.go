package web

import (
	"context"
	"net/url"
	"stationos.local/stationos/internal/station"
	"strings"
	"testing"
)

func TestStationRepeatSettings(t *testing.T) {
	h, repo, _, _ := setupWeb(t, "")
	token := csrf(t, h)
	newPage := request(h, "GET", "/stations/new", nil, nil)
	if !strings.Contains(newPage.Body.String(), `name="repeat_minutes" value="180"`) {
		t.Fatal("default missing")
	}
	st, err := (station.Service{Repo: repo}).Save(context.Background(), station.Station{Name: "Radio", Timezone: "UTC", RepeatMinutes: 120})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"csrf": {token}, "id": {st.ID}, "name": {"Radio"}, "timezone": {"UTC"}, "repeat_minutes": {"240"}}
	saved := request(h, "POST", "/station", form, map[string]string{"HX-Request": "true"})
	if saved.Code != 204 || saved.Header().Get("HX-Location") == "" {
		t.Fatal(saved.Code, saved.Body.String())
	}
	st, err = repo.Get(context.Background(), st.ID)
	if err != nil || st.RepeatMinutes != 240 {
		t.Fatal(st, err)
	}
	settings := request(h, "GET", "/stations/"+st.ID+"/settings", nil, nil)
	if !strings.Contains(settings.Body.String(), `name="repeat_minutes" value="240"`) {
		t.Fatal("saved interval missing")
	}
	form.Set("repeat_minutes", "360")
	ordinary := request(h, "POST", "/station", form, nil)
	if ordinary.Code != 303 {
		t.Fatal("ordinary save failed", ordinary.Code)
	}
	st, _ = repo.Get(context.Background(), st.ID)
	if st.RepeatMinutes != 360 {
		t.Fatal("ordinary save lost interval", st)
	}
	for _, invalid := range []string{"0", "-1", "10081", "bad", ""} {
		form.Set("repeat_minutes", invalid)
		response := request(h, "POST", "/station", form, nil)
		if response.Code != 422 || !strings.Contains(response.Body.String(), "between 1 and 10080 minutes") {
			t.Fatal(invalid, response.Code, response.Body.String())
		}
		st, _ = repo.Get(context.Background(), st.ID)
		if st.RepeatMinutes != 360 {
			t.Fatal("invalid write changed station")
		}
	}
}
