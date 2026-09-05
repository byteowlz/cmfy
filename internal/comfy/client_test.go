package comfy_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cmfy/internal/comfy"
	"cmfy/internal/output"
)

func TestClientBoundsJSONAndRejectsCrossOriginRedirects(t *testing.T) {
	t.Parallel()
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("cross-origin redirect was followed")
	}))
	defer foreign.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/queue":
			_, _ = io.WriteString(w, strings.Repeat("x", 65))
		case "/system_stats":
			http.Redirect(w, r, foreign.URL, http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := comfy.NewClientWithOptions(server.URL, comfy.ClientOptions{MaxJSONBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.QueueContext(context.Background()); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("expected bounded queue error, got %v", err)
	}
	if err := client.PingContext(context.Background()); err == nil || !strings.Contains(err.Error(), "cross-origin redirect") {
		t.Fatalf("expected redirect rejection, got %v", err)
	}
}

func TestClientStreamsRangesAndBoundsUploads(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/view":
			if r.Header.Get("Range") == "bytes=3-" {
				w.WriteHeader(http.StatusPartialContent)
				_, _ = io.WriteString(w, "def")
				return
			}
			_, _ = io.WriteString(w, "abcdef")
		case "/upload/image":
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, `{"name":"uploaded.png"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := comfy.NewClientWithOptions(server.URL, comfy.ClientOptions{MaxUploadBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	body, info, err := client.Fetch(context.Background(), output.Descriptor{Filename: "result.png"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil || string(data) != "def" || !info.Partial {
		t.Fatalf("unexpected ranged fetch data=%q partial=%v err=%v", data, info.Partial, err)
	}
	path := filepath.Join(t.TempDir(), "too-large.png")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UploadContext(context.Background(), path); err == nil || !strings.Contains(err.Error(), "upload byte limit") {
		t.Fatalf("expected upload limit, got %v", err)
	}
}

func TestClientRejectsInvalidBaseURL(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"file:///tmp/comfy", "http://user:secret@example.test", "http://example.test/path", "not a url"} {
		if _, err := comfy.NewClientWithOptions(raw, comfy.ClientOptions{}); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
	if _, err := comfy.NewClientWithOptions("http://127.0.0.1:8188", comfy.ClientOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestClientReportsBoundedServerErrors(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, strings.Repeat("sensitive-detail", 100))
	}))
	defer server.Close()
	client, err := comfy.NewClientWithOptions(server.URL, comfy.ClientOptions{MaxErrorBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.PromptContext(context.Background(), "client", map[string]any{"1": map[string]any{}})
	if err == nil {
		t.Fatal("expected prompt error")
	}
	if len(err.Error()) > 128 {
		t.Fatalf("server error was not bounded: %d bytes", len(err.Error()))
	}
	if !errors.Is(err, comfy.ErrServerResponse) {
		t.Fatalf("expected ErrServerResponse, got %v", err)
	}
}
