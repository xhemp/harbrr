package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/web/api"
)

// TestSeedAnnounceTargetFromQuiConnection covers the one-click qui announce target
// (#72): a qui app-connection's base URL, decrypted API key, and harbrr URL are reused
// to create an independent qui announce-connection with its own freshly minted harbrr
// key, plus its guards (non-qui rejected, duplicate base URL 409).
func TestSeedAnnounceTargetFromQuiConnection(t *testing.T) {
	t.Parallel()
	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)
	ctx := context.Background()

	// A non-qui app-connection: seeding from it must be rejected.
	sonarr := map[string]string{
		"name": "sonarr", "kind": "sonarr", "baseUrl": "http://sonarr:8989",
		"apiKey": "sonarr-key", "harbrrUrl": "http://harbrr:7478",
	}
	resp, body := do(t, c, http.MethodPost, base+"/api/app-connections", sonarr, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var sonarrConn struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &sonarrConn); err != nil {
		t.Fatalf("unmarshal sonarr: %v", err)
	}

	resp, body = do(t, c, http.MethodPost, base+"/api/app-connections/"+strconv.FormatInt(sonarrConn.ID, 10)+"/announce-target", nil, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// A qui app-connection: seeding creates a matching qui announce-connection reusing
	// its base URL + qui API key, with a fresh harbrr key.
	qui := map[string]string{
		"name": "qui", "kind": "qui", "baseUrl": "http://qui:7476",
		"apiKey": "qui-secret", "harbrrUrl": "http://harbrr:7478",
	}
	resp, body = do(t, c, http.MethodPost, base+"/api/app-connections", qui, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var quiConn struct {
		ID             int64 `json:"id"`
		HarbrrAPIKeyID int64 `json:"-"`
	}
	if err := json.Unmarshal(body, &quiConn); err != nil {
		t.Fatalf("unmarshal qui: %v", err)
	}

	resp, body = do(t, c, http.MethodPost, base+"/api/app-connections/"+strconv.FormatInt(quiConn.ID, 10)+"/announce-target", nil, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var target struct {
		ID      int64  `json:"id"`
		Kind    string `json:"kind"`
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
	}
	if err := json.Unmarshal(body, &target); err != nil {
		t.Fatalf("unmarshal target: %v", err)
	}
	if target.Kind != "qui" || target.BaseURL != "http://qui:7476" {
		t.Fatalf("target = %+v, want kind=qui baseUrl=http://qui:7476", target)
	}
	if target.APIKey != secrets.Redacted {
		t.Errorf("apiKey = %q, want redacted", target.APIKey)
	}

	// Seed round-trip: the announce row's decrypted qui api key (under its own row's
	// AAD) equals the app-connection's, and the two rows have independent harbrr keys.
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}

	var appEncrypted string
	if err := e.db.QueryRowContext(ctx, "SELECT api_key_encrypted FROM app_connections WHERE id = ?", quiConn.ID).Scan(&appEncrypted); err != nil {
		t.Fatalf("query app_connections: %v", err)
	}
	appAPIKey, err := kr.Decrypt(quiConn.ID, "app", appEncrypted)
	if err != nil {
		t.Fatalf("decrypt app key: %v", err)
	}

	var announceEncrypted string
	if err := e.db.QueryRowContext(ctx, "SELECT api_key_encrypted FROM announce_connections WHERE id = ?", target.ID).Scan(&announceEncrypted); err != nil {
		t.Fatalf("query announce_connections: %v", err)
	}
	announceAPIKey, err := kr.Decrypt(target.ID, "app", announceEncrypted)
	if err != nil {
		t.Fatalf("decrypt announce key: %v", err)
	}
	if announceAPIKey != appAPIKey || announceAPIKey != "qui-secret" {
		t.Errorf("announce api key = %q, app api key = %q, want both qui-secret", announceAPIKey, appAPIKey)
	}

	var appHarbrrKeyID, announceHarbrrKeyID int64
	if err := e.db.QueryRowContext(ctx, "SELECT harbrr_api_key_id FROM app_connections WHERE id = ?", quiConn.ID).Scan(&appHarbrrKeyID); err != nil {
		t.Fatalf("query app harbrr key id: %v", err)
	}
	if err := e.db.QueryRowContext(ctx, "SELECT harbrr_api_key_id FROM announce_connections WHERE id = ?", target.ID).Scan(&announceHarbrrKeyID); err != nil {
		t.Fatalf("query announce harbrr key id: %v", err)
	}
	if appHarbrrKeyID == announceHarbrrKeyID {
		t.Errorf("app and announce connections share harbrr key id %d, want independent keys", appHarbrrKeyID)
	}

	// Independent revocation: deleting the announce target revokes only its own key,
	// leaving the app-connection's minted key (and thus the connection) untouched.
	resp, body = do(t, c, http.MethodDelete, base+"/api/announce-connections/"+strconv.FormatInt(target.ID, 10), nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, base+"/api/app-connections/"+strconv.FormatInt(quiConn.ID, 10), nil, nil)
	mustStatus(t, resp, body, http.StatusOK)

	// Duplicate guard: seeding again for the same qui app-connection (same base URL)
	// is a 409 once a qui announce target for that base URL exists.
	resp, body = do(t, c, http.MethodPost, base+"/api/app-connections/"+strconv.FormatInt(quiConn.ID, 10)+"/announce-target", nil, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	resp, body = do(t, c, http.MethodPost, base+"/api/app-connections/"+strconv.FormatInt(quiConn.ID, 10)+"/announce-target", nil, nil)
	mustStatus(t, resp, body, http.StatusConflict)
}
