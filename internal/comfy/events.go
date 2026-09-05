package comfy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

type Event struct {
	Schema    string    `json:"schema"`
	Type      string    `json:"type"`
	PromptID  string    `json:"prompt_id,omitempty"`
	NodeID    string    `json:"node_id,omitempty"`
	Value     int       `json:"value,omitempty"`
	Max       int       `json:"max,omitempty"`
	ByteCount int       `json:"byte_count,omitempty"`
	Message   string    `json:"message,omitempty"`
	Time      time.Time `json:"time"`
}

func (c *Client) WatchContext(ctx context.Context, clientID, promptID string) (<-chan Event, <-chan error) {
	events := make(chan Event)
	failures := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(failures)
		if c.initErr != nil {
			failures <- c.initErr
			return
		}
		websocketURL := *c.base
		switch websocketURL.Scheme {
		case "http":
			websocketURL.Scheme = "ws"
		case "https":
			websocketURL.Scheme = "wss"
		default:
			failures <- errors.New("unsupported WebSocket scheme")
			return
		}
		websocketURL.Path = "/ws"
		query := url.Values{}
		query.Set("clientId", clientID)
		websocketURL.RawQuery = query.Encode()
		dialer := websocket.Dialer{HandshakeTimeout: c.options.Timeout}
		connection, response, err := dialer.DialContext(ctx, websocketURL.String(), nil)
		if err != nil {
			if response != nil {
				failures <- fmt.Errorf("WebSocket handshake failed with HTTP %d: %w", response.StatusCode, err)
			} else {
				failures <- err
			}
			return
		}
		defer connection.Close()
		connection.SetReadLimit(c.options.MaxEventBytes)
		for {
			messageType, body, err := connection.ReadMessage()
			if err != nil {
				if ctx.Err() != nil {
					failures <- ctx.Err()
				} else if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					failures <- errors.New("WebSocket closed before a terminal job event")
				} else {
					failures <- err
				}
				return
			}
			event, relevant, terminal, err := decodeEvent(messageType, body, promptID)
			if err != nil {
				failures <- err
				return
			}
			if !relevant {
				continue
			}
			select {
			case events <- event:
			case <-ctx.Done():
				failures <- ctx.Err()
				return
			}
			if terminal {
				failures <- nil
				return
			}
		}
	}()
	return events, failures
}

func decodeEvent(messageType int, body []byte, promptID string) (Event, bool, bool, error) {
	now := time.Now().UTC()
	if messageType == websocket.BinaryMessage {
		return Event{Schema: "cmfy/job-event-v1", Type: "preview", PromptID: promptID, ByteCount: len(body), Time: now}, true, false, nil
	}
	if messageType != websocket.TextMessage {
		return Event{}, false, false, nil
	}
	var envelope struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Event{}, false, false, fmt.Errorf("decode ComfyUI event: %w", err)
	}
	eventPromptID, _ := envelope.Data["prompt_id"].(string)
	if eventPromptID != "" && eventPromptID != promptID {
		return Event{}, false, false, nil
	}
	nodeID, _ := envelope.Data["node"].(string)
	event := Event{Schema: "cmfy/job-event-v1", PromptID: promptID, NodeID: nodeID, Time: now}
	switch envelope.Type {
	case "execution_start":
		event.Type = "running"
	case "executing":
		if envelope.Data["node"] == nil {
			event.Type = "completed"
			return event, true, true, nil
		}
		event.Type = "executing"
	case "progress":
		event.Type = "progress"
		event.Value = numberAsInt(envelope.Data["value"])
		event.Max = numberAsInt(envelope.Data["max"])
	case "executed":
		event.Type = "node_completed"
	case "execution_cached":
		event.Type = "cached"
	case "execution_error":
		event.Type = "failed"
		event.Message, _ = envelope.Data["exception_message"].(string)
		return event, true, true, nil
	case "execution_interrupted":
		event.Type = "cancelled"
		return event, true, true, nil
	default:
		return Event{}, false, false, nil
	}
	return event, true, false, nil
}

func numberAsInt(value any) int {
	switch number := value.(type) {
	case float64:
		return int(number)
	case json.Number:
		integer, _ := number.Int64()
		return int(integer)
	default:
		return 0
	}
}
