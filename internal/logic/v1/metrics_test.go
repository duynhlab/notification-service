package v1

import (
	"context"
	"slices"
	"testing"

	"github.com/duynhlab/notification-service/internal/core/domain"
	"github.com/duynhlab/pkg/obsx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// collectCounter reads an Int64 Sum metric into an attribute→value map keyed by
// one label.
func collectCounter(t *testing.T, reader sdkmetric.Reader, name, label string) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s is %T, want Sum[int64]", name, m.Data)
			}
			for _, dp := range sum.DataPoints {
				v, _ := dp.Attributes.Value(attribute.Key(label))
				out[v.AsString()] = dp.Value
			}
		}
	}
	return out
}

// collectHistogramCounts reads a Float64 histogram into an attribute→count map
// keyed by one label (the number of samples recorded per label value).
func collectHistogramCounts(t *testing.T, reader sdkmetric.Reader, name, label string) map[string]uint64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string]uint64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s is %T, want Histogram[float64]", name, m.Data)
			}
			for _, dp := range hist.DataPoints {
				v, _ := dp.Attributes.Value(attribute.Key(label))
				out[v.AsString()] = dp.Count
			}
		}
	}
	return out
}

// histogramBounds returns the explicit bucket boundaries of the first data point
// of a Float64 histogram, for asserting the instrument's advisory buckets.
func histogramBounds(t *testing.T, reader sdkmetric.Reader, name string) []float64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s is a %T, want a float64 histogram", name, m.Data)
			}
			if len(hist.DataPoints) == 0 {
				t.Fatalf("%s has no data points", name)
			}
			return hist.DataPoints[0].Bounds
		}
	}
	t.Fatalf("%s not found", name)
	return nil
}

// TestBusinessMetrics drives the read and send instruments on one MeterProvider.
// It is intentionally NOT parallel: the OTel global provider is first-wins, so a
// single install per test binary is required, and the ManualReader is cumulative.
// Because every other test in this package calls t.Parallel(), this serial test
// runs alone before the parallel ones resume — so it observes a clean counter.
func TestBusinessMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))

	ctx := context.Background()

	// read{mode=single}: one successful single mark counts exactly once.
	singleSvc := NewNotificationService(&mockRepo{markUpdated: true, findByID: &domain.Notification{ID: "1", Read: true}})
	if _, err := singleSvc.MarkAsRead(ctx, "1", "1"); err != nil {
		t.Fatalf("MarkAsRead: %v", err)
	}
	// A not-updated mark (row absent/foreign) must NOT count.
	if _, err := NewNotificationService(&mockRepo{markUpdated: false}).MarkAsRead(ctx, "2", "1"); err == nil {
		t.Fatal("expected not-found error for un-updated mark")
	}

	// read{mode=all}: a bulk mark of 3 adds 3.
	if _, err := NewNotificationService(&mockRepo{markAllCount: 3}).MarkAllAsRead(ctx, "1"); err != nil {
		t.Fatalf("MarkAllAsRead: %v", err)
	}
	// A bulk mark that flips zero rows is an idempotent no-op and must NOT count.
	if _, err := NewNotificationService(&mockRepo{markAllCount: 0}).MarkAllAsRead(ctx, "1"); err != nil {
		t.Fatalf("MarkAllAsRead(0): %v", err)
	}

	reads := collectCounter(t, reader, "notification.read.total", "mode")
	if reads[modeSingle] != 1 {
		t.Errorf("read{mode=single} = %d, want 1", reads[modeSingle])
	}
	if reads[modeAll] != 3 {
		t.Errorf("read{mode=all} = %d, want 3", reads[modeAll])
	}

	// send.duration: one email send and one sms send each record one sample.
	if _, err := NewNotificationService(&mockRepo{}).SendEmail(ctx, domain.SendEmailRequest{UserID: aliceSub, To: "a@b.com", Subject: "Hi", Body: "Body"}); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	if _, err := NewNotificationService(&mockRepo{}).SendSMS(ctx, domain.SendSMSRequest{UserID: aliceSub, To: "+1555", Message: "hello"}); err != nil {
		t.Fatalf("SendSMS: %v", err)
	}
	// A send that fails at persistence still took time and is recorded.
	if _, err := NewNotificationService(&mockRepo{createErr: errRepo}).SendEmail(ctx, domain.SendEmailRequest{UserID: aliceSub, To: "a@b.com", Subject: "Hi"}); err == nil {
		t.Fatal("expected repo error from SendEmail")
	}
	// A rejected recipient is a bad request, not a send, and must NOT be timed.
	if _, err := NewNotificationService(&mockRepo{}).SendEmail(ctx, domain.SendEmailRequest{UserID: aliceSub, To: "bad", Subject: "Hi"}); err == nil {
		t.Fatal("expected invalid-recipient error")
	}

	sends := collectHistogramCounts(t, reader, "notification.send.duration", "channel")
	if sends[channelEmail] != 2 { // one success + one persist-failure
		t.Errorf("send.duration{channel=email} count = %d, want 2", sends[channelEmail])
	}
	if sends[channelSMS] != 1 {
		t.Errorf("send.duration{channel=sms} count = %d, want 1", sends[channelSMS])
	}

	// The custom histogram must carry the platform's SLO-tuned second-scale
	// buckets (via instrument advisory) rather than the SDK's millisecond default
	// {0,5,…,10000}, or second-scale quantiles would be meaningless.
	if bounds := histogramBounds(t, reader, "notification.send.duration"); !slices.Equal(bounds, obsx.DurationBuckets) {
		t.Errorf("send.duration bounds = %v, want SLO-tuned %v", bounds, obsx.DurationBuckets)
	}
}
