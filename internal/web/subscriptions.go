package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// snapshotResponse buffers the existing read-only fragment handler. It never
// exposes arbitrary URLs/templates and preserves each handler's validation.
type snapshotResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *snapshotResponse) Header() http.Header { return w.header }
func (w *snapshotResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *snapshotResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(b)
}

func (s *Server) subscription(topic, target string, render http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("subscribe") != "1" || r.Method == http.MethodHead {
			render(w, r)
			return
		}
		select {
		case s.eventSlots <- struct{}{}:
			defer func() { <-s.eventSlots }()
		default:
			http.Error(w, "too many live views", http.StatusServiceUnavailable)
			return
		}
		changes, stop := s.d.Changes.Watch(topic)
		defer stop()
		var runtime <-chan struct{}
		if s.d.Broadcast != nil && topic != "assistant" {
			var release func()
			runtime, release = s.d.Broadcast.WatchChanges()
			defer release()
		}
		var activity, health <-chan struct{}
		if topic == "assistant" && s.d.Assistant != nil {
			var release func()
			activity, release = s.d.Assistant.WatchChanges()
			defer release()
		}
		if topic == "operations" && s.d.Recording != nil {
			var release func()
			health, release = s.d.Recording.WatchChanges()
			defer release()
		}
		request := r.Clone(r.Context())
		request.Header = r.Header.Clone()
		request.Header.Set("HX-Request", "true")
		request.Header.Set("HX-Target", target)
		request.Header.Del("HX-Boosted")
		request.Header.Del("HX-History-Restore-Request")
		query := request.URL.Query()
		query.Del("subscribe")
		request.URL.RawQuery = query.Encode()
		snapshot := func() *snapshotResponse {
			out := &snapshotResponse{header: make(http.Header)}
			render(out, request)
			return out
		}
		initial := snapshot()
		if initial.status != http.StatusOK {
			for key, values := range initial.header {
				w.Header()[key] = values
			}
			w.WriteHeader(initial.status)
			_, _ = w.Write(initial.body.Bytes())
			return
		}
		rc := http.NewResponseController(w)
		defer rc.SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		write := func(frame string) bool {
			if rc.SetWriteDeadline(time.Now().Add(10*time.Second)) != nil {
				return false
			}
			if _, err := fmt.Fprint(w, frame); err != nil {
				return false
			}
			return rc.Flush() == nil
		}
		var previous [32]byte
		// A one-shot deadline handles an actual expiry, never periodic reconciliation.
		var deadline <-chan time.Time
		var timer *time.Timer
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		update := func(out *snapshotResponse) bool {
			if out.status != http.StatusOK {
				write("event: unavailable\ndata: {}\n\n")
				return false
			}
			if timer != nil {
				timer.Stop()
			}
			deadline = nil
			if at, err := time.Parse(time.RFC3339Nano, out.header.Get("X-Live-Deadline")); err == nil {
				timer = time.NewTimer(time.Until(at))
				deadline = timer.C
			}
			hash := sha256.Sum256(out.body.Bytes())
			if hash == previous {
				return true
			}
			data, err := json.Marshal(out.body.String())
			if err != nil {
				return false
			}
			if !write("event: snapshot\ndata: " + string(data) + "\n\n") {
				return false
			}
			previous = hash
			return true
		}
		if !update(initial) {
			return
		}
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-activity:
				if !update(snapshot()) {
					return
				}
			case <-health:
				if !update(snapshot()) {
					return
				}
			case <-changes:
				if !update(snapshot()) {
					return
				}
			case _, open := <-runtime:
				if !open {
					return
				}
				if !update(snapshot()) {
					return
				}
			case <-deadline:
				if !update(snapshot()) {
					return
				}
			case <-heartbeat.C:
				if !write(": keepalive\n\n") {
					return
				}
			}
		}
	}
}
