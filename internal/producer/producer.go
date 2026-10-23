// Package producer owns assistant conversations and transport-independent tools.
// It knows neither HTTP handlers, SQL nor the model provider's wire protocol.
package producer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	mathrand "math/rand"
	"reflect"
	"regexp"
	"stationos.local/stationos/internal/ai"
	"stationos.local/stationos/internal/changefeed"
	"stationos.local/stationos/internal/prompts"
	"stationos.local/stationos/internal/station"
	"strings"
	"sync"
	"time"
	"unicode"
)

var ErrNotFound = errors.New("conversation not found")

// ErrEmptyResponse means a provider explicitly completed without usable text or
// calls. It is distinct from uncertain transport failures; no actions were offered.
var ErrEmptyResponse = errors.New("Provider completed without an answer or tools")

// ErrOutputLimit means a completed provider response was truncated. No tool
// calls from that response may be executed or added to durable history.
var ErrOutputLimit = errors.New("The assistant reached its reply limit before completing the response")

// ErrContextBudget is a pre-dispatch failure; no response or tool call ran.
var ErrContextBudget = errors.New("The endpoint context budget cannot fit the saved requirements and tools even with a short reply. Original requirements remain saved; increase the endpoint context budget")

// ErrInputContextLimit rejects a provider reply whose full input window is unsafe.
// It exposes usage only; no response text or tool calls may be executed.
var ErrInputContextLimit = errors.New("Local input reached the full context window; response was discarded before tool execution")

// ErrToolBatchLimit rejects an entire oversized reply before any action runs.
var ErrToolBatchLimit = errors.New("The assistant returned an oversized action batch")
var ErrBusy = errors.New("The assistant is already working. Wait for it to finish.")
var ErrUnavailable = errors.New("Configure the assistant provider and model in Assistant settings first.")
var ErrDuplicate = errors.New("This message has already been submitted.")

const MaxTranscript = 2 << 20
const BatchRounds = 24
const MaxNoProgressRounds = 12

// MaxToolCalls is shared with provider adapters. Accepted calls execute in order,
// with a durable outcome checkpoint after each call.
const MaxToolCalls = 16
const contextTarget = 128 << 10

type Task struct {
	Brief     string   `json:"brief"`
	Completed []string `json:"completed"`
	Remaining []string `json:"remaining"`
	Blockers  []string `json:"blockers"`
}

func (t Task) Validate() error {
	if strings.TrimSpace(t.Brief) == "" || len(t.Brief) > 12000 {
		return errors.New("Save the task brief, up to 12000 characters")
	}
	for _, list := range [][]string{t.Completed, t.Remaining, t.Blockers} {
		if len(list) > 100 {
			return errors.New("The task checklist is too long")
		}
		for _, line := range list {
			if strings.TrimSpace(line) == "" || len(line) > 1000 {
				return errors.New("Use nonempty task steps of up to 1000 characters")
			}
		}
	}
	return nil
}

// Activity describes real work in the current process, without exposing prompts or keys.
type Activity struct {
	Usage            Usage
	Stage            string
	Started, Changed time.Time
	Finished         int
}
type activeRun struct {
	cancel   context.CancelFunc
	activity Activity
}

func (s *Service) Activity(id string) (Activity, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.runs[id]; run != nil {
		return run.activity, true
	}
	return Activity{}, false
}
func (s *Service) recordUsage(id string, usage Usage) {
	defer s.changes.Publish("activity")
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.runs[id]; run != nil {
		run.activity.Usage.Input += usage.Input
		run.activity.Usage.Output += usage.Output
	}
}
func (s *Service) respond(ctx context.Context, id, model, key, prompt string, input []Item, tools []Tool) (Response, error) {
	return s.respondWithOptions(ctx, id, model, key, prompt, input, tools, ai.GenerationOptions{})
}

