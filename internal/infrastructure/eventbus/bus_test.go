package eventbus

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPublishAndSubscribe(t *testing.T) {
	bus := New(nil, 0)
	defer bus.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	msgs, err := bus.Subscribe(ctx, TopicEscalationRecorded)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	payload := EscalationRecordedEvent{ID: "esc-1", Reason: "auto_disabled"}
	bus.Publish(ctx, TopicEscalationRecorded, payload)

	select {
	case m := <-msgs:
		var got EscalationRecordedEvent
		if err := json.Unmarshal(m.Payload, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.ID != "esc-1" || got.Reason != "auto_disabled" {
			t.Errorf("payload: %+v", got)
		}
		m.Ack()
	case <-ctx.Done():
		t.Fatal("timed out waiting for event")
	}
}

func TestPublishToUnsubscribedTopicDoesNotBlock(t *testing.T) {
	bus := New(nil, 0)
	defer bus.Close()
	// No subscriber — Publish must return quickly regardless.
	done := make(chan struct{})
	go func() {
		bus.Publish(context.Background(), TopicToggleFlipped, ToggleFlippedEvent{Actor: "oncall"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("publish blocked without subscriber")
	}
}

func TestNilBusIsSafe(t *testing.T) {
	var bus *Bus
	bus.Publish(context.Background(), TopicToggleFlipped, nil) // no panic
	if err := bus.Close(); err != nil {
		t.Errorf("nil bus Close: %v", err)
	}
}
