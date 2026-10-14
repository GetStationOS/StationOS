package producer_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/inference"
	"stationos.local/stationos/internal/producer"
)

type interruptedProvider struct{ n int }

func (m *interruptedProvider) Respond(context.Context, string, string, string, []producer.Item, []producer.Tool) (producer.Response, error) {
	m.n++
	if m.n == 1 {
		v := responseCall(1, "save_station", map[string]string{"name": "Portable Radio"})
		v.Items = append(v.Items, producer.Item(`{"type":"reasoning","private":"provider-private-thought"}`))
		return v, nil
	}
	return producer.Response{}, errors.New("provider became unavailable")
}
func TestProviderChangeResumesCheckpointWithoutCloudKeyOrMutationReplay(t *testing.T) {
	store, registry, stations, _, _, _ := fixture(t)
	ctx := context.Background()
	if e := store.SaveSettings(ctx, "cloud-model", producer.KeyChange{Value: "cloud-secret"}); e != nil {
		t.Fatal(e)
	}
	providers := store.(ai.Store)
	r := inference.New(providers, &interruptedProvider{})
	svc, e := producer.New(ctx, store, registry, r)
	if e != nil {
		t.Fatal(e)
	}
	defer svc.Close()
	id, _ := svc.Create(ctx, "")
	original := "Create one station called Portable Radio. Keep its ID when continuing."
	if e = svc.Start(ctx, id, producer.ID(), original); e != nil {
		t.Fatal(e)
	}
	c := waitDone(t, store, id)
	if c.Status != "failed" {
		t.Fatal(c.Status)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		if !strings.Contains(body, original) || !strings.Contains(body, "Portable Radio") || strings.Contains(body, "provider-private-thought") || strings.Contains(body, "cloud-secret") {
			t.Error("provider recovery lost intent or leaked private format")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("cloud credential leaked")
		}
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"The saved station exists. Finished."}}]}`)
	}))
	defer server.Close()
	conn := ai.Connection{ID: "local", Name: "Private agent", Type: "compatible", Address: server.URL, ContextTokens: 32768, OutputTokens: 2048}
	if e = providers.SaveConnection(ctx, conn, ai.KeyChange{}); e != nil {
		t.Fatal(e)
	}
	settings, _ := store.Settings(ctx)
	settings.Routes.Assistant.Connection = "local"
	settings.Model = "private-model"
	if e = store.Configure(ctx, settings, producer.KeyChange{Clear: true}); e != nil {
		t.Fatal(e)
	}
	if e = svc.Start(ctx, id, producer.ID(), "Continue the saved request."); e != nil {
		t.Fatal(e)
	}
	c = waitDone(t, store, id)
	saved, _ := stations.List(ctx)
	if c.Status != "idle" || len(saved) != 1 || len(c.Requests) != 2 || c.Requests[0] != original {
		t.Fatal("recovery replayed or lost original", c.Status, c.Requests, saved)
	}
}