func (s *Service) respondWithOptions(ctx context.Context, id, model, key, prompt string, input []Item, tools []Tool, options ai.GenerationOptions) (Response, error) {
	for attempt := 0; ; attempt++ {
		s.activity(id, "Waiting for the AI response", false)
		var response Response
		var err error
		if configurable, ok := s.Model.(interface {
			RespondWithOptions(context.Context, string, string, string, []Item, []Tool, ai.GenerationOptions) (Response, error)
		}); ok {
			response, err = configurable.RespondWithOptions(ctx, model, key, prompt, input, tools, options)
		} else {
			response, err = s.Model.Respond(ctx, model, key, prompt, input, tools)
		}
		s.recordUsage(id, response.Usage)
		if response.Notice != "" {
			if e := s.Store.Append(ctx, id, Message{Role: "action", Text: response.Notice}); e != nil {
				return Response{}, e
			}
		}
		if err == nil {
			if response.Reasoning != "" {
				if e := s.Store.Append(ctx, id, Message{Role: "reasoning", Text: response.Reasoning}); e != nil {
					return Response{}, e
				}
			}
			// Intermediate commentary belongs to the same visible agent turn.
			if len(response.Calls) > 0 && response.Text != "" {
				if e := s.Store.Append(ctx, id, Message{Role: "assistant", Text: response.Text}); e != nil {
					return Response{}, e
				}
			}
			return response, nil
		}
		var temporary interface {
			error
			RetryAfter() time.Duration
		}
		if !errors.As(err, &temporary) || attempt >= 3 {
			return Response{}, err
		}
		delay := temporary.RetryAfter()
		if delay <= 0 {
			delay = time.Second * time.Duration(2<<attempt)
		}
		// Do not retry sooner than a long provider delay. Leave the saved run paused.
		if delay > 30*time.Second {
			return Response{}, fmt.Errorf("The provider requested a wait of at least %d seconds. Retry later. %w", int(delay.Seconds()+0.999), err)
		}
		delay += time.Duration(mathrand.Intn(250)) * time.Millisecond
		s.activity(id, fmt.Sprintf("Rate limited — waiting %d seconds before retry %d of 3", int(delay.Seconds()+0.999), attempt+1), false)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return Response{}, ctx.Err()
		}
	}
}
func (s *Service) activity(id, stage string, finished bool) {
	defer s.changes.Publish("activity")
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.runs[id]; run != nil {
		run.activity.Stage = stage
		run.activity.Changed = time.Now()
		if finished {
			run.activity.Finished++
		}
	}
}

type Settings struct {
	Model                                                               string
	CurationModel, WritingModel, SpeechModel, AudienceModel, ImageModel string
	HasKey                                                              bool
	Routes                                                              ai.Routes
	TextReady                                                           bool
}
type KeyChange struct {
	Value string
	Clear bool
}

// NormalizeKey removes clipboard wrapping from one recognizable OpenAI key.
// Other text and multiple pasted keys remain invalid rather than being joined.
func NormalizeKey(k KeyChange) KeyChange {
	k.Value = strings.TrimSpace(k.Value)
	parts := strings.Fields(k.Value)
	multipleKeys := false
	for _, part := range parts[min(1, len(parts)):] {
		if strings.HasPrefix(part, "sk-") {
			multipleKeys = true
		}
	}
	if strings.HasPrefix(k.Value, "sk-") && !multipleKeys {
		k.Value = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, k.Value)
	}
	return k
}

func ValidateSettings(model string, k KeyChange) error {
	k = NormalizeKey(k)
	if !ai.ValidModel(model) {
		return errors.New("Enter a valid model ID.")
	}
	if k.Clear && k.Value != "" {
		return errors.New("Untick Remove saved key to save a replacement key.")
	}
	if len(k.Value) > 4096 || strings.IndexFunc(k.Value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("Paste one API key without other text.")
	}
	return nil
}

type Message struct {
	ID              int64
	Role, Text, URL string
}
type Conversation struct {
	ID, StationID, Title, Status string
	Transcript                   json.RawMessage
	Task                         Task
	Requests                     []string
	Messages                     []Message
}
type Store interface {
	Settings(context.Context) (Settings, error)
	SaveSettings(context.Context, string, KeyChange) error
	Configure(context.Context, Settings, KeyChange) error
	APIKey(context.Context) (string, error)
	Create(context.Context, Conversation) error
	List(context.Context, string) ([]Conversation, error)
	Get(context.Context, string) (Conversation, error)
	Begin(context.Context, string, string, string) error
	Append(context.Context, string, Message) error
	Checkpoint(context.Context, string, json.RawMessage, Task) error
	Finish(context.Context, string, string, json.RawMessage, Message) error
	Interrupt(context.Context) error
}
type Item = json.RawMessage
type Call struct {
	ID, Name  string
	Arguments json.RawMessage
}

// Usage is measured by the provider. Input includes any cached tokens.
type Usage struct {
	Input  int `json:"input_tokens"`
	Output int `json:"output_tokens"`
}
type Response struct {
	ResponseID string
	// Reasoning contains only readable reasoning explicitly returned by the provider.
	Reasoning string
	Notice    string
	Usage     Usage
	Items     []Item
	Calls     []Call
	Text      string
}
type Model interface {
	Respond(context.Context, string, string, string, []Item, []Tool) (Response, error)
}
type Service struct {
	changes changefeed.Bus
	Prompts prompts.Store
	Store   Store
	Tools   *Registry
	Model   Model
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	slots   chan struct{}
	wg      sync.WaitGroup
	runs    map[string]*activeRun
}

