package comfy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cmfy/internal/comfy"

	"github.com/gorilla/websocket"
)

func TestWatchContextRejectsMalformedFrames(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		_ = connection.WriteMessage(websocket.TextMessage, []byte(`{not-json`))
	}))
	defer server.Close()
	client, err := comfy.NewClientWithOptions(server.URL, comfy.ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	events, failures := client.WatchContext(context.Background(), "client", "prompt")
	for range events {
	}
	if err := <-failures; err == nil {
		t.Fatal("expected malformed WebSocket frame to fail")
	}
}

func TestWatchContextProducesBoundedPromptEvents(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" || r.URL.Query().Get("clientId") != "request-1" {
			http.NotFound(w, r)
			return
		}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		_ = connection.WriteJSON(map[string]any{"type": "progress", "data": map[string]any{"prompt_id": "other", "value": 1, "max": 2}})
		_ = connection.WriteJSON(map[string]any{"type": "progress", "data": map[string]any{"prompt_id": "prompt-1", "node": "7", "value": 3, "max": 10}})
		_ = connection.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3})
		_ = connection.WriteJSON(map[string]any{"type": "executing", "data": map[string]any{"prompt_id": "prompt-1", "node": nil}})
	}))
	defer server.Close()
	client, err := comfy.NewClientWithOptions(server.URL, comfy.ClientOptions{MaxEventBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events, failures := client.WatchContext(ctx, "request-1", "prompt-1")
	var received []comfy.Event
	for event := range events {
		received = append(received, event)
	}
	if err := <-failures; err != nil {
		t.Fatal(err)
	}
	if len(received) != 3 {
		t.Fatalf("got %#v", received)
	}
	if received[0].Type != "progress" || received[0].Value != 3 || received[0].Max != 10 || received[0].NodeID != "7" {
		t.Fatalf("unexpected progress: %#v", received[0])
	}
	if received[1].Type != "preview" || received[1].ByteCount != 3 {
		t.Fatalf("unexpected preview: %#v", received[1])
	}
	if received[2].Type != "completed" {
		t.Fatalf("unexpected terminal event: %#v", received[2])
	}
}
