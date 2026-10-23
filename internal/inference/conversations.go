package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/producer"
	"time"
)

const conversationSaveTimeout = 5 * time.Second

type conversationLock struct {
	gate  chan struct{}
	users int
}

// Serialise only one episode, never unrelated stations. Waiting respects the
// caller's playback deadline and entries disappear when their last user leaves.
func (r *Router) lockConversation(ctx context.Context, key string) (func(), error) {
	r.sessionMu.Lock()
	if r.sessionLocks == nil {
		r.sessionLocks = map[string]*conversationLock{}
	}
	lock := r.sessionLocks[key]
	if lock == nil {
		lock = &conversationLock{gate: make(chan struct{}, 1)}
		r.sessionLocks[key] = lock
	}
	lock.users++
	r.sessionMu.Unlock()
	leave := func() {
		r.sessionMu.Lock()
		defer r.sessionMu.Unlock()
		lock.users--
		if lock.users == 0 {
			delete(r.sessionLocks, key)
		}
	}
	select {
	case lock.gate <- struct{}{}:
		return func() { <-lock.gate; leave() }, nil
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	}
}

func (r *Router) converse(ctx context.Context, q ai.TextRequest, input producer.Item) (producer.Response, error) {
	if q.Session != "" && !q.Options.Stateless {
		q.Instructions += "\nEpisode conversation contract: continue this dated DJ episode. Earlier assistant responses are preparations, not proof of acceptance or airtime. The latest episode checkpoint and current/next metadata are authoritative. Follow the current task and response format; do not repeat an earlier task."
	}
	invoke := func(items []producer.Item, options ai.GenerationOptions) (producer.Response, error) {
		return r.respond(ctx, q.Role, q.Model, q.Instructions, items, nil, q.Schema, options)
	}
	if q.Session == "" || q.Options.Stateless {
		return invoke([]producer.Item{input}, q.Options)
	}
	if r.Conversations == nil {
		return producer.Response{}, errors.New("Episode conversation storage is unavailable")
	}
	release, err := r.lockConversation(ctx, q.Session)
	if err != nil {
		return producer.Response{}, err
	}
	defer release()
	// Pin provider routing for the whole exchange, including an expired-cursor recovery.
	routes, err := r.routes(ctx)
	if err != nil {
		return producer.Response{}, err
	}
	ctx = ai.PinRoutes(ctx, routes)
	selection, conn, key, err := r.selection(ctx, q.Role)
	if err != nil {
		return producer.Response{}, err
	}
	identityRaw, _ := json.Marshal([]string{conn.ID, conn.Type, conn.Address, key, q.Model, fmt.Sprint(selection.Thinking)})
	sum := sha256.Sum256(identityRaw)
	identity := hex.EncodeToString(sum[:])
	saved, err := r.Conversations.LoadConversation(ctx, q.Session)
	if err != nil {
		return producer.Response{}, err
	}
	options := q.Options
	options.StoreConversation = conn.Type == "openai"
	if saved.ProviderIdentity == identity && options.StoreConversation {
		options.PreviousResponseID = saved.PreviousResponseID
	}
	history := make([]producer.Item, 0, len(saved.History)+1)
	for _, item := range saved.History {
		history = append(history, producer.Item(item))
	}
	history = append(history, input)
	items := history
	if options.PreviousResponseID != "" {
		items = []producer.Item{episodeAiredDelta(input, saved.KnownAired)}
	}
	out, err := invoke(items, options)
	// Explicit cursor/context rejections are safe to rebuild from portable memory;
	// ambiguous failures are never replayed.
	if (errors.Is(err, ai.ErrConversationUnavailable) || errors.Is(err, ai.ErrContextLimit)) && options.PreviousResponseID != "" {
		options.PreviousResponseID = ""
		out, err = invoke(history, options)
	}
	if err != nil {
		return out, err
	}
	assistant, _ := json.Marshal(map[string]string{"role": "assistant", "content": out.Text})
	saved.History = append(saved.History, compactEpisodeInput(input, out.Text), assistant)
	if saved.KnownAired == nil {
		saved.KnownAired = map[string]bool{}
	}
	rememberAired(input, saved.KnownAired)
	saved.ProviderIdentity = identity
	saved.PreviousResponseID = out.ResponseID
	// The provider may have completed after our caller was cancelled. Persist its
	// cursor with a short independent deadline, so another turn cannot fork history.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), conversationSaveTimeout)
	defer cancel()
	if err = r.Conversations.SaveConversation(saveCtx, q.Session, saved); err != nil {
		return producer.Response{}, err
	}
	return out, nil
}