func New(parent context.Context, store Store, registry *Registry, model Model) (*Service, error) {
	if err := store.Interrupt(parent); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	return &Service{Store: store, Tools: registry, Model: model, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 4), runs: map[string]*activeRun{}}, nil
}
func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (s *Service) Close() { s.mu.Lock(); s.closed = true; s.cancel(); s.mu.Unlock(); s.wg.Wait() }
func (s *Service) Create(ctx context.Context, stationID string) (string, error) {
	id := ID()
	return id, s.Store.Create(ctx, Conversation{ID: id, StationID: stationID, Title: "New conversation"})
}
func (s *Service) Start(ctx context.Context, id, requestID, text string) error {
	text = strings.TrimSpace(text)
	if regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`).MatchString(text) {
		return errors.New("Enter API keys only in Assistant settings then resend your message without the key.")
	}
	if text == "" || len(text) > 12000 || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(requestID) {
		return errors.New("Enter a message of up to 12000 characters, then try again.")
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled() || s.Model == nil {
		return ErrUnavailable
	}
	key := ""
	if _, routed := s.Store.(ai.Store); !routed && settings.HasKey {
		key, err = s.Store.APIKey(ctx)
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnavailable
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return errors.New("The assistant is busy. Try again shortly.")
	}
	if err = s.Store.Begin(ctx, id, requestID, text); err != nil {
		<-s.slots
		return err
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	if _, routed := s.Store.(ai.Store); routed {
		runCtx = ai.PinRoutes(runCtx, settings.Routes)
	}
	active := &activeRun{cancel: cancel, activity: Activity{Stage: "Starting your request", Started: time.Now(), Changed: time.Now()}}
	s.runs[id] = active
	s.changes.Publish("activity")
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { <-s.slots }()
		defer func() {
			cancel()
			s.mu.Lock()
			if s.runs[id] == active {
				delete(s.runs, id)
				s.changes.Publish("activity")
			}
			s.mu.Unlock()
		}()
		s.run(runCtx, id, settings.Model, key, settings.CurationModel)
	}()
	return nil
}

func (s *Service) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if active := s.runs[id]; active != nil {
		active.cancel()
	} else if c.Status == "running" {
		return s.Store.Finish(ctx, id, "failed", c.Transcript, Message{Role: "assistant", Text: "The worker stopped unexpectedly. Completed changes and the original request remain saved. Use Continue saved request to resume."})
	}
	return nil
}
func (s *Service) run(ctx context.Context, id, model, key, curationModel string) {
	c, err := s.Store.Get(ctx, id)
	if err != nil {
		s.finish(id, "failed", nil, "The conversation could not be loaded. Its brief remains saved; use Continue to retry.")
		return
	}
	var input []Item
	if err = json.Unmarshal(c.Transcript, &input); err != nil {
		s.finish(id, "failed", nil, "The conversation context could not be loaded. Its brief remains saved; use Continue to recover.")
		return
	}
	input = completedHistory(input)
	basePrompt, err := prompts.Load(ctx, s.Prompts, "assistant")
	if err != nil {
		s.finish(id, "failed", c.Transcript, "The assistant prompt could not be loaded. Use Continue to retry.")
		return
	}
	prompt := basePrompt + "\nConversation station ID: " + c.StationID + "\nConfigured assistant model: " + model + ". Show planning model: " + curationModel + ". DJ writing is configured separately in Assistant settings; do not supply model fields to DJ tools.\nCurrent UTC time: " + time.Now().UTC().Format(time.RFC3339)
	task := c.Task
	briefing, _ := json.Marshal(c.Requests)
	prompt += "\nSaved human requests in chronological order (preserve the original brief and apply later amendments):\n" + string(briefing)
	tools := append(s.Tools.List(), Tool{Name: "save_task_progress", Description: "Save the current task brief, completed steps, actionable remaining steps and genuine blockers. Put work requiring human input only in blockers, not in remaining. Before a multi-step request, save a checklist; update it after verified work. Keep remaining work even if a response fails. If the shared key is missing, finish show, schedule and default-playlist configuration before requesting it in Assistant settings.", Parameters: schema(reflect.TypeOf(Task{})), Mutates: true})
	fail := func(message string) {
		raw, _ := json.Marshal(compactHistory(input))
		s.finish(id, "failed", raw, message)
	}
	checkpoint := func() error {
		raw, _ := json.Marshal(compactHistory(input))
		return s.Store.Checkpoint(ctx, id, raw, task)
	}
	focus := ""
	focusReads := map[string]int{}
	checkCounts := map[string]int{}
	lastActionError := ""
	observationRounds := 0
	selectedWritePending := false
	selectedArea := ""
	reviewStations := conversationStations(c)
	if c.Task.Brief != "" && len(reviewStations) > 0 {
		review, nextFocus, err := s.configurationReview(ctx, reviewStations, true)
		if err != nil {
			fail("The saved configuration could not be read before resuming. Completed work remains saved; retry when storage is available.")
			return
		}
		if len(task.Remaining) > 0 && resumingStalled(c) {
			focus = nextFocus
			if focus == "" {
				focus = "remaining"
			}
		}
		input = append(input, review)
	}
	if focus == "" && resumingStalled(c) {
		focus = "choose"
	}
	recoverProgress := func(count int) bool {
		if count != 3 && count != 8 {
			return true
		}
		s.activity(id, "Checking saved configuration and remaining work", false)
		focus = "choose"
		if len(reviewStations) > 0 {
			review, nextFocus, err := s.configurationReview(ctx, reviewStations, true)
			if err != nil {
				fail("The saved configuration could not be checked during recovery. Completed work remains saved.")
				return false
			}
			if len(task.Remaining) > 0 && nextFocus != "" {
				focus = nextFocus
			}
			input = append(input, review)
		}
		input = append(input, mustJSON(map[string]any{"role": "developer", "content": "Progress recovery: recent replies have not advanced the task. Use the fresh measured configuration above and original human brief to choose ONE concrete unfinished step. Empty show pools need verified music rules or tracks; unscheduled requested evenings and weekend hours need shows. Inspect only the specific missing information needed, then perform the next authorised correction. Reuse saved IDs. Do not repeat failed arguments, unchanged reads, completed creations or the full plan. A checklist edit alone does not configure the station. If the work area is not yet bound, call select_work_step with the station name in the human request and the one area to change. Only the tools for this recovery step are now available. Each kind of read is limited to two calls before the next saved change. If genuinely blocked, save the exact blocker and finish other possible work."}))
		if err := checkpoint(); err != nil {
			fail("The recovery checkpoint could not be saved. Completed work remains saved.")
			return false
		}
		return true
	}
	noProgress := 0
	outputRecoveries := 0
	batchRecoveries := 0
	budgetRecovery := false
	emptyRecovery := false
	generationOptions := ai.GenerationOptions{}
	seenResults := map[[32]byte]bool{}
	schedulesToReview := map[string]bool{}
	// Successful creations are idempotent within a run, including across compacted batches.
	creations := map[string]Result{}

	seen := map[string]bool{}
	for round := 0; ; round++ {
		taskJSON, _ := json.Marshal(task)
		s.activity(id, "Waiting for the AI response", false)
		offered := focusedTools(tools, focus, focusReads)
		allowed := map[string]bool{}
		for _, tool := range offered {
			allowed[tool.Name] = true
		}
		response, err := s.respondWithOptions(ctx, id, model, key, prompt+"\nSaved task checklist:\n"+string(taskJSON), input, offered, generationOptions)
		if err == nil && len(response.Calls) > MaxToolCalls {
			err = ErrToolBatchLimit
		}
		if (errors.Is(err, ErrContextBudget) || errors.Is(err, ErrInputContextLimit)) && ctx.Err() == nil && !budgetRecovery {
			budgetRecovery = true
			generationOptions.CompactContext = true
			focus = "choose"
			if len(reviewStations) > 0 && len(task.Remaining) > 0 {
				review, nextFocus, reviewErr := s.configurationReview(ctx, reviewStations, true)
				if reviewErr != nil {
					fail("The saved configuration could not be read for context recovery. Completed work remains saved.")
					return
				}
				if nextFocus != "" {
					focus = nextFocus
				}
				input = append(input, review)
			}
			input = append(input, mustJSON(map[string]any{"role": "developer", "content": "Context recovery: the previous request or response could not be used safely. No actions from it ran. Only tools for one next work area are offered. Use select_work_step if needed, preserve the original brief and saved references, and make one small next action without restating the full plan."}))
			if err := checkpoint(); err != nil {
				fail("The context recovery checkpoint could not be saved. Completed work remains saved.")
				return
			}
			continue
		}
		if errors.Is(err, ErrEmptyResponse) && ctx.Err() == nil && !emptyRecovery {
			emptyRecovery = true
			generationOptions.CompactContext = true
			focus = "choose"
			input = append(input, mustJSON(map[string]any{"role": "developer", "content": "Empty response recovery: the provider explicitly finished without usable text or tool calls. No actions from that response ran. Earlier successful writes remain saved. Use select_work_step for one unfinished area, inspect its saved configuration and continue the original brief with one small action. Do not recreate completed entities or repeat the full plan. If genuinely blocked, record the blocker honestly."}))
			if err = checkpoint(); err != nil {
				fail("The empty-response recovery checkpoint could not be saved. Completed actions remain saved; use Continue to resume.")
				return
			}
			continue
		}
		if errors.Is(err, ErrOutputLimit) && ctx.Err() == nil && outputRecoveries < 2 {
			outputRecoveries++
			generationOptions.DisableThinking = true
			s.activity(id, "Continuing with smaller steps", false)
			if outputRecoveries == 1 {
				input = append(input, mustJSON(map[string]any{"role": "developer", "content": "The previous provider response reached its output limit and was discarded. None of its tool calls were executed. Continue the same saved human request in small steps: when actions are needed, issue one or two tool calls per response with short arguments, save concise task progress, and avoid repeating the full plan or weekly schedule while building it. Preserve all requirements and recorded successful actions. Read current state before retrying uncertain prior work. Give a concise final summary with links to saved configuration. If the human requested only a plan or explanation, answer concisely without making changes."}))
			}
			if err = checkpoint(); err != nil {
				fail("The recovery checkpoint could not be saved. The original brief remains saved; use Continue to resume.")
				return
			}
			continue
		}
		if errors.Is(err, ErrOutputLimit) && ctx.Err() == nil {
			fail("The model repeatedly ran out of reply space. Your original brief and completed actions remain saved; use Continue to resume.")
			return
		}
		if errors.Is(err, ErrToolBatchLimit) && ctx.Err() == nil && batchRecoveries < 2 {
			batchRecoveries++
			s.activity(id, "Splitting the next actions into smaller steps", false)
			if batchRecoveries == 1 {
				input = append(input, mustJSON(map[string]any{"role": "developer", "content": "The last reply contained an oversized action batch and was discarded before any of its calls ran. Continue the same saved human request. Return only one or two tool calls in the next response, wait for their saved results, then continue with the remaining actions. Keep the original brief, task checklist and earlier successful outcomes. Do not invent IDs or repeat already committed creations. If the request is only for a plan or explanation, answer concisely without making changes."}))
			}
			if err = checkpoint(); err != nil {
				fail("The task checkpoint could not be saved. The original brief remains saved; use Continue to resume.")
				return
			}
			continue
		}
		if errors.Is(err, ErrToolBatchLimit) && ctx.Err() == nil {
			fail("The model repeatedly returned an oversized action batch. Your original brief and completed actions remain saved; use Continue to resume.")
			return
		}
		if err != nil {
			message := err.Error() + " Completed actions and the original brief remain saved. Resolve the issue above before using Continue to resume."
			if ctx.Err() != nil {
				message = "The assistant stopped. Completed actions and the original brief remain saved. Use Continue to resume."
			}
			fail(message)
			return
		}
		if len(response.Calls) == 0 {
			if len(task.Remaining) > 0 || selectedWritePending {
				noProgress++
				if !recoverProgress(noProgress) {
					return
				}
				if noProgress >= MaxNoProgressRounds {
					fail(stalledMessage(checkCounts, lastActionError))
					return
				}
				input = append(input, mustJSON(map[string]any{"role": "developer", "content": "The saved task still has actionable unfinished steps. Record tasks that genuinely require human input separately as blockers, then finish the other steps. Continue performing the remaining work now. Do not ask the human to repeat the brief or to continue. If genuinely blocked, record the exact blocker with save_task_progress and complete all other possible steps first."}))
				continue
			}
			text := strings.TrimSpace(response.Text)
			if text == "" {
				fail("The assistant returned no answer. Completed actions remain saved.")
				return
			}
			if len(schedulesToReview) > 0 {
				review, err := s.scheduleReview(ctx, schedulesToReview, false)
				if err != nil {
					fail("The saved schedule could not be checked. Completed actions remain saved; use Continue to retry verification.")
					return
				}
				input = append(input, review)
				schedulesToReview = map[string]bool{}
				if err = checkpoint(); err != nil {
					fail("The schedule review checkpoint could not be saved. Use Continue to resume.")
					return
				}
				continue
			}
			// Retain completed turns as text plus audit results, excluding private provider reasoning.
			input = append(input, mustJSON(map[string]any{"role": "assistant", "content": text}))
			raw, _ := json.Marshal(compactHistory(input))
			s.finish(id, "idle", raw, text)
			return
		}
		// All provider output items, including reasoning, must be passed back within this run.
		input = append(input, response.Items...)
		madeProgress := false
		configurationChanged := false
		afterCalls := []Item{}
		for _, call := range response.Calls {
			if call.ID == "" || seen[call.ID] {
				fail("The assistant repeated an action identifier. Execution stopped to avoid duplicate changes.")
				return
			}
			seen[call.ID] = true
			if err = ctx.Err(); err != nil {
				fail("The assistant stopped. Completed actions remain saved.")
				return
			}
			if !allowed[call.Name] {
				input = append(input, toolOutput(call.ID, Result{Error: "This tool is not offered in the current recovery step. Use the available tools for one unfinished action, or record an exact blocker with save_task_progress. No action ran."}))
				continue
			}
			if call.Name == "select_work_step" {
				checkCounts[call.Name]++
				var step workStep
				decoder := json.NewDecoder(strings.NewReader(string(call.Arguments)))
				decoder.DisallowUnknownFields()
				stepErr := decoder.Decode(&step)
				var owner station.Station
				if stepErr == nil {
					owner, stepErr = s.resolveWorkStep(ctx, step, c)
				}
				result := Result{}
				if stepErr != nil {
					result.Error = stepErr.Error()
				} else {
					focus = step.Area
					if owner.ID != "" {
						reviewStations = map[string]bool{owner.ID: true}
						review, reviewErr := s.scheduleReview(ctx, reviewStations, true)
						if reviewErr != nil {
							result.Error = reviewErr.Error()
						} else {
							afterCalls = append(afterCalls, review)
						}
					}
					if result.Error == "" {
						selectedWritePending = step.Area != "review"
						selectedArea = step.Area
						result.Data = map[string]any{"area": step.Area, "station_id": owner.ID, "station_name": owner.Name, "catalogue_release_year_available": false, "catalogue_source_date_rules_available": true}
						if selectedWritePending && task.Brief == "" {
							brief := c.Title
							for _, request := range c.Requests {
								if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(request)), "continue") {
									brief = request
								}
							}
							task = Task{Brief: brief, Remaining: []string{"Complete the saved human request and verify the resulting configuration"}}
						}
						afterCalls = append(afterCalls, mustJSON(map[string]any{"role": "developer", "content": "Selected StationOS work step: " + step.Area + ". Use the station ID returned by this tool. Honour only the original authorised request. Use musical knowledge of album/track releases together with exact artist, album and track matches in the catalogue for era requests. Source-date year/decade rules are available and selections combine automatically across categories; years prefer original release dates where supplied by the source, falling back to album release dates that may describe reissues. A missing year field alone is not a blocker: use suitable album rules or individual tracks, inspect editions and avoid unverified compilation/reissue extras. Never invent availability or claim unverified dates. Record blockers only when matching music or sufficient evidence is genuinely unavailable. Complete requested configuration, then verify before updating task progress."}))
					}
				}
				input = append(input, toolOutput(call.ID, result))
				if err := checkpoint(); err != nil {
					fail("The work-step checkpoint could not be saved. Completed work remains saved.")
					return
				}
				continue
			}
			if call.Name == "save_task_progress" {
				s.activity(id, "Updating the task checklist", false)
				var next Task
				decoder := json.NewDecoder(strings.NewReader(string(call.Arguments)))
				decoder.DisallowUnknownFields()
				err := decoder.Decode(&next)
				if err == nil {
					err = next.Validate()
				}
				result := Result{Summary: "Task checklist saved"}
				if err != nil {
					result = Result{Error: err.Error()}
				} else {
					task = next
					if len(next.Remaining) == 0 && (len(next.Completed) > 0 || len(next.Blockers) > 0) {
						selectedWritePending = false
						selectedArea = ""
						focus = ""
					}
				}
				input = append(input, toolOutput(call.ID, result))
				input = releaseCatalogueDetails(input)
				if err = checkpoint(); err != nil {
					fail("The task checkpoint could not be saved. Use Continue once storage is available.")
					return
				}
				continue
			}
			tool, ok := s.Tools.Find(call.Name)
			if !ok {
				input = append(input, toolOutput(call.ID, Result{Error: "Unknown tool"}))
				continue
			}
			if focus != "" && !tool.Mutates && focusReads[tool.Name] >= 2 {
				input = append(input, toolOutput(call.ID, Result{Error: "This recovery step has already used its two reads of this tool. Use the saved results for the next action, or record an exact blocker. No action ran."}))
				continue
			}
			if !tool.Mutates {
				checkCounts[tool.Name]++
				if focus != "" {
					focusReads[tool.Name]++
				}
			}
			s.activity(id, toolActivity(tool), false)
			if tool.Mutates {
				// Persist intent before invoking a write. Never replay unfinished actions on restart.
				if err = s.Store.Append(ctx, id, Message{Role: "action", Text: "Started: " + strings.ReplaceAll(tool.Name, "_", " ")}); err != nil {
					fail("The action could not be recorded, so it was not started.")
					return
				}
			}
			cacheKey := canonicalCall(call)
			result, cached := creations[cacheKey]
			if !cached {
				result = s.Tools.call(ctx, call.Name, call.Arguments, false)
				s.recordUsage(id, result.Usage)
			}
			if result.Error != "" && tool.Mutates {
				lastActionError = call.Name + ": " + result.Error
				if len(lastActionError) > 500 {
					lastActionError = lastActionError[:500]
				}
			}
			if result.Error == "" && tool.Mutates && !cached {
				configurationChanged = true
				selectedWritePending = false
				focus = selectedArea
				focusReads = map[string]int{}
			}
			if result.Error == "" {
				if stationID := changedScheduleStation(call); stationID != "" {
					schedulesToReview[stationID] = true
					reviewStations[stationID] = true
				}
				if call.Name == "save_station" {
					if value, ok := result.Data.(station.Station); ok {
						reviewStations[value.ID] = true
					}
				}
			}
			if result.Error == "" && isCreation(call) {
				creations[cacheKey] = result
			}
			s.activity(id, "Checking the result", true)
			resultJSON, _ := json.Marshal(result)
			signature := sha256.Sum256([]byte(cacheKey + ":" + string(resultJSON)))
			if result.Error == "" && !seenResults[signature] {
				madeProgress = true
				seenResults[signature] = true
			}
			if tool.Mutates {
				text := result.Summary
				if result.Error != "" {
					text = "Failed: " + result.Error
				}
				if err = s.Store.Append(ctx, id, Message{Role: "action", Text: text, URL: result.URL}); err != nil {
					fail("An action ran but its result could not be recorded. Inspect current station state before continuing.")
					return
				}
			}
			input = append(input, toolOutput(call.ID, result))
			if err = checkpoint(); err != nil {
				fail("An action finished but its checkpoint could not be saved. Inspect current state before continuing; the original brief remains saved.")
				return
			}
			if !cached && result.Error == "" && isCreation(call) && s.Tools.artwork != nil && (call.Name == "save_station" || call.Name == "save_dj") {
				// The creation and its returned ID are checkpointed before a slow,
				// billable image request, so Stop/restart can resume artwork alone.
				stage := "Creating the station logo"
				if call.Name == "save_dj" {
					stage = "Creating the DJ portrait"
				}
				s.activity(id, stage, false)
				if err = s.Store.Append(ctx, id, Message{Role: "action", Text: stage}); err != nil {
					fail("The entity was saved, but artwork could not be started. Its ID remains saved.")
					return
				}
				enriched := s.Tools.artwork(ctx, call.Name, call.Arguments, result)
				s.recordUsage(id, enriched.Usage)
				creations[cacheKey] = enriched
				input[len(input)-1] = toolOutput(call.ID, enriched)
				if err = s.Store.Append(ctx, id, Message{Role: "action", Text: enriched.Summary, URL: enriched.URL}); err != nil {
					fail("The entity was saved. Artwork was interrupted or its result could not be recorded; inspect its saved ID before retrying artwork.")
					return
				}
				if err = checkpoint(); err != nil {
					fail("The entity was saved, but its artwork checkpoint could not be saved. Inspect existing artwork before retrying.")
					return
				}
			}
		}
		input = append(input, afterCalls...)
		if configurationChanged {
			observationRounds = 0
		} else {
			observationRounds++
			if observationRounds >= 4 {
				madeProgress = false
			}
		}
		if madeProgress {
			noProgress = 0
		} else {
			noProgress++
		}
		if !recoverProgress(noProgress) {
			return
		}
		if noProgress >= MaxNoProgressRounds {
			fail(stalledMessage(checkCounts, lastActionError))
			return
		}
		raw, _ := json.Marshal(input)
		if (round+1)%BatchRounds == 0 || len(raw) > contextTarget {
			input = compactHistory(input)
			input = append(input, mustJSON(map[string]any{"role": "developer", "content": "Continue the same human request from the saved task checklist and recorded results. Check remaining requirements and finish all possible configuration, including shows and default playlist. A batch boundary is not a reason to stop or ask the human to repeat instructions."}))
		}
	}

}
func toolActivity(tool Tool) string {
	name := tool.Name
	switch name {
	case "search_music", "explore_music":
		return "Exploring the music library"
	case "generate_station_logo":
		return "Creating the station logo"
	case "generate_dj_photo":
		return "Creating the DJ portrait"
	case "refresh_source":
		return "Importing music metadata"
	case "save_task_progress":
		return "Updating the task checklist"
	}
	if tool.Mutates {
		return "Applying changes in StationOS"
	}
	return "Checking saved StationOS information"
}
func mustJSON(v any) Item { b, _ := json.Marshal(v); return b }
func toolOutput(id string, r Result) Item {
	b, _ := json.Marshal(r)
	return mustJSON(map[string]any{"type": "function_call_output", "call_id": id, "output": string(b)})
}
func (s *Service) finish(id, status string, raw json.RawMessage, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if activity, ok := s.Activity(id); ok && (activity.Usage.Input > 0 || activity.Usage.Output > 0) {
		// An audit message keeps usage available after navigation and server restarts.
		if err := s.Store.Append(ctx, id, Message{Role: "action", Text: fmt.Sprintf("AI usage this run: %d input tokens (including cached input), %d output tokens. This is a token count, not a cost estimate.", activity.Usage.Input, activity.Usage.Output)}); err != nil {
			slog.Error("assistant usage could not be saved", "conversation_id", id, "error", err)
		}
	}
	// Checkpoints contain completed text/results, never an unanswered call sequence.
	if err := s.Store.Finish(ctx, id, status, raw, Message{Role: "assistant", Text: text}); err != nil {
		slog.Error("assistant result could not be saved", "conversation_id", id, "error", err)
	}
}

// Provider call/reasoning items are needed within a turn. Completed turns become
// ordinary text history so subsequent turns never replay an unfinished protocol.
func completedHistory(input []Item) []Item {
	out := []Item{}
	for _, item := range input {
		var v struct {
			Role   string `json:"role"`
			Type   string `json:"type"`
			Output string `json:"output"`
		}
		if json.Unmarshal(item, &v) != nil {
			continue
		}
		if v.Role == "user" || v.Role == "assistant" {
			var msg struct {
				Content json.RawMessage `json:"content"`
			}
			_ = json.Unmarshal(item, &msg)
			var plain string
			if json.Unmarshal(msg.Content, &plain) != nil {
				var parts []struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(msg.Content, &parts)
				for _, part := range parts {
					plain += part.Text
				}
			}
			out = append(out, mustJSON(map[string]any{"role": v.Role, "content": plain}))
		} else if v.Type == "function_call_output" {
			out = append(out, mustJSON(map[string]any{"role": "assistant", "content": RecordedResultPrefix + v.Output}))
		}
	}
	return releaseCatalogueDetails(out)
}

func isCreation(call Call) bool {
	switch call.Name {
	case "save_station", "save_playlist", "save_dj", "save_source":
		var value struct {
			ID string `json:"id"`
		}
		return json.Unmarshal(call.Arguments, &value) == nil && value.ID == ""
	}
	return false
}

// Compaction retains original chronological order and the latest human-facing
// proposal even when a subsequent catalogue page exceeds the checkpoint target.
func compactHistory(input []Item) []Item {
	history := completedHistory(input)
	selected := make([]bool, len(history))
	budget := contextTarget
	latestProposal, approvedProposal := -1, -1
	for i, item := range history {
		var v struct{ Role, Content string }
		_ = json.Unmarshal(item, &v)
		if v.Role == "assistant" && !strings.HasPrefix(v.Content, RecordedResultPrefix) {
			latestProposal = i
		}
		if v.Role == "user" {
			approvedProposal = latestProposal
			selected[i] = true
			budget -= len(item)
		}
	}
	for _, i := range []int{latestProposal, approvedProposal} {
		if i >= 0 && !selected[i] {
			selected[i] = true
			budget -= len(history[i])
		}
	}
	for i := len(history) - 1; i >= 0; i-- {
		if !selected[i] && len(history[i]) <= budget {
			selected[i] = true
			budget -= len(history[i])
		}
	}
	out := []Item{}
	for i, item := range history {
		if selected[i] {
			out = append(out, item)
		}
	}
	return out
}

func canonicalCall(call Call) string {
	var values map[string]any
	if json.Unmarshal(call.Arguments, &values) != nil {
		return call.Name + ":" + string(call.Arguments)
	}
	if isCreation(call) {
		delete(values, "id")
	}
	raw, _ := json.Marshal(values)
	return call.Name + ":" + string(raw)
}

func (s Settings) Enabled() bool {
	return s.TextReady || (s.Routes.Assistant.Connection == "" && s.HasKey)
}

// WatchChanges observes real activity without starting or advancing a task.
func (s *Service) WatchChanges() (<-chan struct{}, func()) { return s.changes.Watch("activity") }
