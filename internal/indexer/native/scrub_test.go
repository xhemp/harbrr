package native

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// scrubTestDef mirrors a typical native family's caps-only definition: one
// IsSecret-classified field (apikey, matched by name token) and one non-secret
// field (username), so Scrub's derived set and its extra-values path can both be
// exercised.
func scrubTestDef() *loader.Definition {
	def := testDef()
	def.Settings = []loader.SettingsField{
		{Name: "username", Type: "text"},
		{Name: "apikey", Type: "text"},
	}
	return def
}

func newScrubTestBase(t *testing.T, cfg map[string]string) Base {
	t.Helper()
	b, err := NewBase("testfam", Params{Def: scrubTestDef(), Cfg: cfg})
	if err != nil {
		t.Fatalf("NewBase: %v", err)
	}
	return b
}

func TestBaseScrub(t *testing.T) {
	t.Parallel()

	t.Run("derives the secret set from IsSecret over Def.Settings", func(t *testing.T) {
		t.Parallel()
		b := newScrubTestBase(t, map[string]string{"username": "dave", "apikey": "SECRET-KEY"})
		got := b.Scrub("user dave failed with key SECRET-KEY")
		if strings.Contains(got, "SECRET-KEY") {
			t.Fatalf("Scrub left the secret in: %q", got)
		}
		if !strings.Contains(got, "dave") {
			t.Fatalf("Scrub removed the non-secret username: %q", got)
		}
		if !strings.Contains(got, "[redacted]") {
			t.Fatalf("Scrub did not insert the placeholder: %q", got)
		}
	})

	t.Run("extra values are scrubbed alongside the derived set", func(t *testing.T) {
		t.Parallel()
		b := newScrubTestBase(t, map[string]string{"apikey": "SECRET-KEY"})
		got := b.Scrub("bad ua GECKO-BUILD-9000 with key SECRET-KEY", "GECKO-BUILD-9000")
		if strings.Contains(got, "SECRET-KEY") || strings.Contains(got, "GECKO-BUILD-9000") {
			t.Fatalf("Scrub left a secret in: %q", got)
		}
	})

	t.Run("no configured secrets is a no-op", func(t *testing.T) {
		t.Parallel()
		b := newScrubTestBase(t, nil)
		const msg = "plain message, no credentials"
		if got := b.Scrub(msg); got != msg {
			t.Fatalf("Scrub(%q) = %q, want unchanged", msg, got)
		}
	})
}

// TestBaseScrubErrSentinelsSurvive is the fix this hoist exists to prove: a naive
// scrub-then-errors.New (the pattern this replaces in passthepopcorn/torrentday's
// former scrubError) drops the wrapped sentinel the moment scrubbing actually
// changes the message. ScrubErr must keep both errors.Is(login.ErrLoginFailed) and
// errors.As(*search.RateLimitedError) working through the scrub.
func TestBaseScrubErrSentinelsSurvive(t *testing.T) {
	t.Parallel()

	t.Run("errors.Is(ErrLoginFailed) survives a scrub that changes the message", func(t *testing.T) {
		t.Parallel()
		b := newScrubTestBase(t, map[string]string{"apikey": "SECRET-KEY"})
		orig := fmt.Errorf("testfam: request unauthorized (key SECRET-KEY): %w", login.ErrLoginFailed)
		got := b.ScrubErr(orig)
		if strings.Contains(got.Error(), "SECRET-KEY") {
			t.Fatalf("ScrubErr left the secret in: %q", got.Error())
		}
		if !strings.Contains(got.Error(), "[redacted]") {
			t.Fatalf("ScrubErr did not insert the placeholder: %q", got.Error())
		}
		if !errors.Is(got, login.ErrLoginFailed) {
			t.Fatalf("ScrubErr dropped the ErrLoginFailed sentinel: %v", got)
		}
	})

	t.Run("errors.As(*search.RateLimitedError) survives a scrub that changes the message", func(t *testing.T) {
		t.Parallel()
		b := newScrubTestBase(t, map[string]string{"apikey": "SECRET-KEY"})
		rl := &search.RateLimitedError{StatusCode: 429}
		orig := fmt.Errorf("testfam: rate limited (key SECRET-KEY): %w", rl)
		got := b.ScrubErr(orig)
		if strings.Contains(got.Error(), "SECRET-KEY") {
			t.Fatalf("ScrubErr left the secret in: %q", got.Error())
		}
		var target *search.RateLimitedError
		if !errors.As(got, &target) || target != rl {
			t.Fatalf("ScrubErr dropped the RateLimitedError sentinel: %v", got)
		}
	})

	t.Run("extra values are honored alongside sentinel preservation", func(t *testing.T) {
		t.Parallel()
		b := newScrubTestBase(t, nil)
		orig := fmt.Errorf("testfam: unauthorized for user ADMINUSER: %w", login.ErrLoginFailed)
		got := b.ScrubErr(orig, "ADMINUSER")
		if strings.Contains(got.Error(), "ADMINUSER") {
			t.Fatalf("ScrubErr left the extra secret in: %q", got.Error())
		}
		if !errors.Is(got, login.ErrLoginFailed) {
			t.Fatalf("ScrubErr dropped the sentinel: %v", got)
		}
	})

	t.Run("nil error returns nil", func(t *testing.T) {
		t.Parallel()
		b := newScrubTestBase(t, map[string]string{"apikey": "SECRET-KEY"})
		if got := b.ScrubErr(nil); got != nil {
			t.Fatalf("ScrubErr(nil) = %v, want nil", got)
		}
	})

	t.Run("a message with no secret is returned unchanged (no wrapper)", func(t *testing.T) {
		t.Parallel()
		b := newScrubTestBase(t, map[string]string{"apikey": "SECRET-KEY"})
		orig := fmt.Errorf("testfam: %w", login.ErrLoginFailed)
		got := b.ScrubErr(orig)
		// Identity, not mere equivalence: ScrubErr must return the ORIGINAL error value
		// untouched (no scrubbedError wrapper) when scrubbing changed nothing, so a
		// caller comparing it directly (== or errors.As) still works.
		var se *scrubbedError
		if errors.As(got, &se) || got.Error() != orig.Error() {
			t.Fatalf("ScrubErr wrapped an error whose message did not change: %v (orig %v)", got, orig)
		}
	})
}
