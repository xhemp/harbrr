package search

import (
	stdhttp "net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// TestApplySession_ReplaysSolverUA proves the search request carries the
// session's anti-bot solver User-Agent (the cf_clearance cookie in the client
// jar is bound to it), and that a definition's own User-Agent header is left
// untouched. Cookies are the Doer jar's job, so applySession must not add any.
func TestApplySession_ReplaysSolverUA(t *testing.T) {
	t.Parallel()
	session := &login.Session{UserAgent: "Mozilla/5.0 (solver)"}

	t.Run("sets UA when absent", func(t *testing.T) {
		t.Parallel()
		req, _ := stdhttp.NewRequestWithContext(t.Context(), stdhttp.MethodGet, "https://t.invalid/browse", nil)
		applySession(req, session)
		if got := req.Header.Get("User-Agent"); got != "Mozilla/5.0 (solver)" {
			t.Errorf("User-Agent = %q, want the solver UA", got)
		}
		if got := req.Header.Get("Cookie"); got != "" {
			t.Errorf("Cookie = %q, want none (the Doer's jar owns cookies)", got)
		}
	})

	t.Run("definition User-Agent wins", func(t *testing.T) {
		t.Parallel()
		req, _ := stdhttp.NewRequestWithContext(t.Context(), stdhttp.MethodGet, "https://t.invalid/browse", nil)
		req.Header.Set("User-Agent", "DefUA")
		applySession(req, session)
		if got := req.Header.Get("User-Agent"); got != "DefUA" {
			t.Errorf("User-Agent = %q, want the definition's own UA to win", got)
		}
	})

	t.Run("no UA without a solve", func(t *testing.T) {
		t.Parallel()
		req, _ := stdhttp.NewRequestWithContext(t.Context(), stdhttp.MethodGet, "https://t.invalid/browse", nil)
		applySession(req, &login.Session{})
		if got := req.Header.Get("User-Agent"); got != "" {
			t.Errorf("User-Agent = %q, want empty when the session carries no solver UA", got)
		}
	})
}
