package engine_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cmfy/internal/comfy"
	"cmfy/internal/config"
	"cmfy/internal/engine"
	"cmfy/internal/jobs"

	"github.com/gorilla/websocket"
)

func TestResolveSubmitObserveCollectUsesOneDurableSubstrate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var submissions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prompt":
			submissions.Add(1)
			var request struct {
				Prompt map[string]any `json:"prompt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			node := request.Prompt["1"].(map[string]any)
			inputs := node["inputs"].(map[string]any)
			if inputs["text"] != "a durable owl" {
				t.Errorf("prompt was not resolved: %#v", inputs)
			}
			_, _ = w.Write([]byte(`{"prompt_id":"prompt-1"}`))
		case "/history/prompt-1":
			_, _ = w.Write([]byte(`{"prompt-1":{"status":{"completed":true},"outputs":{"9":{"images":[{"filename":"result.png","subfolder":"","type":"output"}]}}}}`))
		case "/queue":
			_, _ = w.Write([]byte(`{"queue_running":[],"queue_pending":[]}`))
		case "/view":
			_, _ = w.Write([]byte("image bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	workflowDir := filepath.Join(root, "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	workflowJSON := `{"prompt":{"1":{"class_type":"CLIPTextEncode","inputs":{"text":"${PROMPT}"}},"9":{"class_type":"SaveImage","inputs":{"images":["1",0]}}}}`
	if err := os.WriteFile(filepath.Join(workflowDir, "portrait.json"), []byte(workflowJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := jobs.Open(filepath.Join(root, "state", "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	client, err := comfy.NewClientWithOptions(server.URL, comfy.ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service := engine.New(engine.Options{
		Config: &config.Config{
			ServerURL:              server.URL,
			OutputDir:              filepath.Join(root, "outputs"),
			WorkflowsDir:           workflowDir,
			Vars:                   map[string]string{},
			WorkflowVars:           map[string]map[string]string{},
			StandardWorkflows:      map[string]string{},
			StandardWorkflowParams: map[string]map[string]string{},
		},
		Client: client,
		Jobs:   store,
	})
	plan, err := service.Resolve(ctx, engine.Request{
		RequestID: "request-1",
		Workflow:  "portrait",
		Prompt:    "a durable owl",
		Variables: map[string]string{"PROMPT": "a durable owl"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Contract.Digest == "" || !plan.Validation.Valid {
		t.Fatalf("invalid plan: %#v", plan)
	}
	job, created, err := service.Submit(ctx, plan)
	if err != nil || !created || job.PromptID != "prompt-1" {
		t.Fatalf("submit job=%#v created=%v err=%v", job, created, err)
	}
	duplicate, created, err := service.Submit(ctx, plan)
	if err != nil || created || duplicate.PromptID != job.PromptID || submissions.Load() != 1 {
		t.Fatalf("idempotency failed duplicate=%#v created=%v submissions=%d err=%v", duplicate, created, submissions.Load(), err)
	}
	observed, err := service.Observe(ctx, job.PromptID)
	if err != nil || observed.Status != "completed" {
		t.Fatalf("observe=%#v err=%v", observed, err)
	}
	collected, err := service.Collect(ctx, job.PromptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(collected.Outputs) != 1 || collected.Outputs[0].Path != "result.png" || collected.Outputs[0].SHA256 == "" {
		t.Fatalf("unexpected collected job: %#v", collected)
	}
	if body, err := os.ReadFile(filepath.Join(root, "outputs", "result.png")); err != nil || string(body) != "image bytes" {
		t.Fatalf("materialized output body=%q err=%v", body, err)
	}
}

func TestCancelReportsExplicitQueuedAndRunningOutcomes(t *testing.T) {
	t.Parallel()
	var queueDeletes atomic.Int32
	var interrupts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/history/queued-prompt" || r.URL.Path == "/history/running-prompt":
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/queue" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"queue_running":[[0,"running-prompt"]],"queue_pending":[[0,"queued-prompt"]]}`))
		case r.URL.Path == "/queue" && r.Method == http.MethodPost:
			queueDeletes.Add(1)
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/interrupt":
			interrupts.Add(1)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	store, err := jobs.Open(filepath.Join(t.TempDir(), "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, pair := range [][2]string{{"queued-request", "queued-prompt"}, {"running-request", "running-prompt"}} {
		if _, _, err := store.Reserve(ctx, jobs.Submission{RequestID: pair[0], ServerID: "server", Workflow: "test"}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkSubmitted(ctx, pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	client, err := comfy.NewClientWithOptions(server.URL, comfy.ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service := engine.New(engine.Options{Config: &config.Config{ServerURL: server.URL}, Client: client, Jobs: store})
	queued, err := service.Cancel(ctx, "queued-prompt")
	if err != nil || queued.PreviousStatus != "queued" || queued.Status != "cancelling" || queued.Outcome != "request_sent" {
		t.Fatalf("queued cancellation=%#v err=%v", queued, err)
	}
	running, err := service.Cancel(ctx, "running-prompt")
	if err != nil || running.PreviousStatus != "running" || running.Status != "cancelling" || running.Outcome != "request_sent" {
		t.Fatalf("running cancellation=%#v err=%v", running, err)
	}
	if queueDeletes.Load() != 1 || interrupts.Load() != 1 {
		t.Fatalf("queue deletes=%d interrupts=%d", queueDeletes.Load(), interrupts.Load())
	}
}

func TestConcurrentIdempotentSubmissionsReturnTheSameSubmittedJob(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var submissions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prompt" {
			http.NotFound(w, r)
			return
		}
		submissions.Add(1)
		once.Do(func() { close(entered) })
		<-release
		_, _ = w.Write([]byte(`{"prompt_id":"same-prompt"}`))
	}))
	defer server.Close()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test.json"), []byte(`{"1":{"class_type":"SaveImage","inputs":{"images":["0",0]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := jobs.Open(filepath.Join(root, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	client, err := comfy.NewClientWithOptions(server.URL, comfy.ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ServerURL: server.URL, WorkflowsDir: root, OutputDir: filepath.Join(root, "out"), Vars: map[string]string{}, WorkflowVars: map[string]map[string]string{}, StandardWorkflows: map[string]string{}, StandardWorkflowParams: map[string]map[string]string{}}
	service := engine.New(engine.Options{Config: cfg, Client: client, Jobs: store})
	plan, err := service.Resolve(context.Background(), engine.Request{RequestID: "same-request", Workflow: "test"})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		job     jobs.Record
		created bool
		err     error
	}
	results := make(chan result, 2)
	go func() {
		job, created, err := service.Submit(context.Background(), plan)
		results <- result{job: job, created: created, err: err}
	}()
	<-entered
	go func() {
		job, created, err := service.Submit(context.Background(), plan)
		results <- result{job: job, created: created, err: err}
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil || first.job.PromptID != "same-prompt" || second.job.PromptID != "same-prompt" {
		t.Fatalf("unexpected results first=%#v second=%#v", first, second)
	}
	if first.created == second.created || submissions.Load() != 1 {
		t.Fatalf("idempotency failed first=%#v second=%#v submissions=%d", first, second, submissions.Load())
	}
}

