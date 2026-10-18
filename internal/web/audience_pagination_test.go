package web

import (
	"context"
	"encoding/json"
	"html"
	"net/url"
	"strings"
	"testing"
	"time"

	"stationos.local/stationos/internal/audience"
	"stationos.local/stationos/internal/station"
)

type pagedAudience struct {
	audience.Repository
	offset, limit int
	status        string
}

func (a *pagedAudience) List(_ context.Context, _ string, status string, offset, limit int, _ time.Time) ([]audience.Message, int, error) {
	a.offset, a.limit, a.status = offset, limit, status
	if offset >= 76 {
		return nil, 76, nil
	}
	return []audience.Message{{ID: "message", Name: "Listener", Text: "Hello", Status: audience.Pending}}, 76, nil
}
func (a *pagedAudience) Decide(context.Context, string, string, audience.Status, string, time.Time) error {
	return nil
}

func TestAudienceSharedPaginationPreservesContext(t *testing.T) {
	_, stations, sources, catalogue := setupWeb(t, "")
	if err := stations.Save(context.Background(), station.Station{ID: "s", Name: "Radio", StreamSlug: "radio", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	repo := &pagedAudience{}
	h, err := New(Dependencies{Stations: stations, Sources: sources, Catalogue: catalogue, Audience: repo})
	if err != nil {
		t.Fatal(err)
	}
	base := "/producer/stations/s/audience"
	headers := map[string]string{"HX-Request": "true", "HX-Target": "audience-queue-results"}
	response := request(h, "GET", base+"?filtered=1&status=pending&status=approved&page=2&per_page=25", nil, headers)
	body := html.UnescapeString(response.Body.String())
	if response.Code != 200 || repo.offset != 25 || repo.limit != 25 || repo.status != "pending,approved" {
		t.Fatal(response.Code, repo)
	}
	for _, want := range []string{`class="pagination-bar"`, "Showing 26–50 of 76", "Page 2 of 4", `hx-target="#audience-queue-results"`, `page=3&per_page=25&status=pending&status=approved`, `name="page" value="2"`, `form="audience-filters" name="per_page" value="25"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, "<html") || strings.Count(body, `class="pagination-bar"`) != 1 {
		t.Fatal("fragment must contain one pager and no shell")
	}
	// A smaller result set clamps the requested page and reloads its actual rows.
	response = request(h, "GET", base+"?page=99&per_page=25", nil, headers)
	if response.Code != 200 || repo.offset != 75 || !strings.Contains(response.Body.String(), "Showing 76–76 of 76") {
		t.Fatal(response.Code, repo.offset, response.Body.String())
	}
	// Existing external offset links remain usable with the original default size.
	response = request(h, "GET", base+"?offset=50", nil, headers)
	if response.Code != 200 || repo.offset != 50 || repo.limit != 50 {
		t.Fatal(response.Code, repo)
	}
	// Row review keeps the filtered page for enhanced and ordinary forms.
	token := csrf(t, h)
	form := url.Values{"csrf": {token}, "action": {"approve"}, "filtered": {"1"}, "status": {"pending", "approved"}, "page": {"2"}, "per_page": {"25"}}
	response = request(h, "POST", base+"/message", form, headers)
	var location struct {
		Path, Target string
		Push         bool
	}
	if err := json.Unmarshal([]byte(response.Header().Get("HX-Location")), &location); err != nil {
		t.Fatal(err, response.Code)
	}
	if location.Target != "#audience-queue-results" || location.Push || !strings.Contains(location.Path, "page=2&per_page=25&status=pending&status=approved") {
		t.Fatal(location)
	}
	for _, query := range []string{"?page=0", "?per_page=12", "?offset=-1"} {
		if got := request(h, "GET", base+query, nil, headers); got.Code != 400 {
			t.Fatal(query, got.Code)
		}
	}
}
