package http

import (
	"strings"
	"testing"
)

// TestScrubValues is the direct table test for the shared value-scrub primitive:
// empty values are skipped (never handed to ReplaceAll), a longer secret that
// contains a shorter one is redacted without leaking a fragment of either, and
// multiple distinct secrets are all removed.
func TestScrubValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		in         string
		values     []string
		wantNoLeak []string
		want       string
	}{
		{
			name:   "empty values slice is a no-op",
			in:     "plain message",
			values: nil,
			want:   "plain message",
		},
		{
			name:       "empty string values are skipped",
			in:         "plain message",
			values:     []string{"", "", ""},
			want:       "plain message",
			wantNoLeak: nil,
		},
		{
			name:   "single secret redacted",
			in:     "login failed for user hunter2",
			values: []string{"hunter2"},
			want:   "login failed for user [redacted]",
		},
		{
			name:       "multiple distinct secrets all redacted",
			in:         "apikey=ABCD1234 cookie=uid%3D1%3Bpass%3DXYZ",
			values:     []string{"ABCD1234", "uid%3D1%3Bpass%3DXYZ"},
			wantNoLeak: []string{"ABCD1234", "uid%3D1%3Bpass%3DXYZ"},
			want:       "apikey=[redacted] cookie=[redacted]",
		},
		{
			// The shorter secret ("USER123") is a substring of the longer one
			// ("USER123KEY"). Redacting the shorter first would leave a "KEY"
			// fragment of the longer secret behind; longest-first must not leak it.
			name:       "longest-first substring safety",
			in:         "leak USER123KEY and USER123",
			values:     []string{"USER123", "USER123KEY"},
			wantNoLeak: []string{"USER123KEY", "KEY"},
			want:       "leak [redacted] and [redacted]",
		},
		{
			// A value that never appears in s is simply not found; no panic, no
			// partial match.
			name:   "value absent from s is a no-op for that value",
			in:     "no secrets here",
			values: []string{"nope"},
			want:   "no secrets here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ScrubValues(tt.in, tt.values)
			if got != tt.want {
				t.Errorf("ScrubValues(%q, %v) = %q, want %q", tt.in, tt.values, got, tt.want)
			}
			for _, leak := range tt.wantNoLeak {
				if strings.Contains(got, leak) {
					t.Errorf("ScrubValues(%q, %v) leaked %q: %q", tt.in, tt.values, leak, got)
				}
			}
		})
	}
}

// TestScrubValuesDoesNotMutateInput proves ScrubValues sorts a COPY of values, never
// the caller's slice — a caller (e.g. Base.Scrub, which builds its extras slice once
// per call) must not see its own argument reordered.
func TestScrubValuesDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	values := []string{"a", "ccc", "bb"}
	orig := append([]string(nil), values...)
	_ = ScrubValues("a ccc bb", values)
	for i := range values {
		if values[i] != orig[i] {
			t.Fatalf("ScrubValues mutated caller's values slice: got %v, want %v", values, orig)
		}
	}
}
