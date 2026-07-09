package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestServerInfoEndpoint proves GET /api/server-info reports the configured listening
// port (not anything derived from the inbound request), which is what the frontend uses
// to detect an app-sync connection's HarbrrURL going stale after a port change.
func TestServerInfoEndpoint(t *testing.T) {
	e := newEnv(t, api.Config{Port: 9117})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	resp, body := do(t, c, http.MethodGet, base+"/api/server-info", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)

	var got struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Port != 9117 {
		t.Errorf("port = %d, want 9117", got.Port)
	}
}

// TestServerInfoEndpointRequiresAuth proves the route sits behind the authenticated group.
func TestServerInfoEndpointRequiresAuth(t *testing.T) {
	e := newEnv(t, api.Config{Port: 9117})
	base, c := serve(t, e)

	resp, body := do(t, c, http.MethodGet, base+"/api/server-info", nil, nil)
	mustStatus(t, resp, body, http.StatusUnauthorized)
}
