package resourcemigrate

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// SplitProxyURLs backfills proxies still holding a legacy encrypted composite URL
// (pre-#71) into structured host/port/username/password fields: decrypt under the
// legacy domain.ProxySecretURL AAD, url.Parse, persist the plain parts, re-encrypt
// just the password under domain.ProxySecretPassword, and clear url_encrypted.
//
// Unlike Run, this needs no app_meta done-flag: ProxiesPendingSplit's own
// host = ” condition is the idempotency check (a split row has a host, so it
// never matches again), so each row is processed independently — a crash midway
// through leaves only the un-split rows for retry next boot, not a rolled-back
// batch.
func SplitProxyURLs(ctx context.Context, db dbinterface.Querier, kr *secrets.Keyring, log zerolog.Logger) error {
	repo := database.Proxies{}
	pending, err := repo.ProxiesPendingSplit(ctx, db)
	if err != nil {
		return fmt.Errorf("resourcemigrate: list proxies pending split: %w", err)
	}
	for _, p := range pending {
		if err := splitProxyURL(ctx, db, kr, repo, p); err != nil {
			return err
		}
	}
	if len(pending) > 0 {
		log.Info().Int("proxies", len(pending)).Msg("split legacy proxy URLs into structured host/port/username/password fields")
	}
	return nil
}

// splitProxyURL backfills one proxy row.
func splitProxyURL(ctx context.Context, db dbinterface.Querier, kr *secrets.Keyring, repo database.Proxies, p database.LegacyProxyURL) error {
	rawURL, err := kr.Decrypt(p.ID, domain.ProxySecretURL, p.URLEncrypted)
	if err != nil {
		return fmt.Errorf("resourcemigrate: decrypt proxy %d url: %w", p.ID, err)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("resourcemigrate: parse proxy %d url: %w", p.ID, err)
	}
	port, _ := strconv.Atoi(u.Port())
	if u.Port() == "" {
		// A port-less legacy URL previously worked (net/http defaults the dial port
		// by scheme), so backfill the scheme's conventional default rather than 0 —
		// composeProxyURL always emits an explicit host:port.
		port = defaultProxyPort(u.Scheme)
	}
	password, _ := u.User.Password()

	sealed, err := kr.Encrypt(p.ID, domain.ProxySecretPassword, password)
	if err != nil {
		return fmt.Errorf("resourcemigrate: encrypt proxy %d password: %w", p.ID, err)
	}
	if err := repo.SplitProxyURL(ctx, db, p.ID, u.Hostname(), port, u.User.Username(), sealed, kr.KeyID()); err != nil {
		return fmt.Errorf("resourcemigrate: split proxy %d: %w", p.ID, err)
	}
	return nil
}

// defaultProxyPort is the conventional port for a proxy scheme, used when a
// legacy URL carried none: http 80, https 443, socks5/socks5h 1080.
func defaultProxyPort(scheme string) int {
	switch scheme {
	case domain.ProxyTypeHTTPS:
		return 443
	case domain.ProxyTypeSOCKS5, domain.ProxyTypeSOCKS5H:
		return 1080
	default: // http
		return 80
	}
}
