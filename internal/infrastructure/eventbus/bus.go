// Package eventbus wraps Watermill with an in-process pub/sub so the
// orchestrator can emit domain events (escalations, conversions, toggle
// flips, backpressure drops) without tight coupling to downstream
// subscribers. The default backend is gochannel — no network dependency —
// so dev and tests run unchanged; production can swap in NATS, Kafka, or
// Redis Streams by providing a different Publisher/Subscriber pair to
// New(). Subscribers decode JSON payloads from the event Body.
package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
)

// Topic is a string alias (not a distinct type) so consumer-side interfaces
// elsewhere can declare `topic string` without importing this package.
type Topic = string

const (
	TopicEscalationRecorded Topic = "escalation.recorded"
	TopicConversionManaged  Topic = "conversion.managed"
	TopicConversionDone     Topic = "conversion.converted"
	TopicToggleFlipped      Topic = "toggle.flipped"
	TopicBackpressureDrop   Topic = "backpressure.dropped"
	TopicBudgetExceeded     Topic = "budget.exceeded"
)

// Bus is the publish/subscribe wrapper. Safe for concurrent publishers;
// subscribers get their own Watermill Subscribe channel per Topic. Close
// shuts the underlying pub/sub down.
type Bus struct {
	pub message.Publisher
	sub message.Subscriber
	log *slog.Logger
}

// ErrDisabled is returned by Publish when the bus has been shut down. Callers
// ignore this because events are advisory: a shut-down bus does not fail
// upstream work.
var ErrDisabled = errors.New("eventbus disabled")

// New constructs an in-memory Bus backed by gochannel. Buffer caps the number
// of pending events per topic before publishers block; for a non-blocking
// bus use a large buffer.
func New(log *slog.Logger, buffer int64) *Bus {
	if buffer <= 0 {
		buffer = 1024
	}
	pubsub := gochannel.NewGoChannel(gochannel.Config{
		OutputChannelBuffer: buffer,
	}, watermill.NopLogger{})
	return &Bus{pub: pubsub, sub: pubsub, log: log}
}

// Publish marshals payload and emits it to topic. Errors are logged but not
// returned — event publication is best-effort; a serialization bug should
// not fail the calling business path. Use Subscribe to observe events.
func (b *Bus) Publish(ctx context.Context, topic string, payload any) {
	if b == nil || b.pub == nil {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		b.logErr(ctx, "eventbus_marshal_failed", err, string(topic))
		return
	}
	msg := message.NewMessage(uuid.NewString(), body)
	msg.Metadata.Set("topic", topic)
	msg.Metadata.Set("ts", time.Now().UTC().Format(time.RFC3339Nano))
	if err := b.pub.Publish(topic, msg); err != nil {
		b.logErr(ctx, "eventbus_publish_failed", err, topic)
	}
}

// Subscribe returns a read-only channel of messages for topic. Callers must
// Ack() each message (Watermill's flow-control contract) and should Close
// the returned context-bound cancel when done. Returning the raw channel
// keeps callers free to fan out or rate-limit as they see fit.
func (b *Bus) Subscribe(ctx context.Context, topic string) (<-chan *message.Message, error) {
	if b == nil || b.sub == nil {
		return nil, ErrDisabled
	}
	return b.sub.Subscribe(ctx, topic)
}

// Close shuts down the pub/sub. Safe to call multiple times.
func (b *Bus) Close() error {
	if b == nil || b.pub == nil {
		return nil
	}
	if p, ok := b.pub.(interface{ Close() error }); ok {
		return p.Close()
	}
	return nil
}

func (b *Bus) logErr(ctx context.Context, msg string, err error, topic string) {
	if b.log == nil {
		return
	}
	b.log.ErrorContext(ctx, msg,
		slog.String("err", err.Error()),
		slog.String("topic", topic),
	)
}