func TestWatchReconnectsAfterPollingFallback(t *testing.T) {
	t.Parallel()
	var websocketAttempts atomic.Int32
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws":
			connection, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer connection.Close()
			if websocketAttempts.Add(1) == 1 {
				return
			}
			_ = connection.WriteJSON(map[string]any{"type": "executing", "data": map[string]any{"prompt_id": "prompt-reconnect", "node": nil}})
		case "/history/prompt-reconnect":
			_, _ = w.Write([]byte(`{}`))
		case "/queue":
			_, _ = w.Write([]byte(`{"queue_running":[[0,"prompt-reconnect"]],"queue_pending":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	store, err := jobs.Open(filepath.Join(t.TempDir(), "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := store.Reserve(ctx, jobs.Submission{RequestID: "request-reconnect", ServerID: "server", Workflow: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSubmitted(ctx, "request-reconnect", "prompt-reconnect"); err != nil {
		t.Fatal(err)
	}
	client, err := comfy.NewClientWithOptions(server.URL, comfy.ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service := engine.New(engine.Options{Config: &config.Config{ServerURL: server.URL}, Client: client, Jobs: store})
	events, failures := service.Watch(ctx, "prompt-reconnect", time.Millisecond)
	var received []comfy.Event
	for event := range events {
		received = append(received, event)
	}
	if err := <-failures; err != nil {
		t.Fatal(err)
	}
	if websocketAttempts.Load() < 2 || len(received) == 0 || received[len(received)-1].Type != "completed" {
		t.Fatalf("watch did not reconnect: attempts=%d events=%#v", websocketAttempts.Load(), received)
	}
}

func TestWatchFallsBackToPollingWhenWebSocketIsUnavailable(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/history/prompt-fallback" {
			_, _ = w.Write([]byte(`{"prompt-fallback":{"status":{"completed":true},"outputs":{}}}`))
			return
		}
		if r.URL.Path == "/queue" {
			_, _ = w.Write([]byte(`{"queue_running":[],"queue_pending":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	store, err := jobs.Open(filepath.Join(t.TempDir(), "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := store.Reserve(ctx, jobs.Submission{RequestID: "request-fallback", ServerID: "server", Workflow: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkSubmitted(ctx, "request-fallback", "prompt-fallback"); err != nil {
		t.Fatal(err)
	}
	client, err := comfy.NewClientWithOptions(server.URL, comfy.ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service := engine.New(engine.Options{Config: &config.Config{ServerURL: server.URL}, Client: client, Jobs: store})
	events, failures := service.Watch(ctx, "prompt-fallback", time.Millisecond)
	var received []comfy.Event
	for event := range events {
		received = append(received, event)
	}
	if err := <-failures; err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || received[0].Type != "completed" {
		t.Fatalf("unexpected fallback events: %#v", received)
	}
}

func TestResolveFailsBeforeNetworkForUnresolvedVariables(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.json"), []byte(`{"1":{"class_type":"LoadImage","inputs":{"image":"${IMAGE}"}},"9":{"class_type":"SaveImage","inputs":{"images":["1",0]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service := engine.New(engine.Options{Config: &config.Config{WorkflowsDir: root, StandardWorkflows: map[string]string{}, StandardWorkflowParams: map[string]map[string]string{}, Vars: map[string]string{}, WorkflowVars: map[string]map[string]string{}}})
	_, err := service.Resolve(context.Background(), engine.Request{RequestID: "request", Workflow: "broken"})
	if err == nil {
		t.Fatal("expected unresolved IMAGE to fail")
	}
}
