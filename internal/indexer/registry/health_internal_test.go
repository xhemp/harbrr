package registry

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func TestClassifyHealth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
		ok   bool
	}{
		{"auth", login.ErrLoginFailed, domain.HealthAuthFailure, true},
		{"anti-bot", login.ErrSolverRequired, domain.HealthAntiBot, true},
		{"rate-limited", search.ErrRateLimited, domain.HealthRateLimited, true},
		{"parse", search.ErrParseError, domain.HealthParseError, true},
		{"wrapped auth", fmt.Errorf("cardigann: login for x: %w", login.ErrLoginFailed), domain.HealthAuthFailure, true},
		{"unclassified", errors.New("boom"), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := classifyHealth(tt.err)
			if ok != tt.ok || got != tt.want {
				t.Errorf("classifyHealth = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDeriveStatus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.June, 14, 12, 0, 0, 0, time.UTC)
	// deriveStatus lives on StatsReporter now; construct it directly (it needs only clock).
	r := &StatsReporter{clock: func() time.Time { return now }}

	recent := []domain.IndexerHealthEvent{{ID: 2, OccurredAt: now.Add(-1 * time.Minute)}}
	old := []domain.IndexerHealthEvent{{ID: 1, OccurredAt: now.Add(-2 * time.Hour)}}
	recovered := database.HealthRecovery{ThroughEventID: 2, OccurredAt: now}
	later := []domain.IndexerHealthEvent{{ID: 3, OccurredAt: now}}
	tests := []struct {
		name     string
		events   []domain.IndexerHealthEvent
		recovery database.HealthRecovery
		want     string
	}{
		{name: "no events", want: "healthy"},
		{name: "recent failure", events: recent, want: "unhealthy"},
		{name: "old failure", events: old, want: "healthy"},
		{name: "recovered failure", events: recent, recovery: recovered, want: "healthy"},
		{name: "failure after recovery", events: later, recovery: recovered, want: "unhealthy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := r.deriveStatus(tt.events, tt.recovery); got != tt.want {
				t.Errorf("deriveStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}
