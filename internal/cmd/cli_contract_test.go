package cmd_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMachineCLIContracts(t *testing.T) {
	var promptCounter atomic.Int64
	var activePrompts atomic.Int64
	var maxActivePrompts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/prompt":
			active := activePrompts.Add(1)
			for active > maxActivePrompts.Load() && !maxActivePrompts.CompareAndSwap(maxActivePrompts.Load(), active) {
			}
			time.Sleep(40 * time.Millisecond)
			activePrompts.Add(-1)
			id := promptCounter.Add(1)
			fmt.Fprintf(w, `{"prompt_id":"prompt-%d"}`, id)
		case r.URL.Path == "/object_info":
			_, _ = w.Write([]byte(`{"KSampler":{},"SaveImage":{}}`))
		case r.URL.Path == "/queue" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"queue_running":[],"queue_pending":[[0,"prompt-1"]]}`))
		case r.URL.Path == "/queue" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/history/prompt-2":
			_, _ = w.Write([]byte(`{"prompt-2":{"status":{"completed":true},"outputs":{"9":{"images":[{"filename":"result.png","subfolder":"","type":"output"}]}}}}`))
		case strings.HasPrefix(r.URL.Path, "/history/"):
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/view":
			_, _ = w.Write([]byte("image bytes"))
		case r.URL.Path == "/system_stats":
			_, _ = w.Write([]byte(`{"system":{"os":"test"}}`))
		case r.URL.Path == "/models":
			_, _ = w.Write([]byte(`["checkpoints"]`))
		case r.URL.Path == "/models/checkpoints":
			_, _ = w.Write([]byte(`["model.safetensors"]`))
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
	workflowBody := `{
		"variables":{"PROMPT":{"default":"default prompt"}},
		"1":{"class_type":"KSampler","inputs":{"text":"${PROMPT}"}},
		"2":{"class_type":"SaveImage","inputs":{"images":["1",0],"filename_prefix":"result"}}
	}`
	if err := os.WriteFile(filepath.Join(workflowDir, "test.json"), []byte(workflowBody), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.toml")
	configBody := fmt.Sprintf("server_url = %q\nworkflows_dir = %q\noutput_dir = %q\n[servers.test]\nurl = %q\n", server.URL, workflowDir, filepath.Join(root, "outputs"), server.URL)
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "cmfy")
	_, currentFile, _, _ := runtime.Caller(0)
	repository := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	build := exec.Command("go", "build", "-o", binary, "./cmd/cmfy")
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build cmfy: %v\n%s", err, output)
	}
	stateDir := filepath.Join(root, "state")
	runJSON := func(arguments ...string) map[string]any {
		t.Helper()
		base := []string{"--config", configPath, "--state-dir", stateDir, "--json"}
		command := exec.Command(binary, append(base, arguments...)...)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		if err := command.Run(); err != nil {
			t.Fatalf("cmfy %v: %v\nstdout=%s\nstderr=%s", arguments, err, stdout.String(), stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("cmfy %v wrote machine success prose to stderr: %q", arguments, stderr.String())
		}
		return decodeSingleJSON(t, stdout.Bytes())
	}

	run := runJSON("run", "--async", "--request-id", "request-1", "--workflow", "test", "--prompt", "exact prompt")
	if run["prompt_id"] != "prompt-1" || run["prompt"] != "exact prompt" || run["status"] != "queued" {
		t.Fatalf("unexpected run result: %#v", run)
	}
	if page := runJSON("jobs", "list"); len(page["jobs"].([]any)) != 1 {
		t.Fatalf("unexpected jobs page: %#v", page)
	}
	if shown := runJSON("jobs", "show", "prompt-1"); shown["request_id"] != "request-1" {
		t.Fatalf("unexpected job: %#v", shown)
	}
	if status := runJSON("jobs", "status", "prompt-1"); status["status"] != "queued" {
		t.Fatalf("unexpected reconciled job: %#v", status)
	}
	completedSubmission := runJSON("run", "--async", "--request-id", "request-completed", "--workflow", "test", "--prompt", "collect me")
	if completedSubmission["prompt_id"] != "prompt-2" {
		t.Fatalf("unexpected completed submission: %#v", completedSubmission)
	}
	collected := runJSON("job", "wait", "--download", "--output-dir", filepath.Join(root, "collected"), "prompt-2")
	if collected["status"] != "completed" || len(collected["outputs"].([]any)) != 1 {
		t.Fatalf("unexpected wait/collect result: %#v", collected)
	}
	if plan := runJSON("run", "--plan", "--workflow", "test", "--prompt", "plan"); plan["schema"] != "cmfy/execution-plan-v1" || !plan["server_validation"].(map[string]any)["valid"].(bool) {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	for _, arguments := range [][]string{
		{"config", "path"},
		{"config", "output"},
		{"config", "print"},
		{"workflows", "list"},
		{"workflows", "show", "test"},
		{"workflows", "inspect", "test"},
		{"workflows", "describe", "test"},
		{"workflows", "validate", "test"},
		{"workflows", "aliases"},
		{"--profile", "test", "server", "ping"},
		{"server", "ping"},
		{"server", "inspect"},
		{"server", "profiles"},
		{"queue"},
		{"job", "status", "prompt-1"},
		{"job", "cancel", "prompt-1"},
	} {
		runJSON(arguments...)
	}
	batchPath := filepath.Join(root, "batch.jsonl")
	batchBody := `{"id":"batch-1","workflow":"test","vars":{"PROMPT":"batch one"},"async":true}` + "\n" +
		`{"id":"batch-2","workflow":"test","vars":{"PROMPT":"batch two"},"async":true}` + "\n"
	if err := os.WriteFile(batchPath, []byte(batchBody), 0o600); err != nil {
		t.Fatal(err)
	}
	batch := runJSON("batch", "run", "--file", batchPath, "--concurrency", "2")
	if rows, ok := batch["array"].([]any); !ok || len(rows) != 2 {
		t.Fatalf("unexpected batch result: %#v", batch)
	}
	if maxActivePrompts.Load() < 2 {
		t.Fatalf("batch did not honor concurrency: max active prompts=%d", maxActivePrompts.Load())
	}

	failure := exec.Command(binary, "--config", configPath, "--state-dir", stateDir, "--json", "workflows", "describe", "missing")
	var failureOut, failureErr bytes.Buffer
	failure.Stdout = &failureOut
	failure.Stderr = &failureErr
	if err := failure.Run(); err == nil {
		t.Fatal("expected missing workflow to fail")
	}
	if failureErr.Len() != 0 {
		t.Fatalf("machine failure wrote prose to stderr: %q", failureErr.String())
	}
	envelope := decodeSingleJSON(t, failureOut.Bytes())
	if envelope["schema"] != "cmfy/error-v1" {
		t.Fatalf("unexpected error envelope: %#v", envelope)
	}
}

func decodeSingleJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("invalid JSON %q: %v", body, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		t.Fatalf("multiple JSON values in %q", body)
	}
	if object, ok := value.(map[string]any); ok {
		return object
	}
	if array, ok := value.([]any); ok {
		return map[string]any{"array": array}
	}
	t.Fatalf("unexpected JSON shape: %#v", value)
	return nil
}
