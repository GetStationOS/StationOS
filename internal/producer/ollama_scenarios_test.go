package producer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/content"
	"stationos.local/stationos/internal/inference"
	"stationos.local/stationos/internal/persistence"
	"stationos.local/stationos/internal/producer"
	"stationos.local/stationos/internal/schedule"
	"stationos.local/stationos/internal/sources"
)

// This opt-in suite uses the real runner, native provider adapter and application
// services, but a fresh in-memory database. It never opens the operator database.
func TestOllamaStationScenarios(t *testing.T) {
	address := os.Getenv("STATIONOS_TEST_OLLAMA_URL")
	if address == "" {
		t.Skip("set STATIONOS_TEST_OLLAMA_URL to run live model scenarios")
	}
	model := os.Getenv("STATIONOS_TEST_OLLAMA_MODEL")
	if model == "" {
		model = "gemma4:12b"
	}
	for _, scenario := range []struct {
		name, brief string
		programmes  []scenarioProgramme
	}{
		{"daily", "Create a complete station named Fixture Jazz in Europe/London. Use Jazz music, one eligible default playlist, and one new fictional DJ named Rowan Vale. Schedule Jazz All Day every day 00:00 to 24:00 with this DJ and a programme-specific music direction. Do not generate artwork. Complete and verify the configuration now.", []scenarioProgramme{{"Jazz All Day", 0, 1440, []int{0, 1, 2, 3, 4, 5, 6}, "Rowan Vale", "Jazz"}}},
		{"weekend", "Create a complete station named Fixture Jazz in Europe/London with a Jazz default playlist and new fictional DJ Rowan Vale. Schedule Weekday Jazz Monday through Friday 08:00–18:00 and Weekend Jazz Saturday and Sunday 10:00–14:00. Reuse the same Jazz pool and DJ for both programmes; write distinct programme-specific music directions. Leave other hours as default music. Do not generate artwork. Complete and verify now.", []scenarioProgramme{{"Weekday Jazz", 480, 1080, []int{0, 1, 2, 3, 4}, "Rowan Vale", "Jazz"}, {"Weekend Jazz", 600, 840, []int{5, 6}, "Rowan Vale", "Jazz"}}},
		{"mixed_week", "Create a complete 24/7 station named Fixture Jazz in Europe/London. Create fictional presenters Rowan Vale for Jazz and Casey Reed for Soul. Use one Jazz pool as the station default and one Soul pool. Schedule Morning Jazz every weekday 00:00–12:00 with Rowan and Afternoon Soul every weekday 12:00–24:00 with Casey. On Saturday and Sunday schedule Weekend Soul 00:00–12:00 with Casey and Weekend Jazz 12:00–24:00 with Rowan. Each programme needs its own music direction. Reuse the two pools across their programmes. Do not generate artwork. Complete and verify all seven days now.", []scenarioProgramme{{"Morning Jazz", 0, 720, []int{0, 1, 2, 3, 4}, "Rowan Vale", "Jazz"}, {"Afternoon Soul", 720, 1440, []int{0, 1, 2, 3, 4}, "Casey Reed", "Soul"}, {"Weekend Soul", 0, 720, []int{5, 6}, "Casey Reed", "Soul"}, {"Weekend Jazz", 720, 1440, []int{5, 6}, "Rowan Vale", "Jazz"}}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
			defer cancel()
			store, registry, stations, pools, shows, presenters := fixture(t)
			source, err := (sources.Service{Repo: persistence.Sources{DB: stations.DB}}).Save(ctx, sources.Config{Name: "Fixture library", Kind: "upnp", Endpoint: "http://fixture.invalid/description"})
			if err != nil {
				t.Fatal(err)
			}
			duration := 4 * time.Minute
			items := []content.AudioItem{}
			for i := 0; i < 48; i++ {
				genre := "Jazz"
				if i >= 24 {
					genre = "Soul"
				}
				items = append(items, content.AudioItem{ID: content.ID(source.ID, fmt.Sprint(i)), SourceID: source.ID, SourceItemID: fmt.Sprint(i), Kind: content.Track, Origin: content.Original, Title: fmt.Sprintf("Fixture recording %d", i), Artist: fmt.Sprintf("Ensemble %d", i%8), Album: fmt.Sprintf("Session %d", i%6), Genre: genre, Year: 1960, Duration: &duration, Resources: []content.Resource{{URL: fmt.Sprintf("http://fixture.invalid/%d.mp3", i), MIME: "audio/mpeg"}}})
			}
			if err = (persistence.Catalogue{DB: stations.DB}).Replace(ctx, source, items); err != nil {
				t.Fatal(err)
			}

			providers := store.(ai.Store)
			if err = providers.SaveConnection(ctx, ai.Connection{ID: "scenario", Name: "Scenario Ollama", Type: "ollama", Address: address, ContextTokens: 16384, OutputTokens: 2048, RequestTimeoutSeconds: 180}, ai.KeyChange{}); err != nil {
				t.Fatal(err)
			}
			settings, err := store.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			settings.Model = model
			settings.Routes.Assistant.Connection = "scenario"
			if err = store.Configure(ctx, settings, producer.KeyChange{Clear: true}); err != nil {
				t.Fatal(err)
			}
			capture := &scenarioModel{model: inference.New(providers, nil), t: t}
			capture.model.Local.HTTP.Transport = scenarioTransport{capture: capture}
			if dir := os.Getenv("STATIONOS_TEST_TRANSCRIPTS"); dir != "" {
				if err = os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				f, e := os.OpenFile(filepath.Join(dir, scenario.name+".jsonl"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				defer f.Close()
				capture.log = json.NewEncoder(f)
			}
			svc, err := producer.New(ctx, store, registry, capture)
			if err != nil {
				t.Fatal(err)
			}
			var id string
			defer func() {
				svc.Close()
				if id != "" {
					final, e := store.Get(context.Background(), id)
					if e == nil {
						messages := []producer.Message{}
						for _, message := range final.Messages {
							if message.Role != "reasoning" {
								messages = append(messages, message)
							}
						}
						capture.record(map[string]any{"event": "checkpoint", "status": final.Status, "task": final.Task, "messages": messages})
					}
				}
			}()
			id, err = svc.Create(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			if err = svc.Start(ctx, id, producer.ID(), scenario.brief); err != nil {
				t.Fatal(err)
			}
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			var conversation producer.Conversation
		waiting:
			for {
				select {
				case <-ctx.Done():
					t.Fatal("scenario deadline exceeded; inspect transcript")
				case <-ticker.C:
					conversation, err = store.Get(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					if conversation.Status != "running" {
						break waiting
					}
				}
			}

			if len(conversation.Messages) > 0 {
				final := conversation.Messages[len(conversation.Messages)-1].Text
				if strings.Contains(final, "https://") || strings.Contains(final, "http://") {
					t.Error("invented absolute link in final answer", final)
				}
			}
			if conversation.Status != "idle" {
				t.Errorf("assistant ended %s", conversation.Status)
			}
			saved, err := stations.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(saved) != 1 {
				t.Fatalf("expected exactly one station, got %d", len(saved))
			}
			if saved[0].Name != "Fixture Jazz" || saved[0].Timezone != "Europe/London" {
				t.Errorf("station identity: %+v", saved[0])
			}
			assigned, err := (persistence.Broadcast{DB: stations.DB}).Assignment(ctx, saved[0].ID)
			if err != nil || assigned == "" {
				t.Error("missing default playlist", err)
			}
			playlists, err := pools.List(ctx, saved[0].ID)
			if err != nil {
				t.Fatal(err)
			}

			genres := map[string]bool{}
			expected := map[string]scenarioProgramme{}
			for _, p := range scenario.programmes {
				genres[p.genre] = true
				for _, day := range p.days {
					expected[fmt.Sprintf("%d/%d", day, p.start)] = p
				}
			}
			if len(playlists) != len(genres) {
				t.Errorf("expected %d reused pools; got %d", len(genres), len(playlists))
			}
			poolGenres := map[string]string{}
			for _, p := range playlists {
				result, e := pools.Resolve(ctx, p.ID, 0, 100)
				if e != nil {
					t.Fatal(e)
				}
				if len(result.Entries) != 24 {
					t.Errorf("pool %s: expected 24 eligible recordings, got %d", p.Name, len(result.Entries))
				}
				for _, entry := range result.Entries {
					if !entry.Available {
						t.Errorf("unavailable recording in pool %s", p.Name)
					}
					if previous := poolGenres[p.ID]; previous != "" && previous != entry.Item.Genre {
						t.Errorf("pool %s mixes unrelated genres", p.Name)
					}
					poolGenres[p.ID] = entry.Item.Genre
				}
			}
			if poolGenres[assigned] != "Jazz" {
				t.Errorf("default pool must contain Jazz; got %q", poolGenres[assigned])
			}
			savedShows, err := shows.List(ctx, saved[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(savedShows) != len(scenario.programmes) {
				t.Errorf("expected %d recurring programmes, got %d", len(scenario.programmes), len(savedShows))
			}
			editions := schedule.ExpandDays(savedShows)
			if len(editions) != len(expected) {
				t.Errorf("expected %d scheduled editions, got %d", len(expected), len(editions))
			}
			seen := map[string]bool{}
			for _, show := range editions {
				key := fmt.Sprintf("%d/%d", show.Day, show.Start)
				want, ok := expected[key]
				if !ok {
					t.Errorf("unexpected programme: %+v", show)
					continue
				}
				if seen[key] {
					t.Errorf("duplicate programme window %s", key)
				}
				seen[key] = true
				actualEnd := show.End
				if actualEnd == 0 {
					actualEnd = 1440
				}
				if actualEnd != want.end || show.Name != want.name {
					t.Errorf("wrong programme: %+v; want %+v", show, want)
				}
				if show.MusicDirection == "" || poolGenres[show.PlaylistID] != want.genre {
					t.Error("missing direction or wrong pool", show.ID)
				}
				dj, e := presenters.Get(ctx, show.DJID)
				if e != nil || dj.Name != want.dj {
					t.Error("wrong presenter", dj.Name, e)
				}
			}
			for key := range expected {
				if !seen[key] {
					t.Errorf("missing programme window %s", key)
				}
			}

		})
	}
}

type scenarioProgramme struct {
	name       string
	start, end int
	days       []int
	dj, genre  string
}

type scenarioModel struct {
	model *inference.Router
	t     *testing.T
	mu    sync.Mutex
	log   *json.Encoder
	round int
}

func (m *scenarioModel) record(v any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.log != nil {
		if err := m.log.Encode(v); err != nil {
			m.t.Error(err)
		}
	}
}
func (m *scenarioModel) Respond(ctx context.Context, model, key, prompt string, input []producer.Item, tools []producer.Tool) (producer.Response, error) {
	return m.RespondWithOptions(ctx, model, key, prompt, input, tools, ai.GenerationOptions{})
}
func (m *scenarioModel) RespondWithOptions(ctx context.Context, model, key, prompt string, input []producer.Item, tools []producer.Tool, options ai.GenerationOptions) (producer.Response, error) {
	m.round++
	start := time.Now()
	names := []string{}
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	m.record(map[string]any{"event": "request", "round": m.round, "model": model, "prompt": prompt, "input": input, "definitions": tools, "options": options})
	response, err := m.model.RespondWithOptions(ctx, model, key, prompt, input, tools, options)
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	// Fixtures contain no credentials or real catalogue data. Record portable
	// inputs and returned text/calls, excluding provider-private reasoning.
	m.record(map[string]any{"event": "response", "round": m.round, "elapsed_ms": time.Since(start).Milliseconds(), "tools": names, "input": input, "calls": response.Calls, "text": response.Text, "usage": response.Usage, "error": failure})
	m.t.Logf("round %d: %v calls=%d error=%s", m.round, time.Since(start).Round(time.Millisecond), len(response.Calls), failure)
	return response, err
}

// Capture native response fields even if adapter validation discards the reply
// (for example, a truncated response containing unexecuted tool calls).
// Never record HTTP headers, keys or model-private thinking.
type scenarioTransport struct{ capture *scenarioModel }

func (s scenarioTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if request.URL.Path != "/api/chat" {
		return response, nil
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	var native struct {
		Done            bool   `json:"done"`
		DoneReason      string `json:"done_reason"`
		PromptEvalCount int    `json:"prompt_eval_count"`
		EvalCount       int    `json:"eval_count"`
		Message         struct {
			Content   string          `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"message"`
	}
	parseErr := json.Unmarshal(raw, &native)
	s.capture.record(map[string]any{"event": "native_response", "status": response.StatusCode, "response": native, "valid_json": parseErr == nil})
	return response, nil
}
