package inference

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"stationos.local/stationos/internal/ai"
)

func TestGenerationDeadlineStartsAfterQueueAdmission(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(map[bool]string{false: "producer", true: "live"}[live], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(400 * time.Millisecond)
				io.WriteString(w, `{"done":true,"message":{"content":"Ready"}}`)
			}))
			defer server.Close()
			c := NewClient()
			conn := connection("ollama", server.URL)
			conn.RequestTimeoutSeconds = 1
			hold, _ := c.Gate.acquire(context.Background(), false)
			done := make(chan error, 1)
			go func() {
				_, err := c.chat(context.Background(), conn, "", "m", "prompt", nil, nil, nil, live)
				done <- err
			}()
			time.Sleep(800 * time.Millisecond)
			hold()
			if err := <-done; err != nil {
				t.Fatal("queue wait consumed response deadline", err)
			}
		})
	}
}
func TestOllamaThinkingSettingForLiveAndBackground(t *testing.T) {
	for _, thinking := range []bool{false, true} {
		for _, live := range []bool{false, true} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/chat" {
					t.Errorf("unexpected metadata request: %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if value, exists := body["think"]; !exists || value != thinking {
					t.Errorf("think = %v, want %v", value, thinking)
				}
				options := body["options"].(map[string]any)
				if options["num_predict"] != float64(512) {
					t.Error("spoken output not capped", options)
				}
				io.WriteString(w, `{"done":true,"message":{"content":"A short spoken link."}}`)
			}))
			conn := connection("ollama", server.URL)
			_, err := NewClient().chatWithOptions(context.Background(), conn, "", "gemma4:12b", "prompt", nil, nil, nil, live, ai.GenerationOptions{MaxOutputTokens: 512, Thinking: thinking})
			server.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestQueueTimeoutIsDistinctAndDoesNotDispatch(t *testing.T) {
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent.Add(1) }))
	defer server.Close()
	c := NewClient()
	hold, _ := c.Gate.acquire(context.Background(), false)
	defer hold()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := c.chat(ctx, connection("ollama", server.URL), "", "m", "prompt", nil, nil, nil, true)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "queue") || sent.Load() != 0 {
		t.Fatal("queue expiry misreported or dispatched", err, sent.Load())
	}
}

func TestLiveQueueWaitMatchesActiveRequestBudget(t *testing.T) {
	conn := connection("ollama", "http://configured-provider")
	conn.RequestTimeoutSeconds = 180
	if conn.QueueTimeout(true) != 180*time.Second || conn.QueueTimeout(false) != 0 {
		t.Fatal("live wait cannot outlast legitimate active inference")
	}
	r := router(conn)
	budget, err := r.Budget(context.Background(), "curation")
	if err != nil || budget != 360*time.Second {
		t.Fatal("production budget omits the live queue wait", budget, err)
	}
}
func TestShortLiveLinksPrecedeQueuedShowPlans(t *testing.T) {
	g := &Gate{}
	hold, err := g.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	plan := make(chan func(), 1)
	link := make(chan func(), 1)
	background := make(chan func(), 1)
	waitQueued := func(n int) {
		until := time.Now().Add(time.Second)
		for time.Now().Before(until) {
			g.mu.Lock()
			count := len(g.urgent) + len(g.live) + len(g.background)
			g.mu.Unlock()
			if count == n {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("queue did not fill")
	}
	go func() {
		f, e := g.acquirePriority(context.Background(), livePriority)
		if e == nil {
			plan <- f
		}
	}()
	waitQueued(1)
	go func() {
		f, e := g.acquirePriority(context.Background(), backgroundPriority)
		if e == nil {
			background <- f
		}
	}()
	waitQueued(2)
	go func() {
		f, e := g.acquirePriority(context.Background(), linkPriority)
		if e == nil {
			link <- f
		}
	}()
	waitQueued(3)
	hold()
	select {
	case f := <-link:
		f()
	case <-plan:
		t.Fatal("show planning overtook a short link")
	case <-time.After(time.Second):
		t.Fatal("link not admitted")
	}
	select {
	case f := <-plan:
		f()
	case <-background:
		t.Fatal("producer overtook live planning")
	case <-time.After(time.Second):
		t.Fatal("plan not admitted")
	}
	select {
	case f := <-background:
		f()
	case <-time.After(time.Second):
		t.Fatal("producer request lost")
	}
}
func TestStaleQueuedLinkIsCancelledWithoutInference(t *testing.T) {
	g := &Gate{}
	hold, _ := g.acquire(context.Background(), false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		f, err := g.acquirePriority(ctx, linkPriority)
		if f != nil {
			f()
		}
		done <- err
	}()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		g.mu.Lock()
		queued := len(g.urgent)
		g.mu.Unlock()
		if queued == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	g.mu.Lock()
	remaining := len(g.urgent)
	g.mu.Unlock()
	if remaining != 0 {
		t.Fatal("stale link retained in queue")
	}
	hold()
	release, err := g.acquire(context.Background(), false)
	if err != nil {
		t.Fatal("cancelled link leaked inference slot", err)
	}
	release()
}

func TestProducerQueueCancellationDoesNotDispatchOrLeakSlot(t *testing.T) {
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		io.WriteString(w, `{"done":true,"message":{"content":"Ready"}}`)
	}))
	defer server.Close()
	client := NewClient()
	conn := connection("ollama", server.URL)
	if conn.QueueTimeout(false) != 0 {
		t.Fatal("producer still has a separate admission deadline")
	}
	hold, err := client.Gate.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := client.chat(ctx, conn, "", "m", "prompt", nil, nil, nil, false); done <- err }()
	deadline := time.Now().Add(time.Second)
	queued := false
	for time.Now().Before(deadline) {
		client.Gate.mu.Lock()
		queued = len(client.Gate.background) == 1
		client.Gate.mu.Unlock()
		if queued {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !queued {
		cancel()
		hold()
		t.Fatal("producer did not queue")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		hold()
		t.Fatal(err)
	}
	if sent.Load() != 0 {
		hold()
		t.Fatal("cancelled request dispatched")
	}
	hold()
	nextCtx, nextCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer nextCancel()
	_, err = client.chat(nextCtx, conn, "", "m", "prompt", nil, nil, nil, false)
	if err != nil || sent.Load() != 1 {
		t.Fatal("cancelled producer leaked inference slot", err, sent.Load())
	}
	budget, err := router(conn).Budget(context.Background(), "assistant")
	if err != nil || budget != 0 {
		t.Fatal("producer was given a deadline that excludes unbounded queue time", budget, err)
	}
}

func TestThinkingIsIndependentForModelsSharingOllamaEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string
			Think *bool
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Think == nil || *body.Think != (body.Model == "planning-model") {
			t.Error("wrong model setting", body)
		}
		io.WriteString(w, `{"done":true,"message":{"content":"Ready"}}`)
	}))
	defer server.Close()
	r := router(connection("ollama", server.URL))
	store := r.Store.(*testStore)
	store.routes.Curation = ai.Selection{Connection: "local", Thinking: true}
	store.routes.Writing = ai.Selection{Connection: "local", Thinking: false}
	for _, role := range []string{"curation", "writing", "assistant"} {
		model := "speech-model"
		if role == "curation" {
			model = "planning-model"
		}
		if _, err := r.Generate(context.Background(), ai.TextRequest{Role: role, Model: model, Input: "test"}); err != nil {
			t.Fatal(err)
		}
	}
}
