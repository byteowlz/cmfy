package jobs_test

import (
	"context"
	"errors"
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
	firstPage, err := store.List(ctx, jobs.ListOptions{Limit: 1})
	if err != nil || len(firstPage.Jobs) != 1 || firstPage.NextCursor == "" {
		t.Fatalf("unexpected first page: %#v err=%v", firstPage, err)
	}
	secondPage, err := store.List(ctx, jobs.ListOptions{Limit: 1, Cursor: firstPage.NextCursor})
	if err != nil || len(secondPage.Jobs) != 1 || secondPage.Jobs[0].PromptID != "prompt-1" {
		t.Fatalf("unexpected second page: %#v err=%v", secondPage, err)
	}
	filtered, err := store.List(ctx, jobs.ListOptions{Limit: 10, Status: "completed", ServerID: "local"})
	if err != nil || len(filtered.Jobs) != 1 || filtered.Jobs[0].Prompt != "first prompt" {
		t.Fatalf("unexpected filtered page: %#v err=%v", filtered, err)
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

func TestPruneRemovesOnlyOldTerminalJobs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := jobs.Open(filepath.Join(t.TempDir(), "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, submission := range []jobs.Submission{
		{RequestID: "completed", ServerID: "server", Workflow: "one", SubmittedAt: old},
		{RequestID: "running", ServerID: "server", Workflow: "two", SubmittedAt: old},
	} {
		if _, _, err := store.Reserve(ctx, submission); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Update(ctx, "completed", jobs.Update{Status: "completed", UpdatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, "running", jobs.Update{Status: "running", UpdatedAt: old}); err != nil {
		t.Fatal(err)
	}
	count, err := store.Prune(ctx, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 0)
	if err != nil || count != 1 {
		t.Fatalf("prune count=%d err=%v", count, err)
	}
	if _, err := store.Get(ctx, "running"); err != nil {
		t.Fatalf("running job was pruned: %v", err)
	}
	if _, err := store.Get(ctx, "completed"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("completed job remains: %v", err)
	}
}

func TestUploadCacheRoundTrip(t *testing.T) {
	t.Parallel()
	store, err := jobs.Open(filepath.Join(t.TempDir(), "state", "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.PutUpload(ctx, jobs.Upload{ServerID: "server-1", SHA256: "abc", RemoteName: "input.png", Size: 42}); err != nil {
		t.Fatal(err)
	}
	upload, found, err := store.GetUpload(ctx, "server-1", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if !found || upload.RemoteName != "input.png" || upload.Size != 42 {
		t.Fatalf("unexpected upload: %#v, found=%v", upload, found)
	}
	if _, found, err := store.GetUpload(ctx, "server-2", "abc"); err != nil || found {
		t.Fatalf("cache leaked across servers: found=%v err=%v", found, err)
	}
}
