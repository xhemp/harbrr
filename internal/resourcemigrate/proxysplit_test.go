package resourcemigrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/resourcemigrate"
	"github.com/autobrr/harbrr/internal/secrets"
)

// seedLegacyProxy inserts a proxy in the pre-#71 shape: no host, a legacy
// composite URL sealed under domain.ProxySecretURL in url_encrypted — exactly
// what an already-migrated (#12) or hand-crafted pre-split row looks like.
func seedLegacyProxy(t *testing.T, db *database.DB, kr *secrets.Keyring, typ, rawURL string) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	id, err := (database.Proxies{}).InsertProxy(ctx, db, domain.Proxy{Name: "legacy", Type: typ, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("InsertProxy: %v", err)
	}
	enc, err := kr.Encrypt(id, domain.ProxySecretURL, rawURL)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// SetProxySecret writes password_encrypted, not url_encrypted — the legacy
	// column needs a direct write to simulate a pre-split row.
	if _, err := db.ExecContext(ctx, db.Rebind(`UPDATE proxies SET url_encrypted = ? WHERE id = ?`), enc, id); err != nil {
		t.Fatalf("seed url_encrypted: %v", err)
	}
	return id
}

func TestSplitProxyURLsBackfillsWithCredentials(t *testing.T) {
	t.Parallel()
	db, kr := setup(t)
	id := seedLegacyProxy(t, db, kr, domain.ProxyTypeSOCKS5, "socks5://alice:s3cret@10.0.0.9:1080")

	if err := resourcemigrate.SplitProxyURLs(context.Background(), db, kr, zerolog.Nop()); err != nil {
		t.Fatalf("SplitProxyURLs: %v", err)
	}

	repo := database.Proxies{}
	got, err := repo.GetProxy(context.Background(), db, id)
	if err != nil {
		t.Fatalf("GetProxy: %v", err)
	}
	if got.Host != "10.0.0.9" || got.Port != 1080 || got.Username != "alice" {
		t.Fatalf("backfilled fields = %+v", got)
	}
	pass, err := kr.Decrypt(got.ID, domain.ProxySecretPassword, got.PasswordEncrypted)
	if err != nil || pass != "s3cret" {
		t.Fatalf("decrypt password = %q, %v; want s3cret", pass, err)
	}
}

func TestSplitProxyURLsBackfillsWithoutCredentials(t *testing.T) {
	t.Parallel()
	db, kr := setup(t)
	id := seedLegacyProxy(t, db, kr, domain.ProxyTypeHTTP, "http://10.0.0.9:3128")

	if err := resourcemigrate.SplitProxyURLs(context.Background(), db, kr, zerolog.Nop()); err != nil {
		t.Fatalf("SplitProxyURLs: %v", err)
	}

	repo := database.Proxies{}
	got, err := repo.GetProxy(context.Background(), db, id)
	if err != nil {
		t.Fatalf("GetProxy: %v", err)
	}
	if got.Host != "10.0.0.9" || got.Port != 3128 || got.Username != "" {
		t.Fatalf("backfilled fields = %+v", got)
	}
	pass, err := kr.Decrypt(got.ID, domain.ProxySecretPassword, got.PasswordEncrypted)
	if err != nil || pass != "" {
		t.Fatalf("decrypt password = %q, %v; want empty", pass, err)
	}
}

// TestSplitProxyURLsDefaultsPortByScheme: a port-less legacy URL previously
// worked (net/http defaults the dial port by scheme), so the backfill must land
// the scheme's conventional default in the port column, never 0 — composeProxyURL
// always emits an explicit host:port.
func TestSplitProxyURLsDefaultsPortByScheme(t *testing.T) {
	t.Parallel()
	db, kr := setup(t)
	httpID := seedLegacyProxy(t, db, kr, domain.ProxyTypeHTTP, "http://myproxy")
	socksID := seedLegacyProxy(t, db, kr, domain.ProxyTypeSOCKS5, "socks5://user:pass@myproxy")

	if err := resourcemigrate.SplitProxyURLs(context.Background(), db, kr, zerolog.Nop()); err != nil {
		t.Fatalf("SplitProxyURLs: %v", err)
	}

	repo := database.Proxies{}
	for _, tc := range []struct {
		id       int64
		wantPort int
	}{
		{httpID, 80},
		{socksID, 1080},
	} {
		got, err := repo.GetProxy(context.Background(), db, tc.id)
		if err != nil {
			t.Fatalf("GetProxy %d: %v", tc.id, err)
		}
		if got.Host != "myproxy" || got.Port != tc.wantPort {
			t.Errorf("proxy %d = host %q port %d, want myproxy:%d", tc.id, got.Host, got.Port, tc.wantPort)
		}
	}
}

func TestSplitProxyURLsIsIdempotent(t *testing.T) {
	t.Parallel()
	db, kr := setup(t)
	id := seedLegacyProxy(t, db, kr, domain.ProxyTypeSOCKS5, "socks5://10.0.0.9:1080")

	ctx := context.Background()
	if err := resourcemigrate.SplitProxyURLs(ctx, db, kr, zerolog.Nop()); err != nil {
		t.Fatalf("SplitProxyURLs (1st): %v", err)
	}
	repo := database.Proxies{}
	first, _ := repo.GetProxy(ctx, db, id)

	// A second run must be a no-op: the row's host is already set, so
	// ProxiesPendingSplit no longer selects it.
	if err := resourcemigrate.SplitProxyURLs(ctx, db, kr, zerolog.Nop()); err != nil {
		t.Fatalf("SplitProxyURLs (2nd): %v", err)
	}
	second, _ := repo.GetProxy(ctx, db, id)
	if first != second {
		t.Fatalf("re-run changed the row: %+v -> %+v", first, second)
	}
}
