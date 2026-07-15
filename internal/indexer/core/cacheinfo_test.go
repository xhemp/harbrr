package core

import (
	"context"
	"testing"
	"time"
)

func TestCacheInfoSinkRoundTrip(t *testing.T) {
	t.Parallel()
	feedClock := time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		// withSink selects a ctx carrying a cache-info sink; without one,
		// recording must be a silent no-op (no panic), which is the whole
		// assertion for that case.
		withSink bool
	}{
		{name: "recording through a sink fills it", withSink: true},
		{name: "recording with no sink is a no-op", withSink: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			var ci *CacheInfo
			if tt.withSink {
				ctx, ci = WithCacheInfoSink(ctx)
			}
			RecordCacheInfo(ctx, CacheInfo{Cached: true, ExpiresAt: feedClock})
			if tt.withSink && (!ci.Cached || !ci.ExpiresAt.Equal(feedClock)) {
				t.Fatalf("sink not filled: %+v", ci)
			}
		})
	}
}
