package main

import (
	"context"
	"log/slog"

	"github.com/chaustre/inquiryiq/internal/infrastructure/eventbus"
)

// startLogSubscribers wires the built-in audit subscribers that echo every
// published event to structured logs. In production you would layer a Slack
// notifier on escalation.recorded + backpressure.dropped, a PagerDuty page
// on toggle.flipped, and an analytics pipeline on conversion.* — they all
// plug in the same way: call bus.Subscribe and Ack each message.
func startLogSubscribers(ctx context.Context, bus *eventbus.Bus, log *slog.Logger) {
	for _, topic := range []string{
		eventbus.TopicEscalationRecorded,
		eventbus.TopicConversionManaged,
		eventbus.TopicConversionDone,
		eventbus.TopicToggleFlipped,
		eventbus.TopicBackpressureDrop,
		eventbus.TopicBudgetExceeded,
	} {
		go consume(ctx, bus, topic, log)
	}
}

func consume(ctx context.Context, bus *eventbus.Bus, topic string, log *slog.Logger) {
	msgs, err := bus.Subscribe(ctx, topic)
	if err != nil {
		log.ErrorContext(ctx, "eventbus_subscribe_failed",
			slog.String("topic", topic), slog.String("err", err.Error()))
		return
	}
	for m := range msgs {
		log.InfoContext(ctx, "eventbus_event",
			slog.String("topic", topic),
			slog.String("body", string(m.Payload)),
		)
		m.Ack()
	}
}
