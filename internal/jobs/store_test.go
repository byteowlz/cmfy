package jobs_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cmfy/internal/jobs"
)

func TestStoreRecoversDistinctJobsAfterRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite3")

	store, err := jobs.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, created, err := store.Reserve(ctx, jobs.Submission{
		RequestID:      "request-1",
		ServerID:       "local",
		Workflow:       "portrait",
		WorkflowDigest: "sha256:first",
		Prompt:         "first prompt",
		Parameters:     map[string]any{"width": 1024, "height": 1024},
		SubmittedAt:    time.Date(2026, 9, 5, 20, 0, 0, 0, time.UTC),
	})
	if err != nil || !created {
		t.Fatalf("reserve first: created=%v err=%v", created, err)
	}
	if err := store.MarkSubmitted(ctx, first.RequestID, "prompt-1"); err != nil {
		t.Fatal(err)
	}
	second, created, err := store.Reserve(ctx, jobs.Submission{
		RequestID:      "request-2",
		ServerID:       "local",
		Workflow:       "landscape",
		WorkflowDigest: "sha256:second",
		Prompt:         "second prompt",
		Parameters:     map[string]any{"width": 1280, "height": 720},
		SubmittedAt:    time.Date(2026, 9, 5, 20, 1, 0, 0, time.UTC),
	})
	if err != nil || !created {
		t.Fatalf("reserve second: created=%v err=%v", created, err)
	}
	if err := store.MarkSubmitted(ctx, second.RequestID, "prompt-2"); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, "prompt-1", jobs.Update{
		Status:    "completed",
		Outputs:   []jobs.Output{{Filename: "first.png", MediaType: "image/png", Size: 42, SHA256: "abc"}},
		UpdatedAt: time.Date(2026, 9, 5, 20, 2, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, "prompt-2", jobs.Update{
		Status:    "cancelled",
		UpdatedAt: time.Date(2026, 9, 5, 20, 3, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = jobs.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	page, err := store.List(ctx, jobs.ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(page.Jobs))
	}
	if page.Jobs[0].PromptID != "prompt-2" || page.Jobs[0].Prompt != "second prompt" || page.Jobs[0].Status != "cancelled" {
		t.Fatalf("unexpected newest job: %#v", page.Jobs[0])
	}
	if page.Jobs[1].PromptID != "prompt-1" || page.Jobs[1].Prompt != "first prompt" || page.Jobs[1].Outputs[0].Filename != "first.png" {
		t.Fatalf("unexpected completed job: %#v", page.Jobs[1])
	}
}

func TestStoreDeduplicatesRequestID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := jobs.Open(filepath.Join(t.TempDir(), "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	submission := jobs.Submission{RequestID: "same-request", ServerID: "local", Workflow: "one", Prompt: "original"}
	original, created, err := store.Reserve(ctx, submission)
	if err != nil || !created {
		t.Fatalf("first reserve: created=%v err=%v", created, err)
	}
	duplicate, created, err := store.Reserve(ctx, jobs.Submission{RequestID: "same-request", ServerID: "other", Workflow: "two", Prompt: "different"})
	if err != nil || created {
		t.Fatalf("duplicate reserve: created=%v err=%v", created, err)
	}
	if duplicate.ID != original.ID || duplicate.Prompt != "original" {
		t.Fatalf("duplicate changed reservation: original=%#v duplicate=%#v", original, duplicate)
	}
}
