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
func TestDJThinkingControlIsAdvertisedAndOutputIsBounded(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(map[bool]string{true: "supports-disabled", false: "mandatory-thinking"}[allowed], func(t *testing.T) {
			var metadata atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/show" {
					metadata.Add(1)
					if allowed {
						io.WriteString(w, `{"thinking":{"values":[false,true],"default":true}}`)
					} else {
						io.WriteString(w, `{"thinking":{"values":["low","medium"],"default":"medium"}}`)
					}
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				value, exists := body["think"]
				if allowed && (!exists || value != false) {
					t.Error("thinking not disabled")
				}
				if !allowed && exists {
					t.Error("unsupported thinking control sent")
				}
				options := body["options"].(map[string]any)
				if options["num_predict"] != float64(512) {
					t.Error("spoken output not capped", options)
				}
				io.WriteString(w, `{"done":true,"message":{"content":"A short spoken link."}}`)
			}))
			defer server.Close()
			c := NewClient()
			conn := connection("ollama", server.URL)
			for range 2 {
				_, err := c.chatWithOptions(context.Background(), conn, "", "gemma4:12b", "prompt", nil, nil, nil, true, ai.GenerationOptions{DisableThinking: true, MaxOutputTokens: 512})
				if err != nil {
					t.Fatal(err)
				}
			}
			if metadata.Load() != 1 {
				t.Fatal("model metadata was not cached", metadata.Load())
			}
		})
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
