package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/web/api"
)

type expiredAlphaRatioDoer struct{}

func (expiredAlphaRatioDoer) Do(req *http.Request) (*http.Response, error) {
	if req.URL.Path == "/login.php" {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`<form id="loginform"></form>`)),
			Request:    req,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{"/login.php"}},
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

// TestTestIndexerNotFound: POST /api/indexers/{slug}/test for an unknown slug is
// a 404 (the registry build fails at lookup before any network call). Uses
// auth-disabled + loopback allowlist so no session/API-key setup is needed.
func TestTestIndexerNotFound(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{
		AuthDisabled: true,
		IPAllowlist:  []string{"127.0.0.0/8", "::1/128"},
	}))
	resp, _ := do(t, c, http.MethodPost, base+"/api/indexers/does-not-exist/test", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestAlphaRatioExpiredSessionDiagnostics proves a failed automatic re-login reaches
// both the direct test response and persisted status event with credential recovery
// guidance. Neither API may collapse it to a generic login failure.
func TestAlphaRatioExpiredSessionDiagnostics(t *testing.T) {
	t.Parallel()

	e := newEnvWithCache(t, api.Config{
		AuthDisabled: true,
		IPAllowlist:  []string{"127.0.0.0/8", "::1/128"},
	}, nil, registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) {
		return expiredAlphaRatioDoer{}, nil
	}))
	if _, err := e.registry.Add(context.Background(), registry.AddParams{
		Slug: "ar", DefinitionID: "alpharatio",
		Settings: map[string]string{
			"username": "ar-api-synthetic-user",
			"password": "AR-API-SYNTHETIC-PASSWORD-0000000000",
			"cookie":   "session=AR-API-SYNTHETIC-SESSION-0000000000",
		},
	}); err != nil {
		t.Fatalf("Add(alpharatio): %v", err)
	}
	base, client := serve(t, e)

	resp, body := do(t, client, http.MethodPost, base+"/api/indexers/ar/test", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test status = %d, body = %s", resp.StatusCode, body)
	}
	var testResult struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &testResult); err != nil {
		t.Fatalf("decode test response: %v", err)
	}
	if testResult.OK {
		t.Fatal("expired-session test reported success")
	}
	assertAutomaticLoginRecoveryContext(t, "test response", testResult.Error)

	resp, body = do(t, client, http.MethodGet, base+"/api/indexers/ar/status", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status status = %d, body = %s", resp.StatusCode, body)
	}
	var status struct {
		Events []struct {
			Kind   string `json:"kind"`
			Detail string `json:"detail"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if len(status.Events) != 1 || status.Events[0].Kind != domain.HealthAuthFailure {
		t.Fatalf("status events = %+v, want one auth_failure", status.Events)
	}
	assertAutomaticLoginRecoveryContext(t, "health detail", status.Events[0].Detail)
}

func assertAutomaticLoginRecoveryContext(t *testing.T, source, message string) {
	t.Helper()
	lower := strings.ToLower(message)
	for _, required := range []string{"automatic login", "username/password"} {
		if !strings.Contains(lower, required) {
			t.Errorf("%s %q missing %q", source, message, required)
		}
	}
	if strings.Contains(lower, "manually renew") {
		t.Errorf("%s retained obsolete manual-cookie guidance: %q", source, message)
	}
}