// Candidate searches and repeated snapshots are working data, not musical memory.
// Retain the task, factual current/next metadata and every assistant response.
// The latest turn still contains the complete authoritative episode checkpoint.
func compactEpisodeInput(raw producer.Item, answer string) json.RawMessage {
	var envelope struct{ Role, Content string }
	if json.Unmarshal(raw, &envelope) != nil {
		return raw
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(envelope.Content), &payload) != nil {
		return raw
	}
	// Resolve offered IDs to metadata before dropping transient candidate lists.
	var choice struct {
		Tracks  []string
		TrackID string `json:"track_id"`
	}
	_ = json.Unmarshal([]byte(answer), &choice)
	selected := map[string]bool{}
	for _, id := range choice.Tracks {
		selected[id] = true
	}
	if choice.TrackID != "" {
		selected[choice.TrackID] = true
	}
	recordings := []map[string]any{}
	var collect func(any)
	collect = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			id, _ := v["ID"].(string)
			if id == "" {
				id, _ = v["id"].(string)
			}
			if selected[id] {
				recordings = append(recordings, v)
				delete(selected, id)
				return
			}
			for _, child := range v {
				collect(child)
			}
		case []any:
			for _, child := range v {
				collect(child)
			}
		}
	}
	var source any
	if json.Unmarshal([]byte(envelope.Content), &source) == nil {
		collect(source)
	}
	if len(recordings) > 0 {
		payload["chosen_recording_metadata"], _ = json.Marshal(recordings)
	}
	for _, field := range []string{"episode", "episode_session", "session_tool_history", "tracks"} {
		delete(payload, field)
	}
	content, _ := json.Marshal(payload)
	result, _ := json.Marshal(map[string]string{"role": envelope.Role, "content": string(content)})
	return result
}

// Only a provider continuation can rely on earlier aired receipts. Portable replay
// always gets the complete current checkpoint, including automatic selections.
func episodeAiredDelta(raw producer.Item, known map[string]bool) producer.Item {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return raw
	}
	var text string
	if json.Unmarshal(envelope["content"], &text) != nil {
		return raw
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &payload) != nil {
		return raw
	}
	for _, field := range []string{"episode", "episode_session"} {
		checkpoint := payload[field]
		encoded := false
		var nested string
		if json.Unmarshal(checkpoint, &nested) == nil {
			checkpoint = []byte(nested)
			encoded = true
		}
		var state map[string]json.RawMessage
		if json.Unmarshal(checkpoint, &state) != nil || state == nil {
			continue
		}
		var aired []json.RawMessage
		if json.Unmarshal(state["Aired"], &aired) != nil {
			continue
		}
		delta := []json.RawMessage{}
		for _, track := range aired {
			var receipt struct {
				ID      string
				AiredAt string
			}
			if json.Unmarshal(track, &receipt) != nil {
				continue
			}
			key := receipt.ID + "@" + receipt.AiredAt
			if !known[key] {
				delta = append(delta, track)
			}
		}
		// Do not mutate known while constructing a request. It advances only after
		// the completed turn has been persisted below.
		state["Aired"], _ = json.Marshal(delta)
		state["aired_history_mode"], _ = json.Marshal("new_since_previous_turn; earlier receipts remain in this episode conversation")
		updated, _ := json.Marshal(state)
		if encoded {
			updated, _ = json.Marshal(string(updated))
		}
		payload[field] = updated
	}
	updated, _ := json.Marshal(payload)
	envelope["content"], _ = json.Marshal(string(updated))
	result, _ := json.Marshal(envelope)
	return result
}

func rememberAired(raw producer.Item, known map[string]bool) {
	var envelope struct{ Content string }
	if json.Unmarshal(raw, &envelope) != nil {
		return
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(envelope.Content), &payload) != nil {
		return
	}
	for _, field := range []string{"episode", "episode_session"} {
		checkpoint := payload[field]
		var nested string
		if json.Unmarshal(checkpoint, &nested) == nil {
			checkpoint = []byte(nested)
		}
		var state struct {
			Aired []struct {
				ID      string
				AiredAt string
			}
		}
		if json.Unmarshal(checkpoint, &state) != nil {
			continue
		}
		for _, receipt := range state.Aired {
			known[receipt.ID+"@"+receipt.AiredAt] = true
		}
	}
}
