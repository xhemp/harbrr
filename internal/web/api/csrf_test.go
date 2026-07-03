package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestCSRF proves the session-bound CSRF token: a cookie-authenticated mutating
// request needs a valid X-CSRF-Token (do auto-attaches it from the companion
// cookie, like a browser); a bad token is 403; an X-API-Key caller is exempt; and
// a safe method needs no token.
func TestCSRF(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c) // establishes the session + the harbrr_csrf cookie in c's jar

	// 1. Session mutating request with the auto-attached token succeeds.
	resp, body := do(t, c, http.MethodPost, base+"/api/apikeys", map[string]string{"name": "ok"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var minted struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &minted); err != nil || minted.Key == "" {
		t.Fatalf("mint response missing key: %s", body)
	}

	// 2. Session mutating request with a BAD token is rejected (do respects the explicit header).
	resp, body = do(t, c, http.MethodPost, base+"/api/apikeys",
		map[string]string{"name": "bad"}, map[string]string{"X-CSRF-Token": "wrong"})
	mustStatus(t, resp, body, http.StatusForbidden)

	// 3. An X-API-Key caller (no session, no jar) is exempt — no CSRF token needed.
	fresh := &http.Client{}
	resp, body = do(t, fresh, http.MethodPost, base+"/api/apikeys",
		map[string]string{"name": "viakey"}, map[string]string{"X-API-Key": minted.Key})
	mustStatus(t, resp, body, http.StatusCreated)

	// 4. A safe method needs no token, and /me exposes the token for a client to bootstrap.
	resp, body = do(t, c, http.MethodGet, base+"/api/auth/me", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var me struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(body, &me); err != nil || me.CSRFToken == "" {
		t.Errorf("/me should expose a non-empty csrfToken for a session caller: %s", body)
	}
}
