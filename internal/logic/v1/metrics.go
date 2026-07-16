package v1

import (
	"context"
	"time"

	"github.com/duynhlab/pkg/obsx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Business metrics for notification, answering the on-call questions that a
// trace/log alone cannot:
//  1. Are users clearing their inbox one-by-one or in bulk? → read{mode}
//  2. How long does the send path take, split by channel?   → send.duration{channel}
//
// Instruments ride the global OTel MeterProvider that the observability setup
// installs (RFC-0014 OTLP pipeline → collector → VictoriaMetrics). Before that
// setup the global provider is a no-op, so package-init here is safe. Names are
// OTel-style; the collector renders them as notification_read_total and
// notification_send_duration_seconds.
//
// Labels are bounded to enumerable domain values (RFC-0017 D-9): no ids, no
// recipient text, no message bodies.
var (
	meter = otel.Meter("notification-service")

	readCounter, _ = meter.Int64Counter("notification.read.total",
		metric.WithDescription("Notifications marked read, split by single vs bulk mark-all"))
	// Advisory second-scale buckets: obsx installs its DurationBuckets View only
	// for the semconv HTTP/RPC instruments by name, so this custom histogram
	// would otherwise fall back to the SDK default explicit buckets ({0,5,…,10000},
	// sized for milliseconds) and collapse every sub-10s send into the first
	// bucket. The SDK honors an instrument's advisory boundaries when no View
	// matches, so reusing the platform's SLO-tuned set keeps quantiles meaningful.
	sendDuration, _ = meter.Float64Histogram("notification.send.duration",
		metric.WithDescription("Send-path latency from validated input to persisted notification, by channel"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(obsx.DurationBuckets...))
)

// Read modes (bounded): a single mark-as-read vs a bulk mark-all-read.
const (
	modeSingle = "single"
	modeAll    = "all"
)

// Send channels (bounded).
const (
	channelEmail = "email"
	channelSMS   = "sms"
)

// recordRead counts notifications transitioned to read, grouped by how the read
// happened (single mark vs bulk mark-all). n is the number of rows actually
// flipped, so a bulk mark-all of 5 adds 5 under mode=all and one single mark
// adds 1 under mode=single — giving a single "notifications read" counter that
// still distinguishes user behavior. An idempotent no-op (nothing flipped) is
// not counted.
func recordRead(ctx context.Context, mode string, n int64) {
	if n <= 0 {
		return
	}
	readCounter.Add(ctx, n, metric.WithAttributes(attribute.String("mode", mode)))
}

// recordSendDuration records one send-path latency sample for a channel. This is
// the seam where a real provider call would live; today it times validation-
// passed input through repository persistence, so slow or failing sends surface
// as latency regardless of whether the provider is real yet.
func recordSendDuration(ctx context.Context, channel string, start time.Time) {
	sendDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(
		attribute.String("channel", channel),
	))
}
