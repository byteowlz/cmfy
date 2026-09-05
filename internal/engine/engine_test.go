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
